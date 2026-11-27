// Copyright 2026 XDC Network

package eth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
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

// TestNewBlockParkingNeedsKnownParentAndByteBudget (F-2026-20605): a future block
// whose parent is unknown is not parked, and the parked blocks together stay
// under maxParkedNewBlockBytes.
func TestNewBlockParkingNeedsKnownParentAndByteBudget(t *testing.T) {
	h, gspec, stop := newBoundsHandler(t, strictClockEngine{ethash.NewFaker()}, 8)
	defer stop()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)
	eh := (*ethHandler)(h)

	// head+1 by number, but its parent is not the genesis we hold.
	orphan := types.NewBlockWithHeader(&types.Header{
		ParentHash: common.Hash{0x01}, Number: blocks[0].Number(), Time: blocks[0].Time(),
		Difficulty: blocks[0].Difficulty(), GasLimit: blocks[0].GasLimit(),
	})
	p, c := boundsPeer(1)
	defer c()
	if err := eh.Handle(p, &eth.NewBlockPacket{Block: orphan}); err != nil {
		t.Fatal(err)
	}
	h.newBlockMu.Lock()
	n := len(h.parkedBlocks)
	h.newBlockMu.Unlock()
	if n != 0 {
		t.Fatalf("block with unknown parent parked: %d", n)
	}

	// Byte budget: blocks that fit are parked, the one that would overflow is not.
	const size = maxParkedNewBlockBytes / 4
	for i := 0; i < 4; i++ {
		if !eh.parkNewBlock(string(rune('a'+i)), common.Hash{byte(i + 1)}, size) {
			t.Fatalf("block %d within the byte budget refused", i)
		}
	}
	if eh.parkNewBlock("e", common.Hash{0x09}, 1) {
		t.Fatal("block over the byte budget parked")
	}
}
