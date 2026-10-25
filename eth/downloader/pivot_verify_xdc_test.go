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

// Tests for Hacken F-2026-19471 residual: pivot hash verification and
// state-root selection before trie download begins.

package downloader

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// makeTestPivotHeader returns a minimal types.Header with the given block number
// and state root.  Header.Hash() is computed from the RLP encoding of the full
// struct, so the hash is deterministic for a given (number, root) pair.
func makeTestPivotHeader(number uint64, root common.Hash) *types.Header {
	return &types.Header{
		Number: big.NewInt(int64(number)),
		Root:   root,
	}
}

// seedHash returns a deterministic non-zero common.Hash whose first byte is b.
func seedHash(b byte) common.Hash {
	var h common.Hash
	h[0] = b
	return h
}

// TestPivotStateRoot_HashMatchUsesServedRoot: when the compiled anchor hash
// matches the served header, pivotStateRoot must return the served header's root,
// NOT the compiled operatorPivotRoot.
//
// This is the primary Hacken F-2026-19471 residual: previously the compiled root
// was always used, meaning a wrong compiled root would seed a bad trie download
// even when the network confirmed the correct block.
//
// Fails on origin/main: pivotStateRoot does not exist.
// Passes after fix: function exists and returns P.Header.Root on hash match.
func TestPivotStateRoot_HashMatchUsesServedRoot(t *testing.T) {
	compiledRoot := seedHash(0x01) // compiled operatorPivotRoot
	servedRoot := seedHash(0x02)   // actual state root on-chain (different from compiled)

	hdr := makeTestPivotHeader(1000, servedRoot)
	opHash := hdr.Hash() // correct compiled hash (matches served block)
	opRoot := compiledRoot

	got, err := pivotStateRoot(hdr, opHash, opRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != servedRoot {
		t.Errorf("hash-verified path: want served root %s, got %s", servedRoot, got)
	}
}

// TestPivotStateRoot_HashMismatchReturnsError: when operatorPivotHash is set but
// the served pivot block's hash does not match, pivotStateRoot must return an error
// so that trie download is aborted immediately (not started for the wrong block).
//
// Fails on origin/main: pivotStateRoot does not exist.
// Passes after fix: function returns non-nil error on hash mismatch.
func TestPivotStateRoot_HashMismatchReturnsError(t *testing.T) {
	compiledRoot := seedHash(0x01)
	servedRoot := seedHash(0x02)

	hdr := makeTestPivotHeader(1000, servedRoot)
	wrongHash := seedHash(0xff) // does not match hdr.Hash()

	_, err := pivotStateRoot(hdr, wrongHash, compiledRoot)
	if err == nil {
		t.Fatal("expected error when served hash does not match compiled anchor, got nil")
	}
}

// TestPivotStateRoot_NoHashFallsBackToCompiledRoot: when operatorPivotHash is not
// set (zero) but operatorPivotRoot is set, pivotStateRoot must return the compiled
// root unchanged.  This is the legacy path where an operator supplied only
// --fastsyncpivotroot without a corresponding hash.
func TestPivotStateRoot_NoHashFallsBackToCompiledRoot(t *testing.T) {
	compiledRoot := seedHash(0x01)
	servedRoot := seedHash(0x02)

	hdr := makeTestPivotHeader(1000, servedRoot)
	zeroHash := common.Hash{}

	got, err := pivotStateRoot(hdr, zeroHash, compiledRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != compiledRoot {
		t.Errorf("no-hash path: want compiled root %s, got %s", compiledRoot, got)
	}
}

// TestPivotStateRoot_NeitherSetUsesServedRoot: dynamic-pivot path — neither
// operatorPivotHash nor operatorPivotRoot is set.  The served header's root is
// used directly, which is what the dynamic pivot path always did.
func TestPivotStateRoot_NeitherSetUsesServedRoot(t *testing.T) {
	servedRoot := seedHash(0x03)
	hdr := makeTestPivotHeader(500, servedRoot)

	got, err := pivotStateRoot(hdr, common.Hash{}, common.Hash{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != servedRoot {
		t.Errorf("dynamic path: want served root %s, got %s", servedRoot, got)
	}
}

// TestPivotStateRoot_HashMatchIdenticalRoots: when compiled root equals the
// served root (the normal case for a correctly compiled checkpoint), the function
// must still succeed and return that root.
func TestPivotStateRoot_HashMatchIdenticalRoots(t *testing.T) {
	root := seedHash(0x05)
	hdr := makeTestPivotHeader(2000, root)
	opHash := hdr.Hash()

	got, err := pivotStateRoot(hdr, opHash, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("identical roots: want %s, got %s", root, got)
	}
}
