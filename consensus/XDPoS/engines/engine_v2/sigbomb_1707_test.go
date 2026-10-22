// Copyright (c) 2026 XDC Network
// CertiK P3 / #1707: QC/TC signature-bomb DoS tests.
//
// Before this fix, RecoverUniqueSigners spawned one goroutine per signature
// before any threshold check, and VerifySyncInfoMessage applied no per-message
// signature count limit. A peer could craft a SyncInfo with ~125 k signatures
// (within the 10 MB message cap) and force the node to ecrecover all of them.
//
// Fix: (1) bounded worker pool in RecoverUniqueSigners (min(GOMAXPROCS, len)
// goroutines); (2) maxPeerSigCap pre-check in VerifySyncInfoMessage before
// any ecrecover is invoked.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/... -run TestSigBomb -count=1

package engine_v2

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"runtime"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestSigBomb_PeerQC_Rejected_BeforeRecovery verifies that VerifySyncInfoMessage
// rejects a SyncInfo whose QC carries more signatures than maxPeerSigCap
// (currently 150, comfortably above MaxMasternodesV2=108) before invoking any
// ecrecover.  Evidence: the function returns an error containing "exceeds peer
// cap" without calling verifyQC (which would need epoch info and a real chain).
func TestSigBomb_PeerQC_Rejected_BeforeRecovery(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.isInitialized = true

	// Build a SyncInfo whose QC has maxPeerSigCap+1 dummy signatures.
	// These are obviously invalid (65 zero bytes each); if the cap is enforced
	// first the error is "exceeds peer cap", not an ecrecover/epoch error.
	bombSigs := make([]types.Signature, maxPeerSigCap+1)
	for i := range bombSigs {
		bombSigs[i] = make(types.Signature, 65)
	}

	blockInfo := &types.BlockInfo{
		Hash:   common.Hash{0x01},
		Round:  types.Round(1),
		Number: big.NewInt(1),
	}
	syncInfo := &types.SyncInfo{
		HighestQuorumCert: &types.QuorumCert{
			ProposedBlockInfo: blockInfo,
			Signatures:        bombSigs,
			GapNumber:         0,
		},
		HighestTimeoutCert: &types.TimeoutCert{
			Round:      types.Round(0),
			Signatures: nil,
		},
	}

	// Fresh engine has highestQuorumCert.Round=0; syncInfo.HighestQuorumCert.Round=1
	// so the "not newer" early-out is skipped.
	chain := &mockChainForA894{}
	ok, err := eng.VerifySyncInfoMessage(chain, syncInfo)
	if err == nil {
		t.Fatalf("expected error for %d-sig QC from peer, got ok=%v", len(bombSigs), ok)
	}
	if !strings.Contains(err.Error(), "exceeds peer cap") {
		t.Fatalf("expected 'exceeds peer cap' error, got: %v", err)
	}
	if ok {
		t.Fatalf("VerifySyncInfoMessage returned ok=true for bomb QC")
	}
	t.Logf("PASS: %d-sig QC rejected before recovery with: %v", len(bombSigs), err)
}

// TestSigBomb_PeerTC_Rejected_BeforeRecovery verifies the TC intake cap in
// VerifySyncInfoMessage: a TC with maxPeerSigCap+1 signatures is rejected with
// "exceeds peer cap" before verifyTC is called.
func TestSigBomb_PeerTC_Rejected_BeforeRecovery(t *testing.T) {
	eng, db := newA894Engine(t)
	eng.isInitialized = true

	// Build a valid 1-sig QC so the QC cap doesn't fire first.
	// Use the same snapshot infrastructure as the malleability tests.
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
		Round:  types.Round(1001),
		Number: big.NewInt(int64(gapBlock)),
	}
	// 3-of-3 QC (all signers, threshold met).
	qcSigs := makeQCSignatures(t, proposedBlock, gapNum, allKeys)
	// Store epoch switch info so verifyQC can find the masternode set.
	switchInfo := &types.BlockInfo{Hash: gapHash, Round: 1001, Number: big.NewInt(int64(gapBlock))}
	epochInfo := &types.EpochSwitchInfo{
		Masternodes:          allAddrs,
		MasternodesLen:       len(allAddrs),
		EpochSwitchBlockInfo: switchInfo,
	}
	eng.epochSwitches.Add(gapHash, epochInfo)
	eng.epochSwitches.Add(gapHeader.Hash(), epochInfo)

	// TC with maxPeerSigCap+1 dummy sigs — should be rejected before verifyTC.
	bombSigs := make([]types.Signature, maxPeerSigCap+1)
	for i := range bombSigs {
		bombSigs[i] = make(types.Signature, 65)
	}
	syncInfo := &types.SyncInfo{
		HighestQuorumCert: &types.QuorumCert{
			ProposedBlockInfo: proposedBlock,
			Signatures:        qcSigs,
			GapNumber:         gapNum,
		},
		HighestTimeoutCert: &types.TimeoutCert{
			Round:      types.Round(1),
			Signatures: bombSigs,
			GapNumber:  0,
		},
	}

	chain := &mockChainForA894{headers: map[common.Hash]*types.Header{gapHash: gapHeader}}
	_, err := eng.VerifySyncInfoMessage(chain, syncInfo)
	if err == nil {
		t.Fatalf("expected error for %d-sig TC from peer, got nil", len(bombSigs))
	}
	if !strings.Contains(err.Error(), "exceeds peer cap") {
		t.Fatalf("expected 'exceeds peer cap' error, got: %v", err)
	}
	t.Logf("PASS: %d-sig TC rejected before recovery with: %v", len(bombSigs), err)
}

// TestSigBomb_RecoverUniqueSigners_BoundedPool verifies that RecoverUniqueSigners
// with 50 000 invalid signatures does not permanently increase the goroutine
// count by ~50 000. Since wg.Wait() is called before return, all worker
// goroutines are done by the time the function returns; the delta should be
// at most GOMAXPROCS + a small constant for background goroutines.
func TestSigBomb_RecoverUniqueSigners_BoundedPool(t *testing.T) {
	const n = 50_000
	hash := common.HexToHash("0xdeadbeef")

	// 65-zero-byte signatures: ecrecover will fail immediately on all of them.
	sigs := make([]types.Signature, n)
	for i := range sigs {
		sigs[i] = make(types.Signature, 65)
	}

	before := runtime.NumGoroutine()
	// Error expected (ecrecover fails on zero-byte sig); that's fine.
	_, _, _ = RecoverUniqueSigners(hash, sigs)
	after := runtime.NumGoroutine()

	// Allow GOMAXPROCS workers + 20 for background goroutines fluctuation.
	// Before the fix this would have spawned 50 000 goroutines.
	maxAllowed := before + runtime.GOMAXPROCS(0) + 20
	if after > maxAllowed {
		t.Errorf("goroutine spike: before=%d after=%d maxAllowed=%d GOMAXPROCS=%d",
			before, after, maxAllowed, runtime.GOMAXPROCS(0))
	}
	t.Logf("PASS: goroutine count before=%d after=%d (GOMAXPROCS=%d)", before, after, runtime.GOMAXPROCS(0))
}

// TestSigBomb_HeaderQC_NotCapped verifies that the header verification path
// (verifyQC called directly, not via VerifySyncInfoMessage) still accepts a
// QC whose signature count exceeds maxPeerSigCap when the unique-signer count
// meets the threshold. Historical canonical headers must always be verifiable.
func TestSigBomb_HeaderQC_NotCapped(t *testing.T) {
	eng, db := newA894Engine(t)

	// Use 3 masternodes, each signing once (unique, low-S). Then add many
	// duplicate copies so len(Signatures) >> maxPeerSigCap.
	// After RecoverUniqueSigners dedupes them we get 3 unique signers,
	// which meets the 2/3 threshold (3 >= ceil(3*2/3) = 2).
	allKeys, allAddrs := genNKeys(t, 3)
	const gapBlock = 82934551 // different from other tests to avoid cache collision
	const epoch, gap = uint64(900), uint64(450)
	gapNum := gapNumForBlock(gapBlock, epoch, gap)
	gapHeader, gapHash := makeGapHeader(t, gapBlock, 1002, gapNum)

	snap := newSnapshotWithPenalties(gapBlock, gapHash, allAddrs, nil)
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	proposedBlock := &types.BlockInfo{
		Hash:   gapHash,
		Round:  types.Round(1003),
		Number: big.NewInt(int64(gapBlock)),
	}
	realSigs := makeQCSignatures(t, proposedBlock, gapNum, allKeys) // 3 unique low-S sigs

	// Build a list with many duplicate copies of the 3 real sigs so the total
	// length exceeds maxPeerSigCap. RecoverUniqueSigners must dedup back to 3.
	const repeats = 60 // 3 * 60 = 180 > maxPeerSigCap=150
	dupSigs := make([]types.Signature, 0, len(realSigs)*repeats)
	for i := 0; i < repeats; i++ {
		dupSigs = append(dupSigs, realSigs...)
	}
	if len(dupSigs) <= maxPeerSigCap {
		t.Fatalf("test setup: need len(dupSigs) > %d, got %d", maxPeerSigCap, len(dupSigs))
	}

	qc := &types.QuorumCert{
		ProposedBlockInfo: proposedBlock,
		Signatures:        dupSigs,
		GapNumber:         gapNum,
	}

	chain := &mockChainForA894{}
	// verifyQC (header path) must NOT apply the peer cap and must pass.
	if err := eng.verifyQC(chain, qc, gapHeader, nil); err != nil {
		t.Fatalf("FAIL: header-path verifyQC with dup sigs > maxPeerSigCap should still pass, got: %v", err)
	}
	t.Logf("PASS: header QC with %d total sigs (%d unique) verified without cap", len(dupSigs), len(realSigs))
}

// TestSigBomb_NonMemberStopsRecovery: with a member set, a certificate made of
// signatures from non-masternode keys fails with errSignerNotMasternode, the
// same outcome verifyQC/verifyTC reach after recovery, so the accepted set is
// unchanged. Byte-identical repeats count as duplicates.
func TestSigBomb_NonMemberStopsRecovery(t *testing.T) {
	hash := common.HexToHash("0x1707")
	member, _ := crypto.GenerateKey()
	memberAddr := crypto.PubkeyToAddress(member.PublicKey)
	sign := func(k *ecdsa.PrivateKey) types.Signature {
		s, err := crypto.Sign(hash.Bytes(), k)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	good := sign(member)

	// Member signature repeated byte-for-byte: one unique signer, the rest duplicates.
	sigs := []types.Signature{good, good, good}
	uniq, dups, err := recoverUniqueSigners(hash, sigs, []common.Address{memberAddr})
	if err != nil || len(uniq) != 1 || len(dups) != 2 {
		t.Fatalf("repeated member sig: uniq=%d dups=%d err=%v, want 1, 2, nil", len(uniq), len(dups), err)
	}

	// Non-member signatures: rejected with a member set, accepted without one.
	bomb := []types.Signature{good}
	for i := 0; i < 500; i++ {
		k, _ := crypto.GenerateKey()
		bomb = append(bomb, sign(k))
	}
	if _, _, err := recoverUniqueSigners(hash, bomb, []common.Address{memberAddr}); !errors.Is(err, errSignerNotMasternode) {
		t.Fatalf("non-member bomb: err=%v, want errSignerNotMasternode", err)
	}
	if uniq, _, err := RecoverUniqueSigners(hash, bomb); err != nil || len(uniq) != len(bomb) {
		t.Fatalf("public RecoverUniqueSigners changed: uniq=%d err=%v", len(uniq), err)
	}
}
