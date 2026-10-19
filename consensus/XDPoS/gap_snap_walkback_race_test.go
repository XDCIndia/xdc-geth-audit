// Copyright 2026 XDC Network

package XDPoS

// TestGapSnapWalkbackMustNotWriteDisk guards against the restart-killing race
// described in issue #1505: when a walk-back through snapshot() arrives at a
// V1 gap block and runs apply() to build a Clique-voted snapshot, it must NOT
// write that snapshot to disk.  UpdateMasternodes() is the only authorised
// writer of gap-block snapshots; any additional writer can overwrite the
// correct masternode set with a Clique-voted (wrong) one, and the corruption
// survives node restarts because the LRU cache is lost while the DB is not.
//
// Reproduction path:
//  1. Thread B: snapshot walk-back produces wrongSnap for the gap block,
//     stores it to DB (line 2346-2351 before the fix).
//  2. Thread A: UpdateMasternodes stores correctSnap to DB after Thread B.
//  3. Thread B's deferred store() executes *after* Thread A's: correctSnap
//     is overwritten with wrongSnap.
//  4. Node restarts; LRU is empty. DB has wrongSnap → checkpoint rejected
//     with errInvalidCheckpointSigners.
//
// The test exercises steps 1–3 deterministically (single-threaded) by
// verifying that the walk-back code path never writes to the DB for gap
// blocks.  The test FAILS before the fix and PASSES after.

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// headerChain serves a fixed set of headers by number; used by poison tests.
type headerChain struct {
	cfg   *params.ChainConfig
	byNum map[uint64]*types.Header
}

func (c headerChain) Config() *params.ChainConfig                 { return c.cfg }
func (c headerChain) CurrentHeader() *types.Header                { return nil }
func (c headerChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c headerChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }
func (c headerChain) GetHeaderByNumber(n uint64) *types.Header    { return c.byNum[n] }

func TestGapSnapWalkbackMustNotWriteDisk(t *testing.T) {
	// Small epoch/gap so the gap block is at block 7 (7+3 = 10, 10%10 = 0).
	cfg := *testConfig
	cfg.Epoch, cfg.Gap = 10, 3
	db := rawdb.NewMemoryDatabase()
	engine := New(&cfg, db)

	// Generate a signing key for the single masternode in the foundation snap.
	signerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := crypto.PubkeyToAddress(signerKey.PublicKey)

	// Foundation snapshot at block 6 (gap - 1) in the LRU.  The walk-back
	// will find this snap and apply the gap header on top of it.
	foundHash := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000006")
	foundSnap := newSnapshot(engine.config, engine.signatures, 6, foundHash, []common.Address{signer})
	engine.recents.Add(foundHash, foundSnap)

	// Gap header (block 7): parent = foundHash, signed by signerKey.
	// Nonce = 0x00 (nonceDropVote) with Coinbase = signer simulates a
	// masternode signing its own block in normal V1 operation.  apply()
	// treats this as a vote to remove signer, ultimately producing an empty
	// Signers set — the "wrong" snapshot the walk-back would persist to disk
	// before the fix.
	gapHeader := &types.Header{
		Number:     big.NewInt(7),
		ParentHash: foundHash,
		Difficulty: big.NewInt(1),
		Extra:      make([]byte, extraVanity+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		Coinbase:   signer,
		// Nonce is zero-valued by default → nonceDropVote.
	}
	sig, err := crypto.Sign(sigHash(gapHeader).Bytes(), signerKey)
	if err != nil {
		t.Fatal(err)
	}
	copy(gapHeader.Extra[extraVanity:], sig)
	gapHash := gapHeader.Hash()

	// Minimal chain stub: Config() is all we need; GetHeader* return nil so
	// the walk-back relies entirely on the parents slice and the LRU.
	chain := cpSignersChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}}

	// Trigger walk-back: gapHash is absent from both LRU and DB, so snapshot()
	// walks back one step to foundHash, finds foundSnap in LRU, applies
	// [gapHeader], and produces a snap.  The bug is that it then writes that
	// snap to DB (lines 2346-2351 before the fix).
	_, err = engine.snapshot(chain, 7, gapHash, []*types.Header{gapHeader})
	if err != nil {
		t.Fatal("snapshot walk-back returned unexpected error:", err)
	}

	// CRITICAL ASSERTION: the walk-back must not have written anything to disk
	// for the gap block.  UpdateMasternodes() is the sole authorised writer.
	key := append([]byte("XDPoS-"), gapHash[:]...)
	if blob, _ := db.Get(key); len(blob) > 0 {
		t.Fatal("BUG: walk-back wrote gap-block snapshot to disk; " +
			"only UpdateMasternodes() may write gap snapshots — " +
			"this data would overwrite the correct set after a restart")
	}

	// Bonus: confirm that once UpdateMasternodes() writes the correct snap to
	// disk, a post-restart lookup (LRU empty) returns the correct set and not
	// the stale walk-back result.
	correct := []common.Address{common.HexToAddress("0xaa"), common.HexToAddress("0xbb")}
	correctSnap := newSnapshot(engine.config, engine.signatures, 7, gapHash, correct)
	if err := correctSnap.store(db); err != nil {
		t.Fatal(err)
	}
	// Simulate LRU eviction / restart.
	engine.recents.Remove(gapHash)

	got, err := engine.snapshot(chain, 7, gapHash, []*types.Header{gapHeader})
	if err != nil {
		t.Fatal("post-UpdateMasternodes snapshot failed:", err)
	}
	signers := got.GetSigners()
	if len(signers) != len(correct) {
		t.Fatalf("after restart: got %d signers, want %d", len(signers), len(correct))
	}
}

// TestPoisonedGapSnapEvictedOnVerify verifies that evictPoisonedGapSnap
// removes a walk-back-poisoned DB entry when the checkpoint header is
// available in the verification context but NOT yet in the chain DB (#1505).
//
// This is the production failure path: the checkpoint is being verified for
// the first time, so chain.GetHeaderByNumber(checkpoint) returns nil, but the
// checkpoint header itself and the gap header (via parents or chain.GetHeader)
// are both in hand.
//
// The test FAILS on main (evictPoisonedGapSnap is absent) and PASSES with fix.
func TestPoisonedGapSnapEvictedOnVerify(t *testing.T) {
	cfg := *testConfig
	cfg.Epoch, cfg.Gap = 10, 3 // gap=7 (7+3=10 ≡ 0 mod 10), checkpoint=10
	db := rawdb.NewMemoryDatabase()
	engine := New(&cfg, db)

	// Correct masternode set as stored in the checkpoint header's Extra field.
	correct := []common.Address{
		common.HexToAddress("0xaa"),
		common.HexToAddress("0xbb"),
	}
	wrong := []common.Address{common.HexToAddress("0x0bad")} // poisoned single-addr snap

	// Build a minimal linked chain: gap header (7) ← h8 ← h9 ← cpHeader (10).
	// parents[0]=gap, parents[1]=h8, parents[2]=h9 so the evict walk-back can
	// reach the gap block through the parents slice (all 3 steps).
	var gapHeader, h8, h9 *types.Header
	{
		ph := common.Hash{} // genesis parent
		for _, n := range []uint64{7, 8, 9} {
			h := &types.Header{
				Number:     big.NewInt(int64(n)),
				ParentHash: ph,
				Difficulty: big.NewInt(1),
				Extra:      make([]byte, extraVanity+extraSeal),
				UncleHash:  types.EmptyUncleHash,
			}
			switch n {
			case 7:
				gapHeader = h
			case 8:
				h8 = h
			case 9:
				h9 = h
			}
			ph = h.Hash()
		}
	}
	gapHash := gapHeader.Hash()

	// Checkpoint header (block 10) with correct masternodes in Extra.
	cpExtra := make([]byte, extraVanity+len(correct)*common.AddressLength+extraSeal)
	for i, addr := range correct {
		copy(cpExtra[extraVanity+i*common.AddressLength:], addr[:])
	}
	cpHeader := &types.Header{
		Number:     big.NewInt(10),
		ParentHash: h9.Hash(),
		Difficulty: big.NewInt(1),
		Extra:      cpExtra,
		UncleHash:  types.EmptyUncleHash,
	}

	// Poison the DB: write wrong snap at gapHash.
	wrongSnap := newSnapshot(engine.config, engine.signatures, 7, gapHash, wrong)
	if err := wrongSnap.store(db); err != nil {
		t.Fatal(err)
	}

	// Chain serves NO headers by number (checkpoint not yet in chain DB).
	chain := cpSignersChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}}

	parents := []*types.Header{gapHeader, h8, h9} // covers the Gap walk-back

	// Call evictPoisonedGapSnap with the wrong gap signers.
	// With the fix: detects mismatch, writes repaired snap to DB and recents → returns true.
	// Without the fix: function doesn't exist → test fails to compile / run.
	repaired := engine.evictPoisonedGapSnap(chain, cpHeader, parents, wrong)
	if !repaired {
		t.Fatal("evictPoisonedGapSnap returned false; poisoned entry was not repaired")
	}

	// DB must now have the correct snap (from checkpoint Extra), not the poisoned one.
	loadedSnap, err := loadSnapshot(engine.config, engine.signatures, db, gapHash)
	if err != nil {
		t.Fatal("DB has no snap after repair:", err)
	}
	for _, addr := range correct {
		if _, ok := loadedSnap.Signers[addr]; !ok {
			t.Fatalf("correct address %v missing from DB snap after repair; got %v", addr, loadedSnap.GetSigners())
		}
	}
	if _, ok := loadedSnap.Signers[wrong[0]]; ok {
		t.Fatal("poisoned address still present in DB snap after repair")
	}

	// LRU must also have the correct snap.
	if s, ok := engine.recents.Get(gapHash); !ok {
		t.Fatal("recents has no snap after repair")
	} else if _, ok := s.Signers[wrong[0]]; ok {
		t.Fatal("poisoned address still present in recents snap after repair")
	}
}

// TestPoisonedGapSnapRepairedOnLoad verifies the secondary defence: when
// snapshot() is called for a gap block whose DB entry is poisoned AND the
// checkpoint IS already in the chain DB (e.g. restart after the checkpoint
// was imported), gapSnapPoisoned detects the mismatch and the bootstrap
// re-derives from the checkpoint's Extra.
func TestPoisonedGapSnapRepairedOnLoad(t *testing.T) {
	cfg := *testConfig
	cfg.Epoch, cfg.Gap = 10, 3 // gap=7, checkpoint=10
	db := rawdb.NewMemoryDatabase()
	engine := New(&cfg, db)

	gapHash := common.HexToHash("0xdeadbeef00000000000000000000000000000000000000000000000000000007")

	// Poison: write a gap snap with the wrong signer set to DB.
	wrongAddr := common.HexToAddress("0x0bad")
	wrongSnap := newSnapshot(engine.config, engine.signatures, 7, gapHash, []common.Address{wrongAddr})
	if err := wrongSnap.store(db); err != nil {
		t.Fatal(err)
	}

	correct := []common.Address{
		common.HexToAddress("0xaa"),
		common.HexToAddress("0xbb"),
	}

	// Checkpoint header served by the chain (already imported, in DB).
	cpExtra := make([]byte, extraVanity+len(correct)*common.AddressLength+extraSeal)
	for i, addr := range correct {
		copy(cpExtra[extraVanity+i*common.AddressLength:], addr[:])
	}
	cpHeader := &types.Header{
		Number:     big.NewInt(10),
		Difficulty: big.NewInt(1),
		Extra:      cpExtra,
	}
	chain := headerChain{
		cfg:   &params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg},
		byNum: map[uint64]*types.Header{10: cpHeader},
	}

	snap, err := engine.snapshot(chain, 7, gapHash, nil)
	if err != nil {
		t.Fatal("snapshot with poisoned DB returned error:", err)
	}
	got := snap.GetSigners()
	if len(got) != len(correct) {
		t.Fatalf("got %d signers (want %d); poisoned snap was not repaired", len(got), len(correct))
	}
	for _, addr := range correct {
		found := false
		for _, g := range got {
			if g == addr {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("correct address %v missing from repaired snap; got %v", addr, got)
		}
	}
}

// TestGapSnapPenaltyDiffNotTreatedAsPoisoned guards against the false-positive
// introduced in #1544: after TIPRandomize a gap snapshot legitimately holds
// more masternodes than the checkpoint's Extra (gap = checkpoint + penalised
// nodes).  evictPoisonedGapSnap and gapSnapPoisoned must NOT treat this as
// poisoning.
//
// Scenario mirroring mainnet block 3,491,100:
//
//	gap snap  = 18 masternodes (contract-elected, written by UpdateMasternodes)
//	checkpoint Extra = 17 masternodes (gap minus 1 penalised node)
//	All 17 listed addresses ARE present in the 18-node gap snap.
//
// On origin/main:
//
//	evictPoisonedGapSnap returns true  → overwrites gap snap with 17 nodes
//	gapSnapPoisoned      returns true  → discards the 18-node gap snap
//
// Both are bugs: the repair causes checkSignersOnCheckpoint to compute
// signers-minus-penalties from the already-reduced 17-node set, producing a
// count below 17 that fails to match the checkpoint's Extra.
//
// After fix:
//
//	evictPoisonedGapSnap returns false (listed ⊆ gapSigners; no repair)
//	gapSnapPoisoned      returns false (listed ⊆ snap.Signers; no discard)
func TestGapSnapPenaltyDiffNotTreatedAsPoisoned(t *testing.T) {
	cfg := *testConfig
	cfg.Epoch, cfg.Gap = 10, 3 // gap=7 (7+3=10), checkpoint=10
	db := rawdb.NewMemoryDatabase()
	engine := New(&cfg, db)

	// 17 addresses that appear in the checkpoint's Extra (post-penalty set).
	checkpointAddrs := make([]common.Address, 17)
	for i := range checkpointAddrs {
		checkpointAddrs[i] = common.HexToAddress(common.Bytes2Hex([]byte{byte(i + 1)}))
	}
	// Gap snap has 18 nodes: all 17 checkpoint nodes + 1 penalised node.
	penalised := common.HexToAddress("0xff")
	gapSigners := append(append([]common.Address{}, checkpointAddrs...), penalised)

	// Build a minimal linked chain: gap(7) ← h8 ← h9 ← cp(10).
	var gapHeader, h8, h9 *types.Header
	{
		ph := common.Hash{}
		for _, n := range []uint64{7, 8, 9} {
			h := &types.Header{
				Number:     big.NewInt(int64(n)),
				ParentHash: ph,
				Difficulty: big.NewInt(1),
				Extra:      make([]byte, extraVanity+extraSeal),
				UncleHash:  types.EmptyUncleHash,
			}
			switch n {
			case 7:
				gapHeader = h
			case 8:
				h8 = h
			case 9:
				h9 = h
			}
			ph = h.Hash()
		}
	}
	gapHash := gapHeader.Hash()

	// Checkpoint Extra encodes the 17 post-penalty masternodes.
	cpExtra := make([]byte, extraVanity+len(checkpointAddrs)*common.AddressLength+extraSeal)
	for i, addr := range checkpointAddrs {
		copy(cpExtra[extraVanity+i*common.AddressLength:], addr[:])
	}
	cpHeader := &types.Header{
		Number:     big.NewInt(10),
		ParentHash: h9.Hash(),
		Difficulty: big.NewInt(1),
		Extra:      cpExtra,
		UncleHash:  types.EmptyUncleHash,
	}

	// Store the CORRECT 18-node gap snap in DB (as UpdateMasternodes would).
	correctSnap := newSnapshot(engine.config, engine.signatures, 7, gapHash, gapSigners)
	if err := correctSnap.store(db); err != nil {
		t.Fatal(err)
	}

	chain := cpSignersChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}}
	parents := []*types.Header{gapHeader, h8, h9}

	// --- evictPoisonedGapSnap must NOT fire ---
	// On main: returns true (wrongly evicts); after fix: returns false.
	if evicted := engine.evictPoisonedGapSnap(chain, cpHeader, parents, gapSigners); evicted {
		t.Fatal("evictPoisonedGapSnap returned true for a legitimate penalty diff " +
			"(gap=18 nodes, checkpoint Extra=17 nodes ⊆ gap); " +
			"this would corrupt the gap snap and cause errInvalidCheckpointSigners")
	}

	// DB must still hold the 18-node snap untouched.
	loaded, err := loadSnapshot(engine.config, engine.signatures, db, gapHash)
	if err != nil {
		t.Fatal("gap snap deleted from DB after false-positive eviction:", err)
	}
	if len(loaded.Signers) != len(gapSigners) {
		t.Fatalf("gap snap signers changed: got %d, want %d", len(loaded.Signers), len(gapSigners))
	}

	// --- gapSnapPoisoned must NOT fire ---
	// Build a chain stub that serves the checkpoint header by number=10.
	chain2 := headerChain{
		cfg:   &params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg},
		byNum: map[uint64]*types.Header{10: cpHeader},
	}
	if poisoned := engine.gapSnapPoisoned(chain2, 7, correctSnap); poisoned {
		t.Fatal("gapSnapPoisoned returned true for a legitimate penalty diff " +
			"(gap snap has 18 nodes, checkpoint Extra lists 17 ⊆ those 18); " +
			"this causes the bootstrap to overwrite the correct snap")
	}
}
