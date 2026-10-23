// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or
// modify it under the terms of the GNU Lesser General Public License as
// published by the Free Software Foundation, either version 3 of the License,
// or (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// SYNC-07 (path-scheme): source-selection tests for fast sync on path-scheme
// (PBSS) databases.  Refs #1850.
//
// These tests verify that fastStateSync.loop() selects the state download
// source based on the LOCAL database scheme, not on peer availability:
//
//  - path-scheme DB + NodeData-eligible peer → snap/1 is used IMMEDIATELY,
//    without waiting for the 90 s noDataPeerTimeout timer.
//  - hash-scheme DB + NodeData-eligible peer → NodeData path is used; snap is
//    NOT called during the loop's lifetime.
//
// Background (from issue #1850): a path-scheme node on the net2040 testnet
// downloaded 18 811 pivot-state nodes via NodeData from the single hash-scheme
// peer, but then failed with "non existent state" because pathdb stores nodes
// by path, not by hash — the NodeData download "succeeded" but the written data
// was unreadable.  The fix (this PR) routes all pivot/gap state downloads to
// snap/1 when the local DB is path-scheme, regardless of NodeData peers.

package downloader

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/params"
)

// newTesterPathScheme creates a fast-sync downloadTester backed by a
// path-scheme (PBSS) blockchain.  All other parameters mirror newTester.
func newTesterPathScheme(t *testing.T) *downloadTester {
	t.Helper()
	gspec := &core.Genesis{
		Config:  params.TestChainConfig,
		Alloc:   types.GenesisAlloc{testAddress: {Balance: big.NewInt(1000000000000000)}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	db, err := rawdb.Open(rawdb.NewMemoryDatabase(), rawdb.OpenOptions{})
	if err != nil {
		t.Fatalf("rawdb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	chain, err := core.NewBlockChain(db, gspec, ethash.NewFaker(),
		core.DefaultConfig().WithStateScheme(rawdb.PathScheme))
	if err != nil {
		t.Fatalf("NewBlockChain (path-scheme): %v", err)
	}
	tester := &downloadTester{
		db:    db,
		chain: chain,
		peers: make(map[string]*downloadTesterPeer),
	}
	tester.downloader = New(db, ethconfig.FastSync, tester.chain, tester.dropPeer, nil, false)
	return tester
}

// TestPathScheme_PivotUsesSnapImmediately verifies source selection (SYNC-07
// path-scheme §1): when the local database uses path-scheme, fastStateSync
// routes pivot state to snap/1 IMMEDIATELY, even when a NodeData-eligible peer
// is present and the noDataPeerTimeout has not elapsed.
//
// Failure before fix: loop() would dispatch NodeData requests to the eligible
// peer, download 18 811 nodes "successfully", and then fail with "non existent
// state" when pathdb tried to look them up by path.
//
// With the fix: loop() checks rawdb.PathScheme at entry and calls
// snapSyncer.Sync before any NodeData machinery runs.
func TestPathScheme_PivotUsesSnapImmediately(t *testing.T) {
	tester := newTesterPathScheme(t)
	t.Cleanup(tester.terminate)

	// Confirm the test chain is actually path-scheme.
	if got := tester.downloader.blockchain.TrieDB().Scheme(); got != rawdb.PathScheme {
		t.Fatalf("expected path scheme, got %q", got)
	}

	// Replace snap syncer with a mock that records being called and returns nil.
	mock := newMockSnapSyncer(nil, 1)
	tester.downloader.snapSyncer = mock

	fakeRoot := common.HexToHash("0xdeadbeef00000000000000000000000000000000000000000000000000path01")

	s := &fastStateSync{
		d:    tester.downloader,
		root: fakeRoot,
		// sched.Pending() must be > 0 for the loop body to run.  We use a real
		// trie.Sync here even though the path-scheme early-exit fires before
		// the scheduling loop — newFastStateSyncSched is imported from
		// fast_snap_fallback_xdc_test.go.
		sched:             newFastStateSyncSched(tester.downloader, fakeRoot, rawdb.PathScheme),
		trieTasks:         make(map[string]*trieTask),
		codeTasks:         make(map[common.Hash]*codeTask),
		inflightHashes:    make(map[common.Hash]struct{}),
		recentHashes:      make(map[common.Hash]struct{}, 16),
		recentHashesOrder: nil,
		deliver:           make(chan *stateReq, 4),
		cancel:            make(chan struct{}),
		done:              make(chan struct{}),
		started:           make(chan struct{}),
		// Set a LONG noDataPeerTimeout to prove we are NOT relying on the timer.
		// If the fix were absent, this test would block for 24 hours.
		noDataPeerTimeout: 24 * time.Hour,
		pivotHdr: &types.Header{
			Number: big.NewInt(1000),
			Root:   fakeRoot,
		},
	}

	// Register a NodeData-eligible peer so the old timeout-based logic would
	// never fire (timer is reset when a NodeData peer appears).
	// With the path-scheme fix, snap is called before that logic runs.
	tester.downloader.peers.lock.Lock()
	tester.downloader.peers.peers["nodedata-peer-1"] = &peerConnection{
		id: "nodedata-peer-1",
	}
	tester.downloader.peers.lock.Unlock()

	err := s.loop()
	if err != nil {
		t.Fatalf("loop() returned %v — want nil (snap success)", err)
	}
	select {
	case <-mock.syncCalled:
		// snap/1 was called — correct path-scheme behaviour.
	default:
		t.Fatal("mockSnapSyncer.Sync was never called — path-scheme pivot did not route to snap/1")
	}
}

// TestHashScheme_PivotDoesNotCallSnap verifies source selection (SYNC-07
// path-scheme §2): when the local database uses hash-scheme, fastStateSync
// does NOT call snap/1 during the pivot state loop; it uses the NodeData path.
// Snap is called only when the noDataPeerTimeout fires and no NodeData peers
// are found — which is the existing SYNC-07 (#1856) behaviour.
//
// This test confirms the fix introduces no regression for hash-scheme nodes.
func TestHashScheme_PivotDoesNotCallSnap(t *testing.T) {
	// Default newTester uses hash-scheme.
	tester := newTester(t, ethconfig.FastSync)
	t.Cleanup(tester.terminate)

	if got := tester.downloader.blockchain.TrieDB().Scheme(); got != rawdb.HashScheme {
		t.Fatalf("expected hash scheme, got %q", got)
	}

	// Replace snap syncer with a mock: Sync returns nil but we want to detect
	// whether it is called at all (it should NOT be called immediately).
	mock := newMockSnapSyncer(nil, 1)
	tester.downloader.snapSyncer = mock

	fakeRoot := common.HexToHash("0xdeadbeef00000000000000000000000000000000000000000000000000hash01")

	s := &fastStateSync{
		d:                 tester.downloader,
		root:              fakeRoot,
		sched:             newFastStateSyncSched(tester.downloader, fakeRoot, rawdb.HashScheme),
		trieTasks:         make(map[string]*trieTask),
		codeTasks:         make(map[common.Hash]*codeTask),
		inflightHashes:    make(map[common.Hash]struct{}),
		recentHashes:      make(map[common.Hash]struct{}, 16),
		recentHashesOrder: nil,
		deliver:           make(chan *stateReq, 4),
		cancel:            make(chan struct{}),
		done:              make(chan struct{}),
		started:           make(chan struct{}),
		// Use a LONG timeout: we want to prove snap is NOT called before cancel.
		noDataPeerTimeout: 24 * time.Hour,
		pivotHdr: &types.Header{
			Number: big.NewInt(1000),
			Root:   fakeRoot,
		},
	}

	// Cancel after 200 ms — well within the 24h timeout window.
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(s.cancel)
	}()

	err := s.loop()
	if err != errCancelStateFetch {
		t.Fatalf("loop() returned %v — want errCancelStateFetch", err)
	}
	select {
	case <-mock.syncCalled:
		t.Fatal("mockSnapSyncer.Sync was called for hash-scheme DB — should not happen (NodeData path expected)")
	default:
		// Correct: snap/1 was not called; hash-scheme uses NodeData.
	}
}
