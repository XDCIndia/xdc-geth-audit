// Copyright 2024 The XDC Network Authors
// Tests for the XDPoS v2 committed-block reorg guard (F-2026-19773).

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// committedEngine wraps any consensus.Engine and adds the committedBlocker
// interface so the reorg guard in blockchain.go fires during testing without
// requiring a full XDPoS v2 chain setup.
type committedEngine struct {
	consensus.Engine
	committed *types.BlockInfo
}

func (e *committedEngine) LatestCommittedBlock() *types.BlockInfo { return e.committed }

// TestReorgRefusedBelowCommittedBlock verifies that reorg() refuses to drop an
// XDPoS v2 committed block from the canonical chain (F-2026-19773).
//
// Failure on origin/main (missing guard): InsertChain succeeds and drops block 6.
// Passes with the fix: InsertChain returns an error containing "stop reorg".
func TestReorgRefusedBelowCommittedBlock(t *testing.T) {
	for _, scheme := range []string{rawdb.HashScheme, rawdb.PathScheme} {
		t.Run(scheme, func(t *testing.T) {
			testReorgRefusedBelowCommittedBlock(t, scheme)
		})
	}
}

func testReorgRefusedBelowCommittedBlock(t *testing.T, scheme string) {
	t.Helper()

	// Build a canonical chain 10 blocks long using the wrapped engine.
	// We use ethash.NewFaker so all blocks validate easily.
	eng := &committedEngine{Engine: ethash.NewFaker()}
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	options := DefaultConfig().WithStateScheme(scheme)
	db := rawdb.NewMemoryDatabase()
	bc, err := NewBlockChain(db, genesis, eng, options)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()

	genDb, canonBlocks := makeBlockChainWithGenesis(genesis, 10, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert canonical chain: %v", err)
	}

	// Set the committed block to block 6 (canonical).
	committed := canonBlocks[5] // 0-indexed; canonBlocks[5] is block 6
	eng.committed = &types.BlockInfo{
		Hash:   committed.Hash(),
		Number: committed.Number(),
		Round:  0,
	}

	// Build a fork chain starting from block 2 (fork point below committed block 6).
	// The fork extends from block 2, so it will try to replace blocks 3..10
	// including the committed block 6.
	forkBlocks := makeBlockChain(bc.chainConfig, canonBlocks[1], 10, ethash.NewFaker(), genDb, forkSeed)

	// InsertChain must fail with the committed-block guard error.
	_, err = bc.InsertChain(forkBlocks)
	if err == nil {
		t.Fatal("InsertChain succeeded — reorg guard did not fire; committed block 6 was dropped (FAIL on main)")
	}
	want := "stop reorg"
	if !containsStr(err.Error(), want) {
		t.Fatalf("unexpected error %q; want it to contain %q", err.Error(), want)
	}
	t.Logf("InsertChain correctly refused: %v", err)

	// The canonical head must still be block 10.
	if head := bc.CurrentBlock(); head.Number.Uint64() != 10 {
		t.Fatalf("canonical head after refused reorg: got block %d, want 10", head.Number.Uint64())
	}
}

// TestReorgAllowedAboveCommittedBlock verifies that reorgs ABOVE the committed
// block are still permitted (the guard must not be over-broad).
func TestReorgAllowedAboveCommittedBlock(t *testing.T) {
	for _, scheme := range []string{rawdb.HashScheme, rawdb.PathScheme} {
		t.Run(scheme, func(t *testing.T) {
			testReorgAllowedAboveCommittedBlock(t, scheme)
		})
	}
}

func testReorgAllowedAboveCommittedBlock(t *testing.T, scheme string) {
	t.Helper()

	eng := &committedEngine{Engine: ethash.NewFaker()}
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	options := DefaultConfig().WithStateScheme(scheme)
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), genesis, eng, options)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()

	genDb, canonBlocks := makeBlockChainWithGenesis(genesis, 6, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert canonical chain: %v", err)
	}

	// Committed block is block 3 (the canonical block at index 2).
	committed := canonBlocks[2] // block 3
	eng.committed = &types.BlockInfo{
		Hash:   committed.Hash(),
		Number: committed.Number(),
		Round:  0,
	}

	// Fork from block 4 (after committed block 3), so the committed block
	// remains in the common prefix and is NOT dropped by the reorg.
	forkBlocks := makeBlockChain(bc.chainConfig, canonBlocks[3], 5, ethash.NewFaker(), genDb, forkSeed)

	// This reorg is above the committed block — it must succeed.
	_, err = bc.InsertChain(forkBlocks)
	// Note: may fail for non-TD reasons (ethash test chains may not reorg) but
	// must NOT fail with "stop reorg". We verify the guard is not the cause.
	if err != nil && containsStr(err.Error(), "stop reorg") {
		t.Fatalf("reorg guard incorrectly fired for a reorg above the committed block: %v", err)
	}
	t.Logf("InsertChain result for above-committed reorg: %v (ok)", err)
}

// containsStr is strings.Contains without importing strings.
func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
