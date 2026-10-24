// Copyright (c) 2026 XDC Network
// SYNC-06 — BeaconSync QC skip for headers below the fast-sync pivot (refs #1859)
//
// Problem: after #1847 fixed the masternode membership issue, live net2040 tests
// showed every backfill attempt failing with "gap number mismatch expected=X got=Y".
// The synthetic epoch-switch block info used for gap-number computation is wrong
// when the real epoch-N switch block is not yet in the local DB. When the pivot
// is at block P and BeaconSync backtracks from P to genesis, every header in
// (0, P) can trigger a missing-epoch-switch-info error.
//
// Fix: register the dynamic-pivot anchor callback on the clean fast-sync path
// (eth/backend.go). Once a pivot is chosen, inCheckpointCatchup returns true for
// ALL blocks in [0, pivot+2*epoch]. verifyHeader skips verifyQC for those blocks;
// the downloader already authenticates the range via parent-hash chain from pivot.
//
// Tests:
//   SYNC-06a: inCheckpointCatchup with a high-anchor pivot covers all blocks from
//             0 up to anchor+2*epoch, and returns false above the window.
//   SYNC-06b: inCheckpointCatchup returns false when no anchor is installed (nil
//             chain), confirming verifyQC runs unconditionally on full sync / clean
//             fast-sync before the downloader selects a pivot.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/... -run TestSync06 -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// mockPivotChain simulates a blockchain.BlockChain that has a fast-sync pivot
// anchor installed at the given block number. Used by SYNC-06 tests.
type mockPivotChain struct {
	pivotNum  uint64
	pivotHash common.Hash
}

func (m *mockPivotChain) GetTrustedCheckpointAnchor() (uint64, common.Hash, bool) {
	return m.pivotNum, m.pivotHash, m.pivotNum != 0
}

// consensus.ChainReader / consensus.ChainHeaderReader stubs (no-ops for this chain).
func (m *mockPivotChain) Config() *params.ChainConfig                     { return nil }
func (m *mockPivotChain) CurrentHeader() *types.Header                    { return nil }
func (m *mockPivotChain) GetHeader(h common.Hash, n uint64) *types.Header { return nil }
func (m *mockPivotChain) GetHeaderByNumber(n uint64) *types.Header        { return nil }
func (m *mockPivotChain) GetHeaderByHash(h common.Hash) *types.Header     { return nil }
func (m *mockPivotChain) GetTd(h common.Hash, n uint64) *big.Int          { return nil }
func (m *mockPivotChain) GetBlock(h common.Hash, n uint64) *types.Block   { return nil }

// TestSync06a_InCheckpointCatchup_BelowPivotCoversFullRange verifies that once
// a fast-sync pivot is installed, inCheckpointCatchup returns true for ALL blocks
// at or below anchor+2*epoch. This is the core invariant that allows BeaconSync
// to backfill the entire pre-pivot range without triggering epoch-switch lookups
// that would fail (the switch blocks have not been downloaded yet).
func TestSync06a_InCheckpointCatchup_BelowPivotCoversFullRange(t *testing.T) {
	const epoch = uint64(900)
	const gap = uint64(450)
	const v2Switch = uint64(0)

	eng := &XDPoS_v2{
		config: &params.XDPoSConfig{
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

	// Simulate a high pivot (e.g. net2040 running at ~20,000 blocks, pivot ~18,000).
	const pivot = uint64(18000)
	chain := &mockPivotChain{pivotNum: pivot, pivotHash: common.HexToHash("0xfeedbeef")}

	window := epoch * 2          // 1800
	upperBound := pivot + window // 19800
	midPivot := pivot / 2        // 9000 — well below pivot

	cases := []struct {
		name  string
		block uint64
		want  bool
	}{
		{"genesis", 0, true},
		{"block-1", 1, true},
		{"mid-pivot", midPivot, true},
		{"pivot-minus-1", pivot - 1, true},
		{"pivot", pivot, true},
		{"pivot-plus-epoch", pivot + epoch, true},
		{"upper-bound", upperBound, true},
		{"upper-bound+1", upperBound + 1, false},
		{"pivot+2*epoch+100", upperBound + 100, false},
	}
	for _, tc := range cases {
		got := eng.inCheckpointCatchup(chain, tc.block)
		if got != tc.want {
			t.Errorf("[SYNC-06a] %s: inCheckpointCatchup(block=%d) = %v, want %v (pivot=%d, epoch=%d)",
				tc.name, tc.block, got, tc.want, pivot, epoch)
		}
	}
	t.Logf("PASS SYNC-06a: all blocks in [0, %d] in catchup window; [%d, ∞) outside (pivot=%d, epoch=%d)",
		upperBound, upperBound+1, pivot, epoch)
}

// TestSync06b_InCheckpointCatchup_NoAnchorReturnsFalse verifies that without a
// pivot anchor (full sync, or clean fast-sync before the downloader picks a pivot),
// inCheckpointCatchup always returns false. This ensures verifyQC runs
// unconditionally — no relaxation outside the BeaconSync backfill window.
func TestSync06b_InCheckpointCatchup_NoAnchorReturnsFalse(t *testing.T) {
	eng := newA975Engine(900, 900, 450)

	// nil chain → type assertion for ckptAware fails → false.
	noAnchorCases := []uint64{0, 1, 900, 1800, 18000, 56_828_700}
	for _, blockNum := range noAnchorCases {
		got := eng.inCheckpointCatchup(nil, blockNum)
		if got {
			t.Errorf("[SYNC-06b] inCheckpointCatchup(nil, block=%d) = true — want false (no anchor)", blockNum)
		}
	}

	// chain with anchor=0 (no anchor installed) → active=false → false.
	inactiveChain := &mockPivotChain{pivotNum: 0}
	for _, blockNum := range noAnchorCases {
		got := eng.inCheckpointCatchup(inactiveChain, blockNum)
		if got {
			t.Errorf("[SYNC-06b] inCheckpointCatchup(anchor=0, block=%d) = true — want false (no anchor)", blockNum)
		}
	}
	t.Log("PASS SYNC-06b: inCheckpointCatchup returns false with no anchor (full sync / pre-pivot clean fast-sync)")
}
