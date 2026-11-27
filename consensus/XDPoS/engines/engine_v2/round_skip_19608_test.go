// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
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
	"github.com/ethereum/go-ethereum/rlp"
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

func TestF202619608ExtraEncodingUnchangedWithoutCertificate(t *testing.T) {
	qc := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: common.Hash{0x01}, Round: 4, Number: big.NewInt(4)},
		GapNumber:         450,
	}
	got, err := (&types.ExtraFields_v2{Round: 5, QuorumCert: qc}).EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	type twoField struct {
		Round      types.Round
		QuorumCert *types.QuorumCert
	}
	body, err := rlp.EncodeToBytes(&twoField{Round: 5, QuorumCert: qc})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{2}, body...)
	if !bytes.Equal(got, want) {
		t.Fatalf("nil timeout certificate changed the extra encoding\n got %x\nwant %x", got, want)
	}
	with, err := (&types.ExtraFields_v2{
		Round: 5, QuorumCert: qc,
		TimeoutCert: &types.TimeoutCert{Round: 4, GapNumber: 450, Signatures: []types.Signature{{1}}},
	}).EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(with, got) {
		t.Fatal("a timeout certificate was not encoded")
	}
	var decoded types.ExtraFields_v2
	if err := DecodeExtraFields(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TimeoutCert != nil || decoded.Round != 5 {
		t.Fatalf("decoded two-field extra = %+v", decoded)
	}
}

func TestF202619608ShippedNetworksLeaveTheGateUnset(t *testing.T) {
	for name, cfg := range map[string]*params.ChainConfig{
		"mainnet":   params.XDCMainnetChainConfig,
		"apothem":   params.XDCApothemChainConfig,
		"devnet":    params.XDCDevnetChainConfig,
		"devnet551": params.XDCDevnet551ChainConfig,
		"private":   params.XDCDefaultPrivateChainConfig,
	} {
		v2 := cfg.XDPoS.V2
		if v2 == nil || v2.RoundSkipTCBlock != nil || v2.IsRoundSkipTC(new(big.Int).SetUint64(^uint64(0))) {
			t.Fatalf("%s enables roundSkipTCBlock", name)
		}
	}
	// A genesis that does not know the field, which is how net 2040 is configured,
	// keeps today's rule.
	var cfg params.ChainConfig
	raw := []byte(`{"chainId":2040,"xdpos":{"period":2,"epoch":900,"reward":1,"gap":450,"v2":{"switchBlock":1}}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.XDPoS == nil || cfg.XDPoS.V2 == nil || cfg.XDPoS.V2.IsRoundSkipTC(big.NewInt(9_000_000)) {
		t.Fatal("a config that omits roundSkipTCBlock enables the new rule")
	}
	v2 := &params.V2{RoundSkipTCBlock: big.NewInt(1000)}
	if v2.IsRoundSkipTC(big.NewInt(999)) || !v2.IsRoundSkipTC(big.NewInt(1000)) || (*params.V2)(nil).IsRoundSkipTC(big.NewInt(1)) {
		t.Fatal("IsRoundSkipTC boundary is wrong")
	}
}

// TestF202619608UnsetGateAcceptsCurrentMainSkip is a header a current-main
// proposer can seal: the round jumps ahead of the parent certificate and the
// extra has no timeout certificate. With the gate unset this node accepts it.
func TestF202619608UnsetGateAcceptsCurrentMainSkip(t *testing.T) {
	eng, chain, _, parent, h := skipFixture(t, 1000, 1005, nil, nil)
	for _, full := range []bool{false, true} {
		if err := eng.verifyHeader(chain, h, []*types.Header{parent}, full); err != nil {
			t.Fatalf("fullVerify=%v rejected a current-main skipped round: %v", full, err)
		}
	}
}

func TestF202619608FutureGateKeepsCurrentMainSkip(t *testing.T) {
	eng, chain, _, parent, h := skipFixture(t, 1000, 1005, nil, big.NewInt(904))
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err != nil {
		t.Fatalf("header below the fork was rejected: %v", err)
	}
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

func TestF202619608ActiveGateAcceptsSkipWithCertificate(t *testing.T) {
	eng, chain, keys, parent, h := skipFixture(t, 1000, 1005, nil, big.NewInt(903))
	gap := eng.gapFromSwitch(901)
	tc := &types.TimeoutCert{Round: 1004, GapNumber: gap, Signatures: signTimeoutGap(t, 1004, gap, keys[:3]...)}
	resealWithCertificate(t, h, keys, eng.config.Epoch, tc)
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err != nil {
		t.Fatalf("skipped round with a certificate for round 1004: %v", err)
	}
}

func TestF202619608ActiveGateKeepsConsecutiveRound(t *testing.T) {
	// Round 1001 follows the parent certificate. No certificate is required,
	// and stuffing one into the extra is not a header this rule produces.
	eng, chain, _, parent, h := skipFixture(t, 1000, 1001, nil, big.NewInt(0))
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err != nil {
		t.Fatalf("consecutive round after activation: %v", err)
	}
	tc := &types.TimeoutCert{Round: 1000, GapNumber: 450, Signatures: []types.Signature{{1}}}
	eng, chain, _, parent, h = skipFixture(t, 1000, 1001, tc, big.NewInt(0))
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); !errors.Is(err, utils.ErrInvalidV2Extra) {
		t.Fatalf("consecutive round carrying a certificate: got %v", err)
	}
}

func TestF202619608UnsetGateRejectsEmbeddedCertificate(t *testing.T) {
	tc := &types.TimeoutCert{Round: 1004, GapNumber: 450, Signatures: []types.Signature{{1}}}
	eng, chain, _, parent, h := skipFixture(t, 1000, 1005, tc, nil)
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); !errors.Is(err, utils.ErrInvalidV2Extra) {
		t.Fatalf("third extra field with the gate unset: got %v", err)
	}
}

func TestF202619608CatchupChecksPresenceNotSignatures(t *testing.T) {
	garbage := &types.TimeoutCert{Round: 1004, GapNumber: 1, Signatures: []types.Signature{{9, 9, 9}}}
	eng, chain, _, parent, h := skipFixture(t, 1000, 1005, garbage, big.NewInt(903))
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err == nil {
		t.Fatal("garbage certificate accepted outside catch-up")
	}
	chain.anchor = 903
	chain.anchorActive = true
	chain.anchorHash = h.Hash() // the checked header is the anchor itself
	eng.verifiedHeaders.Purge()
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err != nil {
		t.Fatalf("catch-up rejected a skipped header whose bytes carry a certificate: %v", err)
	}
}
