// Copyright 2026 XDC Network

package eth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// strictClockEngine refuses any header ahead of the local clock, like XDPoS
// (ethash allows 15 s).
type strictClockEngine struct{ consensus.Engine }

func (e strictClockEngine) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	if header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}
	return e.Engine.VerifyHeader(chain, header)
}

func (e strictClockEngine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort, results := make(chan struct{}), make(chan error, len(headers))
	for _, h := range headers {
		results <- e.VerifyHeader(chain, h)
	}
	return abort, results
}

// TestNewBlockSlightlyEarlyImportedWhenDue: a gossiped block whose timestamp is
// a little ahead of the local clock (a v2.9.3 V1 sealer sends its block about
// 1 s early) is handled when it is due, as canonical XDPoSChain's fetcher does,
// instead of being dropped with ErrFutureBlock.
func TestNewBlockSlightlyEarlyImportedWhenDue(t *testing.T) {
	// GenerateChain spaces blocks 10 s apart, so block 1 is 2 s in the future.
	gspec := &core.Genesis{Config: params.TestChainConfig, Timestamp: uint64(time.Now().Unix()) - 8}
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, gspec, strictClockEngine{ethash.NewFaker()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)
	if blocks[0].Time() <= uint64(time.Now().Unix()) {
		t.Fatal("test block is not in the future")
	}
	txpool := newTestTxPool()
	handler, err := newHandler(&handlerConfig{Database: db, Chain: chain, TxPool: txpool, BlobPool: txpool, Network: 1, Sync: ethconfig.FullSync, BloomCache: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler.Start(1000)
	defer handler.Stop()

	app, net := p2p.MsgPipe()
	defer app.Close()
	defer net.Close()
	peer := eth.NewPeer(eth.ETH69, p2p.NewPeerPipe(enode.ID{1}, "", nil, app), app, txpool, txpool, nil)
	defer peer.Close()

	if err := (*ethHandler)(handler).Handle(peer, &eth.NewBlockPacket{Block: blocks[0]}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for chain.CurrentBlock().Number.Uint64() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("early block not imported once due: head %d", chain.CurrentBlock().Number.Uint64())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
