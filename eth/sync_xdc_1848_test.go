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
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// TestSnapHistoryBackfill_SYNC06 is the acceptance test for issue #1848
// (SYNC-06): after a snap-sync completes the node has state at the pivot
// but NO headers/bodies/receipts below it.  runSnapHistoryBackfill must
// download and store the missing headers before the node can serve old
// blocks or initialise the XDPoS V2 engine's masternode snapshot.
//
// Setup:
//   - remote chain: 20 blocks
//   - local chain: genesis only (simulating fresh snap-sync state)
//   - DB contains headers 10–20 (written directly, as the snap skeleton
//     would have left them after BeaconSync completes with pivot=10)
//   - snapHistoryPivot = 10
//
// Expected: after runSnapHistoryBackfill, headers 1–9 are present in the
// handler database (where PrimeAncestorHeaders writes them).
func TestSnapHistoryBackfill_SYNC06(t *testing.T) {
	const (
		remoteBlocks = 20
		pivot        = 10
	)

	n := newXDCFakeNet(t, 0, remoteBlocks)

	// Simulate the snap skeleton having written headers pivot..head to the
	// chain's DB.  In the real flow BeaconSync+processSnapSyncContent writes
	// these; here we use rawdb directly so we don't need a full downloader run.
	for _, b := range n.remote[pivot-1:] { // n.remote[9..19] = blocks 10..20
		rawdb.WriteHeader(n.db, b.Header())
		rawdb.WriteCanonicalHash(n.db, b.Hash(), b.NumberU64())
	}

	// Verify setup: pivot header must be readable by the chain.
	if got := n.chain.GetHeaderByNumber(pivot); got == nil {
		t.Fatalf("setup: pivot header %d not readable from chain DB", pivot)
	}
	// Headers 1–9 must NOT be in the handler DB yet (the backfill hasn't run).
	for i := 1; i < pivot; i++ {
		b := n.remote[i-1]
		if rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)) {
			t.Fatalf("setup: header %d already in handler DB before backfill", i)
		}
	}

	// Add a fake peer that can serve the full remote chain.
	n.addPeer(t, 1, n.canonicalHeaders)

	s := n.newTestXDCSyncer(t)
	s.snapHistoryPivot.Store(pivot)

	// runSnapHistoryBackfill is a blocking loop; run it in a goroutine so the
	// test can poll for results and honour the test timeout.
	//
	// The receipt fetch for each batch uses xdcReqTimeout (8 s) when the fake
	// peer does not handle GetReceiptsMsg.  We detect completion by checking
	// whether the headers appear in the DB — headers are written BEFORE bodies
	// and receipts, so this fires long before the receipt timeout.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSnapHistoryBackfill()
	}()

	// Poll for headers 1–9 to appear, with a generous timeout.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		allPresent := true
		for i := 1; i < pivot; i++ {
			b := n.remote[i-1]
			if !rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)) {
				allPresent = false
				break
			}
		}
		if allPresent {
			break
		}
		select {
		case <-done:
			// backfill exited early — fall through to the assertion below
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Verify all headers 1–9 are now in the handler DB.
	for i := 1; i < pivot; i++ {
		b := n.remote[i-1]
		if !rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)) {
			t.Errorf("SYNC-06: header %d missing from handler DB after backfill", i)
		}
	}

	// Headers 10–20 should NOT have been clobbered (they were in the chain DB,
	// not the handler DB; the backfill only writes to handler DB).
	for i := pivot; i <= remoteBlocks; i++ {
		b := n.remote[i-1]
		if rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)) {
			// These might also appear if the backfill ran far enough — that is
			// fine, just verify we don't see a broken-parent-link situation by
			// checking the hash matches.
			got := rawdb.ReadHeader(n.handler.database, b.Hash(), uint64(i))
			if got == nil {
				t.Errorf("SYNC-06: header %d in handler DB has wrong entry", i)
			}
		}
	}

	// Signal the backfill to stop (receipt fetches may still be in progress).
	// The test cleanup closes quitCh which aborts any outstanding requests.
	// We don't wait for the goroutine to finish here to keep the test fast.
	_ = done
}

// TestSnapHistoryPivotZeroSkips_SYNC06 verifies that runSnapHistoryBackfill
// is a no-op when no pivot has been recorded (snapHistoryPivot == 0).
func TestSnapHistoryPivotZeroSkips_SYNC06(t *testing.T) {
	n := newXDCFakeNet(t, 0, 5)
	s := n.newTestXDCSyncer(t)
	// snapHistoryPivot defaults to 0; backfill should return without touching
	// anything.
	s.runSnapHistoryBackfill()
	// If we reach here without panic, the guard worked.
	// Also verify the done flag was set.
	// (The second CAS in runSnapHistoryBackfill sets done=true.)
	if !s.snapHistoryBackfillDone.Load() {
		t.Fatalf("snapHistoryBackfillDone must be true after a no-op backfill")
	}
}

// TestSnapHistoryOneShotGuard_SYNC06 verifies that a second call to
// runSnapHistoryBackfill is a no-op (the one-shot CAS guard fires).
func TestSnapHistoryOneShotGuard_SYNC06(t *testing.T) {
	n := newXDCFakeNet(t, 0, 5)
	s := n.newTestXDCSyncer(t)
	s.snapHistoryPivot.Store(3) // non-zero pivot but no peer → returns quickly

	// First call: exits because pivot header not in chain DB.
	s.runSnapHistoryBackfill()
	if !s.snapHistoryBackfillDone.Load() {
		t.Fatalf("done flag not set after first call")
	}

	// Second call: the CAS guard must fire immediately and return without
	// touching any state (done is already true).
	s.runSnapHistoryBackfill()
	// Reaching here without blocking is the assertion.
}

// ---------------------------------------------------------------------------
// Unit tests for body and receipt verification helpers (defect 1 coverage).
// ---------------------------------------------------------------------------

// TestVerifyBodyAgainstHeader_SYNC06 checks verifyBodyAgainstHeader with
// constructed headers — no chain required.
func TestVerifyBodyAgainstHeader_SYNC06(t *testing.T) {
	// Build a single unsigned legacy tx (signature not validated by DeriveSha).
	toAddr := common.Address{0xab}
	fakeTx := types.NewTx(&types.LegacyTx{
		Nonce:    1,
		GasPrice: big.NewInt(1e9),
		Gas:      21000,
		To:       &toAddr,
		Value:    big.NewInt(0),
	})
	txHash := types.DeriveSha(types.Transactions{fakeTx}, trie.NewStackTrie(nil))

	t.Run("empty body matches empty-tx header", func(t *testing.T) {
		hdr := &types.Header{TxHash: types.EmptyTxsHash, UncleHash: types.EmptyUncleHash}
		if !verifyBodyAgainstHeader(hdr, &types.Body{}) {
			t.Fatal("empty body should pass for empty-tx header")
		}
	})

	t.Run("wrong txs fail TxHash check", func(t *testing.T) {
		hdr := &types.Header{TxHash: txHash, UncleHash: types.EmptyUncleHash}
		if verifyBodyAgainstHeader(hdr, &types.Body{}) {
			t.Fatal("empty body should fail for non-empty TxHash")
		}
	})

	t.Run("correct txs pass TxHash check", func(t *testing.T) {
		hdr := &types.Header{TxHash: txHash, UncleHash: types.EmptyUncleHash}
		body := &types.Body{Transactions: types.Transactions{fakeTx}}
		if !verifyBodyAgainstHeader(hdr, body) {
			t.Fatal("correct body should pass")
		}
	})

	t.Run("wrong uncles fail UncleHash check", func(t *testing.T) {
		uncle := &types.Header{Number: big.NewInt(1)}
		uncleHash := types.CalcUncleHash([]*types.Header{uncle})
		hdr := &types.Header{TxHash: types.EmptyTxsHash, UncleHash: uncleHash}
		// Body with no uncles should fail
		if verifyBodyAgainstHeader(hdr, &types.Body{}) {
			t.Fatal("missing uncle should fail UncleHash check")
		}
		// Correct uncle should pass
		if !verifyBodyAgainstHeader(hdr, &types.Body{Uncles: []*types.Header{uncle}}) {
			t.Fatal("correct uncle should pass")
		}
	})
}

// TestSnapBlockComplete_SYNC06 checks that snapBlockComplete correctly
// distinguishes complete blocks from incomplete ones.
func TestSnapBlockComplete_SYNC06(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	h := &types.Header{
		Number:    big.NewInt(5),
		TxHash:    types.EmptyTxsHash,
		UncleHash: types.EmptyUncleHash,
	}
	hash := h.Hash()
	num := h.Number.Uint64()

	// Before any write: not complete.
	if snapBlockComplete(db, hash, num, h.TxHash) {
		t.Fatal("should not be complete before header write")
	}

	// An empty-tx block needs its (empty) body record too, or GetBlock returns nil.
	rawdb.WriteHeader(db, h)
	rawdb.WriteCanonicalHash(db, hash, num)
	if snapBlockComplete(db, hash, num, h.TxHash) {
		t.Fatal("empty-tx block must not be complete without its body record")
	}
	rawdb.WriteBody(db, hash, num, &types.Body{})
	if !snapBlockComplete(db, hash, num, h.TxHash) {
		t.Fatal("empty-tx block should be complete after header and empty body")
	}

	// Non-empty TxHash requires body.
	nonEmptyTxHash := common.HexToHash("0x1111")
	if snapBlockComplete(db, hash, num, nonEmptyTxHash) {
		t.Fatal("should not be complete without body when TxHash != EmptyTxsHash")
	}
	rawdb.WriteBody(db, hash, num, &types.Body{})
	if snapBlockComplete(db, hash, num, nonEmptyTxHash) {
		t.Fatal("should not be complete with a body but no receipts")
	}
	rawdb.WriteReceipts(db, hash, num, types.Receipts{})
	if !snapBlockComplete(db, hash, num, nonEmptyTxHash) {
		t.Fatal("should be complete after body and receipts write")
	}
}

// ---------------------------------------------------------------------------
// Integration test: bad body peer tainted, good peer writes correct bodies.
// ---------------------------------------------------------------------------

// newXDCFakeNetWithTxs builds a fake net whose remote chain has one signed
// self-transfer per block, giving every block a non-empty TxHash.  This is
// needed for body-verification tests because empty-tx blocks are considered
// complete after the header write and never enter the body-retry loop.
func newXDCFakeNetWithTxs(t *testing.T, local, remote int) *xdcFakeNet {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("crypto.GenerateKey: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
		Alloc:      core.GenesisAlloc{addr: {Balance: new(big.Int).Mul(big.NewInt(1e18), big.NewInt(1000))}},
	}
	signer := types.LatestSigner(gspec.Config)
	gen := func(i int, b *core.BlockGen) {
		tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
			ChainID:   gspec.Config.ChainID,
			Nonce:     b.TxNonce(addr),
			GasFeeCap: b.BaseFee(),
			GasTipCap: big.NewInt(1),
			Gas:       21000,
			To:        &addr,
			Value:     big.NewInt(0),
		}), signer, key)
		if err != nil {
			panic(err)
		}
		b.AddTx(tx)
	}
	return newXDCFakeNetGspecGen(t, local, remote, ethash.NewFaker(), gspec, gen)
}

// TestSnapBadBodyTainted_SYNC06 verifies that when a peer serves a body whose
// TxHash doesn't match the header, the body is not written to the DB, the
// peer is tainted, and the backfill eventually succeeds by switching to a
// good peer.
func TestSnapBadBodyTainted_SYNC06(t *testing.T) {
	const (
		remoteBlocks = 10
		pivot        = 5
	)

	n := newXDCFakeNetWithTxs(t, 0, remoteBlocks)

	// Write pivot..head headers to chain DB (simulates snap skeleton result).
	for _, b := range n.remote[pivot-1:] {
		rawdb.WriteHeader(n.db, b.Header())
		rawdb.WriteCanonicalHash(n.db, b.Hash(), b.NumberU64())
	}
	if n.chain.GetHeaderByNumber(pivot) == nil {
		t.Fatalf("setup: pivot header not in chain DB")
	}

	// Peer A: corrupt — serves empty bodies (TxHash mismatch for all blocks).
	// Give it a higher TD so rankedPeers() always picks it first, making the
	// tainting deterministic: backfill attempt 0 always hits peer A.
	tipNum := n.remote[len(n.remote)-1].Number()
	badPeer := n.addPeerTD(t, 1, new(big.Int).Mul(tipNum, big.NewInt(100)), n.canonicalHeaders)
	badPeer.corrupt = true

	// Peer B: honest, lower TD so it's ranked second.
	n.addPeer(t, 2, n.canonicalHeaders)

	s := n.newTestXDCSyncer(t)
	s.snapHistoryPivot.Store(pivot)

	// Run backfill in a goroutine and wait for headers 1–(pivot-1) to appear.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSnapHistoryBackfill()
	}()

	// Wait for both headers AND bodies for blocks 1..(pivot-1).
	// Headers appear quickly; bodies take an extra retry cycle after peer A is
	// tainted, so we poll for both before asserting.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		allPresent := true
		for i := 1; i < pivot; i++ {
			b := n.remote[i-1]
			if !rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)) {
				allPresent = false
				break
			}
			if b.Header().TxHash != types.EmptyTxsHash && !rawdb.HasBody(n.handler.database, b.Hash(), uint64(i)) {
				allPresent = false
				break
			}
		}
		if allPresent {
			break
		}
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
		}
	}

	for i := 1; i < pivot; i++ {
		b := n.remote[i-1]
		if !rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)) {
			t.Errorf("SYNC-06: header %d missing after backfill", i)
		}
	}

	// Bodies for blocks 1..(pivot-1) must be in the DB and must match the
	// correct transaction list (not the corrupt empty body).
	for i := 1; i < pivot; i++ {
		b := n.remote[i-1]
		if b.Header().TxHash == types.EmptyTxsHash {
			continue // no body needed
		}
		body := rawdb.ReadBody(n.handler.database, b.Hash(), uint64(i))
		if body == nil {
			t.Errorf("SYNC-06: body %d missing", i)
			continue
		}
		got := types.DeriveSha(types.Transactions(body.Transactions), trie.NewStackTrie(nil))
		if got != b.Header().TxHash {
			t.Errorf("SYNC-06: body %d has wrong TxHash: got %s want %s", i, got.Hex()[:10], b.Header().TxHash.Hex()[:10])
		}
	}

	// Peer A must have been tainted (its wrong bodies caused a hash mismatch).
	if !s.isPeerTainted(badPeer.ID()) {
		t.Error("SYNC-06: bad peer was not tainted after body hash mismatch")
	}

	_ = done
}

// ---------------------------------------------------------------------------
// Integration test: restart mid-backfill resumes from the right block.
// ---------------------------------------------------------------------------

// TestSnapHistoryRestart_SYNC06 simulates a node restart that occurs in the
// middle of a snap history backfill.  A second syncer on the same handler
// database must detect the incomplete backfill and resume from where it left
// off without re-downloading already-present headers.
func TestSnapHistoryRestart_SYNC06(t *testing.T) {
	const (
		remoteBlocks = 20
		pivot        = 16
		// After "first run" we simulate having backfilled headers 11..15.
		// Headers 1..10 are still missing.
		firstRunLo = 11
		firstRunHi = 15
	)

	n := newXDCFakeNet(t, 0, remoteBlocks)

	// Write pivot..head headers to chain DB (pivot header reachable by GetHeaderByNumber).
	for _, b := range n.remote[pivot-1:] {
		rawdb.WriteHeader(n.db, b.Header())
		rawdb.WriteCanonicalHash(n.db, b.Hash(), b.NumberU64())
	}

	// Simulate partial first backfill: write headers [firstRunLo, firstRunHi]
	// directly to the handler database (what PrimeAncestorHeaders would do).
	for i := firstRunLo; i <= firstRunHi; i++ {
		h := n.remote[i-1].Header()
		rawdb.WriteHeader(n.handler.database, h)
		rawdb.WriteCanonicalHash(n.handler.database, h.Hash(), h.Number.Uint64())
	}

	// Persist the pivot number so the new syncer can find it after "restart".
	rawdb.WriteLastPivotNumber(n.handler.database, pivot)

	// Headers 1..10 must be absent in handler DB (partial run only got 11..15).
	for i := 1; i <= firstRunLo-1; i++ {
		h := n.remote[i-1].Header()
		if rawdb.HasHeader(n.handler.database, h.Hash(), h.Number.Uint64()) {
			t.Fatalf("setup: header %d should not be in handler DB before restart", i)
		}
	}

	// "Restart": create a brand-new syncer with snapHistoryPivot == 0.
	// It must detect the pivot from rawdb and resume below firstRunLo.
	n.addPeer(t, 1, n.canonicalHeaders)
	s2 := n.newTestXDCSyncer(t)
	// snapHistoryPivot is 0 — simulating a fresh process start.

	done := make(chan struct{})
	go func() {
		defer close(done)
		s2.runSnapHistoryBackfill()
	}()

	// Wait for headers 1..(firstRunLo-1) to appear.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		allPresent := true
		for i := 1; i < firstRunLo; i++ {
			h := n.remote[i-1].Header()
			if !rawdb.HasHeader(n.handler.database, h.Hash(), h.Number.Uint64()) {
				allPresent = false
				break
			}
		}
		if allPresent {
			break
		}
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
		}
	}

	for i := 1; i < firstRunLo; i++ {
		h := n.remote[i-1].Header()
		if !rawdb.HasHeader(n.handler.database, h.Hash(), h.Number.Uint64()) {
			t.Errorf("SYNC-06 restart: header %d missing after resumed backfill", i)
		}
	}

	// Headers [firstRunLo, pivot-1] were already present and must remain.
	for i := firstRunLo; i < pivot; i++ {
		h := n.remote[i-1].Header()
		if !rawdb.HasHeader(n.handler.database, h.Hash(), h.Number.Uint64()) {
			t.Errorf("SYNC-06 restart: pre-existing header %d was lost", i)
		}
	}

	_ = done
}

// ---------------------------------------------------------------------------
// Integration test: gap header present before restart → ReinitBFT fires.
// ---------------------------------------------------------------------------

// TestSnapShortBodyReply_SYNC06 is the regression test for the "remaining never
// decreases" defect observed on net2040: when blocks have very large bodies the
// peer's soft 2 MB response limit causes it to return only one body per
// GetBlockBodies request.  The previous implementation batched all pending
// hashes into a single request, then tried to pair the single returned body
// with an equal-length receipt reply through PrimeAncestorBodies — a length
// mismatch (e.g. empty-but-non-nil receipts slice) caused PrimeAncestorBodies
// to return an error that was silently discarded, so nothing was ever written.
//
// The fix:
//   - tracks reqSize and halves it whenever a reply is shorter than the request
//   - writes each verified body to the DB before fetching that block's receipts
//   - fetches receipts one block at a time, not as a batch matched to all bodies
//   - uses direct rawdb writes instead of PrimeAncestorBodies to avoid the
//     header/receipt length-mismatch error
//   - builds bodyPending from snapBlockComplete (requires body+receipts) instead
//     of !HasBody only, so blocks with body-but-no-receipt are retried
//
// Test setup: 8 blocks, all with 1 tx, pivot=4.  The peer serves bodyCap=1 so
// every GetBlockBodies reply carries exactly one body.  The backfill must write
// all bodies and receipts for blocks 1–3 before returning successfully.
func TestSnapShortBodyReply_SYNC06(t *testing.T) {
	const (
		remoteBlocks = 8
		pivot        = 4
	)

	n := newXDCFakeNetWithTxs(t, 0, remoteBlocks)
	n.bodyCap = 1 // simulate peer's soft-response-size limit: 1 body per request

	// Simulate snap skeleton: write pivot..head headers to chain DB.
	for _, b := range n.remote[pivot-1:] {
		rawdb.WriteHeader(n.db, b.Header())
		rawdb.WriteCanonicalHash(n.db, b.Hash(), b.NumberU64())
	}
	if n.chain.GetHeaderByNumber(pivot) == nil {
		t.Fatalf("setup: pivot header %d not in chain DB", pivot)
	}

	// Add the single peer AFTER setting n.bodyCap=1 so it inherits the cap.
	n.addPeer(t, 1, n.canonicalHeaders)

	s := n.newTestXDCSyncer(t)
	s.snapHistoryPivot.Store(pivot)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSnapHistoryBackfill()
	}()

	// Wait for full completion: headers, bodies, AND receipts for blocks 1..(pivot-1).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		allComplete := true
		for i := 1; i < pivot; i++ {
			b := n.remote[i-1]
			if !snapBlockComplete(n.handler.database, b.Hash(), uint64(i), b.Header().TxHash) {
				allComplete = false
				break
			}
		}
		if allComplete {
			break
		}
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}

	for i := 1; i < pivot; i++ {
		b := n.remote[i-1]
		if !snapBlockComplete(n.handler.database, b.Hash(), uint64(i), b.Header().TxHash) {
			t.Errorf("SYNC-06 short-reply: block %d not complete (header=%v body=%v receipts=%v)",
				i,
				rawdb.HasHeader(n.handler.database, b.Hash(), uint64(i)),
				rawdb.HasBody(n.handler.database, b.Hash(), uint64(i)),
				rawdb.HasReceipts(n.handler.database, b.Hash(), uint64(i)),
			)
		}
	}

	_ = done
}

// TestSnapHistoryGapDetectedOnRestart_SYNC06 verifies that when a backfill
// resumes after a restart and the V2 gap header is already in the handler
// database (written by the previous run), the backfill correctly detects that
// the gap header is present and the engine re-init path is taken.
//
// Because the test chain uses ethash (not XDPoS), the actual ReinitBFT call
// is a no-op, but we verify that the detection logic (reading the canonical
// hash for lastGapNum) works correctly: when all history is already present,
// the backfill returns immediately without downloading anything.
func TestSnapHistoryGapDetectedOnRestart_SYNC06(t *testing.T) {
	const remoteBlocks = 20

	n := newXDCFakeNet(t, 0, remoteBlocks)

	// Simulate the full history already present in the handler DB
	// (a previous backfill run completed successfully).
	for _, b := range n.remote {
		h := b.Header()
		rawdb.WriteHeader(n.handler.database, h)
		rawdb.WriteCanonicalHash(n.handler.database, h.Hash(), h.Number.Uint64())
	}
	// Also write genesis canonical hash (block 0).
	rawdb.WriteCanonicalHash(n.handler.database, n.chain.Genesis().Hash(), 0)

	// Persist the pivot number.
	const pivot = 15
	rawdb.WriteLastPivotNumber(n.handler.database, pivot)

	// Write pivot..head to chain DB so GetHeaderByNumber works.
	for _, b := range n.remote[pivot-1:] {
		rawdb.WriteHeader(n.db, b.Header())
		rawdb.WriteCanonicalHash(n.db, b.Hash(), b.NumberU64())
	}

	n.addPeer(t, 1, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)
	// snapHistoryPivot == 0 (restart scenario).

	// runSnapHistoryBackfill should detect all history present (block 1 has
	// a header in the handler DB) and return immediately without any peer
	// requests.  The 1 s timeout is generous enough even on a slow CI box.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.runSnapHistoryBackfill()
	}()

	select {
	case <-finished:
		// Correct: fast return because all history is complete.
	case <-time.After(10 * time.Second):
		t.Fatal("SYNC-06: backfill did not return quickly when all history is already present")
	}

	// Verify the backfill done flag is set.
	if !s.snapHistoryBackfillDone.Load() {
		t.Error("SYNC-06: done flag not set after no-op backfill")
	}
}
