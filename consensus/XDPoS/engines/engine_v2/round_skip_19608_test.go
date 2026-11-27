// Copyright (c) 2026 XDC Network

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
	"github.com/ethereum/go-ethereum/params"
)

// skipChain is the leader-turn fixture chain plus a head for timeout-certificate
// lookup and an optional trusted-checkpoint anchor.
type skipChain struct {
	ltChain
	head         *types.Header
	anchor       uint64
	anchorActive bool
	anchorHash   common.Hash
}

func (c *skipChain) CurrentHeader() *types.Header { return c.head }
func (c *skipChain) GetTrustedCheckpointAnchor() (uint64, common.Hash, bool) {
	if c.anchorHash != (common.Hash{}) {
		return c.anchor, c.anchorHash, c.anchorActive
	}
	return c.anchor, common.Hash{0x11}, c.anchorActive
}

// skipFixture builds the same epoch shape as ltFixture. The checked header is
// at headerRound and its parent certificate is at parentRound. Both rounds stay
// inside the epoch that opened at round 900, so the header is not an epoch
// switch. The returned header is sealed by that round's leader.
func skipFixture(t *testing.T, parentRound, headerRound types.Round, tc *types.TimeoutCert, activate *big.Int) (*XDPoS_v2, *skipChain, []*ecdsa.PrivateKey, *types.Header, *types.Header) {
	t.Helper()
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: 900,
			Gap:   450,
			V2: &params.V2{
				SwitchBlock:      big.NewInt(0),
				RoundSkipTCBlock: activate,
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
	db := rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	keys, mns := genNKeys(t, 4)
	chain := &skipChain{ltChain: ltChain{cfg: cfg, byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}}
	now := uint64(time.Now().Unix())

	gap := &types.Header{Number: big.NewInt(450), Difficulty: big.NewInt(1), Extra: []byte{0x01}}
	chain.add(gap)
	if err := storeSnapshot(newSnapshot(450, gap.Hash(), mns), db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	pre := &types.Header{Number: big.NewInt(900), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	e1 := ltV2Header(t, 901, pre, 900, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: pre.Hash(), Round: 899, Number: big.NewInt(900)},
	}, now-300)
	e1.Validators = ltPack(mns)
	chain.add(e1)
	chain.head = e1
	eng.epochSwitches.Add(e1.Hash(), &types.EpochSwitchInfo{
		Masternodes:          append([]common.Address{}, mns...),
		MasternodesLen:       len(mns),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: e1.Hash(), Round: 900, Number: big.NewInt(901)},
	})

	parent := ltV2Header(t, 902, e1, parentRound, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: e1.Hash(), Round: 900, Number: big.NewInt(901)},
		GapNumber:         450,
	}, now-200)
	chain.add(parent)

	parentInfo := &types.BlockInfo{Hash: parent.Hash(), Round: parentRound, Number: big.NewInt(902)}
	qc := &types.QuorumCert{
		ProposedBlockInfo: parentInfo,
		Signatures:        makeQCSignatures(t, parentInfo, 450, keys),
		GapNumber:         450,
	}
	h := ltV2Header(t, 903, parent, headerRound, qc, now-50)
	if tc != nil {
		extra, err := (&types.ExtraFields_v2{Round: headerRound, QuorumCert: qc, TimeoutCert: tc}).EncodeToBytes()
		if err != nil {
			t.Fatal(err)
		}
		h.Extra = extra
	}
	leader := int(uint64(headerRound) % cfg.XDPoS.Epoch % uint64(len(keys)))
	ltSeal(t, h, keys[leader])
	return eng, chain, keys, parent, h
}

func TestF202619608ActiveGateRejectsSkipWithoutCertificate(t *testing.T) {
	eng, chain, _, parent, h := skipFixture(t, 1000, 1005, nil, big.NewInt(903))
	err := eng.verifyHeader(chain, h, []*types.Header{parent}, true)
	if !errors.Is(err, utils.ErrRoundSkipWithoutTimeout) {
		t.Fatalf("skip without a certificate: got %v", err)
	}
	bad := &types.TimeoutCert{Round: 1000, GapNumber: 450, Signatures: []types.Signature{{1}}}
	eng, chain, _, parent, h = skipFixture(t, 1000, 1005, bad, big.NewInt(903))
	err = eng.verifyHeader(chain, h, []*types.Header{parent}, true)
	if !errors.Is(err, utils.ErrRoundSkipWithoutTimeout) {
		t.Fatalf("certificate for the parent round: got %v", err)
	}
}

func signTimeoutGap(t *testing.T, round types.Round, gap uint64, keys ...*ecdsa.PrivateKey) []types.Signature {
	t.Helper()
	hash := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: gap})
	sigs := make([]types.Signature, len(keys))
	for i, key := range keys {
		sig, err := crypto.Sign(hash[:], key)
		if err != nil {
			t.Fatal(err)
		}
		sigs[i] = sig
	}
	return sigs
}

func resealWithCertificate(t *testing.T, h *types.Header, keys []*ecdsa.PrivateKey, epoch uint64, tc *types.TimeoutCert) {
	t.Helper()
	var extra types.ExtraFields_v2
	if err := DecodeExtraFields(h.Extra, &extra); err != nil {
		t.Fatal(err)
	}
	extra.TimeoutCert = tc
	encoded, err := extra.EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	h.Extra = encoded
	h.Validator = nil
	leader := int(uint64(extra.Round) % epoch % uint64(len(keys)))
	ltSeal(t, h, keys[leader])
}
