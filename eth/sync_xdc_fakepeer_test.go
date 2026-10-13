// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package eth

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
)

// fakeXDCPeer is an XDPOS2 peer whose remote side is served in-process. It
// answers GetBlockHeaders with headersFor(origin, amount) capped at
// xdpos2MaxHeaderFetch, and GetBlockBodies with at most bodyCap bodies, the
// way legacy XDPoSChain peers do (192 headers / 128 bodies per request).
type fakeXDCPeer struct {
	*eth.Peer
	headersFor func(origin uint64, amount int) []*types.Header
	blocks     map[common.Hash]*types.Block
	bodyCap    int

	mu         sync.Mutex
	headerReqs []uint64 // origins of every GetBlockHeaders received
}

func (f *fakeXDCPeer) serve(rw p2p.MsgReadWriter) {
	for {
		msg, err := rw.ReadMsg()
		if err != nil {
			return
		}
		switch msg.Code {
		case eth.GetBlockHeadersMsg:
			var req eth.GetBlockHeadersRequest
			if err := msg.Decode(&req); err != nil {
				return
			}
			f.mu.Lock()
			f.headerReqs = append(f.headerReqs, req.Origin.Number)
			f.mu.Unlock()
			amount := min(int(req.Amount), xdpos2MaxHeaderFetch)
			select {
			case f.XDCHeaderResp <- f.headersFor(req.Origin.Number, amount):
			default:
			}
		case eth.GetBlockBodiesMsg:
			var hashes eth.GetBlockBodiesRequest
			if err := msg.Decode(&hashes); err != nil {
				return
			}
			var bodies []eth.BlockBody
			for _, h := range hashes {
				b := f.blocks[h]
				if b == nil || len(bodies) == f.bodyCap {
					break
				}
				txs, _ := rlp.EncodeToRawList(b.Transactions())
				uncles, _ := rlp.EncodeToRawList(b.Uncles())
				bodies = append(bodies, eth.BlockBody{Transactions: txs, Uncles: uncles})
			}
			select {
			case f.XDCBodyResp <- bodies:
			default:
			}
		default:
			msg.Discard()
		}
	}
}

// xdcFakeNet is a local chain plus a canonical remote chain sharing its genesis.
type xdcFakeNet struct {
	gspec   *core.Genesis
	chain   *core.BlockChain
	handler *handler
	remote  []*types.Block // remote[i] is block i+1
	bodyCap int            // bodies served per GetBlockBodies by peers added after it is set
}

// newXDCFakeNet builds a local chain holding the first `local` blocks of a
// `remote`-block canonical chain, and a handler (not started) around it.
func newXDCFakeNet(t *testing.T, local, remote int) *xdcFakeNet {
	t.Helper()
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), remote, nil)
	chain, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	if _, err := chain.InsertChain(blocks[:local]); err != nil {
		t.Fatalf("InsertChain: %v", err)
	}
	h, err := newHandler(&handlerConfig{
		Database:   rawdb.NewMemoryDatabase(),
		Chain:      chain,
		TxPool:     newTestTxPool(),
		BlobPool:   newTestTxPool(),
		Network:    1,
		Sync:       ethconfig.FullSync,
		BloomCache: 1,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	t.Cleanup(chain.Stop)
	return &xdcFakeNet{gspec: gspec, chain: chain, handler: h, remote: blocks, bodyCap: 128}
}

// canonicalHeaders serves the remote chain by number.
func (n *xdcFakeNet) canonicalHeaders(origin uint64, amount int) []*types.Header {
	var out []*types.Header
	for num := origin; num >= 1 && num <= uint64(len(n.remote)) && len(out) < amount; num++ {
		out = append(out, n.remote[num-1].Header())
	}
	return out
}

// addPeer handshakes and registers a fake XDPOS2 peer advertising the remote tip.
func (n *xdcFakeNet) addPeer(t *testing.T, id byte, headersFor func(uint64, int) []*types.Header, extra ...*types.Block) *fakeXDCPeer {
	t.Helper()
	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := eth.NewPeer(eth.XDPOS2, p2p.NewPeer(enode.ID{id}, "fake", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)
	f := &fakeXDCPeer{Peer: p, headersFor: headersFor, blocks: make(map[common.Hash]*types.Block), bodyCap: n.bodyCap}
	for _, b := range append(append([]*types.Block(nil), n.remote...), extra...) {
		f.blocks[b.Hash()] = b
	}
	errc := make(chan error, 1)
	go func() { errc <- p.Handshake(1, n.chain, eth.BlockRangeUpdatePacket{}) }()
	status, err := remote.ReadMsg() // our StatusPacket62
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	status.Discard()
	tip := n.remote[len(n.remote)-1]
	if err := p2p.Send(remote, eth.StatusMsg, &eth.StatusPacket62{
		ProtocolVersion: eth.XDPOS2,
		NetworkID:       1,
		TD:              new(big.Int).Add(tip.Number(), common.Big1),
		Head:            tip.Hash(),
		Genesis:         n.chain.Genesis().Hash(),
	}); err != nil {
		t.Fatalf("send status: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("handshake: %v", err)
	}
	go f.serve(remote)
	if err := n.handler.peers.registerPeer(p, nil); err != nil {
		t.Fatalf("registerPeer: %v", err)
	}
	return f
}

// newTestXDCSyncer returns a syncer over the fake net with the one-shot startup
// repairs already marked done, so fullSyncOnce goes straight to the sync round.
func (n *xdcFakeNet) newTestXDCSyncer(t *testing.T) *xdcSyncer {
	s := newXDCSyncer(n.handler)
	s.anchorGapRepaired.Store(true)
	t.Cleanup(func() { close(s.quitCh) })
	bulk, fork := core.XdcBulkSyncMode.Load(), core.XdcForkRecovering.Load()
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(bulk); core.XdcForkRecovering.Store(fork) })
	return s
}
