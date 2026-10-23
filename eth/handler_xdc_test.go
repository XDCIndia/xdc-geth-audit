// Copyright 2026 XDC Network
// Tests for the non-blocking BFT broadcast helpers in handler_xdc.go.
// CertiK XDC-13: BroadcastVote/Timeout/SyncInfo must not block on a stalled peer.
// review R2-D03: BroadcastBlock must not block on a stalled peer.

package eth

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
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

// TestBroadcastBlockNonBlocking verifies that a stalled peer does not block
// BroadcastBlock, and that a healthy peer receives the NewBlock message.
//
// Without the goroutine fix (origin/main), the loop is synchronous: the
// stalled peer's p2p.MsgPipe write blocks forever, so BroadcastBlock never
// returns. With the fix, BroadcastBlock spawns per-peer goroutines and returns
// immediately. (review R2-D03)
func TestBroadcastBlockNonBlocking(t *testing.T) {
	// Minimal chain — needed so h.chain.GetTd returns nil and the fallback TD
	// path is taken (block.Difficulty()). No blocks inserted.
	db := rawdb.NewMemoryDatabase()
	gspec := &core.Genesis{Config: params.AllEthashProtocolChanges, Difficulty: common.Big1}
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()

	stallPeer, stalledRW := makeBlockingPeer(enode.ID{3})
	defer stalledRW.Close()
	defer stallPeer.Close()

	healthyPeer, msgCh := makeReadingPeer(enode.ID{4})
	defer healthyPeer.Close()

	ps := newPeerSet()
	ps.peers[stallPeer.ID()] = stallPeer
	ps.peers[healthyPeer.ID()] = healthyPeer
	h := &handler{peers: ps, chain: bc}

	block := types.NewBlockWithHeader(&types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		Time:       1,
	})

	done := make(chan struct{})
	go func() {
		h.BroadcastBlock(block)
		close(done)
	}()

	select {
	case <-done:
		// Expected: spawned goroutines, returned immediately.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("BroadcastBlock blocked: stalled peer held up the broadcast loop (R2-D03)")
	}

	select {
	case msg := <-msgCh:
		if msg.Code != ethproto.NewBlockMsg {
			t.Fatalf("got msg code %d, want NewBlockMsg (%d)", msg.Code, ethproto.NewBlockMsg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("healthy peer did not receive the block within deadline")
	}
}
