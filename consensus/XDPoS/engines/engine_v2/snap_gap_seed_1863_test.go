// Copyright (c) 2026 XDC Network
// Regression test for issue #1863 (review SYNC-11): a fresh snap node hits one
// BAD BLOCK at the first V2 epoch switch after its pivot because the gap-block
// masternode snapshot was never built. The gap block is at or below the snap
// state-download pivot, so its state was never present and the snapshot was
// never stored. The fix (seedSnapPivotGapSnapshots in eth/sync_xdc.go) seeds
// the snapshot from the epoch-switch header's Validators field at snap
// completion — the A.97.4 path already used by fast sync (#1862).
//
// This test verifies the core mechanism: calling UpdateMasternodesWithPenalties
// with the epoch-switch header's masternodes (as seedSnapPivotGapSnapshots
// does) causes GetSnapshot to return the correct epoch-118 masternodes for
// gap block 105750, rather than the wrong epoch-119 masternodes that
// findEpochSwitchAfter would find when only above-pivot headers are in the DB.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/... -run TestSnapGapSeedSYNC11 -count=1 -v

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestSnapGapSeedSYNC11 is the regression test for issue #1863 / review SYNC-11.
//
// Scenario (epoch=900, gap=450, V2 from genesis):
//
//	State pivot  = 107086  (= head − 64, the downloader's state-download pivot)
//	Gap block    = 105750  (= epoch-118 checkpoint 106200 − gap 450; strictly
//	                         below pivot → state absent; snapshot never built)
//	Epoch-118 switch at 106200: below pivot → NOT in local DB at SyncCompleted
//	Epoch-119 switch at 107100: above pivot → IN local DB (skeleton range)
//
// Without the SYNC-11 fix, findEpochSwitchAfter(chain, 105750) walks forward
// through only the above-pivot headers and finds the epoch-119 switch at 107100
// instead of the correct epoch-118 switch at 106200, seeding wrong masternodes
// for epoch-118 blocks. With the fix, seedSnapPivotGapSnapshots pre-seeds gap
// 105750 from the epoch-118 switch header (peer-fetched when below pivot), so
// GetSnapshot returns the correct masternodes.
//
// The test FAILS on the final assertion if the seeding call (simulating
// seedSnapPivotGapSnapshots) is removed.
func TestSnapGapSeedSYNC11(t *testing.T) {
	const (
		epoch    = 900
		gap      = 450
		pivotNum = 107086 // state-download pivot (head − 64)
		gapBlock = 105750 // gap for epoch-118 (106200 − 450); below pivot
		sw118Num = 106200 // epoch-118 switch: below pivot, not in DB
		sw119Num = 107100 // epoch-119 switch: above pivot, in DB
	)

	// Two distinct masternode sets so a mix-up is clearly detectable.
	epoch118MNs := []common.Address{
		common.HexToAddress("0x1800000000000000000000000000000000000001"),
		common.HexToAddress("0x1800000000000000000000000000000000000002"),
	}
	epoch119MNs := []common.Address{
		common.HexToAddress("0x1900000000000000000000000000000000000001"),
	}

	// Build synthetic epoch-switch headers with properly encoded V2 Extra fields
	// so IsEpochSwitch and GetMasternodesFromEpochSwitchHeader work correctly.
	// Rounds use epoch multiples so IsEpochSwitch(parentRound < epochStartRound) fires.
	sw118 := makeRoundEpochSwitchHeader(t, sw118Num,
		types.Round(118*epoch), types.Round(117*epoch), epoch118MNs)
	sw119 := makeRoundEpochSwitchHeader(t, sw119Num,
		types.Round(119*epoch), types.Round(118*epoch), epoch119MNs)
	gapHeader := &types.Header{Number: big.NewInt(gapBlock), Difficulty: big.NewInt(1)}

	// chainAbovePivot simulates the snap node's DB immediately after
	// SyncCompleted: only the epoch-119 switch (above pivot) is present.
	// The epoch-118 switch (below pivot) must be peer-fetched.
	chainAbovePivot := &mockChain1330{byNumber: map[uint64]*types.Header{
		sw119Num: sw119,
		gapBlock: gapHeader,
	}}

	// --- Without the fix ---
	// Create a fresh engine (no cached snapshots) and call GetSnapshot for the
	// gap block. findEpochSwitchAfter will walk forward and find the epoch-119
	// switch (the only one in the "DB"), seeding WRONG masternodes.
	engBefore, _ := newA894Engine(t)
	snapBefore, errBefore := engBefore.GetSnapshot(chainAbovePivot, gapHeader)
	if errBefore != nil {
		// Unexpected: we should get a snapshot (seeded from epoch-119), not an error.
		t.Logf("without fix: GetSnapshot returned error (acceptable): %v", errBefore)
	} else if len(snapBefore.NextEpochCandidates) > 0 {
		// Document the bug: the masternodes come from epoch-119, not epoch-118.
		if snapBefore.NextEpochCandidates[0] == epoch119MNs[0] {
			t.Logf("without fix (bug reproduced): gap 105750 snapshot has epoch-119 MN %s (should be epoch-118) [SYNC-11]",
				snapBefore.NextEpochCandidates[0])
		}
	}

	// --- With the fix (simulating seedSnapPivotGapSnapshots) ---
	// Create a second fresh engine and seed gap 105750 from the epoch-118
	// switch header (peer-fetched in the real code path). This is exactly what
	// seedSnapPivotGapSnapshots in eth/sync_xdc.go does at snap SyncCompleted.
	engAfter, _ := newA894Engine(t)

	// chainWithPeer extends chainAbovePivot to also return the epoch-118 switch
	// header (as if fetched from a peer by fetchOneHeaderByNumber).
	chainWithPeer := &mockChain1330{byNumber: map[uint64]*types.Header{
		sw118Num: sw118,
		sw119Num: sw119,
		gapBlock: gapHeader,
	}}

	// Seed: this is the A.97.4 path used by seedSnapPivotGapSnapshots.
	mns := engAfter.GetMasternodesFromEpochSwitchHeader(chainWithPeer, sw118)
	if len(mns) == 0 {
		t.Fatal("GetMasternodesFromEpochSwitchHeader returned empty for epoch-118 switch")
	}
	if err := engAfter.UpdateMasternodesWithPenalties(chainWithPeer, gapHeader, mns, nil); err != nil {
		t.Fatalf("UpdateMasternodesWithPenalties (seed): %v", err)
	}
	// Flush the in-memory snapshot cache to force a DB read (as would happen
	// after a process restart or when the cache evicts the entry).
	engAfter.snapshots.Remove(gapHeader.Hash())

	// Verify: GetSnapshot must now return epoch-118 masternodes.
	// This assertion FAILS if the seeding above is removed (the fix is omitted).
	snapAfter, err := engAfter.GetSnapshot(chainAbovePivot, gapHeader)
	if err != nil {
		t.Fatalf("with fix: GetSnapshot returned error: %v — snap gap snapshot was not seeded (SYNC-11)", err)
	}
	if len(snapAfter.NextEpochCandidates) != len(epoch118MNs) {
		t.Fatalf("with fix: want %d epoch-118 MNs, got %d: %v",
			len(epoch118MNs), len(snapAfter.NextEpochCandidates), snapAfter.NextEpochCandidates)
	}
	if snapAfter.NextEpochCandidates[0] != epoch118MNs[0] {
		t.Fatalf("with fix: want first MN %s (epoch-118), got %s — "+
			"wrong epoch-switch header seeded gap 105750 (SYNC-11)",
			epoch118MNs[0], snapAfter.NextEpochCandidates[0])
	}
}
