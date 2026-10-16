// Copyright (c) 2026 XDC Network
// #1074 (go-ethereum#1074) regression tests: verifyQC/verifyTC threshold
// counting must dedup by recovered ADDRESS and reject high-S (ECDSA
// malleability) signatures — the same behaviour as legacy XDPoSChain
// (engines/engine_v2/engine.go:915, timeout.go:177) and native eth2030 #136
// (pkg/consensus/xdpos/pool_v2.go).
//
// Prior bug: verifyQC/verifyTC deduped QC/TC signatures by BYTE content
// (UniqueSignatures), so a masternode key's high-S (malleated) counterpart
// of its own low-S vote/timeout counted as a SECOND distinct signature
// toward the 2/3 quorum threshold — letting a single signer forge a QC/TC.
//
// This file asserts:
//   - hasLowS boundary and rejection behaviour.
//   - RecoverUniqueSigners rejects the whole batch on a single high-S sig,
//     and address-dedups genuinely repeated signatures from one signer.
//   - verifyQC/verifyTC reject a "one signer + malleated copy" QC/TC instead
//     of counting it as two signers.
//   - verifyQC/verifyTC still accept a genuine >=2/3 set of distinct,
//     canonical low-S signers.

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// malleateSig returns the secp256k1 "complementary" signature for sig.
// For a signature (r || s || v), the malleated form is (r || (n-s) || (v^1)).
// Both recover the same public key via Ecrecover.
func malleateSig(t *testing.T, sig types.Signature) types.Signature {
	t.Helper()
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(sig))
	}

	// secp256k1 curve order n.
	n := crypto.S256().Params().N

	s := new(big.Int).SetBytes(sig[32:64])
	sNeg := new(big.Int).Sub(n, s)

	malleated := make([]byte, 65)
	copy(malleated[0:32], sig[0:32]) // r unchanged
	sNegBytes := sNeg.Bytes()
	// left-pad to 32 bytes
	copy(malleated[32+(32-len(sNegBytes)):64], sNegBytes)
	malleated[64] = sig[64] ^ 0x01 // flip v

	return types.Signature(malleated)
}

// --- hasLowS -----------------------------------------------------------------

// TestHasLowS_BoundaryAtHalfN mirrors legacy XDPoSChain's boundary test:
// s == halfN is accepted (canonical low-S), s == halfN+1 is rejected.
func TestHasLowS_BoundaryAtHalfN(t *testing.T) {
	mkSig := func(sVal *big.Int) types.Signature {
		sig := make([]byte, 65)
		sig[0] = 0x01 // arbitrary non-zero r
		sBytes := sVal.Bytes()
		copy(sig[32+(32-len(sBytes)):64], sBytes)
		return types.Signature(sig)
	}

	atHalf := mkSig(new(big.Int).Set(secp256k1halfN))
	if !hasLowS(atHalf) {
		t.Errorf("s == halfN should be accepted as low-S")
	}

	aboveHalf := mkSig(new(big.Int).Add(secp256k1halfN, big.NewInt(1)))
	if hasLowS(aboveHalf) {
		t.Errorf("s == halfN+1 should be rejected as high-S")
	}
}

// TestHasLowS_WrongLength rejects malformed (non-65-byte) signatures.
func TestHasLowS_WrongLength(t *testing.T) {
	if hasLowS(types.Signature(make([]byte, 64))) {
		t.Errorf("64-byte signature should be rejected (wrong length)")
	}
	if hasLowS(types.Signature(nil)) {
		t.Errorf("nil signature should be rejected (wrong length)")
	}
}

// TestHasLowS_RejectsMalleableCounterpart verifies that the ECDSA-malleated
// (high-S) counterpart of a valid, canonical low-S signature is rejected.
func TestHasLowS_RejectsMalleableCounterpart(t *testing.T) {
	k := genKey(t)
	hash := common.HexToHash("0xfeed")
	sig, err := crypto.Sign(hash[:], k)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !hasLowS(sig) {
		t.Fatalf("crypto.Sign output must be canonical low-S")
	}
	malleated := malleateSig(t, sig)
	if hasLowS(malleated) {
		t.Errorf("malleated (high-S) counterpart must be rejected by hasLowS")
	}
}

// --- RecoverUniqueSigners ----------------------------------------------------

// TestRecoverUniqueSigners_RejectsBatchOnMalleableSignature mirrors legacy
// XDPoSChain's TestRecoverUniqueSigners_RejectsBatchOnMalleableSignature:
// a single high-S signature anywhere in the batch fails the WHOLE call.
func TestRecoverUniqueSigners_RejectsBatchOnMalleableSignature(t *testing.T) {
	hash := common.HexToHash("0xbad00d")
	k1 := genKey(t)
	k2 := genKey(t)
	sig1, err := crypto.Sign(hash[:], k1)
	if err != nil {
		t.Fatalf("Sign1: %v", err)
	}
	sig2, err := crypto.Sign(hash[:], k2)
	if err != nil {
		t.Fatalf("Sign2: %v", err)
	}
	malleated1 := malleateSig(t, sig1)

	_, _, err = RecoverUniqueSigners(hash, []types.Signature{sig2, malleated1})
	if err == nil {
		t.Fatalf("RecoverUniqueSigners should reject a batch containing a high-S signature")
	}
}

// TestRecoverUniqueSigners_AddressDedup_SameSignerRepeated verifies that N
// byte-identical low-S signatures from the SAME signer collapse to 1 unique
// signer (matches legacy address-dedup semantics; also matches the old
// byte-dedup for identical bytes).
func TestRecoverUniqueSigners_AddressDedup_SameSignerRepeated(t *testing.T) {
	k := genKey(t)
	hash := common.HexToHash("0x777")
	sig, err := crypto.Sign(hash[:], k)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	unique, dups, err := RecoverUniqueSigners(hash, []types.Signature{sig, sig, sig})
	if err != nil {
		t.Fatalf("RecoverUniqueSigners: %v", err)
	}
	if len(unique) != 1 {
		t.Errorf("want 1 unique signer, got %d", len(unique))
	}
	if len(dups) != 2 {
		t.Errorf("want 2 duplicates, got %d", len(dups))
	}
}

// TestRecoverUniqueSigners_DistinctLowSSigners_AllKept verifies that N
// signatures from N distinct signers, all canonical low-S, all count.
func TestRecoverUniqueSigners_DistinctLowSSigners_AllKept(t *testing.T) {
	const n = 5
	hash := common.HexToHash("0x424242")
	sigs := make([]types.Signature, n)
	for i := 0; i < n; i++ {
		k := genKey(t)
		sig, err := crypto.Sign(hash[:], k)
		if err != nil {
			t.Fatalf("Sign[%d]: %v", i, err)
		}
		sigs[i] = sig
	}

	unique, dups, err := RecoverUniqueSigners(hash, sigs)
	if err != nil {
		t.Fatalf("RecoverUniqueSigners: %v", err)
	}
	if len(unique) != n {
		t.Errorf("want %d unique signers, got %d", n, len(unique))
	}
	if len(dups) != 0 {
		t.Errorf("want 0 duplicates, got %d", len(dups))
	}
}

// TestRecoverUniqueSigners_Empty checks the empty-slice edge case.
func TestRecoverUniqueSigners_Empty(t *testing.T) {
	unique, dups, err := RecoverUniqueSigners(common.HexToHash("0x1"), nil)
	if err != nil {
		t.Fatalf("RecoverUniqueSigners: %v", err)
	}
	if len(unique) != 0 || len(dups) != 0 {
		t.Errorf("empty input: want (0,0), got (%d,%d)", len(unique), len(dups))
	}
}

// --- verifyQC hot-path regression --------------------------------------------

// TestVerifyQC_MalleatedCopy_RejectedNotCountedTwice is the headline QC
// regression test for #1074: a QC containing one signer's genuine low-S
// vote plus a malleated (high-S) copy of that SAME vote must NOT be counted
// as two signers toward the 2/3 threshold. With address-dedup + low-S
// enforcement, the malleated copy causes verifyQC to reject the whole QC
// (rather than the old byte-dedup behaviour of accepting it as 2 signers).
func TestVerifyQC_MalleatedCopy_RejectedNotCountedTwice(t *testing.T) {
	eng, db := newA894Engine(t)

	// Small pool: 3 signers, threshold = ceil(3*2/3) = 2.
	allKeys, allAddrs := genNKeys(t, 3)

	const gapBlock = 82934550
	const epoch, gap = uint64(900), uint64(450)
	gapNum := gapNumForBlock(gapBlock, epoch, gap)
	gapHeader, gapHash := makeGapHeader(t, gapBlock, 1000, gapNum)

	snap := newSnapshotWithPenalties(gapBlock, gapHash, allAddrs, nil)
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	proposedBlock := &types.BlockInfo{
		Hash:   gapHash,
		Round:  1001,
		Number: big.NewInt(int64(gapBlock)),
	}

	// ONE genuine signer's vote + a malleated copy of the SAME vote. Under
	// the old byte-dedup this reached len(signatures)==2 (>= threshold of 2)
	// and PASSED with only one real signer's key — the #1074 vulnerability.
	realSigs := makeQCSignatures(t, proposedBlock, gapNum, allKeys[:1])
	malleatedCopy := malleateSig(t, realSigs[0])
	qc := &types.QuorumCert{
		ProposedBlockInfo: proposedBlock,
		Signatures:        []types.Signature{realSigs[0], malleatedCopy},
		GapNumber:         gapNum,
	}

	chain := &mockChainForA894{}
	err := eng.verifyQC(chain, qc, gapHeader, nil)
	if err == nil {
		t.Fatalf("FAIL #1074: verifyQC accepted a single signer's vote + its malleated copy as if from two signers")
	}
	t.Logf("PASS: verifyQC correctly rejected single-signer + malleated-copy QC: %v", err)
}

// TestVerifyQC_HonestQuorum_DistinctSigners_Passes verifies that a genuine
// 2/3 quorum of distinct, canonical low-S signers still verifies correctly
// through the corrected (RecoverUniqueSigners-based) hot path.
func TestVerifyQC_HonestQuorum_DistinctSigners_Passes(t *testing.T) {
	eng, db := newA894Engine(t)

	allKeys, allAddrs := genNKeys(t, 46)
	penaltyAddrs := allAddrs[32:]

	const gapBlock = 82934550
	const epoch, gap = uint64(900), uint64(450)
	gapNum := gapNumForBlock(gapBlock, epoch, gap)
	gapHeader, gapHash := makeGapHeader(t, gapBlock, 1000, gapNum)

	snap := newSnapshotWithPenalties(gapBlock, gapHash, allAddrs, penaltyAddrs)
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	proposedBlock := &types.BlockInfo{
		Hash:   gapHash,
		Round:  1001,
		Number: big.NewInt(int64(gapBlock)),
	}

	// 22 of 32 active signers, all genuine distinct low-S signatures.
	sigs22 := makeQCSignatures(t, proposedBlock, gapNum, allKeys[:22])
	qc22 := &types.QuorumCert{
		ProposedBlockInfo: proposedBlock,
		Signatures:        sigs22,
		GapNumber:         gapNum,
	}

	chain := &mockChainForA894{}
	if err := eng.verifyQC(chain, qc22, gapHeader, nil); err != nil {
		t.Fatalf("FAIL: honest 22-of-32 distinct-signer QC should PASS through RecoverUniqueSigners path, got: %v", err)
	}
	t.Log("PASS: honest 22-of-32 distinct-signer QC verifies under address-dedup + low-S enforcement")
}

// --- verifyTC hot-path regression --------------------------------------------

// mockChainForTC is a minimal consensus.ChainReader backing a single genesis
// (V2-genesis-format) header, sufficient to drive verifyTC's
// getTCEpochInfo -> getEpochSwitchInfo genesis special-case.
type mockChainForTC struct {
	genesis *types.Header
}

func (m *mockChainForTC) Config() *params.ChainConfig                     { return nil }
func (m *mockChainForTC) CurrentHeader() *types.Header                    { return m.genesis }
func (m *mockChainForTC) GetHeader(h common.Hash, n uint64) *types.Header { return nil }
func (m *mockChainForTC) GetHeaderByNumber(n uint64) *types.Header {
	if n == 0 {
		return m.genesis
	}
	return nil
}
func (m *mockChainForTC) GetBlock(h common.Hash, n uint64) *types.Block { return nil }
func (m *mockChainForTC) GetTd(h common.Hash, n uint64) *big.Int        { return nil }
func (m *mockChainForTC) GetHeaderByHash(h common.Hash) *types.Header {
	if m.genesis != nil && m.genesis.Hash() == h {
		return m.genesis
	}
	return nil
}

var _ consensus.ChainReader = (*mockChainForTC)(nil)

// makeTCGenesisHeader builds a block-0 header encoding masternodes in the V1
// vanity(32)+addresses+seal(65) extra format that getEpochSwitchInfoInner's
// genesis special-case decodes directly (round 0, no snapshot walk needed).
func makeTCGenesisHeader(masternodes []common.Address) *types.Header {
	extra := make([]byte, 32+len(masternodes)*common.AddressLength+65)
	for i, addr := range masternodes {
		copy(extra[32+i*common.AddressLength:], addr[:])
	}
	return &types.Header{
		Number:     big.NewInt(0),
		Extra:      extra,
		Difficulty: big.NewInt(1),
	}
}

// TestVerifyTC_MalleatedCopy_RejectedNotCountedTwice is the headline TC
// regression test for #1074: mirrors
// TestVerifyQC_MalleatedCopy_RejectedNotCountedTwice but for the TC path
// (timeout.go:339 verifyTC).
func TestVerifyTC_MalleatedCopy_RejectedNotCountedTwice(t *testing.T) {
	eng, db := newA894Engine(t)

	// Pool of 3 signers, threshold = ceil(3*2/3) = 2. GapNumber = 0 so
	// getSnapshot resolves the gap header via GetHeaderByNumber(0) = genesis.
	allKeys, allAddrs := genNKeys(t, 3)
	genesisHeader := makeTCGenesisHeader(allAddrs)
	genesisHash := genesisHeader.Hash()

	snap := newSnapshotWithPenalties(0, genesisHash, allAddrs, nil)
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	chain := &mockChainForTC{genesis: genesisHeader}

	signedTimeoutObj := types.TimeoutSigHash(&types.TimeoutForSign{Round: 0, GapNumber: 0})
	realSig, err := crypto.Sign(signedTimeoutObj[:], allKeys[0])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	malleatedCopy := malleateSig(t, realSig)

	tc := &types.TimeoutCert{
		Round:      0,
		Signatures: []types.Signature{realSig, malleatedCopy},
		GapNumber:  0,
	}

	if err := eng.verifyTC(chain, tc); err == nil {
		t.Fatalf("FAIL #1074: verifyTC accepted a single signer's timeout + its malleated copy as if from two signers")
	} else {
		t.Logf("PASS: verifyTC correctly rejected single-signer + malleated-copy TC: %v", err)
	}
}

// TestVerifyTC_HonestQuorum_DistinctSigners_Passes verifies that a genuine
// 2/3 quorum of distinct, canonical low-S timeout signatures still verifies
// through the corrected (RecoverUniqueSigners-based) hot path.
func TestVerifyTC_HonestQuorum_DistinctSigners_Passes(t *testing.T) {
	eng, db := newA894Engine(t)

	// Pool of 3 signers, threshold = ceil(3*2/3) = 2.
	allKeys, allAddrs := genNKeys(t, 3)
	genesisHeader := makeTCGenesisHeader(allAddrs)
	genesisHash := genesisHeader.Hash()

	snap := newSnapshotWithPenalties(0, genesisHash, allAddrs, nil)
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	chain := &mockChainForTC{genesis: genesisHeader}

	signedTimeoutObj := types.TimeoutSigHash(&types.TimeoutForSign{Round: 0, GapNumber: 0})
	sig0, err := crypto.Sign(signedTimeoutObj[:], allKeys[0])
	if err != nil {
		t.Fatalf("Sign0: %v", err)
	}
	sig1, err := crypto.Sign(signedTimeoutObj[:], allKeys[1])
	if err != nil {
		t.Fatalf("Sign1: %v", err)
	}

	tc := &types.TimeoutCert{
		Round:      0,
		Signatures: []types.Signature{sig0, sig1},
		GapNumber:  0,
	}

	if err := eng.verifyTC(chain, tc); err != nil {
		t.Fatalf("FAIL: honest 2-of-3 distinct-signer TC should PASS through RecoverUniqueSigners path, got: %v", err)
	}
	t.Log("PASS: honest 2-of-3 distinct-signer TC verifies under address-dedup + low-S enforcement")
}

// TestVerifyTC_21of32BelowThreshold_Fails sanity-checks that the corrected
// path still correctly rejects a genuinely insufficient set of distinct
// signers (not a malleability case) — i.e. the fix didn't accidentally loosen
// the threshold gate itself.
func TestVerifyTC_BelowThreshold_Fails(t *testing.T) {
	eng, db := newA894Engine(t)

	// Pool of 3 signers, threshold = ceil(3*2/3) = 2; only 1 signs.
	allKeys, allAddrs := genNKeys(t, 3)
	genesisHeader := makeTCGenesisHeader(allAddrs)
	genesisHash := genesisHeader.Hash()

	snap := newSnapshotWithPenalties(0, genesisHash, allAddrs, nil)
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	chain := &mockChainForTC{genesis: genesisHeader}

	signedTimeoutObj := types.TimeoutSigHash(&types.TimeoutForSign{Round: 0, GapNumber: 0})
	sig0, err := crypto.Sign(signedTimeoutObj[:], allKeys[0])
	if err != nil {
		t.Fatalf("Sign0: %v", err)
	}

	tc := &types.TimeoutCert{
		Round:      0,
		Signatures: []types.Signature{sig0},
		GapNumber:  0,
	}

	if err := eng.verifyTC(chain, tc); err != utils.ErrInvalidTCSignatures {
		t.Fatalf("1-of-3 should return ErrInvalidTCSignatures, got: %v", err)
	}
	t.Log("PASS: 1-of-3 correctly rejected (below 2/3 threshold)")
}
