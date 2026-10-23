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

// Tests for R3-S01: fetchHeaderByHash must validate that the returned header
// hash matches the requested hash.

package eth

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// TestFetchHeaderByHashValidatesHash verifies that fetchHeaderByHash returns nil
// when the peer returns a header with a different hash than requested, and
// accepts the correct header when it matches.
//
// Before the fix (origin/main), fetchHeaderByHash would return headers[0]
// unconditionally without checking that headers[0].Hash() == hash.
// A malicious or buggy peer could return any header and it would be used.
// After the fix, we validate the hash and return nil on mismatch. (review R3-S01)
func TestFetchHeaderByHashValidatesHash(t *testing.T) {
	peer, remote := makeXDCPeerPair(t)
	s := &xdcSyncer{quitCh: make(chan struct{})}

	// Create two distinct headers with different numbers so they have different hashes.
	wrongHeader := makeHeader(100)
	wantHeader := makeHeader(101)

	wantHash := wantHeader.Hash()

	// Remote side: send a header with a different hash than what we request.
	go func() {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		msg.Discard()
		// Send a header that doesn't match the requested hash.
		peer.XDCHeaderResp <- []*types.Header{wrongHeader}
	}()

	// Call should get nil because the header hash doesn't match the request.
	// On origin/main this returns the wrong header (bug).
	// After the fix, this returns nil.
	got := s.fetchHeaderByHash(peer, wantHash)
	if got != nil {
		t.Fatalf("expected nil on hash mismatch, got header #%d with hash %v", got.Number.Uint64(), got.Hash())
	}
}

// TestFetchHeaderByHashAcceptsMatchingHash verifies that fetchHeaderByHash
// accepts a header when its hash matches the request.
func TestFetchHeaderByHashAcceptsMatchingHash(t *testing.T) {
	peer, remote := makeXDCPeerPair(t)
	s := &xdcSyncer{quitCh: make(chan struct{})}

	wantHeader := makeHeader(200)
	wantHash := wantHeader.Hash()

	go func() {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		msg.Discard()
		peer.XDCHeaderResp <- []*types.Header{wantHeader}
	}()

	got := s.fetchHeaderByHash(peer, wantHash)
	if got == nil {
		t.Fatalf("expected header, got nil")
	}
	if got.Hash() != wantHash {
		t.Fatalf("expected header hash %v, got %v", wantHash, got.Hash())
	}
}
