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

// Integration tests for the SYNC-11 security fix (#1863):
// fetchVerifiedHeaderRange must reject a peer that serves a forged switch header
// and must succeed with an honest peer. These tests use the fakeXDCPeer harness
// so they exercise the actual fetch-and-verify path in xdcSyncer.
//
// Why seedSnapPivotGapSnapshots is not tested end-to-end here:
// seedSnapPivotGapSnapshots casts the chain's consensus engine to *XDPoS.XDPoS /
// *engine_v2.XDPoS_v2. The fakeXDCPeer harness uses ethash.NewFaker() for the
// block engine and building a correctly-configured XDPoS genesis (with masternode
// SMC state, epoch parameters, and signed epoch-switch headers) requires the full
// testnet scaffold that lives in the consensus/XDPoS/… package tests. That
// scaffold cannot be imported into the eth package without a circular dependency.
// The security property (forged-chain rejection) is fully exercised here via
// fetchVerifiedHeaderRange, and the masternode-set correctness is covered by
// TestSnapGapSeedSYNC11 in consensus/XDPoS/engines/engine_v2/.

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestFetchVerifiedHeaderRange_ForgedSwitchRejected is the SYNC-11 security
// regression test. It exercises fetchVerifiedHeaderRange (the function called by
// seedSnapPivotGapSnapshots for gaps whose switch is below the snap pivot) with
// two peers:
//
//   - A malicious peer that serves the correct range EXCEPT the switch header,
//     which has a tampered Extra field (simulating wrong Validators). The tampered
//     header changes the switch header's hash, breaking the parent-hash chain that
//     follows it, so the range does NOT terminate at the local pivot's ParentHash.
//     fetchVerifiedHeaderRange must reject this peer.
//
//   - An honest peer that serves the true canonical range. fetchVerifiedHeaderRange
//     must accept it and return the full slice.
//
// Chain setup: remote=20 blocks, local=15 blocks. Pivot = block 15 (in local DB,
// trusted). Fetch range = blocks 5..14 (10 headers). The switch header is
// arbitrarily placed at block 10 (within the range). The malicious peer tampers
// block 10's Extra; the forged chain from block 11 onward cannot reproduce the
// correct pivot parent hash.
func TestFetchVerifiedHeaderRange_ForgedSwitchRejected(t *testing.T) {
	const (
		remoteLen  = 20
		localLen   = 15
		pivotBlock = uint64(15)
		rangeFrom  = uint64(5)
		switchNum  = uint64(10) // the "switch header" the malicious peer forges
	)

	net := newXDCFakeNet(t, localLen, remoteLen)
	s := net.newTestXDCSyncer(t)

	// The pivot header is canonical in the local chain (InsertChain put it there).
	pivotHdr := net.remote[pivotBlock-1].Header() // remote is 0-indexed

	// --- Malicious peer (higher TD → tried first by rankedPeers) ---
	// Serves the correct range for blocks rangeFrom..switchNum-1, then a forged
	// block switchNum (Extra tampered → hash changes → subsequent parent links break).
	forgedSwitch := *net.remote[switchNum-1].Header()
	forgedSwitch.Extra = append([]byte("FORGED"), forgedSwitch.Extra...)

	maliciousHeaders := func(origin uint64, amount int) []*types.Header {
		var out []*types.Header
		for n := origin; n < origin+uint64(amount) && n <= pivotBlock-1; n++ {
			if n == switchNum {
				out = append(out, &forgedSwitch)
			} else {
				out = append(out, net.remote[n-1].Header())
			}
		}
		return out
	}
	net.addPeerTD(t, 0x01, big.NewInt(1000), maliciousHeaders)

	// fetchVerifiedHeaderRange must reject the malicious peer — the forged switch
	// header changes hash(block 10), breaking the parent link of block 11 onward.
	// The last header in the range therefore cannot match pivotHdr.ParentHash.
	got := s.fetchVerifiedHeaderRange(rangeFrom, pivotBlock, pivotHdr)
	if got != nil {
		t.Fatalf("SYNC-11 security: forged switch header was accepted — seed would use wrong masternodes")
	}

	// --- Honest peer (lower TD → tried after malicious) ---
	net.addPeerTD(t, 0x02, big.NewInt(500), net.canonicalHeaders)

	got = s.fetchVerifiedHeaderRange(rangeFrom, pivotBlock, pivotHdr)
	if got == nil {
		t.Fatal("honest peer range rejected — fetchVerifiedHeaderRange broken")
	}
	want := pivotBlock - rangeFrom
	if uint64(len(got)) != want {
		t.Fatalf("want %d headers from honest peer, got %d", want, len(got))
	}
	// Confirm the returned switch header is the genuine one (not the forged one).
	if got[switchNum-rangeFrom].Hash() == (common.Hash{}) {
		t.Fatal("switch header hash is zero — returned forged header")
	}
	if got[switchNum-rangeFrom].Extra != nil {
		// Genuine block 10 has its real Extra; forged one starts with "FORGED".
		if len(got[switchNum-rangeFrom].Extra) >= 6 &&
			string(got[switchNum-rangeFrom].Extra[:6]) == "FORGED" {
			t.Fatal("honest range returned forged switch header")
		}
	}
	// Confirm chain termination: last header's hash == pivot's ParentHash.
	if got[len(got)-1].Hash() != pivotHdr.ParentHash {
		t.Fatalf("range tail hash %s != pivot.ParentHash %s",
			got[len(got)-1].Hash(), pivotHdr.ParentHash)
	}
}

// TestFetchVerifiedHeaderRange_BatchedFetch verifies that fetchVerifiedHeaderRange
// correctly handles ranges that exceed xdpos2MaxHeaderFetch (192), requiring
// multiple batch requests to cover the full span.
func TestFetchVerifiedHeaderRange_BatchedFetch(t *testing.T) {
	// remote=250, local=220: range 1..219 = 219 headers → two batches (192+27).
	const (
		remoteLen  = 250
		localLen   = 220
		pivotBlock = uint64(220)
		rangeFrom  = uint64(1)
	)

	net := newXDCFakeNet(t, localLen, remoteLen)
	s := net.newTestXDCSyncer(t)

	pivotHdr := net.remote[pivotBlock-1].Header()
	net.addPeer(t, 0x01, net.canonicalHeaders)

	got := s.fetchVerifiedHeaderRange(rangeFrom, pivotBlock, pivotHdr)
	if got == nil {
		t.Fatal("multi-batch range rejected unexpectedly")
	}
	want := pivotBlock - rangeFrom
	if uint64(len(got)) != want {
		t.Fatalf("want %d headers, got %d", want, len(got))
	}
	if got[len(got)-1].Hash() != pivotHdr.ParentHash {
		t.Fatal("range tail does not connect to pivot")
	}
}
