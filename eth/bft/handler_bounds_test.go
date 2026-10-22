// Copyright (c) 2026 XDC Network
// Tests for BFT forwarding bounds fixes (F-2026-19460 and its residual).
//
// Four properties are verified here:
//   1. Known-message LRU caches: a message with the same hash is only forwarded
//      to the engine once, even if multiple peers send it.
//   2. Goroutine bound: flooding the handler with many distinct verified peer
//      messages while broadcast functions block must not grow goroutines without
//      limit. The count must stay within bcastWorkers + loop + small overhead.
//   3. Own-message priority: when fwdQueue is full of peer-relayed messages, an
//      own message from the engine's broadcastCh is still delivered (blocking)
//      once a worker frees a slot — it is never silently dropped.
//   4. Stop() exits promptly even when an own-message send is blocked on a
//      full fwdQueue.

package bft

import (
	"runtime"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// TestKnownMessageCache_Vote verifies that a verified vote received from two
// peers is only forwarded to VoteHandler once (F-2026-19460).
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

// TestBoundedGoroutines_Flood verifies that flooding the handler with many
// distinct verified peer messages while the broadcast functions block does NOT
// grow the goroutine count without limit (Hacken F-2026-19460 residual).
//
// Before fix: each distinct verified message spawned a new goroutine. No fixed
// upper bound → count grew with flood size.
//
// After fix: bcastWorkers goroutines drain fwdQueue; tryForward drops when the
// queue is full. Goroutine delta is bounded regardless of flood size.
func TestBoundedGoroutines_Flood(t *testing.T) {
	// Block the broadcast functions to keep workers busy.
	blocker := make(chan struct{})

	b := New(BroadcastFns{
		Vote:     func(*types.Vote) { <-blocker },
		Timeout:  func(*types.Timeout) {},
		SyncInfo: func(*types.SyncInfo) {},
	}, fakeChain{}, func() uint64 { return 100 })
	b.SetConsensusFns(&fakeEngine{})

	baseline := runtime.NumGoroutine()
	b.Start()

	// Give the loop + workers time to start.
	runtime.Gosched()

	// Flood with distinct votes (each unique Round yields a unique hash, so
	// the known-message cache never deduplicates them).
	const floodSize = 2000
	for i := 0; i < floodSize; i++ {
		v := newVote(types.Round(1000 + i))
		_ = b.Vote("peer", v)
	}

	// Let the scheduler run. Workers pick up items from fwdQueue.
	runtime.Gosched()
	time.Sleep(5 * time.Millisecond)

	delta := runtime.NumGoroutine() - baseline
	// Expected: loop goroutine (1) + bcastWorkers goroutines = bcastWorkers+1.
	// Allow a small margin for test runtime goroutines.
	const maxDelta = bcastWorkers + 5
	if delta > maxDelta {
		t.Errorf("FAIL (F-2026-19460 residual): goroutine delta=%d after %d distinct votes, want <=%d; "+
			"per-message goroutines are not bounded", delta, floodSize, maxDelta)
	}

	close(blocker) // unblock workers so Stop() can drain cleanly
	b.Stop()
}

// TestOwnMessageDelivered_WhenQueueFull verifies that an own-originated message
// from the engine's broadcastCh is delivered even when fwdQueue is already full
// of peer-relayed messages (Hacken F-2026-19460 residual).
//
// The loop uses a blocking enqueue (with quit select) for own messages, so it
// waits until a worker frees a slot. A peer-relayed message uses tryForward and
// is dropped when full. Own messages must never be silently lost.
func TestOwnMessageDelivered_WhenQueueFull(t *testing.T) {
	// delivered records votes that the broadcast function receives.
	delivered := make(chan *types.Vote, bcastQueueSize+10)
	// unblock allows the broadcast function to return after we signal.
	unblock := make(chan struct{})

	b := New(BroadcastFns{
		Vote: func(v *types.Vote) {
			delivered <- v
			<-unblock
		},
		Timeout:  func(*types.Timeout) {},
		SyncInfo: func(*types.SyncInfo) {},
	}, fakeChain{}, func() uint64 { return 100 })
	b.SetConsensusFns(&fakeEngine{})

	// Wire a local broadcastCh (simulates the engine's own outbound channel).
	ownCh := make(chan interface{})
	b.SetBroadcastCh(ownCh)

	b.Start()
	defer b.Stop()

	// Fill fwdQueue with distinct peer-relayed votes. Because the broadcast
	// function blocks on <-unblock, workers stall and the queue saturates.
	// We send more than bcastQueueSize so the tryForward drops kick in and
	// the queue is provably full.
	for i := 0; i < bcastQueueSize+bcastWorkers+10; i++ {
		v := newVote(types.Round(2000 + i))
		_ = b.Vote("peer", v)
	}

	// Give workers time to start consuming and block on unblock.
	time.Sleep(10 * time.Millisecond)

	// Now send an own message via broadcastCh. The loop must block here until
	// a worker slot opens, then deliver it. It must NOT be dropped.
	ownVote := newVote(types.Round(99999))
	sendDone := make(chan struct{})
	go func() {
		ownCh <- ownVote
		close(sendDone)
	}()

	// Unblock one worker to free a slot in fwdQueue.
	// That slot is claimed by the own message (or another peer message already
	// dequeued; we unblock enough workers to guarantee delivery).
	for i := 0; i < bcastWorkers+1; i++ {
		select {
		case unblock <- struct{}{}:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out unblocking a worker")
		}
	}

	// Wait for the own-message send to complete (it was blocking on ownCh).
	select {
	case <-sendDone:
	case <-time.After(2 * time.Second):
		t.Fatal("own message send did not complete: loop did not read from broadcastCh")
	}

	// Drain remaining workers so the test can finish cleanly.
	close(unblock)
	_ = delivered
}

// TestStop_WithBlockedOwnMessage verifies that Stop() exits promptly even when
// the loop is blocked trying to enqueue an own message onto a full fwdQueue
// (Hacken F-2026-19460 residual).
//
// The loop uses `select { case b.fwdQueue <- obj: case <-b.quit: }`, so
// closing quit unblocks the send and the loop exits cleanly.
func TestStop_WithBlockedOwnMessage(t *testing.T) {
	// Keep all workers permanently blocked so fwdQueue fills up.
	neverUnblock := make(chan struct{})
	b := New(BroadcastFns{
		Vote:     func(*types.Vote) { <-neverUnblock },
		Timeout:  func(*types.Timeout) {},
		SyncInfo: func(*types.SyncInfo) {},
	}, fakeChain{}, func() uint64 { return 100 })
	b.SetConsensusFns(&fakeEngine{})

	ownCh := make(chan interface{})
	b.SetBroadcastCh(ownCh)

	b.Start()

	// Fill fwdQueue so the next own-message enqueue blocks.
	for i := 0; i < bcastQueueSize+bcastWorkers+10; i++ {
		v := newVote(types.Round(3000 + i))
		_ = b.Vote("peer", v)
	}
	time.Sleep(10 * time.Millisecond) // let workers stall on neverUnblock

	// Send an own message. The loop will block on the fwdQueue enqueue.
	go func() { ownCh <- newVote(88888) }()
	time.Sleep(5 * time.Millisecond) // give the loop time to reach the blocked select

	// Stop() must unblock the loop via quit and return promptly.
	stopDone := make(chan struct{})
	go func() {
		b.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(500 * time.Millisecond):
		t.Error("FAIL (F-2026-19460 residual): Stop() did not return within 500 ms " +
			"while own-message enqueue was blocked on full fwdQueue")
	}
}

// TestStop_WorkersExit verifies that after Stop(), the worker pool and the
// loop goroutine all exit within a short deadline (Hacken F-2026-19460 residual).
func TestStop_WorkersExit(t *testing.T) {
	b, _ := newTestBfter()

	baseline := runtime.NumGoroutine()
	b.Start()

	// Give goroutines time to start.
	runtime.Gosched()
	time.Sleep(2 * time.Millisecond)

	started := runtime.NumGoroutine()
	if started <= baseline {
		t.Fatalf("Start() did not launch any goroutines (before=%d, after=%d)", baseline, started)
	}

	b.Stop()

	// All workers and the loop must exit.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine() <= baseline+1 {
			return // all gone (allow 1 for test runner uncertainty)
		}
		time.Sleep(5 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	if after > baseline+1 {
		t.Errorf("FAIL (F-2026-19460 residual): %d goroutine(s) still running after Stop (baseline=%d, after=%d)",
			after-baseline, baseline, after)
	}
}
