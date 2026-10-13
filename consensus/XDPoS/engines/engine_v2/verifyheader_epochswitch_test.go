// Copyright (c) 2026 XDC Network
// #1404 / #1415: under fullVerify an epoch-switch header must carry exactly the
// locally computed masternode list (canonical XDPoSChain verifyHeader.go:137-155),
// and a calcMasternodes failure (e.g. missing gap snapshot) is fatal.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestVerifyHeaderEpochSwitch -count=1

package engine_v2

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

// esChain is a ChainReader backed by explicit header maps.
type esChain struct {
	cfg      *params.ChainConfig
	byHash   map[common.Hash]*types.Header
	byNumber map[uint64]*types.Header
}

func (c *esChain) add(h *types.Header) {
	c.byHash[h.Hash()] = h
	c.byNumber[h.Number.Uint64()] = h
}
func (c *esChain) Config() *params.ChainConfig                     { return c.cfg }
func (c *esChain) CurrentHeader() *types.Header                    { return nil }
func (c *esChain) GetBlock(common.Hash, uint64) *types.Block       { return nil }
func (c *esChain) GetTd(common.Hash, uint64) *big.Int              { return nil }
func (c *esChain) GetHeader(h common.Hash, _ uint64) *types.Header { return c.byHash[h] }
func (c *esChain) GetHeaderByHash(h common.Hash) *types.Header     { return c.byHash[h] }
func (c *esChain) GetHeaderByNumber(n uint64) *types.Header        { return c.byNumber[n] }

func packValidators(addrs []common.Address) []byte {
	out := make([]byte, 0, len(addrs)*common.AddressLength)
	for _, a := range addrs {
		out = append(out, a.Bytes()...)
	}
	return out
}

func esV2Header(t *testing.T, num uint64, parent *types.Header, round types.Round, qc *types.QuorumCert, ts uint64) *types.Header {
	t.Helper()
	extra, err := (&types.ExtraFields_v2{Round: round, QuorumCert: qc}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode extra: %v", err)
	}
	h := &types.Header{
		Number:     new(big.Int).SetUint64(num),
		Extra:      extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       ts,
		UncleHash:  utils.UncleHash,
	}
	if parent != nil {
		h.ParentHash = parent.Hash()
	}
	return h
}

func esSeal(t *testing.T, h *types.Header, key *ecdsa.PrivateKey) {
	t.Helper()
	h.Coinbase = crypto.PubkeyToAddress(key.PublicKey)
	sig, err := crypto.Sign(sigHash(h).Bytes(), key)
	if err != nil {
		t.Fatalf("sign header: %v", err)
	}
	h.Validator = sig
}

// esFixture builds (epoch=900, gap=450, V2 from genesis):
//
//	450  gap block (snapshot holds the N masternodes, unless skipSnapshot)
//	901  epoch switch E1, round 900, Validators = the N masternodes
//	902  P, round 1798 (same epoch as E1)
//	903  epoch switch H, round 1800, QC over P signed by all N masternodes
//
// H is returned unsealed with Validators unset.
func esFixture(t *testing.T, skipSnapshot bool) (*XDPoS_v2, *esChain, []*ecdsa.PrivateKey, []common.Address, *types.Header, *types.Header) {
	t.Helper()
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: 900,
			Gap:   450,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
				CurrentConfig: &params.V2Config{
					MaxMasternodes: 18,
					MinePeriod:     2,
					TimeoutPeriod:  10,
					ExpTimeoutConfig: params.ExpTimeoutConfig{
						Base:        2.0,
						MaxExponent: 6,
					},
					CertThreshold: 2.0 / 3.0,
				},
				AllConfigs: map[uint64]*params.V2Config{},
			},
		},
	}
	var db ethdb.Database = rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	keys, mns := genNKeys(t, 4)
	chain := &esChain{cfg: cfg, byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
	now := uint64(time.Now().Unix())

	gap := &types.Header{Number: big.NewInt(450), Difficulty: big.NewInt(1), Extra: []byte{0x01}}
	chain.add(gap)
	if !skipSnapshot {
		if err := storeSnapshot(newSnapshot(450, gap.Hash(), mns), db); err != nil {
			t.Fatalf("storeSnapshot: %v", err)
		}
	}

	pre := &types.Header{Number: big.NewInt(900), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	e1 := esV2Header(t, 901, pre, 900, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: pre.Hash(), Round: 899, Number: big.NewInt(900)},
	}, now-200)
	e1.Validators = packValidators(mns)
	chain.add(e1)

	p := esV2Header(t, 902, e1, 1798, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: e1.Hash(), Round: 900, Number: big.NewInt(901)},
		GapNumber:         450,
	}, now-100)
	chain.add(p)

	pInfo := &types.BlockInfo{Hash: p.Hash(), Round: 1798, Number: big.NewInt(902)}
	h := esV2Header(t, 903, p, 1800, &types.QuorumCert{
		ProposedBlockInfo: pInfo,
		Signatures:        makeQCSignatures(t, pInfo, 450, keys),
		GapNumber:         450,
	}, now-50)
	return eng, chain, keys, mns, p, h
}

// Positive control: the exact local list is accepted under fullVerify.
func TestVerifyHeaderEpochSwitch_ExactListAccepted(t *testing.T) {
	eng, chain, keys, mns, p, h := esFixture(t, false)
	h.Validators = packValidators(mns)
	esSeal(t, h, keys[0]) // leaderIndex = 1800 % 900 % 4 = 0
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); err != nil {
		t.Fatalf("exact masternode list must be accepted, got %v", err)
	}
}

// #1404: local set N, header lists N+1 (the extra address signs as leader).
func TestVerifyHeaderEpochSwitch_SupersetRejectedUnderFullVerify(t *testing.T) {
	eng, chain, _, mns, p, h := esFixture(t, false)
	extraKeys, extra := genNKeys(t, 1)
	h.Validators = packValidators(append(extra, mns...))
	esSeal(t, h, extraKeys[0])
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); !errors.Is(err, utils.ErrValidatorsNotLegit) {
		t.Fatalf("superset header.Validators under fullVerify: want ErrValidatorsNotLegit, got %v", err)
	}
}

// #1415: gap snapshot missing, a non-masternode lists only itself.
func TestVerifyHeaderEpochSwitch_MissingSnapshotFatalUnderFullVerify(t *testing.T) {
	eng, chain, _, _, p, h := esFixture(t, true)
	outsiderKeys, outsider := genNKeys(t, 1)
	h.Validators = packValidators(outsider)
	esSeal(t, h, outsiderKeys[0])
	err := eng.verifyHeader(chain, h, []*types.Header{p}, true)
	if err == nil {
		t.Fatal("self-listed non-masternode accepted with the gap snapshot missing under fullVerify")
	}
	if !errors.Is(err, utils.ErrV2SnapshotMissing) {
		t.Fatalf("want ErrV2SnapshotMissing, got %v", err)
	}
}

// !fullVerify keeps trusting header.Validators (canonical verifyHeader.go:167-168).
func TestVerifyHeaderEpochSwitch_SupersetTrustedWithoutFullVerify(t *testing.T) {
	eng, chain, _, mns, p, h := esFixture(t, false)
	extraKeys, extra := genNKeys(t, 1)
	h.Validators = packValidators(append(extra, mns...))
	esSeal(t, h, extraKeys[0])
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("!fullVerify must trust header.Validators, got %v", err)
	}
}
