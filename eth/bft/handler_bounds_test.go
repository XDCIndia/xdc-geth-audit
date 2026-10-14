// Copyright (c) 2026 XDC Network
// Tests for BFT forwarding bounds fixes (F-2026-19460).
//
// Two fixes are verified here:
//   1. Known-message LRU caches: a message with the same hash is only forwarded
//      to the engine once, even if multiple peers send it. Canonical
//      XDPoSChain/eth/handler.go knownVotes/knownTimeouts/knownSyncInfos.
//   2. Quit-aware enqueue: goroutines spawned to forward verified messages to
//      the broadcast channel exit immediately after Stop() rather than blocking
//      until the channel is drained. Canonical enqueueForBroadcast
//      (XDPoSChain/eth/bft/bft_handler.go:189-196).
//
// Both tests FAIL on origin/main and PASS with the fix.

package bft

import (
	"testing"
	"time"
)

// TestKnownMessageCache_Vote verifies that a verified vote received from two
// peers is only forwarded to VoteHandler once (F-2026-19460).
//
// Before fix (origin/main): no known-message cache → both sends reach
// VoteHandler → votesCalled=2 → FAIL.
// After fix: second send hits the LRU cache → dropped before verification
// → votesCalled=1 → PASS.
func TestKnownMessageCache_Vote(t *testing.T) {
	b, eng := newTestBfter()
	v := newVote(5)

	if err := b.Vote("peer1", v); err != nil {
		t.Fatalf("first Vote: %v", err)
	}
	if eng.votesCalled != 1 {
		t.Fatalf("first send: want votesCalled=1, got %d", eng.votesCalled)
	}

	// Same message from a second peer — must be deduplicated.
	if err := b.Vote("peer2", v); err != nil {
		t.Fatalf("second Vote: %v", err)
	}
	if eng.votesCalled != 1 {
		t.Errorf("FAIL (missing known-message cache, F-2026-19460): "+
			"same vote forwarded twice; votesCalled=%d, want 1", eng.votesCalled)
	}
}

// TestKnownMessageCache_Timeout verifies the same dedup for Timeout messages.
func TestKnownMessageCache_Timeout(t *testing.T) {
	b, eng := newTestBfter()
	to := newTimeout(6)

	if err := b.Timeout("peer1", to); err != nil {
		t.Fatalf("first Timeout: %v", err)
	}
	if eng.timeoutsCalled != 1 {
		t.Fatalf("first send: want timeoutsCalled=1, got %d", eng.timeoutsCalled)
	}

	if err := b.Timeout("peer2", to); err != nil {
		t.Fatalf("second Timeout: %v", err)
	}
	if eng.timeoutsCalled != 1 {
		t.Errorf("FAIL (missing known-message cache, F-2026-19460): "+
			"same timeout forwarded twice; timeoutsCalled=%d, want 1", eng.timeoutsCalled)
	}
}

// TestKnownMessageCache_SyncInfo verifies the same dedup for SyncInfo messages.
func TestKnownMessageCache_SyncInfo(t *testing.T) {
	b, eng := newTestBfter()
	si := newSyncInfo(7)

	if err := b.SyncInfo("peer1", si); err != nil {
		t.Fatalf("first SyncInfo: %v", err)
	}
	if eng.syncInfosCalled != 1 {
		t.Fatalf("first send: want syncInfosCalled=1, got %d", eng.syncInfosCalled)
	}

	if err := b.SyncInfo("peer2", si); err != nil {
		t.Fatalf("second SyncInfo: %v", err)
	}
	if eng.syncInfosCalled != 1 {
		t.Errorf("FAIL (missing known-message cache, F-2026-19460): "+
			"same syncInfo forwarded twice; syncInfosCalled=%d, want 1", eng.syncInfosCalled)
	}
}

// TestEnqueueForBroadcast_ExitsAfterStop verifies that enqueueForBroadcast
// returns false immediately after Stop(), not block indefinitely (F-2026-19460).
//
// Before fix (origin/main): enqueueForBroadcast does not exist; Vote/Timeout/
// SyncInfo use `go func() { b.broadcastCh <- msg }()` which blocks forever
// after Stop() closes the loop. This test would fail to compile on main
// because enqueueForBroadcast is a new method — confirming the before state:
// the old code leaks goroutines.
//
// After fix: enqueueForBroadcast selects on quit → returns false immediately
// when quit is closed → goroutines exit → PASS.
func TestEnqueueForBroadcast_ExitsAfterStop(t *testing.T) {
	b, _ := newTestBfter()
	b.Start()
	b.Stop() // close quit

	done := make(chan bool, 1)
	go func() {
		done <- b.enqueueForBroadcast(newVote(99))
	}()

	select {
	case result := <-done:
		if result {
			t.Error("enqueueForBroadcast returned true after Stop (expected false)")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("FAIL (missing quit-aware enqueue, F-2026-19460): " +
			"enqueueForBroadcast blocked after Stop (goroutine leak)")
	}
}
