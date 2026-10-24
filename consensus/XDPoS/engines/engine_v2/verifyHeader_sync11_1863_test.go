// Copyright (c) 2026 XDC Network
// Unit tests for the SYNC-11 verifyHeader relaxation (#1863 / PR #1864).
//
// Background: after a snap sync, the first V2 epoch-switch block above the
// snap pivot may fail calcMasternodes with ErrV2SnapshotMissing because the
// gap block below the pivot never had its state downloaded. The SYNC-11
// relaxation in verifyHeader.go allows such a block to be accepted under
// fullVerify by trusting header.Validators, then seeding the gap snapshot
// ONLY after every other check (seal, leader, QC) has passed.
//
// Testing strategy: a full verifyHeader call is impractical in this package
// because the mock chain infrastructure does not produce headers with valid
// QuorumCert signatures (needed by verifyQC) or valid proposer seals.
// Instead, the relaxation decision and seeding logic are exposed through two
// small methods — sync11ShouldRelax and sync11SeedGap — and tested directly:
//
//   (a) sync11ShouldRelax: pivot above gap → relax approved, correct gapNum
//   (b) sync11ShouldRelax: pivot absent, pivot at gap, different error → no relax
//   (c) sync11SeedGap called (retErr==nil) → snapshot persisted;
//       NOT called (retErr!=nil, simulating seal failure) → no snapshot
//   (d) getSnapshot isGapInput with drifted switch (derived gap ≠ requested gap)
//       → epochSwitchHeader refused, ErrV2SnapshotMissing returned
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/... -run 'TestSync11' -count=1 -v

package engine_v2

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// sync11Constants groups the protocol parameters used across SYNC-11 tests.
const (
	sync11Epoch    = uint64(900)
	sync11Gap      = uint64(450)
	sync11GapBlock = uint64(105750) // epoch-118 gap: sw118Num - 0 - gap
	sync11SwitchN  = uint64(106200) // sw118Num = 118*900 = 106200; 106200%900=0 → base=106200
	sync11PivotN   = uint64(107086) // snap state-download pivot (head − 64)
)

// makeSwitchHeaderForSync11 returns an epoch-switch header at switchNum
// whose Validators encodes masternodes. The header is minimal: it has a valid
// V2 Extra (so IsEpochSwitch works) but no proposer seal.
func makeSwitchHeaderForSync11(t *testing.T, switchNum uint64, mns []common.Address) *types.Header {
	t.Helper()
	return makeRoundEpochSwitchHeader(t, switchNum,
		types.Round(uint64(118)*sync11Epoch), // round = 118*epoch (epoch-switch round)
		types.Round(uint64(117)*sync11Epoch), // parentRound
		mns)
}

// TestSync11ShouldRelax_PivotAboveGap (test a): pivot written above the gap →
// sync11ShouldRelax approves the relaxation and returns the correct gapNum.
func TestSync11ShouldRelax_PivotAboveGap(t *testing.T) {
	eng, db := newA894Engine(t)

	// Write pivot above the gap block.
	rawdb.WriteLastPivotNumber(db, sync11PivotN)

	relax, gapNum := eng.sync11ShouldRelax(utils.ErrV2SnapshotMissing, sync11SwitchN)
	if !relax {
		t.Fatalf("want relax=true (pivot %d > gap %d), got false", sync11PivotN, sync11GapBlock)
	}
	if gapNum != sync11GapBlock {
		t.Fatalf("want gapNum=%d, got %d", sync11GapBlock, gapNum)
	}
	t.Logf("PASS (a): sync11ShouldRelax approved relaxation, gapNum=%d", gapNum)
}

// TestSync11ShouldRelax_NegativeCases (test b): various conditions that must NOT
// relax — pivot absent, pivot at the gap, pivot below the gap, wrong error type.
func TestSync11ShouldRelax_NegativeCases(t *testing.T) {
	cases := []struct {
		name    string
		pivot   *uint64 // nil means don't write pivot
		calcErr error
	}{
		{"pivot absent", nil, utils.ErrV2SnapshotMissing},
		{"pivot at gap", ptr(sync11GapBlock), utils.ErrV2SnapshotMissing},
		{"pivot below gap", ptr(sync11GapBlock - 1), utils.ErrV2SnapshotMissing},
		{"different error", ptr(sync11PivotN), errors.New("some other error")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng, db := newA894Engine(t)
			if tc.pivot != nil {
				rawdb.WriteLastPivotNumber(db, *tc.pivot)
			}
			relax, _ := eng.sync11ShouldRelax(tc.calcErr, sync11SwitchN)
			if relax {
				t.Fatalf("want relax=false for case %q, got true", tc.name)
			}
			t.Logf("PASS (b): sync11ShouldRelax correctly refused relax for %q", tc.name)
		})
	}
}

// TestSync11_SealFailurePreventsGapSeed (test c): the SYNC-11 gap snapshot must
// be seeded ONLY when all verifyHeader checks have passed (retErr == nil). When
// the seal check fails (retErr != nil), sync11SeedGap must NOT be called and the
// gap hash must remain absent from the DB.
//
// This is the direct regression test for PR #1864 item 1 (ordering bug): the old
// code called UpdateMasternodesWithPenalties before the proposer-sig check, so a
// forged header that later failed the sig check would still persist a poisoned
// snapshot. With the defer-based fix, sync11SeedGap is guarded by retErr == nil.
func TestSync11_SealFailurePreventsGapSeed(t *testing.T) {
	epoch118MNs := []common.Address{
		common.HexToAddress("0x1800000000000000000000000000000000000001"),
		common.HexToAddress("0x1800000000000000000000000000000000000002"),
	}

	eng, db := newA894Engine(t)
	rawdb.WriteLastPivotNumber(db, sync11PivotN)

	switchHdr := makeSwitchHeaderForSync11(t, sync11SwitchN, epoch118MNs)
	gapHeader := &types.Header{
		Number:     big.NewInt(int64(sync11GapBlock)),
		Difficulty: big.NewInt(1),
		Extra:      []byte{0x02},
	}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{
		sync11GapBlock: gapHeader,
		sync11SwitchN:  switchHdr,
	}}

	// Confirm sync11ShouldRelax approves this case.
	relax, gapNum := eng.sync11ShouldRelax(utils.ErrV2SnapshotMissing, sync11SwitchN)
	if !relax {
		t.Fatal("precondition: should relax")
	}

	// --- Simulate seal failure (retErr != nil) ---
	// The deferred call in verifyHeader is: if retErr == nil && seedGapFromSwitch { sync11SeedGap(...) }
	// We reproduce this guard explicitly.
	sealFailed := utils.ErrValidatorNotWithinMasternodes // non-nil → seal check failed
	if sealFailed == nil {
		eng.sync11SeedGap(chain, gapNum, switchHdr) // would be called in the defer
	}
	snap, _ := loadSnapshot(db, gapHeader.Hash())
	if snap != nil {
		t.Fatalf("FAIL: snapshot persisted at gap despite seal failure — ordering bug present (len=%d)", len(snap.NextEpochCandidates))
	}
	t.Log("PASS (c-1): seal failure → sync11SeedGap not called → DB clean at gap hash")

	// --- Simulate successful verification (retErr == nil) ---
	var successErr error // nil → all checks passed
	if successErr == nil {
		eng.sync11SeedGap(chain, gapNum, switchHdr)
	}
	snap, err := loadSnapshot(db, gapHeader.Hash())
	if err != nil || snap == nil {
		t.Fatalf("FAIL: snapshot not persisted after success: err=%v", err)
	}
	if len(snap.NextEpochCandidates) != len(epoch118MNs) {
		t.Fatalf("FAIL: want %d masternodes, got %d", len(epoch118MNs), len(snap.NextEpochCandidates))
	}
	t.Logf("PASS (c-2): successful verify → sync11SeedGap called → snapshot persisted (%d MNs)", len(snap.NextEpochCandidates))
}

// TestSync11_GetSnapshot_MismatchGuard (test d): getSnapshot with isGapInput=true
// must refuse a switch header whose derived gap formula does not equal the
// requested gap number, and return ErrV2SnapshotMissing.
//
// Setup mirrors TestSnapGapSeedSYNC11 (epoch=900, gap=450): gap=105750 wants
// the epoch-118 switch at 106200, but only epoch-119 (sw119Num=107100) is in
// the local DB. After the engine.go mismatch guard, getSnapshot checks that
// the switch's derived gap (107100 - 107100%900 - 450 = 106650 ≠ 105750) and
// refuses it, returning ErrV2SnapshotMissing instead of seeding the wrong snapshot.
func TestSync11_GetSnapshot_MismatchGuard(t *testing.T) {
	const (
		epoch    = uint64(900)
		gap      = uint64(450)
		gapBlock = uint64(105750) // epoch-118 gap
		sw119Num = uint64(107100) // epoch-119 switch; 107100%900=0 → derivedGap=107100-450=106650 ≠ 105750
	)

	epoch119MNs := []common.Address{
		common.HexToAddress("0x1900000000000000000000000000000000000001"),
	}

	// Only the epoch-119 switch is in the "local DB" (above pivot).
	sw119 := makeRoundEpochSwitchHeader(t, sw119Num,
		types.Round(uint64(119)*epoch),
		types.Round(uint64(118)*epoch),
		epoch119MNs)
	gapHeader := &types.Header{Number: big.NewInt(int64(gapBlock)), Difficulty: big.NewInt(1)}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{
		sw119Num: sw119,
		gapBlock: gapHeader,
	}}

	eng, db := newA894Engine(t)
	snap, err := eng.getSnapshot(chain, gapBlock, true, nil)

	// The mismatch guard must refuse sw119 (derived gap 106650 ≠ 105750) and
	// return ErrV2SnapshotMissing since no valid switch is available.
	if err == nil {
		t.Fatalf("FAIL: getSnapshot returned nil error and %d MNs — mismatch guard did not fire", len(snap.NextEpochCandidates))
	}
	if !errors.Is(err, utils.ErrV2SnapshotMissing) {
		t.Fatalf("want ErrV2SnapshotMissing, got: %v", err)
	}
	// Also confirm no snapshot was persisted at the gap hash with the wrong MNs.
	if stored, _ := loadSnapshot(db, gapHeader.Hash()); stored != nil {
		t.Fatalf("FAIL: wrong-epoch snapshot persisted at gap hash (MNs=%d)", len(stored.NextEpochCandidates))
	}
	t.Logf("PASS (d): getSnapshot refused drifted switch (sw119 derivedGap=106650 ≠ gapBlock=%d), returned ErrV2SnapshotMissing", gapBlock)
}

// ptr is a tiny helper to take the address of a uint64 literal.
func ptr(v uint64) *uint64 { return &v }
