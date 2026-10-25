// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Tests for maybeRollbackOnUnknownAncestor (#1667):
//   - Items 2+3: positive-fork-evidence check (triggering peer + ranked-peer
//     fallback; only roll back on confirmed hash divergence)
//   - Item 1: finality clamp (clamp rollback target to >= committed block)
//   - Accounting fix: stuckRolledBack counts actual distance, resets on progress

// NOTE on SetHead in test chains: on an in-memory ethash blockchain without
// persistent state tries, blockchain.SetHead always reverts to genesis because
// rewindHashHead keeps walking back until it finds a block with state available
// (only genesis qualifies in the hash-scheme test chain). Tests therefore
// verify which CODE PATH was taken — peer tainted, XdcLastForkRollbackUnix
// stamped, stuckRolledBack set — rather than the final head value.

package eth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

// primeStuck sets the xdcSyncer's stuck state to (stuckFrom, threshold-1)
// so that one more call to maybeRollbackOnUnknownAncestor hits the threshold.
// It also sets XdcForkRecovering=true to mirror the state after threshold-1
// accumulated failures (the function sets it from failure 2 onward).
func primeStuck(s *xdcSyncer, stuckFrom uint64) {
	s.stuckFrom = stuckFrom
	s.stuckFailures = xdcUnknownAncestorThreshold - 1
	core.XdcForkRecovering.Store(true)
	// Model a repeat episode: a single-peer fork at this head was already
	// deferred once, so these tests exercise the confirmed path.
	if cur := s.handler.chain.CurrentBlock(); cur != nil {
		s.singlePeerForkHead = cur.Hash()
	}
}

// forkHeaders returns a headersFor function that serves headers from chain
// (a pre-built slice of blocks indexed [0]=block1).
func forkHeaders(chain []*types.Block) func(uint64, int) []*types.Header {
	return func(origin uint64, amount int) []*types.Header {
		var out []*types.Header
		for num := origin; num >= 1 && int(num) <= len(chain) && len(out) < amount; num++ {
			out = append(out, chain[num-1].Header())
		}
		return out
	}
}

// noHeaders is a headersFor stub that always returns nil — models a peer that
// is behind our head or times out, producing an inconclusive fetch result.
var noHeaders = func(uint64, int) []*types.Header { return nil }

// rollbackEntered returns true when the rollback code path was taken: the peer
// was tainted, XdcLastForkRollbackUnix was stamped after beforeUnix, and
// stuckRolledBack is non-zero (accounting now tracks actual distance, not window).
func rollbackEntered(s *xdcSyncer, peerID string, beforeUnix int64) bool {
	return s.isPeerTainted(peerID) &&
		core.XdcLastForkRollbackUnix.Load() >= beforeUnix &&
		s.stuckRolledBack > 0
}

// ── Out-of-order / transient tests ────────────────────────────────────────────

// TestMaybeRollback_OutOfOrder_NoPeerDivergence verifies that when the peer's
// canonical header at our head height has the SAME hash as ours, no rollback is
// attempted even after reaching the threshold (#1667 items 2+3).
//
// FAILS on origin/main (peer tainted, stamp set).  PASSES with fix.
func TestMaybeRollback_OutOfOrder_NoPeerDivergence(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local+10)
	peer := n.addPeer(t, 1, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)

	if s.isPeerTainted(peer.ID()) {
		t.Error("peer was tainted despite no fork evidence — false rollback triggered (refs #1667)")
	}
	if ts := core.XdcLastForkRollbackUnix.Load(); ts >= beforeUnix {
		t.Errorf("XdcLastForkRollbackUnix stamped (%d) for a non-fork event (refs #1667)", ts)
	}
	if s.stuckFrom != 0 || s.stuckFailures != 0 || s.stuckRolledBack != 0 {
		t.Errorf("stuck state not cleared: stuckFrom=%d stuckFailures=%d stuckRolledBack=%d",
			s.stuckFrom, s.stuckFailures, s.stuckRolledBack)
	}
	if core.XdcForkRecovering.Load() {
		t.Error("XdcForkRecovering not cleared after transient ErrUnknownAncestor (refs #1667)")
	}
}

// ── Inconclusive peer tests (#1667 network-load fix) ─────────────────────────

// TestMaybeRollback_InconclusivePeer_NoOtherPeers verifies that when the
// triggering peer is inconclusive (returns nil headers) and there are no other
// ranked peers to consult, the rollback is skipped.
func TestMaybeRollback_InconclusivePeer_NoOtherPeers(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	// Triggering peer returns nil — models timeout / peer behind head.
	peer := n.addPeer(t, 1, noHeaders)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)

	if s.isPeerTainted(peer.ID()) {
		t.Error("peer tainted on inconclusive evidence with no other peers (refs #1667)")
	}
	if ts := core.XdcLastForkRollbackUnix.Load(); ts >= beforeUnix {
		t.Errorf("XdcLastForkRollbackUnix stamped (%d) on inconclusive evidence (refs #1667)", ts)
	}
	if core.XdcForkRecovering.Load() {
		t.Error("XdcForkRecovering not cleared when all evidence inconclusive (refs #1667)")
	}
}

// TestMaybeRollback_InconclusivePeer_AltPeerAgrees verifies that when the
// triggering peer is inconclusive but a second ranked peer agrees on our head
// hash, the rollback is skipped and stuck state is cleared.
func TestMaybeRollback_InconclusivePeer_AltPeerAgrees(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local+10)
	// Triggering peer: inconclusive.
	peer1 := n.addPeer(t, 1, noHeaders)
	// Alt peer: agrees on canonical head.
	_ = n.addPeer(t, 2, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer1.Peer, consensus.ErrUnknownAncestor)

	if s.isPeerTainted(peer1.ID()) {
		t.Error("triggering peer tainted when alt peer agreed on head (refs #1667)")
	}
	if ts := core.XdcLastForkRollbackUnix.Load(); ts >= beforeUnix {
		t.Errorf("XdcLastForkRollbackUnix stamped (%d) when alt peer agreed (refs #1667)", ts)
	}
	if s.stuckFrom != 0 || s.stuckFailures != 0 {
		t.Errorf("stuck state not cleared: stuckFrom=%d stuckFailures=%d", s.stuckFrom, s.stuckFailures)
	}
	if core.XdcForkRecovering.Load() {
		t.Error("XdcForkRecovering not cleared when alt peer agreed (refs #1667)")
	}
}

// TestMaybeRollback_InconclusivePeer_AltPeerDisagrees verifies that when the
// triggering peer is inconclusive but a second ranked peer disagrees (different
// hash at our head), the rollback proceeds with a clamped target.
//
// Setup: head=200, finalized=197, triggering peer inconclusive, alt peer forks.
// Unclamped target=72, clamped=197.  Actual distance=3.
func TestMaybeRollback_InconclusivePeer_AltPeerDisagrees(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	// finalized at head-3 (V2-shaped)
	n.chain.SetFinalized(n.remote[196].Header()) // block 197
	// Triggering peer: inconclusive.
	peer1 := n.addPeer(t, 1, noHeaders)
	// Alt peer: serves fork chain — hash at 200 differs.
	_ = n.addPeer(t, 2, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer1.Peer, consensus.ErrUnknownAncestor)

	// Rollback must have been entered (triggered peer tainted, stamp set).
	if !rollbackEntered(s, peer1.ID(), beforeUnix) {
		t.Errorf("rollback not entered when alt peer disagreed: tainted=%v stamp=%d stuckRolledBack=%d (refs #1667)",
			s.isPeerTainted(peer1.ID()), core.XdcLastForkRollbackUnix.Load(), s.stuckRolledBack)
	}
	// Accounting: actual distance = 200 - 197 = 3, not 128.
	const wantActual = local - 197 // 3
	if s.stuckRolledBack != wantActual {
		t.Errorf("stuckRolledBack=%d, want %d (actual distance not window) (refs #1667)",
			s.stuckRolledBack, wantActual)
	}
}

// ── Real fork tests ───────────────────────────────────────────────────────────

// TestMaybeRollback_RealFork_RollsBack verifies that a genuine competing-slot
// fork (peer head hash differs from ours at our head height) still triggers the
// rollback code path (#801/#951 behaviour preserved).
//
// Finalized=60, head=200, window=128 → unclamped target=72 > 60 → no clamping.
// Actual distance = 200 - 72 = 128 (coincides with window in unclamped case).
func TestMaybeRollback_RealFork_RollsBack(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	// Finalized well below rollback target so the clamp doesn't activate.
	n.chain.SetFinalized(n.remote[59].Header())
	// Peer serves the fork chain — hash differs at 200.
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)

	if !rollbackEntered(s, peer.ID(), beforeUnix) {
		t.Errorf("rollback not entered: tainted=%v stamp=%d stuckRolledBack=%d (refs #801)",
			s.isPeerTainted(peer.ID()), core.XdcLastForkRollbackUnix.Load(), s.stuckRolledBack)
	}
	// In the unclamped case actual == window == 128.
	if s.stuckRolledBack != xdcRollbackWindowInit {
		t.Errorf("stuckRolledBack=%d, want %d (unclamped actual==window) (refs #1667)",
			s.stuckRolledBack, xdcRollbackWindowInit)
	}
}

// ── Finality clamp tests ──────────────────────────────────────────────────────

// TestMaybeRollback_FinalityClamp_ClampsAndRollsBack verifies that when the
// rollback window would go below the finalized block, the target is clamped to
// finalized and the rollback PROCEEDS (not skipped) (#1667 item 1).
//
// head=200, finalized=150, window=128 → unclamped 72 → clamped 150.
// Actual distance = 200 - 150 = 50.
func TestMaybeRollback_FinalityClamp_ClampsAndRollsBack(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	n.chain.SetFinalized(n.remote[149].Header()) // block 150
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)

	if !rollbackEntered(s, peer.ID(), beforeUnix) {
		t.Errorf("rollback not entered after finality clamp: tainted=%v stamp=%d stuckRolledBack=%d (refs #1667)",
			s.isPeerTainted(peer.ID()), core.XdcLastForkRollbackUnix.Load(), s.stuckRolledBack)
	}
	// Accounting: actual = 200 - 150 = 50.
	const wantActual = uint64(200 - 150)
	if s.stuckRolledBack != wantActual {
		t.Errorf("stuckRolledBack=%d, want %d (actual distance, not window 128) (refs #1667)",
			s.stuckRolledBack, wantActual)
	}
}

// TestMaybeRollback_V2Shape_ClampToFinalized exercises the V2 scenario where
// the finalized tip trails head by only ~3 blocks (far less than the 128-block
// window), so the clamp fires on every trigger.
//
// head=200, finalized=197, window=128 → unclamped 72 → clamped 197.
// Actual distance = 200 - 197 = 3.
func TestMaybeRollback_V2Shape_ClampToFinalized(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	n.chain.SetFinalized(n.remote[196].Header()) // block 197
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)

	if !rollbackEntered(s, peer.ID(), beforeUnix) {
		t.Errorf("V2-shape rollback skipped: tainted=%v stamp=%d stuckRolledBack=%d — fork recovery disabled on V2 (refs #1667)",
			s.isPeerTainted(peer.ID()), core.XdcLastForkRollbackUnix.Load(), s.stuckRolledBack)
	}
	// Actual = 3, not 128.
	const wantActual = uint64(local - 197)
	if s.stuckRolledBack != wantActual {
		t.Errorf("stuckRolledBack=%d, want %d (actual distance, not window 128) (refs #1667)",
			s.stuckRolledBack, wantActual)
	}
}

// TestMaybeRollback_ClampLandsAtHead verifies that when clamping would produce
// target >= cur.Number (finalized == head), the rollback is skipped cleanly and
// XdcForkRecovering is released.
func TestMaybeRollback_ClampLandsAtHead(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	// Finalized at current head — clamp yields target == head.
	n.chain.SetFinalized(n.remote[local-1].Header()) // block 200
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)

	if s.isPeerTainted(peer.ID()) {
		t.Error("peer tainted when clamp yields target == head (refs #1667)")
	}
	if ts := core.XdcLastForkRollbackUnix.Load(); ts >= beforeUnix {
		t.Errorf("XdcLastForkRollbackUnix stamped (%d) when clamp yields target == head (refs #1667)", ts)
	}
	if s.stuckFrom != 0 || s.stuckFailures != 0 {
		t.Errorf("stuck state not cleared: stuckFrom=%d stuckFailures=%d", s.stuckFrom, s.stuckFailures)
	}
	if core.XdcForkRecovering.Load() {
		t.Error("XdcForkRecovering not cleared after clamp-at-head (refs #1667)")
	}
}

// ── Accounting tests (#1667 stuckRolledBack fix) ──────────────────────────────

// TestMaybeRollback_AccountsActualDistance verifies that after a clamped rollback
// stuckRolledBack equals the ACTUAL blocks rolled back, not the intended window.
// This prevents the cap from being reached after a few tiny V2 rollbacks.
//
// head=200, finalized=195, actual=5, window=128 → stuckRolledBack must be 5.
func TestMaybeRollback_AccountsActualDistance(t *testing.T) {
	const (
		local     = 200
		finalized = 195
	)
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	n.chain.SetFinalized(n.remote[finalized-1].Header())
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	primeStuck(s, uint64(local+1))
	s.maybeRollbackOnUnknownAncestor(uint64(local+1), peer.Peer, consensus.ErrUnknownAncestor)

	const wantActual = uint64(local - finalized) // 5
	if s.stuckRolledBack != wantActual {
		t.Errorf("stuckRolledBack=%d, want %d (must be actual distance, not window=%d) (refs #1667)",
			s.stuckRolledBack, wantActual, xdcRollbackWindowInit)
	}
	// After 5 clamped rollbacks the cumulative would be 25, far below the cap.
	const maxClamped = wantActual * 5
	if maxClamped >= xdcRollbackWindowMax {
		t.Errorf("5 clamped rollbacks would reach cap (%d >= %d) — accounting still wrong (refs #1667)",
			maxClamped, xdcRollbackWindowMax)
	}
}

// TestMaybeRollback_ProgressResetsWindow verifies that stuckRolledBack is reset
// when the chain advances past the head recorded at the last rollback, so the
// window escalation starts fresh after a successful recovery.
//
// We simulate this by pre-setting stuckLastRollbackHead=50 (below the local head
// of 200) and stuckRolledBack=512.  When maybeRollbackOnUnknownAncestor runs, it
// detects cur > stuckLastRollbackHead and resets stuckRolledBack to 0 before
// computing the new window.  The new rollback uses window=128 (fresh), and
// actual=200-60=140 (finalized=60, unclamped target=72, not clamped).
func TestMaybeRollback_ProgressResetsWindow(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	// Finalized well below rollback target so the clamp doesn't activate.
	n.chain.SetFinalized(n.remote[59].Header()) // block 60
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	// Pre-set leftover state from a previous stuck period.
	s.stuckRolledBack = 512
	s.stuckLastRollbackHead = 50 // chain was at 50 when we last rolled back; now at 200

	primeStuck(s, uint64(local+1))
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(uint64(local+1), peer.Peer, consensus.ErrUnknownAncestor)

	// Rollback must have been entered.
	if !rollbackEntered(s, peer.ID(), beforeUnix) {
		t.Errorf("rollback not entered after window reset: tainted=%v stamp=%d stuckRolledBack=%d (refs #1667)",
			s.isPeerTainted(peer.ID()), core.XdcLastForkRollbackUnix.Load(), s.stuckRolledBack)
	}
	// Window was reset to 0 before computing, so new window=128 (xdcRollbackWindowInit).
	// Unclamped target = 200-128 = 72.  Finalized=60 < 72, so no clamp.
	// Actual = 200 - 72 = 128.
	const wantActual = uint64(128)
	if s.stuckRolledBack != wantActual {
		t.Errorf("stuckRolledBack=%d, want %d (fresh window after reset) (refs #1667)",
			s.stuckRolledBack, wantActual)
	}
}

// TestMaybeRollback_TriggerOnOtherBranch_AltPeerAgrees verifies that a single
// peer on another branch cannot roll back a head the rest of the network agrees
// with. The triggering peer disagrees at our head height, an alt peer agrees:
// the triggering peer is tainted, no rollback is stamped, stuck state clears.
// Seen live on Apothem: one non-canonical peer forced a rollback plus a 60 s
// mint cooldown every ~23 blocks and the validator missed its turn each time.
func TestMaybeRollback_TriggerOnOtherBranch_AltPeerAgrees(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	// Triggering peer: serves the sibling branch, hash at 200 differs.
	peer1 := n.addPeer(t, 1, forkHeaders(sib), sib...)
	// Alt peer: agrees on our canonical head.
	_ = n.addPeer(t, 2, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	beforeUnix := time.Now().Unix()

	s.maybeRollbackOnUnknownAncestor(fromBlock, peer1.Peer, consensus.ErrUnknownAncestor)

	if ts := core.XdcLastForkRollbackUnix.Load(); ts >= beforeUnix {
		t.Errorf("rollback stamped (%d) although an alt peer agrees on our head", ts)
	}
	if s.stuckRolledBack != 0 {
		t.Errorf("stuckRolledBack=%d, want 0 (no rollback)", s.stuckRolledBack)
	}
	if !s.isPeerTainted(peer1.ID()) {
		t.Error("triggering peer on another branch was not tainted")
	}
	if s.stuckFrom != 0 || s.stuckFailures != 0 || core.XdcForkRecovering.Load() {
		t.Errorf("stuck state not cleared: stuckFrom=%d stuckFailures=%d recovering=%v",
			s.stuckFrom, s.stuckFailures, core.XdcForkRecovering.Load())
	}
}

// TestMaybeRollback_NewHeightClearsForkRecovering verifies that the miner pause
// raised while failures accumulate at one height is dropped once a failure
// arrives at a new height (the head moved, so there is no stuck fork). Before
// the fix the flag stayed set until a successful syncer batch, which a live
// validator rarely does, and the validator skipped every turn.
func TestMaybeRollback_NewHeightClearsForkRecovering(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local+10)
	peer := n.addPeer(t, 1, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcForkRecovering.Store(false) })

	// Transient failures (fewer than half the threshold) must not pause the miner.
	s.maybeRollbackOnUnknownAncestor(local+1, peer.Peer, consensus.ErrUnknownAncestor)
	s.maybeRollbackOnUnknownAncestor(local+1, peer.Peer, consensus.ErrUnknownAncestor)
	if core.XdcForkRecovering.Load() {
		t.Fatal("XdcForkRecovering set after only two failures at one height")
	}
	for i := 2; i < xdcUnknownAncestorThreshold/2; i++ {
		s.maybeRollbackOnUnknownAncestor(local+1, peer.Peer, consensus.ErrUnknownAncestor)
	}
	if !core.XdcForkRecovering.Load() {
		t.Fatal("precondition: XdcForkRecovering should be set after half the threshold at one height")
	}
	s.maybeRollbackOnUnknownAncestor(local+2, peer.Peer, consensus.ErrUnknownAncestor)
	if core.XdcForkRecovering.Load() {
		t.Error("XdcForkRecovering still set after a failure at a new height — miner stays paused")
	}
	if s.stuckFrom != local+2 || s.stuckFailures != 1 {
		t.Errorf("stuck state = (%d, %d), want (%d, 1)", s.stuckFrom, s.stuckFailures, local+2)
	}
}

// TestMaybeRollback_SinglePeerDeferredOnce verifies that when only the
// triggering peer disagrees and no other peer is conclusive, the first episode
// defers the rollback and the second at the same head rolls back. On Apothem a
// side-branch peer rolled back a canonical head every few minutes because the
// alt peers did not have the fresh head yet; 4 s later they agreed.
func TestMaybeRollback_SinglePeerDeferredOnce(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, local)
	sib := siblingBranch(n, 100, local)
	n.chain.SetFinalized(n.remote[59].Header())
	peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
	s := n.newTestXDCSyncer(t)
	t.Cleanup(func() { core.XdcLastForkRollbackUnix.Store(0) })

	fromBlock := uint64(local + 1)
	primeStuck(s, fromBlock)
	s.singlePeerForkHead = common.Hash{} // first episode
	beforeUnix := time.Now().Unix()
	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)
	if ts := core.XdcLastForkRollbackUnix.Load(); ts >= beforeUnix || s.stuckRolledBack != 0 {
		t.Fatalf("first single-peer episode rolled back (stamp=%d stuckRolledBack=%d), want deferral", ts, s.stuckRolledBack)
	}
	if core.XdcForkRecovering.Load() {
		t.Error("XdcForkRecovering still set after a deferred episode")
	}

	// Second episode at the same head: the fork is confirmed.
	primeStuck(s, fromBlock)
	s.maybeRollbackOnUnknownAncestor(fromBlock, peer.Peer, consensus.ErrUnknownAncestor)
	if !rollbackEntered(s, peer.ID(), beforeUnix) {
		t.Errorf("second single-peer episode at the same head did not roll back: stamp=%d stuckRolledBack=%d",
			core.XdcLastForkRollbackUnix.Load(), s.stuckRolledBack)
	}
}
