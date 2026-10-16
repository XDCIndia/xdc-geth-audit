// Copyright 2026 XDC Network

package eth

import (
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
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

// noM2Engine refuses every header with an empty Validator as XDPoS does for a
// V1 block past epoch 1 that the assigned M2 has not co-signed yet.
type noM2Engine struct{ consensus.Engine }

func (e noM2Engine) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	if len(header.Validator) == 0 {
		return XDPoS.ErrNoValidatorSignature
	}
	return e.Engine.VerifyHeader(chain, header)
}

func (e noM2Engine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort, results := make(chan struct{}), make(chan error, len(headers))
	for _, h := range headers {
		results <- e.VerifyHeader(chain, h)
	}
	return abort, results
}

// TestNewBlockAwaitingM2NotImported: a gossiped V1 block that still waits for
// its M2 co-signature is not imported (canonical XDPoSChain fetcher: relay and
// prepare, no insert), so it is not reported or stored as a bad block.
func TestNewBlockAwaitingM2NotImported(t *testing.T) {
	gspec := &core.Genesis{Config: params.TestChainConfig}
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, gspec, noM2Engine{ethash.NewFaker()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)
	txpool := newTestTxPool()
	handler, err := newHandler(&handlerConfig{Database: db, Chain: chain, TxPool: txpool, BlobPool: txpool, Network: 1, Sync: ethconfig.FullSync, BloomCache: 1})
	if err != nil {
		t.Fatal(err)
	}
	app, net := p2p.MsgPipe()
	defer app.Close()
	defer net.Close()
	peer := eth.NewPeer(eth.ETH69, p2p.NewPeerPipe(enode.ID{1}, "", nil, app), app, txpool, txpool, nil)
	defer peer.Close()

	if err := (*ethHandler)(handler).Handle(peer, &eth.NewBlockPacket{Block: blocks[0]}); err != nil {
		t.Fatal(err)
	}
	if bad := rawdb.ReadAllBadBlocks(db); len(bad) != 0 {
		t.Fatalf("block awaiting M2 co-signature stored as a bad block: %d", len(bad))
	}
	if n := chain.CurrentBlock().Number.Uint64(); n != 0 {
		t.Fatalf("block awaiting M2 co-signature imported: head %d", n)
	}
}
