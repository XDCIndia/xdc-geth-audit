// Copyright (c) 2026 XDC Network
// Unit tests for snap_pivot.go — issue #807 Phase 3a.
//
// These tests cover the control-flow checks (V2-only, depth, epoch
// transition, hardfork buffer, fetcher behavior, parent linkage).
// QC verification (checks 2+3) requires masternode set wiring and is
// exercised end-to-end by Phase 5's bench, not here.
//
// SYNC-02 tests (TestSelectSnapPivot_FreshNode_*) verify issue #1842:
// a fresh node with no locally-stored epoch-switch headers can now select
// a pivot because tryPivotCandidate fetches the epoch-switch header from
// the peer and seeds the epochSwitches cache before calling verifyQC.

package engine_v2

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// newTestXDPoS_v2 returns a minimal XDPoS_v2 with only the config fields
// SelectSnapPivot reads. The test deliberately leaves caches / channels /
// signing-state nil — none are touched by the helper's pre-check phase.
func newTestXDPoS_v2(v2Switch uint64, epoch uint64) *XDPoS_v2 {
	return &XDPoS_v2{
		config: &params.XDPoSConfig{
			Epoch: epoch,
			V2: &params.V2{
				SwitchBlock: new(big.Int).SetUint64(v2Switch),
			},
		},
	}
}

// chainHeader returns a header at `n` with `parentHash` set so a sequence
// of such headers forms a valid parent chain. Used for fetcher fixtures.
func chainHeader(n uint64, parentHash common.Hash) *types.Header {
	return &types.Header{
		Number:     new(big.Int).SetUint64(n),
		ParentHash: parentHash,
		Root:       common.HexToHash("0x1234"), // synthetic
		Extra:      nil,                        // QC check expected to fail; we test pre-check rejections only
	}
}

// fetcherFromMap returns a HeaderFetcher backed by a map. Missing entries
// return nil (simulating peer not serving that header).
func fetcherFromMap(m map[uint64]*types.Header) HeaderFetcher {
	return func(n uint64) *types.Header {
		return m[n]
	}
}

func TestSelectSnapPivot_PeerHeadTooLow(t *testing.T) {
	x := newTestXDPoS_v2(1_000_000, 900)
	got := x.SelectSnapPivot(nil, 5, fetcherFromMap(nil), nil)
	if got != nil {
		t.Fatalf("expected nil for peerHead=5 < SnapPivotMinDepth=12, got %+v", got)
	}
}

func TestSelectSnapPivot_PreV2Rejected(t *testing.T) {
	// V2 switch at 80M; peer head at 80,000,020 — only candidate would be
	// in pre-V2 territory after the depth+offset adjustment.
	x := newTestXDPoS_v2(80_000_000, 900)
	got := x.SelectSnapPivot(nil, 80_000_020, fetcherFromMap(nil), nil)
	if got != nil {
		t.Fatalf("expected nil (only candidate is pre-V2), got %+v", got)
	}
}

func TestSelectSnapPivot_NoHeadersFromPeer(t *testing.T) {
	// Peer head well past V2 switch + epoch math lands cleanly mid-epoch,
	// but fetcher returns nil for everything → expect nil result.
	x := newTestXDPoS_v2(1_000_000, 900)
	got := x.SelectSnapPivot(nil, 82_000_000, fetcherFromMap(nil), nil)
	if got != nil {
		t.Fatalf("expected nil (fetcher returns nil), got %+v", got)
	}
}

func TestSelectSnapPivot_ParentLinkageBroken(t *testing.T) {
	// Build N..N+3 with WRONG parent links → algorithm should reject and
	// walk to next candidate, eventually returning nil since they all
	// have broken parents.
	x := newTestXDPoS_v2(1_000_000, 900)
	m := map[uint64]*types.Header{}
	// Build 20 candidates' worth, all with bad parent linkage.
	for i := range SnapPivotMaxCandidates {
		base := uint64(82_000_000 - i*900 + 450)
		for j := range uint64(4) {
			m[base+j] = chainHeader(base+j, common.HexToHash("0xdeadbeef")) // all same wrong parent
		}
	}
	got := x.SelectSnapPivot(nil, 82_000_000, fetcherFromMap(m), nil)
	if got != nil {
		t.Fatalf("expected nil (parent linkage broken), got %+v", got)
	}
}

func TestSelectSnapPivot_HardforkBufferRejection(t *testing.T) {
	// Place a hardfork right where the first candidate would land.
	x := newTestXDPoS_v2(1_000_000, 900)
	peerHead := uint64(82_000_000)
	// First candidate per algorithm: ((peerHead-12)/epoch)*epoch + 450
	startEpoch := (peerHead - SnapPivotMinDepth) / 900
	firstCandidate := startEpoch*900 + SnapPivotEpochOffset
	if firstCandidate > peerHead-SnapPivotMinDepth {
		firstCandidate = (startEpoch-1)*900 + SnapPivotEpochOffset
	}
	// Hardfork exactly at firstCandidate; algorithm should reject it
	// (and all subsequent — we want a NO_PIVOT to demonstrate the check).
	// To force NO_PIVOT we put hardforks at EVERY candidate position.
	hardforks := []uint64{}
	c := firstCandidate
	for range SnapPivotMaxCandidates {
		hardforks = append(hardforks, c)
		if c < 900 {
			break
		}
		c -= 900
	}
	got := x.SelectSnapPivot(nil, peerHead, fetcherFromMap(nil), hardforks)
	if got != nil {
		t.Fatalf("expected nil (all candidates near hardfork), got %+v", got)
	}
}

func TestSelectSnapPivot_EpochBoundaryAvoided(t *testing.T) {
	// This is the bug Phase 2 discovered. peerHead lands on epoch=0
	// position. Without the offset, all candidates would land on
	// epoch=0 positions and fail check 5. With the offset, candidates
	// land mid-epoch.
	_ = newTestXDPoS_v2(1_000_000, 900) // not invoked; we test the algorithm constants only
	peerHead := uint64(82_215_000)      // 82_215_000 % 900 == 0 (epoch boundary)
	// Compute where the first candidate lands.
	startEpoch := (peerHead - SnapPivotMinDepth) / 900
	firstCandidate := startEpoch*900 + SnapPivotEpochOffset
	if firstCandidate > peerHead-SnapPivotMinDepth {
		firstCandidate = (startEpoch-1)*900 + SnapPivotEpochOffset
	}
	pos := firstCandidate % 900
	if pos < SnapPivotHardforkBuffer || pos > 900-SnapPivotHardforkBuffer {
		t.Fatalf("first candidate %d is in epoch-transition window (pos=%d) — offset failed",
			firstCandidate, pos)
	}
	// Smoke-pass: candidate is mid-epoch as intended.
	t.Logf("epoch-boundary peerHead=%d → first candidate %d (pos %d in epoch) — OK",
		peerHead, firstCandidate, pos)
}

func TestSelectSnapPivot_DepthInvariant(t *testing.T) {
	// Algorithm invariant: depth = peerHead - candidate is monotonically
	// non-decreasing across iterations (stepping down by epoch always
	// increases depth). First candidate must already satisfy depth ≥ MIN.
	peerHead := uint64(82_000_000)
	startEpoch := (peerHead - SnapPivotMinDepth) / 900
	firstCandidate := startEpoch*900 + SnapPivotEpochOffset
	if firstCandidate > peerHead-SnapPivotMinDepth {
		firstCandidate = (startEpoch-1)*900 + SnapPivotEpochOffset
	}
	firstDepth := peerHead - firstCandidate
	if firstDepth < SnapPivotMinDepth {
		t.Fatalf("first candidate %d has depth %d < %d — algorithm broken",
			firstCandidate, firstDepth, SnapPivotMinDepth)
	}
}

// --- SYNC-02 tests (#1842): fresh-node epoch-switch seeding with round-based binary search ---
//
// Scenario: epoch=100, gap=50, v2Switch=0. The epoch-4 switch happened at
// block 437 (round=400) NOT at block 400 = (450/100)*100, demonstrating
// round drift: 37 rounds were skipped in epoch 3, so epoch 4 started 37
// blocks later than the block-number formula would predict.
//
// The fetcher deliberately omits block 400 (the old formula's guess).
// On origin/main, trySeedEpochSwitchForSnap fetches block 400 → nil →
// no seeding → verifyQC fails: "failed to get epoch switch info".
// After the fix, findEpochSwitchHeaderForSnap binary-searches [437, 450]
// and finds block 437 (4 fetches: blocks 443, 440, 438, 437).
//
// gapNum: epochSwitchNum=437; g=437−437%100=400; 400>50 → g−=50 → 350.
//
// Threshold: 3 masternodes, CertThreshold=2/3 → ≥2 valid sigs required
// (all 3 are used so the threshold is always satisfied).

// stubChain implements consensus.ChainReader with nil/zero returns for all
// methods. Used by SYNC-02 tests to simulate a fresh node whose local chain
// DB has no committed headers.
type stubChain struct{}

var _ consensus.ChainReader = stubChain{}

func (stubChain) Config() *params.ChainConfig                 { return nil }
func (stubChain) CurrentHeader() *types.Header                { return nil }
func (stubChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (stubChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (stubChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }
func (stubChain) GetBlock(common.Hash, uint64) *types.Block   { return nil }

// newSnapPivotTestEngine creates an engine with a real in-memory DB so that
// on origin/main the SYNC-02 tests show FAIL (not panic) when loadSnapshot
// receives the empty DB.
func newSnapPivotTestEngine(t *testing.T, v2Switch, epoch, gap uint64) *XDPoS_v2 {
	t.Helper()
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: epoch,
			Gap:   gap,
			V2: &params.V2{
				SwitchBlock: new(big.Int).SetUint64(v2Switch),
				CurrentConfig: &params.V2Config{
					MinePeriod:    2,
					TimeoutPeriod: 10,
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
	return createEngine(cfg, rawdb.NewMemoryDatabase(), nil, nil)
}

// buildV2Header builds a header with ExtraFields_v2.Round=round and a QC
// pointing at parentHash with QC.ProposedBlockInfo.Round=parentRound.
// validators, when non-nil, is stored verbatim in header.Validators.
func buildV2Header(t *testing.T, blockNum uint64, round, parentRound types.Round, parentHash common.Hash, validators []byte) *types.Header {
	t.Helper()
	ext := &types.ExtraFields_v2{
		Round: round,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   parentHash,
				Round:  parentRound,
				Number: new(big.Int).SetUint64(blockNum - 1),
			},
			Signatures: []types.Signature{},
		},
	}
	b, err := ext.EncodeToBytes()
	if err != nil {
		t.Skipf("buildV2Header EncodeToBytes: %v", err)
	}
	h := &types.Header{
		Number:     new(big.Int).SetUint64(blockNum),
		ParentHash: parentHash,
		Extra:      b,
		Difficulty: big.NewInt(1),
	}
	if len(validators) > 0 {
		h.Validators = validators
	}
	return h
}

// buildSignedHeader builds a header at blockNum with ExtraFields_v2.Round=ownRound,
// carrying a QC that certifies proposedHeader (whose own round is proposedRound)
// signed by signingKeys with the given gapNum.
func buildSignedHeader(t *testing.T, blockNum uint64, ownRound, proposedRound types.Round, proposedHeader *types.Header, gapNum uint64, signingKeys []*ecdsa.PrivateKey) *types.Header {
	t.Helper()
	propInfo := &types.BlockInfo{
		Hash:   proposedHeader.Hash(),
		Round:  proposedRound,
		Number: new(big.Int).Set(proposedHeader.Number),
	}
	sigs := makeQCSignatures(t, propInfo, gapNum, signingKeys)
	ext := &types.ExtraFields_v2{
		Round: ownRound,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: propInfo,
			Signatures:        sigs,
			GapNumber:         gapNum,
		},
	}
	b, err := ext.EncodeToBytes()
	if err != nil {
		t.Skipf("buildSignedHeader EncodeToBytes: %v", err)
	}
	return &types.Header{
		Number:     new(big.Int).SetUint64(blockNum),
		ParentHash: proposedHeader.Hash(),
		Extra:      b,
		Difficulty: big.NewInt(1),
	}
}

// sync02Constants holds the fixed test parameters for the SYNC-02 scenario.
// epoch=100, gap=50, v2Switch=0, epoch-4 switch at block 437 (NOT 400).
const (
	sync02Epoch             = uint64(100)
	sync02Gap               = uint64(50)
	sync02V2Switch          = uint64(0)
	sync02SwitchBlock       = uint64(437)      // epoch-4 switch; NOT (450/100)*100=400
	sync02SwitchRound       = types.Round(400) // first round of epoch 4
	sync02SwitchParentRound = types.Round(399) // last round of epoch 3
	// gapNum: epochSwitchNum=437; g=437-437%100=400; 400>50 → g-=50 → 350
	sync02GapNum    = uint64(350)
	sync02Candidate = uint64(450)
	sync02CandRound = types.Round(413) // = 400 + (450-437); 413/100=4=target ✓
	sync02PeerHead  = uint64(470)      // depth=20 ≥ 12; posInEpoch=50 ∈ [10,90]
)

// buildSync02EpochSwitch builds the epoch-switch header at block 437 with the
// given masternode addresses packed into Validators.
// ExtraFields_v2.Round=400, QC.ProposedBlockInfo.Round=399 →
// IsEpochSwitch: 399 < epochStartRound(400) → YES.
func buildSync02EpochSwitch(t *testing.T, addrs []common.Address) *types.Header {
	t.Helper()
	validators := make([]byte, len(addrs)*common.AddressLength)
	for i, a := range addrs {
		copy(validators[i*common.AddressLength:], a[:])
	}
	return buildV2Header(t, sync02SwitchBlock, sync02SwitchRound, sync02SwitchParentRound,
		common.HexToHash("0xab00436"), validators)
}

// buildSync02Chain assembles the binary-search blocks and the hN..hN+3 chain.
// switchHdr is the epoch-switch header (block 437); signingKeys sign the QCs
// in hN1, hN2, hN3.
//
// The fetcher for these tests must contain:
//
//	437 (epoch switch), 438, 440, 443  — binary search fetches
//	450 (hN), 451 (hN1), 452 (hN2), 453 (hN3)
//
// Block 400 is intentionally absent: the old (450/100)*100=400 formula would
// fetch it and get nil → seeding fails → test (a) fails on origin/main.
func buildSync02Chain(t *testing.T, switchHdr *types.Header, signingKeys []*ecdsa.PrivateKey) (
	m map[uint64]*types.Header, hN, hN1, hN2, hN3 *types.Header,
) {
	t.Helper()
	// Binary-search intermediates: rounds 401, 403, 406 (all in epoch 4).
	// Parent hashes are dummies — binary search only reads Extra.Round.
	block438 := buildV2Header(t, 438, 401, 400, switchHdr.Hash(), nil)
	block440 := buildV2Header(t, 440, 403, 402, common.Hash{0x04, 0x40}, nil)
	block443 := buildV2Header(t, 443, 406, 405, common.Hash{0x04, 0x43}, nil)

	// hN (block 450, round 413): QC points at a dummy block 449 (not verified).
	hN = buildV2Header(t, sync02Candidate, sync02CandRound, 412,
		common.Hash{0x04, 0x49}, nil)
	// hN1..hN3: real signed QCs; each certifies the previous block.
	hN1 = buildSignedHeader(t, 451, 414, sync02CandRound, hN, sync02GapNum, signingKeys)
	hN2 = buildSignedHeader(t, 452, 415, 414, hN1, sync02GapNum, signingKeys)
	hN3 = buildSignedHeader(t, 453, 416, 415, hN2, sync02GapNum, signingKeys)

	m = map[uint64]*types.Header{
		437: switchHdr,
		438: block438,
		440: block440,
		443: block443,
		// 400 is absent — proves old block-number formula fails.
		450: hN, 451: hN1, 452: hN2, 453: hN3,
	}
	return
}

// TestSelectSnapPivot_FreshNode_ValidChain is the SYNC-02 positive regression
// test (issue #1842). It FAILS on origin/main (verifyQC returns "failed to get
// epoch switch info for QC verification" because no epoch-switch headers exist
// locally and block 400 is absent from the peer) and PASSES after the fix
// (findEpochSwitchHeaderForSnap binary-searches to block 437 and seeds the
// epochSwitches cache; verifyQC then finds the epoch info on the fast path).
//
// Round drift: epoch-4 switch at block 437 (round=400), not block 400
// (block-number formula). The binary-search path [437..450] fetches
// blocks 443 → 440 → 438 → 437 (4 fetches) to locate the switch.
func TestSelectSnapPivot_FreshNode_ValidChain(t *testing.T) {
	eng := newSnapPivotTestEngine(t, sync02V2Switch, sync02Epoch, sync02Gap)
	keys, addrs := genNKeys(t, 3)
	switchHdr := buildSync02EpochSwitch(t, addrs)
	m, hN, _, _, _ := buildSync02Chain(t, switchHdr, keys)

	got := eng.tryPivotCandidate(stubChain{}, sync02Candidate, sync02PeerHead,
		fetcherFromMap(m), nil)
	if got == nil {
		t.Fatal("SYNC-02 (a): expected non-nil pivot on fresh node with valid chain and " +
			"round drift; got nil — epoch-switch binary search or cache seeding may be broken")
	}
	if got.Block.Number.Uint64() != sync02Candidate {
		t.Errorf("SYNC-02 (a): expected pivot at block %d, got %d",
			sync02Candidate, got.Block.Number.Uint64())
	}
	// Cache must be seeded after a successful pivot.
	if _, ok := eng.epochSwitches.Get(hN.Hash()); !ok {
		t.Error("SYNC-02 (a): epochSwitches must be seeded with hN.Hash() after valid pivot")
	}
}

// TestSelectSnapPivot_FreshNode_FakeSwitchHeader verifies the cache-poisoning
// defence (SYNC-02, issue #1842). The peer serves a fake epoch-switch header
// with different masternodes, so the seeded epoch info has the wrong master­node
// set and verifyQC fails (real QC signers are not in the fake set). The deferred
// cleanup in tryPivotCandidate must then remove the three seeded cache entries so
// no stale info is left behind.
func TestSelectSnapPivot_FreshNode_FakeSwitchHeader(t *testing.T) {
	eng := newSnapPivotTestEngine(t, sync02V2Switch, sync02Epoch, sync02Gap)
	// Real masternode keys sign the QCs.
	realKeys, _ := genNKeys(t, 3)
	// Fake masternodes in the switch header — different addresses from signers.
	_, fakeAddrs := genNKeys(t, 3)

	// switchHdr encodes fakeAddrs → seeded masternodes are fake → verifyQC fails.
	fakeSwitchHdr := buildSync02EpochSwitch(t, fakeAddrs)
	m, hN, hN1, hN2, _ := buildSync02Chain(t, fakeSwitchHdr, realKeys)

	got := eng.tryPivotCandidate(stubChain{}, sync02Candidate, sync02PeerHead,
		fetcherFromMap(m), nil)
	if got != nil {
		t.Fatalf("SYNC-02 (b): expected nil (fake switch header, signers ∉ fake masternodes); "+
			"got pivot at %d", got.Block.Number.Uint64())
	}
	// The deferred cleanup must have removed the three seeded cache entries.
	for _, h := range []*types.Header{hN, hN1, hN2} {
		if _, ok := eng.epochSwitches.Get(h.Hash()); ok {
			t.Errorf("SYNC-02 (b): epochSwitches must not contain %s after fake-switch "+
				"rejection (cache poisoning guard failed)", h.Hash().Hex())
		}
	}
}

// TestSelectSnapPivot_FreshNode_BrokenParentLink verifies that a broken N..N+3
// parent link causes tryPivotCandidate to return nil before any seeding occurs.
func TestSelectSnapPivot_FreshNode_BrokenParentLink(t *testing.T) {
	eng := newSnapPivotTestEngine(t, sync02V2Switch, sync02Epoch, sync02Gap)
	keys, addrs := genNKeys(t, 3)
	switchHdr := buildSync02EpochSwitch(t, addrs)
	m, hN, hN1, _, _ := buildSync02Chain(t, switchHdr, keys)

	// Replace hN2 with a header whose ParentHash doesn't match hN1.Hash().
	hN2bad := buildV2Header(t, 452, 415, 414, common.HexToHash("0xbadbad"), nil)
	hN3bad := buildSignedHeader(t, 453, 416, 415, hN2bad, sync02GapNum, keys)
	m[452] = hN2bad
	m[453] = hN3bad

	got := eng.tryPivotCandidate(stubChain{}, sync02Candidate, sync02PeerHead,
		fetcherFromMap(m), nil)
	if got != nil {
		t.Fatalf("SYNC-02 (c): expected nil (hN2 parent link broken); got pivot at %d",
			got.Block.Number.Uint64())
	}
	// Parent-link check fires before seeding; cache must remain empty.
	if _, ok := eng.epochSwitches.Get(hN.Hash()); ok {
		t.Error("SYNC-02 (c): epochSwitches must not be seeded when parent link is broken")
	}
	_ = hN1 // hN1's link to hN is intact; only hN2 is broken
}

// TestSelectSnapPivot_FreshNode_NonMasterSig verifies that QCs signed by keys
// not in the epoch-switch header's masternode set are rejected: verifyQC finds
// the seeded epoch info (correct masternode set) but the recovered signers don't
// match it, so the threshold check fails and tryPivotCandidate returns nil.
func TestSelectSnapPivot_FreshNode_NonMasterSig(t *testing.T) {
	eng := newSnapPivotTestEngine(t, sync02V2Switch, sync02Epoch, sync02Gap)
	// Real masternodes in the switch header.
	_, masterAddrs := genNKeys(t, 3)
	switchHdr := buildSync02EpochSwitch(t, masterAddrs)
	// QCs signed by unrelated (non-masternode) keys.
	nonMasterKeys, _ := genNKeys(t, 3)
	m, _, _, _, _ := buildSync02Chain(t, switchHdr, nonMasterKeys)

	got := eng.tryPivotCandidate(stubChain{}, sync02Candidate, sync02PeerHead,
		fetcherFromMap(m), nil)
	if got != nil {
		t.Fatalf("SYNC-02 (d): expected nil (QC signed by non-masternodes); got pivot at %d",
			got.Block.Number.Uint64())
	}
}
