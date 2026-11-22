// Copyright 2026 XDC Network

package eth

import (
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/consensus"
	xdcutils "github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
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

// rejectEngine fails every header with a definitive XDPoS error.
type rejectEngine struct{ consensus.Engine }

func (e rejectEngine) VerifyHeader(consensus.ChainHeaderReader, *types.Header) error {
	return xdcutils.ErrInvalidQC
}

func (e rejectEngine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort, results := make(chan struct{}), make(chan error, len(headers))
	for _, h := range headers {
		results <- e.VerifyHeader(chain, h)
	}
	return abort, results
}

func newBoundsHandler(t *testing.T, engine consensus.Engine, genesisAge uint64) (*handler, *core.Genesis, func()) {
	t.Helper()
	gspec := &core.Genesis{Config: params.TestChainConfig, Timestamp: uint64(time.Now().Unix()) - genesisAge}
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, gspec, engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	txpool := newTestTxPool()
	h, err := newHandler(&handlerConfig{Database: db, Chain: chain, TxPool: txpool, BlobPool: txpool, Network: 1, Sync: ethconfig.FullSync, BloomCache: 1})
	if err != nil {
		t.Fatal(err)
	}
	h.Start(1000)
	return h, gspec, func() { h.Stop(); chain.Stop() }
}

func boundsPeer(id byte) (*eth.Peer, func()) {
	app, net := p2p.MsgPipe()
	txpool := newTestTxPool()
	peer := eth.NewPeer(eth.ETH69, p2p.NewPeerPipe(enode.ID{id}, "", nil, app), app, txpool, txpool, nil)
	return peer, func() { peer.Close(); app.Close(); net.Close() }
}

// TestNewBlockFutureParkingIsBounded: a peer may park one future-timestamp block,
// all peers together a bounded number, and a block far from the head none.
func TestNewBlockFutureParkingIsBounded(t *testing.T) {
	h, gspec, stop := newBoundsHandler(t, strictClockEngine{ethash.NewFaker()}, 8)
	defer stop()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 3, nil)
	if blocks[0].Time() <= uint64(time.Now().Unix()) {
		t.Fatal("test block is not in the future")
	}
	parked := func() int {
		h.newBlockMu.Lock()
		defer h.newBlockMu.Unlock()
		return len(h.parkedBlocks)
	}
	eh := (*ethHandler)(h)

	// One peer, many packets: one slot.
	p1, c1 := boundsPeer(1)
	defer c1()
	for i := 0; i < 50; i++ {
		if err := eh.Handle(p1, &eth.NewBlockPacket{Block: blocks[0]}); err != nil {
			t.Fatal(err)
		}
	}
	if n := parked(); n != 1 {
		t.Fatalf("parked after 50 packets from one peer = %d, want 1", n)
	}
	// A block three ahead of the head is not parked at all.
	p2, c2 := boundsPeer(2)
	defer c2()
	if err := eh.Handle(p2, &eth.NewBlockPacket{Block: blocks[2]}); err != nil {
		t.Fatal(err)
	}
	if n := parked(); n != 1 {
		t.Fatalf("far-ahead block was parked: %d", n)
	}
	// Many peers: bounded in total.
	for i := 3; i < 3+3*maxParkedNewBlocks; i++ {
		p, c := boundsPeer(byte(i))
		defer c()
		if err := eh.Handle(p, &eth.NewBlockPacket{Block: blocks[0]}); err != nil {
			t.Fatal(err)
		}
	}
	if n := parked(); n != maxParkedNewBlocks {
		t.Fatalf("parked = %d, want cap %d", n, maxParkedNewBlocks)
	}
}

// TestNewBlockInvalidPeerDisconnectedAndNoBadBlockWrites: a NewBlock whose header
// is definitively invalid is refused before InsertChain (no bad-block record) and
// the peer is disconnected after maxInvalidNewBlocks of them.
func TestNewBlockInvalidPeerDisconnectedAndNoBadBlockWrites(t *testing.T) {
	h, gspec, stop := newBoundsHandler(t, rejectEngine{ethash.NewFaker()}, 100)
	defer stop()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)
	peer, c := boundsPeer(1)
	defer c()
	eh := (*ethHandler)(h)
	for i := 1; i <= maxInvalidNewBlocks+2; i++ {
		err := eh.Handle(peer, &eth.NewBlockPacket{Block: blocks[0]})
		if want := i > maxInvalidNewBlocks; (err != nil) != want {
			t.Fatalf("packet %d: err=%v, want error=%v", i, err, want)
		} else if want && !errors.Is(err, errTooManyInvalidBlocks) {
			t.Fatalf("packet %d: unexpected error %v", i, err)
		}
	}
	if n := len(rawdb.ReadAllBadBlocks(h.database)); n != 0 {
		t.Fatalf("%d bad-block records written, want 0", n)
	}
}
