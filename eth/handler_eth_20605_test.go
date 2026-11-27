// Copyright (c) 2026 XDC Network

package eth

import (
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/types"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// verifyCountEngine counts single-header verifications, the first step the
// NewBlock path takes before InsertChain.
type verifyCountEngine struct {
	consensus.Engine
	verifies atomic.Int32
}

func (e *verifyCountEngine) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	e.verifies.Add(1)
	return e.Engine.VerifyHeader(chain, header)
}

// TestF202620605KnownHeadReplayDropped checks that replaying the current head
// over NewBlock is dropped before header verification and import.
func TestF202620605KnownHeadReplayDropped(t *testing.T) {
	engine := &verifyCountEngine{Engine: ethash.NewFaker()}
	n := newXDCFakeNetEngine(t, 3, 3, engine)
	head := n.chain.CurrentBlock()
	block := n.chain.GetBlock(head.Hash(), head.Number.Uint64())

	app, _ := p2p.MsgPipe()
	defer app.Close()
	txpool := newTestTxPool()
	peer := ethproto.NewPeer(ethproto.ETH69, p2p.NewPeerPipe(enode.ID{3}, "", nil, app), app, txpool, txpool, nil)
	defer peer.Close()

	engine.verifies.Store(0) // the setup InsertChain may verify too
	for i := 0; i < 5; i++ {
		if err := (*ethHandler)(n.handler).Handle(peer, &ethproto.NewBlockPacket{Block: block}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if got := engine.verifies.Load(); got != 0 {
		t.Fatalf("known head replay reached header verification %d times", got)
	}
}
