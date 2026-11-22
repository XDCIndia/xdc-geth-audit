// F-2026-19460, F-2026-20592: bounded per-peer BFT sends. Helpers copied from the work tree test file, which is not part of the audit tree.
// Copyright 2026 XDC Network
// Tests for the non-blocking BFT broadcast helpers in handler_xdc.go.
// CertiK XDC-13: BroadcastVote/Timeout/SyncInfo must not block on a stalled peer.
// review R2-D03: BroadcastBlock must not block on a stalled peer.

package eth

import (
	"math/big"
	"runtime"
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

// TestF202620592BroadcastVoteGoroutinesAreBounded checks that broadcasting many
// votes to a peer that does not read starts one sender goroutine, not one
// goroutine per message.
func TestF202620592BroadcastVoteGoroutinesAreBounded(t *testing.T) {
	stall, stalledRW := makeBlockingPeer(enode.ID{7})
	defer stalledRW.Close()
	defer stall.Close()
	defer stall.stopBFTSend()

	h := makeBFTTestHandler(stall)
	vote := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   common.Hash{0x01},
			Number: big.NewInt(1),
			Round:  1,
		},
	}
	before := runtime.NumGoroutine()
	for i := 0; i < 2000; i++ {
		h.BroadcastVote(vote)
	}
	time.Sleep(30 * time.Millisecond)
	if delta := runtime.NumGoroutine() - before; delta > 8 {
		t.Fatalf("goroutines grew by %d after 2000 broadcasts, want one sender", delta)
	}
}

// TestF202620592FullQueueDisconnectsPeer checks that a peer whose send queue
// stays full is disconnected.
func TestF202620592FullQueueDisconnectsPeer(t *testing.T) {
	prev := bftPeerSendFullTimeout
	bftPeerSendFullTimeout = 20 * time.Millisecond
	t.Cleanup(func() { bftPeerSendFullTimeout = prev })

	app, remote := p2p.MsgPipe()
	defer remote.Close()
	p2pPeer := p2p.NewPeerPipe(enode.ID{8}, "stall", nil, app)
	ep := ethproto.NewPeer(ethproto.ETH69, p2pPeer, app, nil, nil, nil)
	stall := &ethPeer{Peer: ep}
	defer stall.Close()
	defer stall.stopBFTSend()

	h := makeBFTTestHandler(stall)
	vote := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   common.Hash{0x02},
			Number: big.NewInt(1),
			Round:  1,
		},
	}
	// Fill the queue. The sender blocks on the first write, which frees one slot,
	// so a second burst is what leaves the queue full.
	for i := 0; i < bftPeerSendQueueLen+2; i++ {
		h.BroadcastVote(vote)
	}
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 2; i++ {
		h.BroadcastVote(vote)
	}
	time.Sleep(40 * time.Millisecond)
	h.BroadcastVote(vote)

	done := make(chan struct{})
	go func() {
		for {
			if _, err := remote.ReadMsg(); err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the stalled peer to be disconnected")
	}
}
