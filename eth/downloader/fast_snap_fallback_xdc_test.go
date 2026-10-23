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

// SYNC-07: fast sync snaps/1 state fallback tests. Refs #1850.
//
// These tests verify that when --syncmode fast is used on an all-path-scheme
// (PBSS) network where no peer serves GetNodeData, the fastStateSync loop()
// detects the stall and falls back to snap/1 range requests to download the
// pivot state, rather than spinning forever.

package downloader

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

// mockSnapSyncer is a minimal snap.Syncer that returns syncResult from Sync()
// and reports snapPeerCount from PeerCount().  All other methods are no-ops.
type mockSnapSyncer struct {
	syncResult    error
	snapPeerCount int
	syncCalled    chan struct{} // closed when Sync is first called
	once          bool
}

func newMockSnapSyncer(result error, peerCount int) *mockSnapSyncer {
	return &mockSnapSyncer{
		syncResult:    result,
		snapPeerCount: peerCount,
		syncCalled:    make(chan struct{}),
	}
}

func (m *mockSnapSyncer) Sync(_ *types.Header, _ chan struct{}) error {
	if !m.once {
		m.once = true
		close(m.syncCalled)
	}
	return m.syncResult
}
func (m *mockSnapSyncer) PeerCount() int                   { return m.snapPeerCount }
func (m *mockSnapSyncer) Progress() snap.Progress          { return snap.Progress{} }
func (m *mockSnapSyncer) Register(_ snap.SyncPeerV2) error { return nil }
func (m *mockSnapSyncer) Unregister(_ string) error        { return nil }
func (m *mockSnapSyncer) FrozenPivot() *types.Header       { return nil }
func (m *mockSnapSyncer) Version() uint                    { return 1 }
func (m *mockSnapSyncer) OnAccounts(_ snap.SyncPeerV2, _ uint64, _ []common.Hash, _ [][]byte, _ [][]byte) error {
	return nil
}
func (m *mockSnapSyncer) OnStorage(_ snap.SyncPeerV2, _ uint64, _ [][]common.Hash, _ [][][]byte, _ [][]byte) error {
	return nil
}
func (m *mockSnapSyncer) OnByteCodes(_ snap.SyncPeerV2, _ uint64, _ [][]byte) error { return nil }
func (m *mockSnapSyncer) OnTrieNodes(_ snap.SyncPeerV2, _ uint64, _ [][]byte) error { return nil }
func (m *mockSnapSyncer) OnAccessLists(_ snap.SyncPeerV2, _ uint64, _ rlp.RawList[rlp.RawValue]) error {
	return nil
}

// TestFastSyncSnapFallback_SelectsSnapWhenNoNodeDataPeers verifies the
// source-selection decision (SYNC-07 §1): when all connected peers are
// PBSS-tagged (no NodeData eligibility) but snap peers are registered, loop()
// falls back to snap/1 and returns nil, rather than stalling indefinitely.
//
// Failure on origin/main: the loop() has no stall detector and no snap fallback
// — it loops on newPeer forever with 0 NodeData peers, never returning.
// With the fix: noDataTimer fires after the test-injected short timeout and
// loop() calls mockSnapSyncer.Sync(), which returns nil, completing the sync.
func TestFastSyncSnapFallback_SelectsSnapWhenNoNodeDataPeers(t *testing.T) {
	tester := newTester(t, ethconfig.FastSync)
	t.Cleanup(tester.terminate)

	// Replace the private snap syncer with our mock (1 snap peer, returns nil).
	mock := newMockSnapSyncer(nil, 1)
	tester.downloader.snapSyncer = mock

	// Build a minimal fastStateSync that schedules a state root which no peer
	// will serve via NodeData.  We use a fake root (non-empty hash) so
	// sched.Pending() > 0 and the loop body actually runs.
	fakeRoot := common.HexToHash("0xdeadbeef0000000000000000000000000000000000000000000000000000cafe")

	s := &fastStateSync{
		d:    tester.downloader,
		root: fakeRoot,
		// sched.Pending() > 0 requires a real trie.Sync with pending tasks.
		// We use the exported newFastStateSync helper and then set pivotHdr.
		trieTasks:         make(map[string]*trieTask),
		codeTasks:         make(map[common.Hash]*codeTask),
		inflightHashes:    make(map[common.Hash]struct{}),
		recentHashes:      make(map[common.Hash]struct{}, 16),
		recentHashesOrder: nil,
		deliver:           make(chan *stateReq, 4),
		cancel:            make(chan struct{}),
		done:              make(chan struct{}),
		started:           make(chan struct{}),
		// Short timeout so the test completes quickly.
		noDataPeerTimeout: 200 * time.Millisecond,
		// pivotHdr triggers the snap fallback path.
		pivotHdr: &types.Header{
			Number: newBigInt(100),
			Root:   fakeRoot,
		},
	}
	// We need sched.Pending() > 0 to enter the loop.  Construct a real
	// trie.Sync for the fake root — it will have at least the root itself
	// as a pending task.
	scheme := tester.downloader.blockchain.TrieDB().Scheme()
	s.sched = newFastStateSyncSched(tester.downloader, fakeRoot, scheme)

	log.Info("SYNC-07 test: starting loop with 0 NodeData peers, 1 snap peer, short timeout")
	err := s.loop()
	if err != nil {
		t.Fatalf("loop() returned unexpected error: %v — want nil (snap fallback success)", err)
	}
	select {
	case <-mock.syncCalled:
		// Sync was called — correct behaviour.
	default:
		t.Fatal("mockSnapSyncer.Sync was never called — snap fallback did not fire")
	}
}

// TestFastSyncSnapFallback_NoFallbackForGapBlocks verifies the source-selection
// decision (SYNC-07 §2): when pivotHdr is nil (gap-block download), loop() does
// NOT call snap fallback regardless of how long NodeData stalls.  The timer
// re-arms and loop() runs until cancelled.
func TestFastSyncSnapFallback_NoFallbackForGapBlocks(t *testing.T) {
	tester := newTester(t, ethconfig.FastSync)
	t.Cleanup(tester.terminate)

	mock := newMockSnapSyncer(nil, 1)
	tester.downloader.snapSyncer = mock

	fakeRoot := common.HexToHash("0xgapblock0000000000000000000000000000000000000000000000000000cafe")

	s := &fastStateSync{
		d:                 tester.downloader,
		root:              fakeRoot,
		trieTasks:         make(map[string]*trieTask),
		codeTasks:         make(map[common.Hash]*codeTask),
		inflightHashes:    make(map[common.Hash]struct{}),
		recentHashes:      make(map[common.Hash]struct{}, 16),
		recentHashesOrder: nil,
		deliver:           make(chan *stateReq, 4),
		cancel:            make(chan struct{}),
		done:              make(chan struct{}),
		started:           make(chan struct{}),
		noDataPeerTimeout: 150 * time.Millisecond,
		pivotHdr:          nil, // nil = gap-block, no snap fallback
	}
	scheme := tester.downloader.blockchain.TrieDB().Scheme()
	s.sched = newFastStateSyncSched(tester.downloader, fakeRoot, scheme)

	// Cancel after two timer firings — loop should still not have called Sync.
	go func() {
		time.Sleep(400 * time.Millisecond) // two timer windows
		close(s.cancel)
	}()

	err := s.loop()
	if err != errCancelStateFetch {
		t.Fatalf("loop() returned %v — want errCancelStateFetch", err)
	}
	select {
	case <-mock.syncCalled:
		t.Fatal("mockSnapSyncer.Sync was called for a gap-block download — should not happen")
	default:
		// Correct: snap fallback did not fire for gap blocks.
	}
}

// newFastStateSyncSched constructs a trie.Sync for root via the same path as
// newFastStateSync, returning it for injection into hand-built fastStateSync
// structs in tests.
func newFastStateSyncSched(d *Downloader, root common.Hash, scheme string) *trie.Sync {
	return state.NewStateSync(root, d.stateDB, nil, scheme)
}

var _ *trie.Sync // keep trie import referenced

// newBigInt is a test helper that returns a *big.Int from a uint64.
func newBigInt(n uint64) *big.Int {
	return new(big.Int).SetUint64(n)
}
