// Copyright (c) 2026 XDC Network

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestF202619636UnverifiedHeadersDoNotRollBack checks that two peers serving a
// different hash at the local head cannot move the head when that header does
// not pass consensus verification. The chain is longer than the 128-block
// rollback window: a shorter one returns before the evidence check and the test
// would pass without the fix (XDC-HUNT-33).
func TestF202619636UnverifiedHeadersDoNotRollBack(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, 240)
	badAtHead := func(origin uint64, amount int) []*types.Header {
		if origin != local || amount < 1 {
			return nil
		}
		h := types.CopyHeader(n.chain.GetHeaderByNumber(local))
		h.Extra = []byte{0xde, 0xad}
		h.Difficulty = big.NewInt(99)
		return []*types.Header{h}
	}
	first := n.addPeer(t, 1, badAtHead)
	n.addPeer(t, 2, badAtHead)
	s := n.newTestXDCSyncer(t)
	head := n.chain.CurrentBlock().Number.Uint64()
	for i := 0; i < xdcUnknownAncestorThreshold+2; i++ {
		s.maybeRollbackOnUnknownAncestor(head+1, first.Peer, consensus.ErrUnknownAncestor)
	}
	if got := n.chain.CurrentBlock().Number.Uint64(); got != head {
		t.Fatalf("head moved from %d to %d on unverified competing headers", head, got)
	}
	if s.verifiedCompetingBranch(first.Peer, badAtHead(local, 1)[0]) {
		t.Fatal("the test header unexpectedly passed verification")
	}
}
