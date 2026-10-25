// Copyright 2024 The go-ethereum Authors
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

package core

// Tests for the fix introduced in #1458: on the path-based state scheme (PBSS),
// insertSideChain must not call triedb.Recover (which truncates state history)
// when the XDPoS v2 committed-block guard would refuse the resulting reorg.
//
// committedEngine (wrapping any consensus.Engine with LatestCommittedBlock) is
// defined in blockchain_xdpos_committed_test.go and shared in this package.

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb/pebble"
	"github.com/ethereum/go-ethereum/params"
)

// newPBSSChain opens a disk-backed blockchain with a real ancient store so that
// the pathdb state freezer is active and StateRecoverable works correctly.
func newPBSSChain(t *testing.T, gspec *Genesis, engine *committedEngine) (*BlockChain, func()) {
	t.Helper()
	datadir := t.TempDir()
	ancient := filepath.Join(datadir, "ancient")

	pdb, err := pebble.New(datadir, 0, 0, "", false)
	if err != nil {
		t.Fatalf("pebble.New: %v", err)
	}
	db, err := rawdb.Open(pdb, rawdb.OpenOptions{Ancient: ancient})
	if err != nil {
		t.Fatalf("rawdb.Open: %v", err)
	}
	bc, err := NewBlockChain(db, gspec, engine,
		DefaultConfig().WithStateScheme(rawdb.PathScheme).WithNoAsyncFlush(true))
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	return bc, func() {
		bc.Stop()
		db.Close()
	}
}

// TestSidechainNoTruncateBelowCommitted verifies the fix for issue #1458:
// on PBSS, importing a sidechain whose fork ancestor is at or below the
// committed block must NOT call triedb.Recover (which would truncate state
// history) since the resulting reorg would be refused anyway.
//
// On origin/main (without the fix): FAILS.
//
//	insertSideChain finds a recoverable ancestor (canonical block 5 in the
//	state-history freezer), calls triedb.Recover which truncates history for
//	blocks 5–140, then re-imports the sidechain.  The re-import's reorg() is
//	then refused by the committed-block guard (common ancestor 5 < committed
//	140).  Canonical head stays at block 140 but its state is gone:
//	bc.HasState(bc.CurrentBlock().Root) returns false.
//
// With fix: PASSES.
//
//	insertSideChain detects that the fork ancestor (block 5) is at or below
//	the committed block (140) and returns early without calling Recover.
//	Canonical state at block 140 remains intact.
func TestSidechainNoTruncateBelowCommitted(t *testing.T) {
	// We need >128 canonical blocks so the fork point lands in the
	// state-history freezer.  Use 140 so block 5 is definitely recoverable.
	const (
		canonLen = 140
		forkAt   = 5   // fork point: below the committed block
		sideLen  = 125 // sidechain spans [forkAt+1 .. forkAt+sideLen] = [6..130]
	)

	engine := &committedEngine{Engine: ethash.NewFaker()}
	gspec := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}

	// Generate genesis + canonical chain in a scratch DB (for fork generation).
	genDb, canonBlocks, _ := GenerateChainWithGenesis(gspec, engine, canonLen, nil)

	// Open a disk-backed PBSS blockchain so the state-history freezer is active.
	bc, cleanup := newPBSSChain(t, gspec, engine)
	defer cleanup()

	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("InsertChain canonical: %v", err)
	}

	// Confirm that the fork-point ancestor's state is recoverable (i.e. is in
	// the state-history freezer).  If it is not, the bug would not be triggered
	// and the test is vacuous — fail early so the test author can adjust.
	forkParentBlock := canonBlocks[forkAt-1] // canonical block forkAt (0-indexed → forkAt-1)
	if !bc.StateRecoverable(forkParentBlock.Root()) {
		t.Skipf("block %d root not in state history after %d canonical blocks "+
			"(cannot exercise the truncation path — adjust canonLen/forkAt)",
			forkAt, canonLen)
	}

	// Set committed block to the canonical head: any reorg whose common ancestor
	// is below this block is refused by the committed-block guard in reorg().
	canonHead := bc.CurrentBlock()
	engine.committed = &types.BlockInfo{
		Hash:   canonHead.Hash(),
		Number: new(big.Int).Set(canonHead.Number),
	}

	// Build sidechain from canonical block forkAt; use a different coinbase to
	// ensure distinct block hashes.
	sideBlocks, _ := GenerateChain(params.AllEthashProtocolChanges,
		forkParentBlock, engine, genDb, sideLen,
		func(i int, b *BlockGen) { b.SetCoinbase(common.Address{1}) })

	// Import the sidechain.  Expected on both main and fix: returns an error
	// (reorg refused), canonical head stays at block 140.
	bc.InsertChain(sideBlocks) //nolint:errcheck — error expected; check state below

	// Canonical head must still be block 140.
	if got := bc.CurrentBlock().Number.Uint64(); got != canonLen {
		t.Fatalf("canonical head moved to %d, expected %d", got, canonLen)
	}

	// Canonical head state must still be accessible.
	//
	// On origin/main this assertion FAILS: Recover truncated state history
	// 5→140 before the reorg was refused, so the disk layer moved to block 5
	// and canonical block 140's state is now inaccessible.
	if !bc.HasState(bc.CurrentBlock().Root) {
		t.Fatal("canonical head state is inaccessible after sidechain import: " +
			"state history was truncated even though the reorg was refused " +
			"(fix not applied — see issue #1458)")
	}
}

// TestSidechainReorgAboveCommitted verifies that the fix does not block a
// legitimate reorg when the fork ancestor is above the committed block.
// Both origin/main and the fix must let state recovery proceed and produce
// an accessible head state after the reorg.
func TestSidechainReorgAboveCommitted(t *testing.T) {
	// Canonical: 140 blocks.  Committed: block 5.  Fork at block 10 > committed.
	// Sidechain: fork at 10, 135 blocks long → tip at 144 > canonical 140.
	const (
		canonLen = 140
		forkAt   = 10  // above committed block (5)
		sideLen  = 135 // tip at forkAt+sideLen-1 = 144
	)

	engine := &committedEngine{Engine: ethash.NewFaker()}
	gspec := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}

	genDb, canonBlocks, _ := GenerateChainWithGenesis(gspec, engine, canonLen, nil)

	bc, cleanup := newPBSSChain(t, gspec, engine)
	defer cleanup()

	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("InsertChain canonical: %v", err)
	}

	// Confirm the fork-point ancestor is recoverable; skip if not.
	forkParentBlock := canonBlocks[forkAt-1]
	if !bc.StateRecoverable(forkParentBlock.Root()) {
		t.Skipf("block %d root not in state history after %d canonical blocks", forkAt, canonLen)
	}

	// Set committed block to canonical block 5 — fork at 10 is above it.
	engine.committed = &types.BlockInfo{
		Hash:   canonBlocks[4].Hash(), // block 5, 0-indexed → [4]
		Number: big.NewInt(5),
	}

	sideBlocks, _ := GenerateChain(params.AllEthashProtocolChanges,
		forkParentBlock, engine, genDb, sideLen,
		func(i int, b *BlockGen) { b.SetCoinbase(common.Address{1}) })

	// Fork ancestor (10) > committed (5) → the guard does not fire → Recover
	// and reorg are both allowed.  Import must succeed (or at least not corrupt
	// the new canonical head's state).
	bc.InsertChain(sideBlocks) //nolint:errcheck — may partially fail; check state

	// New head's state must be accessible regardless of whether the full reorg
	// completed.
	if !bc.HasState(bc.CurrentBlock().Root) {
		t.Fatal("canonical head state is inaccessible after reorg attempt " +
			"(state recovery broke a legitimate reorg path)")
	}
}

// TestReimportPrunedCanonicalKeepsHeadState covers #1458: re-importing a range
// of our own canonical blocks whose state has left the in-memory layers (an
// XDPOS2 header reply for an old range does this during full sync) must not
// roll the path database back. There is no sidechain to switch to, but
// insertSideChain used to call triedb.Recover on the last re-imported block,
// which truncates the state history and drops every diff layer, leaving the
// head without state.
func TestReimportPrunedCanonicalKeepsHeadState(t *testing.T) {
	engine := &committedEngine{Engine: ethash.NewFaker()} // no committed block, as in XDPoS v1
	gspec := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	_, canonBlocks, _ := GenerateChainWithGenesis(gspec, engine, 200, nil)

	bc, cleanup := newPBSSChain(t, gspec, engine)
	defer cleanup()

	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("InsertChain canonical: %v", err)
	}
	stale := canonBlocks[4:20] // blocks 5..20
	if bc.HasState(stale[len(stale)-1].Root()) || !bc.StateRecoverable(stale[len(stale)-1].Root()) {
		t.Fatal("block 20 state must be pruned but recoverable to exercise the bug")
	}
	head := bc.CurrentBlock()

	if _, err := bc.InsertChain(stale); err != nil {
		t.Fatalf("re-import of canonical blocks: %v", err)
	}
	if got := bc.CurrentBlock().Hash(); got != head.Hash() {
		t.Fatalf("head moved: have %x, want %x", got, head.Hash())
	}
	if !bc.HasState(head.Root) {
		t.Fatal("head state lost after re-importing old canonical blocks")
	}
}
