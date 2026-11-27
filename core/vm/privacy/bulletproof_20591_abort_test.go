// Copyright (c) 2026 XDC Network

package privacy

import (
	"math/big"
	"sync/atomic"
	"testing"
	"time"
)

// An aborted verification returns the shared lock.
func TestF202620591AbortedCallReleasesLock(t *testing.T) {
	proof, err := MRPProve([]*big.Int{big.NewInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	// Abort from the 3rd check on, which is after keygen.
	calls := 0
	abort := func() bool { calls++; return calls > 3 }
	if MRPVerify(&proof, abort) {
		t.Fatal("aborted verification returned true")
	}
	if calls <= 3 {
		t.Fatalf("abort checked %d times, did not reach the post-keygen loops", calls)
	}
	if !ecMu.TryLock() {
		t.Fatal("aborted call kept ecMu")
	}
	ecMu.Unlock()
}

// A call queued behind a long verification aborts while it waits.
func TestF202620591QueuedCallAbortsWhileWaiting(t *testing.T) {
	mrp := &MultiRangeProof{Comms: make([]ECPoint, 1)}
	ecMu.Lock()
	defer ecMu.Unlock()
	var stop atomic.Bool
	done := make(chan bool, 1)
	go func() { done <- MRPVerify(mrp, func() bool { return stop.Load() }) }()
	select {
	case <-done:
		t.Fatal("call returned while the lock was held")
	case <-time.After(50 * time.Millisecond):
	}
	stop.Store(true)
	select {
	case ok := <-done:
		if ok {
			t.Fatal("aborted call returned true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued call did not abort while waiting for ecMu")
	}
}
