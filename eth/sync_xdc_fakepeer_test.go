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
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/ethdb"
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
	receipts   map[common.Hash]types.Receipts // nil means "no receipts available"
	bodyCap    int
	corrupt    bool          // if true, serve empty bodies (wrong hash) for every body request
	bodyDelay  time.Duration // SYNC-12: if > 0, sleep before sending body response

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
				if len(bodies) == f.bodyCap {
					break
				}
				if f.corrupt {
					// Serve an empty body regardless of the real block content.
					// For blocks with TxHash != EmptyTxsHash this causes a hash
					// mismatch in verifyBodyAgainstHeader, exercising peer tainting.
					bodies = append(bodies, eth.BlockBody{})
					continue
				}
				b := f.blocks[h]
				if b == nil {
					break
				}
				txs, _ := rlp.EncodeToRawList(b.Transactions())
				uncles, _ := rlp.EncodeToRawList(b.Uncles())
				bodies = append(bodies, eth.BlockBody{Transactions: txs, Uncles: uncles})
			}
			// SYNC-12: simulate a slow peer by sleeping before the response.
			if f.bodyDelay > 0 {
				time.Sleep(f.bodyDelay)
			}
			select {
			case f.XDCBodyResp <- bodies:
			default:
			}
		case eth.GetReceiptsMsg:
			// XDPOS2 bare-hash receipt request (eth/63 wire shape, no RequestId).
			// Rather than sending ReceiptsMsg on the wire (which would require
			// the peer's read loop to be running for handleReceiptsXDPOS2 to
			// dispatch it), write directly to XDCReceiptResp — mirroring how
			// the body path writes to XDCBodyResp without the dispatcher.
			var hashes eth.GetReceiptsRequest
			if err := msg.Decode(&hashes); err != nil {
				return
			}
			var out []types.Receipts
			for _, h := range hashes {
				if f.receipts == nil {
					break
				}
				rcpts, ok := f.receipts[h]
				if !ok {
					break
				}
				out = append(out, rcpts)
				break // serve at most 1 block of receipts per request (mirrors the
				// soft-response-limit behaviour of production XDC peers under load)
			}
			select {
			case f.XDCReceiptResp <- out:
			default:
			}
		default:
			msg.Discard()
		}
	}
}

// xdcFakeNet is a local chain plus a canonical remote chain sharing its genesis.
type xdcFakeNet struct {
	gspec          *core.Genesis
	db             ethdb.Database // the local chain's database
	chain          *core.BlockChain
	handler        *handler
	remote         []*types.Block   // remote[i] is block i+1
	remoteReceipts []types.Receipts // remoteReceipts[i] are the receipts for remote[i]
	bodyCap        int              // bodies served per GetBlockBodies by peers added after it is set
}

// newXDCFakeNet builds a local chain holding the first `local` blocks of a
// `remote`-block canonical chain, and a handler (not started) around it.
func newXDCFakeNet(t *testing.T, local, remote int) *xdcFakeNet {
	t.Helper()
	return newXDCFakeNetEngine(t, local, remote, ethash.NewFaker())
}

// newXDCFakeNetTime is newXDCFakeNet with a custom genesis timestamp. Use it
// when tests need a fresh (recent) or explicitly stale head timestamp — for
// example, to exercise the headFresh at-tip signal in fullSyncOnce.
func newXDCFakeNetTime(t *testing.T, local, remote int, genesisTime uint64) *xdcFakeNet {
	t.Helper()
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
		Timestamp:  genesisTime,
	}
	return newXDCFakeNetGspec(t, local, remote, ethash.NewFaker(), gspec)
}

// newXDCFakeNetEngine is newXDCFakeNet with the local chain verifying blocks with engine.
func newXDCFakeNetEngine(t *testing.T, local, remote int, engine consensus.Engine) *xdcFakeNet {
	t.Helper()
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	return newXDCFakeNetGspec(t, local, remote, engine, gspec)
}

// newXDCFakeNetGspec is the shared builder used by newXDCFakeNetEngine and
// newXDCFakeNetTime. Callers supply the fully-configured genesis.
func newXDCFakeNetGspec(t *testing.T, local, remote int, engine consensus.Engine, gspec *core.Genesis) *xdcFakeNet {
	t.Helper()
	return newXDCFakeNetGspecGen(t, local, remote, engine, gspec, nil)
}

// newXDCFakeNetGspecGen is newXDCFakeNetGspec with a block generator function.
// Pass nil generator for the default (no-transaction) behavior.
func newXDCFakeNetGspecGen(t *testing.T, local, remote int, engine consensus.Engine, gspec *core.Genesis, gen func(int, *core.BlockGen)) *xdcFakeNet {
	t.Helper()
	_, blocks, receipts := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), remote, gen)
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, gspec, engine, nil)
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
	return &xdcFakeNet{gspec: gspec, db: db, chain: chain, handler: h, remote: blocks, remoteReceipts: receipts, bodyCap: 128}
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
	tip := n.remote[len(n.remote)-1]
	return n.addPeerTD(t, id, new(big.Int).Add(tip.Number(), common.Big1), headersFor, extra...)
}

// addPeerTD is addPeer with a custom total-difficulty advertised in the
// handshake.  A higher TD causes rankedPeers() to prefer this peer over
// peers with a lower TD, giving tests deterministic peer-selection order.
func (n *xdcFakeNet) addPeerTD(t *testing.T, id byte, td *big.Int, headersFor func(uint64, int) []*types.Header, extra ...*types.Block) *fakeXDCPeer {
	t.Helper()
	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := eth.NewPeer(eth.XDPOS2, p2p.NewPeer(enode.ID{id}, "fake", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)
	f := &fakeXDCPeer{Peer: p, headersFor: headersFor, blocks: make(map[common.Hash]*types.Block), receipts: make(map[common.Hash]types.Receipts), bodyCap: n.bodyCap}
	for i, b := range n.remote {
		f.blocks[b.Hash()] = b
		if i < len(n.remoteReceipts) {
			f.receipts[b.Hash()] = n.remoteReceipts[i]
		}
	}
	for _, b := range extra {
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
		TD:              td,
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

// TestSyncProgressReportsNetworkTip: after one bulk round on a chain far longer
// than one header batch, eth_syncing's highest block is the peer's tip, not the
// top of the batch just fetched (#1887).
func TestSyncProgressReportsNetworkTip(t *testing.T) {
	const remoteBlocks = 3 * xdpos2MaxHeaderFetch
	n := newXDCFakeNet(t, 5, remoteBlocks)
	core.XdcBulkSyncMode.Store(true)
	// A by-hash request (the peer-tip fetch) arrives with origin 0: serve the tip.
	n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
		if origin == 0 {
			return n.canonicalHeaders(remoteBlocks, 1)
		}
		return n.canonicalHeaders(origin, amount)
	})

	s := n.newTestXDCSyncer(t)
	s.fullSyncOnce()

	if head := n.chain.CurrentBlock().Number.Uint64(); head >= remoteBlocks-xdpos2MaxHeaderFetch {
		t.Fatalf("one round synced to %d; test needs a chain longer than a batch", head)
	}
	if highest, _ := s.SyncProgress(); highest != remoteBlocks {
		t.Fatalf("SyncProgress highest = %d, want network tip %d", highest, remoteBlocks)
	}
}
