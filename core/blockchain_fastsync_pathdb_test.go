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

// SYNC-08: FastSyncCommitHead must call triedb.Enable(root) for path-scheme
// databases.  Without this, trie nodes written directly to the KV store via
// trie.Sync (the NodeData download path or inline snap.Sync) are invisible to
// pathdb's layer tree, and HasState returns false even though all data is
// present on disk.
// Refs: #1850, net2040 live-test failure.

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// TestFastSyncCommitHeadPathScheme verifies that FastSyncCommitHead succeeds on
// a path-scheme database after the pivot state was downloaded via NodeData (i.e.
// trie.Sync wrote path-keyed nodes directly to the raw KV store, bypassing the
// pathdb layer-commit mechanism).
//
// Failure on origin (before fix):
//
//	FastSyncCommitHead calls bc.HasState(root) → pathdb.NodeReader(root) →
//	db.tree.get(root) returns nil (root never entered the layer tree via
//	triedb.Enable) → returns "non existent state [xxxx..]".
//
// With fix:
//
//	FastSyncCommitHead calls bc.triedb.Enable(root) first for path-scheme,
//	which registers the root as the new disk layer, then HasState succeeds.
func TestFastSyncCommitHeadPathScheme(t *testing.T) {
	// Use a genesis with balance allocations so block 1 has a non-empty,
	// non-trivial state root (empty-root is a degenerate special case).
	genesis := &Genesis{
		Config:  params.AllEthashProtocolChanges,
		BaseFee: big.NewInt(params.InitialBaseFee),
		Alloc: GenesisAlloc{
			common.HexToAddress("0xdeadbeef"): {Balance: big.NewInt(1e18)},
			common.HexToAddress("0xcafebabe"): {Balance: big.NewInt(2e18)},
		},
	}
	engine := ethash.NewFaker()

	// Generate one block on a hash-scheme DB (simulates a NodeData archive peer).
	// genDb holds hash-keyed trie nodes; blocks[0] is our pivot block.
	genDb, blocks := makeBlockChainWithGenesis(genesis, 1, engine, canonicalSeed)
	pivot := blocks[0]
	pivotRoot := pivot.Root()

	// Sanity: the pivot must have a real (non-empty) state root.
	if pivotRoot == (common.Hash{}) {
		t.Fatal("pivot block has zero state root — test setup error")
	}

	// Create a fresh path-scheme blockchain (simulates the syncing node).
	// At this point pathdb's disk layer is the genesis root, not pivotRoot.
	chain2, err := NewBlockChain(rawdb.NewMemoryDatabase(), genesis, engine,
		DefaultConfig().WithStateScheme(rawdb.PathScheme))
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer chain2.Stop()

	// Simulate what fastStateSync.loop() does via NodeData: use trie.Sync with
	// path scheme to copy all pivot-state trie nodes from the hash-scheme genDb
	// into chain2's raw KV store.  This is the exact path that triggers the bug:
	// nodes are written directly to the underlying KV, bypassing pathdb's layer
	// commit, so pathdb's layer tree never records pivotRoot.
	sched := state.NewStateSync(pivotRoot, chain2.db, nil, rawdb.PathScheme)
	for sched.Pending() > 0 {
		// Missing pops items from the internal queue; use a single call per cycle.
		nodePaths, nodeHashes, _ := sched.Missing(256)
		if len(nodePaths) == 0 {
			break
		}
		// Fetch each node blob from genDb by hash (genDb is hash-scheme, so blobs
		// live at the legacy hash-keyed location — same raw RLP regardless of scheme).
		for i, path := range nodePaths {
			blob := rawdb.ReadLegacyTrieNode(genDb, nodeHashes[i])
			if len(blob) == 0 {
				t.Fatalf("trie node hash=%x not found in genDb (path=%q)", nodeHashes[i], path)
			}
			if err := sched.ProcessNode(trie.NodeSyncResult{Path: path, Data: blob}); err != nil {
				t.Fatalf("ProcessNode path=%q hash=%x: %v", path, nodeHashes[i], err)
			}
		}
		b := chain2.db.NewBatch()
		if err := sched.Commit(b); err != nil {
			t.Fatalf("sched.Commit: %v", err)
		}
		if err := b.Write(); err != nil {
			t.Fatalf("batch.Write: %v", err)
		}
	}
	if sched.Pending() != 0 {
		t.Fatalf("trie.Sync still pending after full copy: %d nodes remain", sched.Pending())
	}

	// Write the pivot block header+body to chain2's DB so GetBlockByHash works.
	rawdb.WriteBlock(chain2.db, pivot)

	// FastSyncCommitHead must succeed with the fix, and fail with "non existent
	// state" without it (because pathdb's layer tree has no record of pivotRoot).
	if err := chain2.FastSyncCommitHead(pivot.Hash()); err != nil {
		t.Fatalf("FastSyncCommitHead: %v — SYNC-08 fix missing (triedb.Enable not called for PathScheme)", err)
	}
	if !chain2.HasState(pivotRoot) {
		t.Fatal("HasState returned false after FastSyncCommitHead — state not registered in pathdb")
	}
}
