// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// CertiK XDC-33: re-seed from bootnodes when the discovery table empties.
// When revalidation removes the last node, the loop must trigger an
// immediate refresh instead of waiting up to nextRefreshTime() (~30 min).
//
// Refs #1720 / XDC-33.

package discover

import (
	"net"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/mclock"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// reseedTransport wraps a pingRecorder and records calls to lookupSelf.
type reseedTransport struct {
	*pingRecorder
	lookupCh chan struct{}
}

// lookupSelf overrides pingRecorder.lookupSelf() to signal the channel.
func (t *reseedTransport) lookupSelf() []*enode.Node {
	select {
	case t.lookupCh <- struct{}{}:
	default:
	}
	return nil
}

// lookupRandom overrides pingRecorder.lookupRandom().
func (t *reseedTransport) lookupRandom() []*enode.Node { return nil }

// TestTableReseedsWhenEmpty verifies that when revalidation removes the last
// node from the discovery table, the loop triggers an immediate refresh
// (doRefresh → lookupSelf) instead of waiting for the refresh timer.
//
// On origin/main the immediate re-seed does not exist: the next refresh fires
// only when the real-time refresh timer elapses (~15–30 min), so the 2 s
// deadline below fires first and the test fails.
// With the fix, the loop starts doRefresh in the same iteration that processes
// the empty-table revalResponse, and lookupSelf is called within milliseconds.
func TestTableReseedsWhenEmpty(t *testing.T) {
	var clock mclock.Simulated

	lookupCh := make(chan struct{}, 32)
	transport := &reseedTransport{
		pingRecorder: newPingRecorder(),
		lookupCh:     lookupCh,
	}

	// Use a very short ping interval so the sim clock advance below is small.
	tab, db := newTestTable(transport, Config{
		Clock:        &clock,
		PingInterval: 10 * time.Millisecond,
	})
	defer db.Close()
	defer tab.close()

	// Drain the lookupSelf call from the initial startup refresh.
	select {
	case <-lookupCh:
	case <-time.After(5 * time.Second):
		t.Fatal("initial refresh never called lookupSelf")
	}

	// Add one node and mark it dead so revalidation removes it.
	node := nodeAtDistance(tab.self().ID(), 255, net.IP{77, 88, 99, 1})
	transport.pingRecorder.mu.Lock()
	transport.pingRecorder.dead[node.ID()] = true
	transport.pingRecorder.mu.Unlock()

	if !tab.addFoundNode(node, false) {
		t.Fatal("node was not added to the table")
	}
	if tab.len() == 0 {
		t.Fatal("table should have one entry after addFoundNode")
	}

	// Advance the simulated clock in short steps until the node is pinged
	// and revalidation removes it (livenessChecks=1, fails→0, deleted).
	removed := false
	for attempt := 0; attempt < 20; attempt++ {
		clock.Run(tab.cfg.PingInterval * slowRevalidationFactor)
		// waitPing blocks briefly for the ping to arrive or times out.
		transport.pingRecorder.waitPing(200 * time.Millisecond)
		if tab.len() == 0 {
			removed = true
			break
		}
	}
	if !removed {
		t.Fatalf("table still has %d entries after 20 revalidation attempts", tab.len())
	}

	// With the fix, the loop re-seeds immediately after the table empties,
	// calling doRefresh → lookupSelf within real-time milliseconds.
	// Without the fix, the next lookupSelf fires only after the real-time
	// refresh timer (~15-30 min), long after this 2 s deadline.
	select {
	case <-lookupCh:
		// Good: table re-seeded promptly after becoming empty.
	case <-time.After(2 * time.Second):
		t.Fatal("table did not re-seed after becoming empty (certik xdc-33): " +
			"expected doRefresh to trigger immediately when the last node is removed")
	}
}
