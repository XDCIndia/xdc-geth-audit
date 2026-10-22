// Copyright 2026 The XDC Network Authors
// Tests for the side-chain-over-pruned-ancestor write cap (CertiK XDC-19).

package core

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/ethdb/pebble"
	"github.com/ethereum/go-ethereum/params"
)

// TestSidechainChildCap verifies XDC-19: insertSideChain refuses to write a
// sidechain block via writeBlockWithoutState once sidechainChildCap blocks have
// already been stored from the same pruned parent.
//
// Because the first successful insertSideChain triggers Phase 4 (ancestor
// re-execution) which regenerates the parent state, subsequent InsertChain
// calls skip Phase 1 entirely (parent now has state). The test therefore
// pre-loads the LRU counter to sidechainChildCap and confirms that the very
// next InsertChain from the same pruned parent is rejected.
//
// Failure on origin/main: InsertChain returns nil instead of the cap error.
// Passes with fix: InsertChain returns an error containing "sidechain child cap".
func TestSidechainChildCap(t *testing.T) {
	for _, scheme := range []string{rawdb.HashScheme, rawdb.PathScheme} {
		t.Run(scheme, func(t *testing.T) {
			testSidechainChildCap(t, scheme)
		})
	}
}

func testSidechainChildCap(t *testing.T, scheme string) {
	t.Helper()

	engine := ethash.NewFaker()
	genesis := &Genesis{
		Config:  params.TestChainConfig,
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	chainLen := 2 * state.TriesInMemory // 256 blocks; early block states are pruned

	// Generate canonical chain and keep genDb for forking.
	genDb, canonBlocks := makeBlockChainWithGenesis(genesis, chainLen, engine, canonicalSeed)

	// Use a real disk DB so the ancient store is present.
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

	bc, err := NewBlockChain(db, genesis, engine, DefaultConfig().WithStateScheme(scheme))
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()

	if n, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("block %d: failed to insert canonical chain: %v", n, err)
	}

	// Verify that block 5 is state-pruned so that forks from it enter insertSideChain.
	b5 := canonBlocks[4] // canonical block 5 (0-indexed), state pruned
	if bc.HasBlockAndState(b5.Hash(), b5.NumberU64()) {
		t.Skip("block 5 still has state; cannot test pruned-ancestor cap")
	}

	// Pre-fill the cap counter for b5 as if sidechainChildCap blocks have
	// already been stored from this parent. A real attack would fill the counter
	// by sending that many blocks before Phase 4 can restore the parent state;
	// in this synchronous test we short-circuit that by seeding the LRU directly.
	bc.sidechainKids.Add(b5.Hash(), sidechainChildCap)

	// One more fork from b5 must be refused.
	fork := makeBlockChain(bc.chainConfig, b5, 1, engine, genDb, 100)
	_, err = bc.InsertChain(fork)
	if err == nil {
		t.Fatal("InsertChain returned nil — sidechain child cap was not enforced (FAIL on main)")
	}
	if !containsStr(err.Error(), "sidechain child cap") {
		t.Fatalf("unexpected error %q; want it to contain 'sidechain child cap'", err.Error())
	}
	t.Logf("cap correctly enforced: %v", err)
}
