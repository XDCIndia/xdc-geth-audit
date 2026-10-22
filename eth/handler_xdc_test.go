// Copyright 2026 XDC Network
// Tests for the non-blocking BFT broadcast helpers in handler_xdc.go.
// CertiK XDC-13: BroadcastVote/Timeout/SyncInfo must not block on a stalled peer.

package eth

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// makeBFTTestHandler builds a minimal handler whose only populated field is
// peers. The broadcast helpers only access h.peers.lock and h.peers.peers.
func makeBFTTestHandler(epList ...*ethPeer) *handler {
	ps := newPeerSet()
	for _, ep := range epList {
		ps.peers[ep.ID()] = ep
	}
	return &handler{peers: ps}
}

// makeBlockingPeer creates a peer whose write end is never read (blocks forever).
// The caller must call Close() on the returned peer to free the goroutines.
func makeBlockingPeer(id enode.ID) (*ethPeer, *p2p.MsgPipeRW) {
	rw1, rw2 := p2p.MsgPipe()
	p2pPeer := p2p.NewPeer(id, "stall", nil)
	ep := ethproto.NewPeer(ethproto.ETH69, p2pPeer, rw1, nil, nil, nil)
	// rw2 is the other end — intentionally not consumed, so writes to rw1 block.
	return &ethPeer{Peer: ep}, rw2
}

// makeReadingPeer creates a peer with a goroutine reading messages into msgCh.
func makeReadingPeer(id enode.ID) (*ethPeer, <-chan p2p.Msg) {
	rw1, rw2 := p2p.MsgPipe()
	p2pPeer := p2p.NewPeer(id, "healthy", nil)
	ep := ethproto.NewPeer(ethproto.ETH69, p2pPeer, rw1, nil, nil, nil)
	msgCh := make(chan p2p.Msg, 1)
	go func() {
		msg, err := rw2.ReadMsg()
		if err == nil {
			msgCh <- msg
		}
		rw2.Close()
	}()
	return &ethPeer{Peer: ep}, msgCh
}

// TestBroadcastVoteNonBlocking verifies that a stalled peer (one that never
// reads) does not block BroadcastVote from returning, and that a healthy peer
// receives the vote promptly.
//
// On origin/main the broadcast loop is sequential: the stalled peer's
// p2p.MsgPipe write blocks forever, so BroadcastVote never returns.
// With the goroutine fix, BroadcastVote spawns per-peer goroutines and
// returns immediately, and the healthy peer's goroutine delivers the message.
func TestBroadcastVoteNonBlocking(t *testing.T) {
	stallPeer, stalledRW := makeBlockingPeer(enode.ID{1})
	defer stalledRW.Close()
	defer stallPeer.Close()

	healthyPeer, msgCh := makeReadingPeer(enode.ID{2})
	defer healthyPeer.Close()

	h := makeBFTTestHandler(stallPeer, healthyPeer)

	vote := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   common.Hash{0xde, 0xad},
			Number: big.NewInt(1),
			Round:  42,
		},
	}

	// BroadcastVote must return quickly — it should only spawn goroutines.
	done := make(chan struct{})
	go func() {
		h.BroadcastVote(vote)
		close(done)
	}()

	select {
	case <-done:
		// Expected: broadcast loop returned immediately after spawning goroutines.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("BroadcastVote blocked: stalled peer held up the broadcast loop (XDC-13)")
	}

	// Healthy peer should also have received the message.
	select {
	case msg := <-msgCh:
		if msg.Code != ethproto.VoteMsg {
			t.Fatalf("got msg code %d, want VoteMsg (%d)", msg.Code, ethproto.VoteMsg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("healthy peer did not receive the vote within deadline")
	}
}
