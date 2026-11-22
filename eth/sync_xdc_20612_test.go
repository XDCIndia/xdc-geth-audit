// Copyright (c) 2026 XDC Network

package eth

import (
	"testing"
	"time"
)

// TestF202620612EmptyBodiesTaintRaceWinner checks that a peer which wins the
// header race and then withholds bodies is tainted, so the next round can
// import from an honest peer. The parent check stays in fullSyncOnce: a batch
// that does not extend the head is still eligible for the rollback path.
func TestF202620612EmptyBodiesTaintRaceWinner(t *testing.T) {
	const local = 20
	n := newXDCFakeNet(t, local, 40)
	sib := siblingBranch(n, 15, 40)
	// Sibling headers, and no bodies for those hashes.
	bad := n.addPeer(t, 1, servesFrom(sib, local+1))
	honest := n.addPeer(t, 2, n.canonicalHeaders)
	honest.headerDelay = 200 * time.Millisecond

	s := n.newTestXDCSyncer(t)
	s.fullSyncOnce()

	if got := n.chain.CurrentBlock().Number.Uint64(); got != local {
		t.Fatalf("head moved to %d on a batch with no bodies", got)
	}
	if !s.isPeerTainted(bad.ID()) {
		t.Fatal("header-race winner that withheld bodies was not tainted")
	}

	s.fullSyncOnce()
	if got := n.chain.CurrentBlock().Number.Uint64(); got <= local {
		t.Fatalf("head stayed at %d after the winner was tainted", got)
	}
}
