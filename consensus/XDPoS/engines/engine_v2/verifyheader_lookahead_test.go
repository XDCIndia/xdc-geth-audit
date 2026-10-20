// Copyright (c) 2026 XDC Network
//
// Regression test for issue #1660: A.77 lookahead bulk-sync failures caused by
// GetMasternodesWithParents returning the previous epoch's masternodes when the
// in-batch epoch switch block is not yet committed to the DB.
//
// Root cause: GetMasternodesWithParents called getEpochSwitchInfo (the DB/cache
// path) unconditionally, even when in-batch parents were available.  For a
// non-epoch-switch block N whose parent is not yet in the DB, getEpochSwitchInfo's
// synthetic fallback (getEpochSwitchInfoInner, ~line 2384) fires:
//
//  1. Source B0 loads the epoch-2 gap snapshot from the DB → epoch-2 masternodes
//  2. The DB walk (lines 2491-2512) scans backward for the real epoch-switch block.
//     The epoch-2 switch IS in the in-batch parents, but is invisible to
//     GetHeaderByNumber (not yet committed).  The walk skips it and continues to
//     the epoch-1 switch which IS in the DB.
//  3. The DB walk overwrites masternodes with epoch-1's set.
//  4. The synthetic fallback returns epoch-1 info WITHOUT ERROR.
//  5. GetMasternodesWithParents returns epoch-1 masternodes → wrong leader →
//     "not its turn" / "QC signature verification failed" / "gap number mismatch".
//
// Fix: gate the DB path in GetMasternodesWithParents on len(parents) == 0.
// When parents are available, skip directly to getEpochSwitchInfoWithParents,
// whose O(N) parents scan correctly finds the in-batch epoch switch.
//
// The test calls GetMasternodesWithParents directly with a cold LRU cache to
// avoid the verifyQC pre-seeding that masks the bug in the higher-level
// verifyHeader path (verifyQC populates epochSwitches[epoch2SwitchHash] before
// GetMasternodesWithParents checks it, so the fast path hits and the DB path is
// never reached in that scenario).
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ \
//
//	-run TestGetMasternodesWithParents_InBatchEpochSwitch -v
package engine_v2

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestGetMasternodesWithParents_InBatchEpochSwitch reproduces the race that
// causes A.77 lookahead bulk-sync failures when an epoch switch lies inside the
// current verification batch and its block is not yet committed to the DB.
//
// Topology (epoch=10, gap=5, SwitchBlock=0):
//
//	DB:      gap5(5) · epoch1Switch(10, Validators=addrs) · gap15(15, snap=addrs2)
//	parents: epoch2Switch(20, Validators=addrs2) · block21 · block22
//	target:  block23  ← GetMasternodesWithParents called here, cold cache
//
// Expected result: addrs2 (epoch-2 masternodes).
// Without the fix: addrs (epoch-1 masternodes) — DB walk overwrites Source B0.
// With the fix:    addrs2 — DB path skipped; parents scan finds epoch2Switch.
func TestGetMasternodesWithParents_InBatchEpochSwitch(t *testing.T) {
	t.Parallel()
	cfg := newIB2Config() // epoch=10, gap=5, SwitchBlock=0
	db := rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	// Two distinct masternode sets, deliberately in different orders so that
	// returning the wrong one is detectable by address comparison.
	_, addrs := genNKeys(t, 3)                               // epoch-1: [A, B, C]
	addrs2 := []common.Address{addrs[2], addrs[0], addrs[1]} // epoch-2: [C, A, B]

	chain := newIBChain(cfg)
	now := uint64(time.Now().Unix())

	// ── Blocks committed to the DB (InsertChain has processed these) ─────────

	// Epoch-1 gap block at 5.
	gap5 := &types.Header{
		Number:     big.NewInt(5),
		Extra:      []byte{0x05},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 800,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap5)
	if err := storeSnapshot(newSnapshot(5, gap5.Hash(), addrs), db); err != nil {
		t.Fatalf("storeSnapshot gap5: %v", err)
	}

	// Epoch-1 switch block at round=10 (block 10).
	// NON-EMPTY Validators — matches Apothem V2 production format.
	// When the DB walk reaches this block it finds len(Validators)>0 and
	// overwrites masternodes with addrs (epoch-1 set) — that is the bug.
	e1Extra, err := (&types.ExtraFields_v2{
		Round: 10,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xaa}),
				Round:  9,
				Number: big.NewInt(9),
			},
			GapNumber: 5,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch1 switch extra: %v", err)
	}
	epoch1Switch := &types.Header{
		Number:     big.NewInt(10),
		Extra:      e1Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 600,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs), // epoch-1 masternodes, NON-EMPTY
	}
	chain.add(epoch1Switch)

	// Epoch-2 gap block at 15 (in DB so getSnapshot can find it).
	gap15 := &types.Header{
		Number:     big.NewInt(15),
		Extra:      []byte{0x0f},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 400,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap15)
	// This snapshot is loaded by Source B0 in the synthetic fallback
	// (getEpochSwitchInfoInner, quorumCert.GapNumber=15).  Without the fix,
	// the DB walk overwrites these addrs2 with epoch-1's addrs.
	if err := storeSnapshot(newSnapshot(15, gap15.Hash(), addrs2), db); err != nil {
		t.Fatalf("storeSnapshot gap15: %v", err)
	}

	// ── Blocks NOT yet committed (VerifyHeaders goroutine is ahead of InsertChain)

	// Epoch-2 switch block at round=20 (block 20) — NOT in DB.
	// NON-EMPTY Validators (matching Apothem V2: 620 bytes = 31 masternodes in
	// production; we use 3 addresses here for test simplicity).
	e2Extra, err := (&types.ExtraFields_v2{
		Round: 20,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xbb}),
				Round:  19,
				Number: big.NewInt(19),
			},
			GapNumber: 15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch2 switch extra: %v", err)
	}
	epoch2Switch := &types.Header{
		Number:     big.NewInt(20),
		Extra:      e2Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 300,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs2), // epoch-2 masternodes, NON-EMPTY
	}
	esHash := epoch2Switch.Hash()

	// Block 21 (round=21) — NOT in DB.
	b21Extra, err := (&types.ExtraFields_v2{
		Round: 21,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{Hash: esHash, Round: 20, Number: big.NewInt(20)},
			GapNumber:         15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block21 extra: %v", err)
	}
	block21 := &types.Header{
		Number:     big.NewInt(21),
		ParentHash: esHash,
		Extra:      b21Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 200,
		UncleHash:  utils.UncleHash,
	}

	// Block 22 (round=22) — NOT in DB.
	b22Extra, err := (&types.ExtraFields_v2{
		Round: 22,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{Hash: block21.Hash(), Round: 21, Number: big.NewInt(21)},
			GapNumber:         15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block22 extra: %v", err)
	}
	block22 := &types.Header{
		Number:     big.NewInt(22),
		ParentHash: block21.Hash(),
		Extra:      b22Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 100,
		UncleHash:  utils.UncleHash,
	}

	// Block 23 (round=23) — the TARGET block, NOT in DB, parent (block22) also
	// not in DB.  QC.GapNumber=15 ensures Source B0 in the synthetic fallback
	// loads the gap-15 snapshot (which holds addrs2), triggering the DB walk
	// that overwrites them with epoch-1's addrs in the unfixed code.
	b23Extra, err := (&types.ExtraFields_v2{
		Round: 23,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{Hash: block22.Hash(), Round: 22, Number: big.NewInt(22)},
			GapNumber:         15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block23 extra: %v", err)
	}
	block23 := &types.Header{
		Number:     big.NewInt(23),
		ParentHash: block22.Hash(),
		Extra:      b23Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now,
		UncleHash:  utils.UncleHash,
	}

	// parents mirrors what VerifyHeaders passes: all headers preceding block23
	// in the current batch (epoch2Switch is at offset 0, block23 is offset 3).
	parents := []*types.Header{epoch2Switch, block21, block22}

	// Call with a COLD LRU cache — no prior verifyQC call to seed it.
	// This is the condition that exposes the bug in production: the LRU cache
	// (capacity = InMemoryEpochs = 10) can be evicted by concurrent InsertChain
	// writes between verifyQC and the GetMasternodesWithParents call that follows.
	got := eng.GetMasternodesWithParents(chain.esChain, block23, parents)

	if len(got) != len(addrs2) {
		t.Fatalf("want %d epoch-2 masternodes, got %d — wrong epoch returned",
			len(addrs2), len(got))
	}
	for i, want := range addrs2 {
		if got[i] != want {
			t.Errorf("masternode[%d]: got %s, want %s — epoch-1 masternodes returned instead of epoch-2 (DB walk overwrote Source B0)",
				i, got[i].Hex(), want.Hex())
		}
	}
}
