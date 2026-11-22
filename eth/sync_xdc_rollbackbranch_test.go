// Copyright 2026 XDC Network

package eth

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestRollbackForgedUnknownParentRefused: two peers serving a header at our head
// height with a made-up parent hash and no ancestors cannot move the head. Such a
// header always stops at ErrUnknownAncestor before any seal check, and used to
// count as rollback evidence (XDC-HUNT-33). The chain is longer than the window.
func TestRollbackForgedUnknownParentRefused(t *testing.T) {
	const local = 200
	n := newXDCFakeNet(t, local, 240)
	forgedAtHead := func(origin uint64, amount int) []*types.Header {
		if origin != local || amount < 1 {
			return nil // no ancestors: the branch cannot be fetched
		}
		h := types.CopyHeader(n.chain.GetHeaderByNumber(local))
		h.ParentHash = common.Hash{0xde, 0xad}
		return []*types.Header{h}
	}
	first := n.addPeer(t, 1, forgedAtHead)
	n.addPeer(t, 2, forgedAtHead)
	s := n.newTestXDCSyncer(t)
	for i := 0; i < xdcUnknownAncestorThreshold+2; i++ {
		s.maybeRollbackOnUnknownAncestor(local+1, first.Peer, consensus.ErrUnknownAncestor)
	}
	if got := n.chain.CurrentBlock().Number.Uint64(); got != local {
		t.Fatalf("head moved from %d to %d on a forged unknown-parent header", local, got)
	}
	if s.verifiedCompetingBranch(first.Peer, forgedAtHead(local, 1)[0]) {
		t.Fatal("forged unknown-parent header accepted as evidence")
	}
}

// rejectBranchEngine accepts single headers as the wrapped engine does, but
// fails a VerifyHeaders batch that holds a header sealed by the fork's coinbase.
type rejectBranchEngine struct{ consensus.Engine }

func (e rejectBranchEngine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort, results := make(chan struct{}), make(chan error, len(headers))
	for _, h := range headers {
		if h.Coinbase == (common.Address{0xbb}) {
			results <- errors.New("invalid seal")
			continue
		}
		results <- nil
	}
	return abort, results
}

// TestRollbackBranchFailingVerificationRefused: a linked branch that fetches
// fine but fails VerifyHeaders is not evidence; the same branch with an engine
// that accepts it rolls back, over more than one 192-header batch.
func TestRollbackBranchFailingVerificationRefused(t *testing.T) {
	const local, forkAt = 300, 40 // the branch is 260 headers: two batches
	for _, tc := range []struct {
		name       string
		reject     bool
		wantRolled bool
	}{
		{"branch fails verification", true, false},
		{"branch verifies", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var engine consensus.Engine = ethash.NewFaker()
			if tc.reject {
				engine = rejectBranchEngine{engine}
			}
			n := newXDCFakeNetEngine(t, local, local, engine)
			sib := siblingBranch(n, forkAt, local)
			n.chain.SetFinalized(n.remote[9].Header())
			peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
			s := n.newTestXDCSyncer(t)
			primeStuck(s, local+1)
			s.maybeRollbackOnUnknownAncestor(local+1, peer.Peer, consensus.ErrUnknownAncestor)
			if got := s.stuckRolledBack > 0; got != tc.wantRolled {
				t.Fatalf("rolled back = %v, want %v (stuckRolledBack=%d)", got, tc.wantRolled, s.stuckRolledBack)
			}
		})
	}
}

// TestRollbackStaysOutsideCatchupWindow: on a fast-sync node whose head is past
// the anchor + 2 epochs window (where verifyQC is skipped), a rollback is clamped
// to the end of the window so it cannot re-open it; without an anchor the
// rollback is the plain window (XDC-HUNT-53).
func TestRollbackStaysOutsideCatchupWindow(t *testing.T) {
	const local, forkAt, anchor = 1900, 1850, 10 // window ends at 10 + 2*900 = 1810
	for _, tc := range []struct {
		name       string
		withAnchor bool
		wantActual uint64
	}{
		{"fast-sync anchor", true, local - (anchor + 2*900)},
		{"no anchor", false, xdcRollbackWindowInit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newXDCFakeNet(t, local, local)
			if tc.withAnchor {
				n.chain.SetFastSyncTrustedAnchor(anchor, n.remote[anchor-1].Hash())
			}
			sib := siblingBranch(n, forkAt, local)
			n.chain.SetFinalized(n.remote[9].Header())
			peer := n.addPeer(t, 1, forkHeaders(sib), sib...)
			s := n.newTestXDCSyncer(t)
			primeStuck(s, local+1)
			s.maybeRollbackOnUnknownAncestor(local+1, peer.Peer, consensus.ErrUnknownAncestor)
			if s.stuckRolledBack != tc.wantActual {
				t.Fatalf("rolled back %d blocks, want %d", s.stuckRolledBack, tc.wantActual)
			}
		})
	}
}
