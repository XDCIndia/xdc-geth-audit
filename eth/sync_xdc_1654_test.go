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
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// addPeerAtSameHead registers a fake XDPOS2 peer whose STATUS head is exactly
// our local chain's current head (simulating the "peer was at our block at
// handshake time" scenario). The peer CAN serve blocks beyond our head (so sync
// should proceed), but its LatestBlockHash is frozen at hash(localHead).
func (n *xdcFakeNet) addPeerAtSameHead(t *testing.T, id byte) *fakeXDCPeer {
	t.Helper()
	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := eth.NewPeer(eth.XDPOS2, p2p.NewPeer(enode.ID{id}, "fake-same-head", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)
	f := &fakeXDCPeer{
		Peer:       p,
		headersFor: n.canonicalHeaders, // can serve all remote blocks
		blocks:     make(map[common.Hash]*types.Block),
		bodyCap:    n.bodyCap,
	}
	for _, b := range n.remote {
		f.blocks[b.Hash()] = b
	}
	errc := make(chan error, 1)
	go func() { errc <- p.Handshake(1, n.chain, eth.BlockRangeUpdatePacket{}) }()
	status, err := remote.ReadMsg() // consume our STATUS
	if err != nil {
		t.Fatalf("addPeerAtSameHead: read status: %v", err)
	}
	status.Discard()

	// Advertise HEAD = our current local head — not the remote tip.
	// This simulates: peer was at block N (same as us) at handshake time.
	// For XDPOS2, LatestBlockHash is frozen here and never updated.
	localHead := n.chain.CurrentBlock()
	if err := p2p.Send(remote, eth.StatusMsg, &eth.StatusPacket62{
		ProtocolVersion: eth.XDPOS2,
		NetworkID:       1,
		TD:              new(big.Int).Add(localHead.Number, big.NewInt(1)),
		Head:            localHead.Hash(), // frozen at our curHash
		Genesis:         n.chain.Genesis().Hash(),
	}); err != nil {
		t.Fatalf("addPeerAtSameHead: send status: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("addPeerAtSameHead: handshake: %v", err)
	}
	go f.serve(remote)
	if err := n.handler.peers.registerPeer(p, nil); err != nil {
		t.Fatalf("addPeerAtSameHead: registerPeer: %v", err)
	}
	return f
}

// TestXDPOS2PeerAtSameHeadFetchHeadersParallel verifies the fetchHeadersParallel
// fix for issue #1654: an XDPOS2 peer with LatestBlockHash==curHash must NOT be
// counted as atTip; it must be placed in the raced set so headers are requested.
//
// Root cause: XDPOS2 peers never receive BlockRangeUpdate messages (see
// SendBlockRangeUpdate in peer.go). Their LatestBlockHash is frozen at handshake
// time. If a peer connects via admin.addPeer while it is at our exact head block
// (common when all nodes restart together and BulkSyncMode delays minting for
// 90-120 s), rng.LatestBlockHash == curHash permanently. fetchHeadersParallel
// counted such a peer as "at-tip" and skipped the header request — a permanent
// sync stall even though the peer has since advanced.
//
// Fix: skip the hash-equality at-tip shortcut for XDPOS2 peers; rely on the
// empty header response as the only correct at-tip signal.
func TestXDPOS2PeerAtSameHeadFetchHeadersParallel(t *testing.T) {
	const localBlocks = 5
	const remoteBlocks = 20

	n := newXDCFakeNet(t, localBlocks, remoteBlocks)

	// Peer's STATUS.Head == our chain head (frozen at handshake).
	n.addPeerAtSameHead(t, 1)

	s := n.newTestXDCSyncer(t)
	candidates := s.rankedPeers()
	if len(candidates) == 0 {
		t.Fatal("rankedPeers: no candidates")
	}

	cur := n.chain.CurrentBlock()
	curHash := cur.Hash()
	fromBlock := cur.Number.Uint64() + 1

	// Sanity-check: the peer's LatestBlockHash must equal our curHash.
	rng := candidates[0].BlockRange()
	if rng == nil {
		t.Fatal("peer has nil BlockRange")
	}
	if rng.LatestBlockHash != curHash {
		t.Skipf("peer LatestBlockHash=%v != curHash=%v; precondition not met", rng.LatestBlockHash, curHash)
	}

	_, atTipCount, tried, _ := s.fetchHeadersParallel(candidates, fromBlock, curHash)

	// After the fix: atTipCount must be 0 for XDPOS2 peers. The peer is placed
	// in raced and asked for headers; hash-equality at-tip is bypassed.
	// Before the fix: atTipCount==1, tried==1 → fullSyncOnce returned without
	// importing, stalling the node indefinitely (issue #1654).
	if atTipCount != 0 {
		t.Fatalf("fetchHeadersParallel counted XDPOS2 peer as at-tip (atTipCount=%d, tried=%d) "+
			"because its frozen LatestBlockHash==curHash; XDPOS2 peers must bypass "+
			"the hash-equality at-tip shortcut — issue #1654", atTipCount, tried)
	}
	_ = tried
}

// TestXDPOS2PeerAtSameHeadDoesNotStallSync is the higher-level regression test
// for issue #1654. It builds a local chain at 5 blocks, adds an XDPOS2 peer
// whose STATUS head is our block 5 (frozen LatestBlockHash == curHash), and
// verifies that fullSyncOnce imports blocks anyway rather than stalling.
func TestXDPOS2PeerAtSameHeadDoesNotStallSync(t *testing.T) {
	const localBlocks = 5
	const remoteBlocks = 20

	n := newXDCFakeNet(t, localBlocks, remoteBlocks)
	core.XdcBulkSyncMode.Store(true)

	n.addPeerAtSameHead(t, 1)

	s := n.newTestXDCSyncer(t)
	s.fullSyncOnce()

	head := n.chain.CurrentBlock().Number.Uint64()
	if head <= localBlocks {
		t.Fatalf("sync stalled at block %d (expected >%d): XDPOS2 peer's frozen "+
			"LatestBlockHash==curHash caused false at-tip detection — issue #1654",
			head, localBlocks)
	}
}
