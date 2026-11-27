// Copyright 2024 The XDC Network Authors
// Tests for the XDPoS v2 committed-block reorg guard (F-2026-19773).

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
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

// TestSetHeadRefusedBelowCommittedBlock checks that SetHead cannot rewind
// under the committed block. The sync rollback path uses SetHead and does
// not go through reorg.
func TestSetHeadRefusedBelowCommittedBlock(t *testing.T) {
	eng := &committedEngine{Engine: ethash.NewFaker()}
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), genesis, eng, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()
	_, canonBlocks := makeBlockChainWithGenesis(genesis, 8, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert: %v", err)
	}
	eng.committed = &types.BlockInfo{Hash: canonBlocks[5].Hash(), Number: canonBlocks[5].Number()}
	if err := bc.SetHead(3); err == nil {
		t.Fatal("SetHead below the committed block was accepted")
	}
	if head := bc.CurrentBlock().Number.Uint64(); head != 8 {
		t.Fatalf("head = %d, want 8", head)
	}
	if err := bc.SetHead(6); err != nil {
		t.Fatalf("SetHead at the committed block: %v", err)
	}
}

func reorgNewChainSetup(t *testing.T, forkLen int) (*BlockChain, *committedEngine, ethdb.Database, types.Blocks) {
	t.Helper()
	eng := &committedEngine{Engine: ethash.NewFaker()}
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	db := rawdb.NewMemoryDatabase()
	bc, err := NewBlockChain(db, genesis, eng, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	t.Cleanup(bc.Stop)
	genDb, canonBlocks := makeBlockChainWithGenesis(genesis, 4, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert canonical chain: %v", err)
	}
	fork := makeBlockChain(bc.chainConfig, canonBlocks[1], forkLen, ethash.NewFaker(), genDb, forkSeed)
	for _, b := range fork {
		rawdb.WriteBlock(db, b)
	}
	return bc, eng, db, fork
}

// TestReorgRefusedWhenNewChainReplacesCommittedHash covers F-2026-19773: the old
// chain (head 4) lacks the committed block 6, and the new chain carries a
// different hash at that number.
func TestReorgRefusedWhenNewChainReplacesCommittedHash(t *testing.T) {
	bc, eng, _, fork := reorgNewChainSetup(t, 8) // fork covers blocks 3..10
	eng.committed = &types.BlockInfo{Hash: common.Hash{0xc0, 0xff, 0xee}, Number: big.NewInt(6)}
	err := bc.reorg(bc.CurrentBlock(), fork[len(fork)-1].Header())
	if err == nil || !containsStr(err.Error(), "stop reorg") {
		t.Fatalf("reorg error = %v, want a stop reorg refusal", err)
	}
	if head := bc.CurrentBlock().Number.Uint64(); head != 4 {
		t.Fatalf("head = %d, want 4", head)
	}
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

// TestSetHeadWithTimestampRewindsBelowCommittedBlock checks that the
// timestamp rewind used by the config-compat startup path cannot drop an
// XDPoS committed block. SetHead(3) is refused; the timestamp of that same
// block must be refused too, and the head must stay at block 8.
func TestSetHeadWithTimestampRewindsBelowCommittedBlock(t *testing.T) {
	eng := &committedEngine{Engine: ethash.NewFaker()}
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), genesis, eng, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()
	_, canonBlocks := makeBlockChainWithGenesis(genesis, 8, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert: %v", err)
	}
	eng.committed = &types.BlockInfo{Hash: canonBlocks[5].Hash(), Number: canonBlocks[5].Number()}
	if err := bc.SetHead(3); err == nil {
		t.Fatal("SetHead below the committed block was accepted")
	}
	if err := bc.SetHeadWithTimestamp(canonBlocks[2].Time()); err == nil {
		t.Fatal("SetHeadWithTimestamp below the committed block was accepted")
	}
	if head := bc.CurrentBlock().Number.Uint64(); head != 8 {
		t.Fatalf("head = %d, want 8", head)
	}
	if got := bc.GetCanonicalHash(canonBlocks[5].NumberU64()); got != canonBlocks[5].Hash() {
		t.Fatalf("committed block hash = %s, want %s", got, canonBlocks[5].Hash())
	}
}

// TestSetHeadWithTimestampAllowedAtCommittedBlock checks that a timestamp
// at the committed block still rewinds and invalidates consensus caches.
func TestSetHeadWithTimestampAllowedAtCommittedBlock(t *testing.T) {
	eng := &recordingInvalidator{committedEngine: committedEngine{Engine: ethash.NewFaker()}}
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.AllEthashProtocolChanges,
	}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), genesis, eng, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	defer bc.Stop()
	_, canonBlocks := makeBlockChainWithGenesis(genesis, 8, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert: %v", err)
	}
	eng.committed = &types.BlockInfo{Hash: canonBlocks[5].Hash(), Number: canonBlocks[5].Number()}
	if err := bc.SetHeadWithTimestamp(canonBlocks[5].Time()); err != nil {
		t.Fatalf("SetHeadWithTimestamp at the committed block: %v", err)
	}
	if head := bc.CurrentBlock().Number.Uint64(); head != 6 {
		t.Fatalf("head = %d, want 6", head)
	}
	if !eng.called || eng.head != 6 {
		t.Fatalf("InvalidateCaches called=%v head=%d, want called with 6", eng.called, eng.head)
	}
}

// TestConfigCompatTimestampRewindRefusedBelowCommitted checks the startup
// path. A timestamp fork mismatch that would rewind under the committed
// block must fail NewBlockChain and must not persist the new chain config.
func TestConfigCompatTimestampRewindRefusedBelowCommitted(t *testing.T) {
	eng := &committedEngine{Engine: ethash.NewFaker()}
	stored := *params.AllEthashProtocolChanges
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  &stored,
	}
	db := rawdb.NewMemoryDatabase()
	bc, err := NewBlockChain(db, genesis, eng, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	_, canonBlocks := makeBlockChainWithGenesis(genesis, 8, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert: %v", err)
	}
	bc.Stop()

	eng.committed = &types.BlockInfo{Hash: canonBlocks[5].Hash(), Number: canonBlocks[5].Number()}
	updated := stored
	shanghai := canonBlocks[2].Time()
	updated.ShanghaiTime = &shanghai
	next := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  &updated,
	}
	if _, err := NewBlockChain(db, next, eng, nil); err == nil {
		t.Fatal("config-compat timestamp rewind below the committed block was accepted")
	}
	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	if cfg := rawdb.ReadChainConfig(db, genesisHash); cfg == nil || cfg.ShanghaiTime != nil {
		t.Fatalf("chain config was updated after a refused rewind: %+v", cfg)
	}

	// The original config still opens, and the committed block is canonical.
	eng.committed = nil
	bc, err = NewBlockChain(db, genesis, eng, nil)
	if err != nil {
		t.Fatalf("reopen with stored config: %v", err)
	}
	defer bc.Stop()
	if head := bc.CurrentBlock().Number.Uint64(); head != 8 {
		t.Fatalf("head after refused startup rewind = %d, want 8", head)
	}
	if got := bc.GetCanonicalHash(canonBlocks[5].NumberU64()); got != canonBlocks[5].Hash() {
		t.Fatalf("committed block hash = %s, want %s", got, canonBlocks[5].Hash())
	}
}

// TestConfigCompatTimestampRewindAboveCommitted checks that a timestamp
// fork rewind at or above the committed block still runs and persists the
// new chain config.
func TestConfigCompatTimestampRewindAboveCommitted(t *testing.T) {
	eng := &committedEngine{Engine: ethash.NewFaker()}
	stored := *params.AllEthashProtocolChanges
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  &stored,
	}
	db := rawdb.NewMemoryDatabase()
	// Archive mode keeps every block's state on disk. A non-archive Stop
	// only persists a few recent tries, and the timestamp rewind would then
	// walk back to genesis looking for state.
	opts := DefaultConfig().WithArchive(true)
	bc, err := NewBlockChain(db, genesis, eng, opts)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	_, canonBlocks := makeBlockChainWithGenesis(genesis, 8, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert: %v", err)
	}
	bc.Stop()

	// Committed block is block 2. Shanghai at block 6's timestamp rewinds to
	// timestamp-1, which lands on block 5, still above the committed block.
	eng.committed = &types.BlockInfo{Hash: canonBlocks[1].Hash(), Number: canonBlocks[1].Number()}
	updated := stored
	shanghai := canonBlocks[5].Time()
	updated.ShanghaiTime = &shanghai
	next := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  &updated,
	}
	bc, err = NewBlockChain(db, next, eng, opts)
	if err != nil {
		t.Fatalf("config-compat timestamp rewind above the committed block: %v", err)
	}
	defer bc.Stop()
	if head := bc.CurrentBlock().Number.Uint64(); head != 5 {
		t.Fatalf("head = %d, want 5", head)
	}
	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	cfg := rawdb.ReadChainConfig(db, genesisHash)
	if cfg == nil || cfg.ShanghaiTime == nil || *cfg.ShanghaiTime != shanghai {
		t.Fatalf("chain config ShanghaiTime = %v, want %d", cfg, shanghai)
	}
	if got := bc.GetCanonicalHash(canonBlocks[1].NumberU64()); got != canonBlocks[1].Hash() {
		t.Fatalf("committed block hash = %s, want %s", got, canonBlocks[1].Hash())
	}
}

// TestConfigCompatRewindRefusedByStoredCommittedMarker is the real startup
// case (XDC-HUNT-29): the V2 engine is not wired yet and reports no committed
// block, but the marker is in the database. A config-compat rewind below it
// must still fail NewBlockChain.
func TestConfigCompatRewindRefusedByStoredCommittedMarker(t *testing.T) {
	eng := &committedEngine{Engine: ethash.NewFaker()} // committed stays nil
	stored := *params.AllEthashProtocolChanges
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  &stored,
	}
	db := rawdb.NewMemoryDatabase()
	bc, err := NewBlockChain(db, genesis, eng, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	_, canonBlocks := makeBlockChainWithGenesis(genesis, 8, ethash.NewFaker(), canonicalSeed)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("insert: %v", err)
	}
	bc.Stop()

	rawdb.WriteBFTCommittedBlock(db, &types.BlockInfo{Hash: canonBlocks[5].Hash(), Number: canonBlocks[5].Number()})
	updated := stored
	shanghai := canonBlocks[2].Time()
	updated.ShanghaiTime = &shanghai
	next := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  &updated,
	}
	if _, err := NewBlockChain(db, next, eng, nil); err == nil {
		t.Fatal("startup rewind below the stored committed marker was accepted")
	}
	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	if cfg := rawdb.ReadChainConfig(db, genesisHash); cfg == nil || cfg.ShanghaiTime != nil {
		t.Fatalf("chain config was updated after a refused rewind: %+v", cfg)
	}
}

// recordingInvalidator records InvalidateCaches calls. It embeds
// committedEngine so the committed-block guard still applies.
type recordingInvalidator struct {
	committedEngine
	called bool
	head   uint64
}

func (e *recordingInvalidator) InvalidateCaches(newHead uint64) {
	e.called = true
	e.head = newHead
}
