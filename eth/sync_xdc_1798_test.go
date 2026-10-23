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

// Tests for R2-D01: fetchHeaderBatch/fetchHeaderBatchTimeout/fetchBodiesBatch
// must validate that received responses match the request.

package eth

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// makeHeader is a minimal header at the given block number.
func makeHeader(num uint64) *types.Header {
	return &types.Header{
		Number:     big.NewInt(int64(num)),
		Difficulty: big.NewInt(1),
		Time:       num,
		ParentHash: common.Hash{},
	}
}

// makeXDCPeerPair creates an eth.Peer on rw1 and returns the other end (rw2)
// for the test to use as the "remote side".
func makeXDCPeerPair(t *testing.T) (*ethproto.Peer, p2p.MsgReadWriter) {
	t.Helper()
	rw1, rw2 := p2p.MsgPipe()
	t.Cleanup(func() { rw1.Close(); rw2.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2, p2p.NewPeer(enode.ID{0xab}, "test", nil), rw1, nil, nil, params.AllEthashProtocolChanges)
	t.Cleanup(p.Close)
	return p, rw2
}

// TestFetchHeaderBatchIgnoresUnsolicitedHeaders verifies that fetchHeaderBatch
// discards a header batch whose first header number does not match `from`, waits
// for another response, and returns the matching one.
//
// Before the fix (origin/main), the single select case accepted any batch from
// XDCHeaderResp unconditionally — a peer sending unsolicited (wrong-origin)
// headers could make the syncer use them as the real response.
// After the fix, the loop continues on a mismatch and only returns when
// headers[0].Number == from. (review R2-D01)
func TestFetchHeaderBatchIgnoresUnsolicitedHeaders(t *testing.T) {
	peer, remote := makeXDCPeerPair(t)
	s := &xdcSyncer{quitCh: make(chan struct{})}

	const wantFrom = uint64(100)
	const wantCount = 3
	wrongHeaders := []*types.Header{makeHeader(50)} // wrong origin
	rightHeaders := []*types.Header{makeHeader(wantFrom), makeHeader(101), makeHeader(102)}

	// Remote side: consume the GetBlockHeaders request, then send wrong headers
	// first, then the correct ones. The XDCHeaderResp channel has cap=1 so each
	// blocking send completes only after the loop reads and discards/accepts it.
	go func() {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		msg.Discard()
		// Wrong batch first — the validation loop discards it and unblocks the send.
		peer.XDCHeaderResp <- wrongHeaders
		// Correct batch second — returned by the loop.
		peer.XDCHeaderResp <- rightHeaders
	}()

	got := s.fetchHeaderBatch(peer, wantFrom, wantCount)
	if len(got) != len(rightHeaders) {
		t.Fatalf("want %d headers, got %d", len(rightHeaders), len(got))
	}
	if got[0].Number.Uint64() != wantFrom {
		t.Fatalf("want first header #%d, got #%d", wantFrom, got[0].Number.Uint64())
	}
}

// TestFetchHeaderBatchTimeoutIgnoresUnsolicitedHeaders is the same as above but
// exercises fetchHeaderBatchTimeout with an explicit deadline.
func TestFetchHeaderBatchTimeoutIgnoresUnsolicitedHeaders(t *testing.T) {
	peer, remote := makeXDCPeerPair(t)
	s := &xdcSyncer{quitCh: make(chan struct{})}

	const wantFrom = uint64(200)
	const wantCount = 2
	wrongHeaders := []*types.Header{makeHeader(1)} // wrong origin
	rightHeaders := []*types.Header{makeHeader(wantFrom), makeHeader(201)}

	go func() {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		msg.Discard()
		peer.XDCHeaderResp <- wrongHeaders
		peer.XDCHeaderResp <- rightHeaders
	}()

	got := s.fetchHeaderBatchTimeout(peer, wantFrom, wantCount, 5*time.Second)
	if len(got) != len(rightHeaders) {
		t.Fatalf("want %d headers, got %d", len(rightHeaders), len(got))
	}
	if got[0].Number.Uint64() != wantFrom {
		t.Fatalf("want first header #%d, got #%d", wantFrom, got[0].Number.Uint64())
	}
}

// TestFetchBodiesBatchIgnoresUnsolicitedBodies verifies that fetchBodiesBatch
// discards a response with the wrong body count and waits for the matching one.
func TestFetchBodiesBatchIgnoresUnsolicitedBodies(t *testing.T) {
	peer, remote := makeXDCPeerPair(t)
	s := &xdcSyncer{quitCh: make(chan struct{})}

	hashes := []common.Hash{{0x01}, {0x02}}
	wrongBodies := []ethproto.BlockBody{{}} // count 1, want 2
	rightBodies := []ethproto.BlockBody{{}, {}}

	go func() {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		msg.Discard()
		peer.XDCBodyResp <- wrongBodies
		peer.XDCBodyResp <- rightBodies
	}()

	got := s.fetchBodiesBatch(peer, hashes)
	if len(got) != len(rightBodies) {
		t.Fatalf("want %d bodies, got %d", len(rightBodies), len(got))
	}
}
