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

// Tests for issue #1761: prefetchAncestorEpochs must require each batch to reach
// hi and match the expected parent hash before writing (review R1-S02).

package eth

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestPrefetchAncestorEpochs_ShortBatch_WritesNothing verifies that a peer
// returning fewer headers than requested does not have its batch written to the
// DB, and that the peer is tainted.
//
// FAILS on origin/main (headers written despite unanchored batch).
// PASSES with the fix (short batch rejected before PrimeAncestorHeaders).
func TestPrefetchAncestorEpochs_ShortBatch_WritesNothing(t *testing.T) {
	// 300 blocks so batchStart > 0 when epoch=900 depth=1800.
	// hi = 299, batchStart = max(0, 299-192+1) = 108, want = 192.
	const remote = 300
	n := newXDCFakeNet(t, 0, remote)

	// Short peer: returns only 1 header per request regardless of amount.
	shortPeer := n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
		return n.canonicalHeaders(origin, 1) // always 1, short
	})
	s := n.newTestXDCSyncer(t)

	primeBase := n.remote[remote-1].Header() // block 300
	s.prefetchAncestorEpochs(shortPeer.Peer, primeBase)

	// Nothing must be written to the downloader's DB.
	for i := 1; i <= remote-1; i++ {
		h := n.remote[i-1].Header()
		if rawdb.HasHeader(n.handler.database, h.Hash(), h.Number.Uint64()) {
			t.Errorf("block %d was written to DB despite short peer response (refs #1761)", h.Number.Uint64())
		}
	}
	// The short-responding peer must be tainted.
	if !s.isPeerTainted(shortPeer.ID()) {
		t.Error("short-response peer was not tainted (refs #1761)")
	}
}

// TestPrefetchAncestorEpochs_FullBatch_WritesHeaders verifies that a peer
// returning the full requested batch still has its headers written after the fix.
func TestPrefetchAncestorEpochs_FullBatch_WritesHeaders(t *testing.T) {
	const remote = 300
	n := newXDCFakeNet(t, 0, remote)

	// Canonical peer: serves the full chain.
	canonPeer := n.addPeer(t, 1, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)

	primeBase := n.remote[remote-1].Header() // block 300
	s.prefetchAncestorEpochs(canonPeer.Peer, primeBase)

	// The first batch covers blocks 108..299 (batchStart=108, want=192).
	// Verify at least the top header of that batch was written.
	top := n.remote[298].Header() // block 299 = hi
	if !rawdb.HasHeader(n.handler.database, top.Hash(), top.Number.Uint64()) {
		t.Errorf("block 299 (top of first batch) was NOT written — fix broke legitimate peers (refs #1761)")
	}
	// Peer must NOT be tainted.
	if s.isPeerTainted(canonPeer.ID()) {
		t.Error("canonical peer was wrongly tainted (refs #1761)")
	}
}
