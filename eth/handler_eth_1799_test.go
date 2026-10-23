// Copyright 2026 XDC Network
// Test for R2-D02: delayed future-block handling must be skipped when the
// handler stops before the timer fires.

package eth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// TestFutureBlockSkippedAfterHandlerStop verifies that a NewBlock scheduled for
// future delivery (via time.AfterFunc) is not handled when the handler has
// already stopped.
//
// Without the fix (origin/main): the AfterFunc callback calls h.Handle
// unconditionally. When the timer fires ~2 s after the handler stopped, Handle
// is invoked again, the block's timestamp is now past, InsertChain succeeds
// (ethash.NewFaker accepts any seal), and chain head advances to 1 — the test
// asserts head==0 and fails.
//
// With the fix: the callback selects on h.quitSync and returns early. Handle is
// never called again, head stays at 0, the test passes. (review R2-D02)
func TestFutureBlockSkippedAfterHandlerStop(t *testing.T) {
	// Build a genesis whose block-1 timestamp is ~2 s in the future.
	// GenerateChainWithGenesis spaces blocks 10 s apart, so a genesis
	// timestamp of (now - 8) puts block-1 at (now + 2).
	gspec := &core.Genesis{
		Config:    params.AllEthashProtocolChanges,
		Timestamp: uint64(time.Now().Unix()) - 8,
	}
	db := rawdb.NewMemoryDatabase()
	// ethash.NewFaker() skips PoW — InsertChain succeeds for a structurally
	// valid block even after its timestamp becomes past.
	chain, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()

	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)
	block := blocks[0]
	if block.Time() <= uint64(time.Now().Unix()) {
		t.Skip("generated block is not in the future — skip (slow machine or clock skew)")
	}

	txpool := newTestTxPool()
	// Build the handler but do NOT call handler.Start() — that would launch
	// the xdcSyncer goroutines, and their early termination can race on nil
	// subscriptions if Stop() is called before the goroutines initialise.
	// The future-block timer path only uses h.chain, h.quitSync and h.peers,
	// all of which are initialised by newHandler.
	handler, err := newHandler(&handlerConfig{
		Database:   db,
		Chain:      chain,
		TxPool:     txpool,
		BlobPool:   txpool,
		Network:    1,
		Sync:       ethconfig.FullSync,
		BloomCache: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	app, _ := p2p.MsgPipe()
	defer app.Close()
	peer := ethproto.NewPeer(ethproto.ETH69, p2p.NewPeerPipe(enode.ID{2}, "", nil, app), app, txpool, txpool, nil)
	defer peer.Close()

	// Handle schedules an AfterFunc because the block's timestamp is in the
	// future. The handler is not started so no background goroutines run.
	if err := (*ethHandler)(handler).Handle(peer, &ethproto.NewBlockPacket{Block: block}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if chain.CurrentBlock().Number.Uint64() != 0 {
		t.Fatal("block was imported immediately — block is not in the future")
	}

	// Simulate handler stop by closing quitSync. This is exactly what
	// handler.Stop() does (close(h.quitSync) followed by wg.Wait()).
	close(handler.quitSync)

	// Wait for the timer to fire and any import goroutine to complete.
	remaining := time.Until(time.Unix(int64(block.Time()), 0))
	if remaining < 0 {
		remaining = 0
	}
	time.Sleep(remaining + 500*time.Millisecond)

	// With the fix: callback returned early on quitSync, head stays at 0.
	// Without the fix: callback called Handle again, InsertChain ran
	// (ethash.NewFaker accepts any seal), head advanced to 1 → test fails.
	if chain.CurrentBlock().Number.Uint64() != 0 {
		t.Fatalf("block was handled after handler stopped (R2-D02 regression): head=%d",
			chain.CurrentBlock().Number.Uint64())
	}
}
