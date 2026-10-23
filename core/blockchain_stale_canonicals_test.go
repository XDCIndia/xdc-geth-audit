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

// Tests for sweepStaleCanonicals: the startup consistency sweep that removes
// canonical-hash and tx-lookup entries above the header head (CertiK XDC-21,
// XDC-27, XDC-28).

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// TestSweepStaleCanonicals_crashedSetHead simulates a crash that left canonical
// hashes (and tx-lookup entries) above the head header. After NewBlockChain the
// stale entries must be gone and everything at or below the head must be intact.
func TestSweepStaleCanonicals_crashedSetHead(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	db := rawdb.NewMemoryDatabase()
	bc, _ := NewBlockChain(db, genesis, ethash.NewFaker(), DefaultConfig())
	defer bc.Stop()

	// Build and insert 5 blocks.
	genDb, blocks := makeBlockChainWithGenesis(genesis, 5, ethash.NewFaker(), canonicalSeed)
	_ = genDb
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}
	// Confirm head is at 5.
	if bc.CurrentHeader().Number.Uint64() != 5 {
		t.Fatalf("want head=5, got %d", bc.CurrentHeader().Number.Uint64())
	}
	bc.Stop()

	// Inject stale canonical-hash entries at 6 and 7, simulating what a crash
	// during SetHead or a reorg leaves behind.
	staleHash6 := common.HexToHash("0xdead000000000000000000000000000000000000000000000000000000000001")
	staleHash7 := common.HexToHash("0xdead000000000000000000000000000000000000000000000000000000000002")

	// At block 6 also store a body and tx-lookup entry so we can verify that
	// sweepStaleCanonicals removes the tx index when the body is present.
	staleTx := types.NewTx(&types.LegacyTx{Nonce: 99, Gas: 21000, Value: big.NewInt(0)})
	staleBody := &types.Body{Transactions: []*types.Transaction{staleTx}}
	rawdb.WriteBody(db, staleHash6, 6, staleBody)
	rawdb.WriteCanonicalHash(db, staleHash6, 6)
	rawdb.WriteTxLookupEntries(db, 6, []common.Hash{staleTx.Hash()})

	// At block 7 there is no body (simulating a header-only stale entry).
	rawdb.WriteCanonicalHash(db, staleHash7, 7)

	// Sanity: stale entries are present before restart.
	if h := rawdb.ReadCanonicalHash(db, 6); h != staleHash6 {
		t.Fatalf("pre-restart: want stale hash at 6, got %v", h)
	}

	// Restart BlockChain with the same DB — sweepStaleCanonicals runs inside loadLastState.
	bc2, err := NewBlockChain(db, genesis, ethash.NewFaker(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer bc2.Stop()

	// Stale canonical entries above head (6, 7) must be gone.
	if h := rawdb.ReadCanonicalHash(db, 6); h != (common.Hash{}) {
		t.Errorf("stale canonical at 6 not removed: %v", h)
	}
	if h := rawdb.ReadCanonicalHash(db, 7); h != (common.Hash{}) {
		t.Errorf("stale canonical at 7 not removed: %v", h)
	}
	// Stale tx-lookup for block 6 (which had a body) must be gone too.
	if rawdb.ReadTxLookupEntry(db, staleTx.Hash()) != nil {
		t.Errorf("stale tx-lookup for tx %v not removed", staleTx.Hash())
	}

	// Head and all canonical entries at or below it must be intact.
	if bc2.CurrentHeader().Number.Uint64() != 5 {
		t.Fatalf("head changed after sweep: want 5, got %d", bc2.CurrentHeader().Number.Uint64())
	}
	for i := uint64(1); i <= 5; i++ {
		if h := rawdb.ReadCanonicalHash(db, i); h == (common.Hash{}) {
			t.Errorf("canonical at %d wiped by sweep", i)
		}
	}
}

// TestSweepStaleCanonicals_consistentDB verifies that sweepStaleCanonicals is a
// no-op when the database is already consistent.
func TestSweepStaleCanonicals_consistentDB(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	db := rawdb.NewMemoryDatabase()
	bc, _ := NewBlockChain(db, genesis, ethash.NewFaker(), DefaultConfig())

	genDb, blocks := makeBlockChainWithGenesis(genesis, 4, ethash.NewFaker(), canonicalSeed)
	_ = genDb
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}
	bc.Stop()

	// Snapshot canonical hashes before restart.
	want := make([]common.Hash, 5)
	for i := uint64(0); i <= 4; i++ {
		want[i] = rawdb.ReadCanonicalHash(db, i)
	}

	// Restart: a consistent DB must not be changed.
	bc2, err := NewBlockChain(db, genesis, ethash.NewFaker(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer bc2.Stop()

	for i := uint64(0); i <= 4; i++ {
		if got := rawdb.ReadCanonicalHash(db, i); got != want[i] {
			t.Errorf("canonical at %d changed: want %v got %v", i, want[i], got)
		}
	}
}

// TestSweepStaleCanonicals_checkpointSync verifies that the sweep uses the
// header head, not the block head: a node that has downloaded headers via
// checkpoint sync must keep all canonical entries up to the header head.
func TestSweepStaleCanonicals_checkpointSync(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	db := rawdb.NewMemoryDatabase()
	bc, _ := NewBlockChain(db, genesis, ethash.NewFaker(), DefaultConfig())

	// Insert 3 full blocks (block head = 3).
	genDb, fullBlocks := makeBlockChainWithGenesis(genesis, 3, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(fullBlocks); err != nil {
		t.Fatal(err)
	}

	// Insert 5 headers-only beyond block 3 (header head = 8, block head = 3).
	extraHeaders := makeHeaderChain(
		params.AllEthashProtocolChanges,
		fullBlocks[2].Header(),
		5, ethash.NewFaker(), genDb, canonicalSeed,
	)
	if _, err := bc.InsertHeaderChain(extraHeaders); err != nil {
		t.Fatal(err)
	}
	headerHead := bc.CurrentHeader().Number.Uint64() // expect 8
	blockHead := bc.CurrentBlock().Number.Uint64()   // expect 3
	if headerHead != 8 {
		t.Fatalf("want headerHead=8, got %d", headerHead)
	}
	if blockHead != 3 {
		t.Fatalf("want blockHead=3, got %d", blockHead)
	}
	bc.Stop()

	// Inject a stale entry one above the header head (9) — crash artefact.
	staleHash9 := common.HexToHash("0xdeadc0de")
	rawdb.WriteCanonicalHash(db, staleHash9, 9)

	// Restart: only the entry at 9 must be removed; 4..8 (header-only) must remain.
	bc2, err := NewBlockChain(db, genesis, ethash.NewFaker(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer bc2.Stop()

	// Stale entry above header head must be gone.
	if h := rawdb.ReadCanonicalHash(db, 9); h != (common.Hash{}) {
		t.Errorf("stale canonical at 9 not removed: %v", h)
	}
	// All header-only canonical entries (4..8) must still exist.
	for _, h := range extraHeaders {
		num := h.Number.Uint64()
		if got := rawdb.ReadCanonicalHash(db, num); got == (common.Hash{}) {
			t.Errorf("checkpoint-sync header canonical at %d wiped by sweep", num)
		}
	}
	// Block head and header head are unchanged.
	if bc2.CurrentBlock().Number.Uint64() != 3 {
		t.Errorf("block head changed: want 3, got %d", bc2.CurrentBlock().Number.Uint64())
	}
	if bc2.CurrentHeader().Number.Uint64() != 8 {
		t.Errorf("header head changed: want 8, got %d", bc2.CurrentHeader().Number.Uint64())
	}
}
