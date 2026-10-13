package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
)

// xdcTestConfig is an ethash test config marked as an XDC chain (IsXDC).
func xdcTestConfig() *params.ChainConfig {
	cfg := *params.AllEthashProtocolChanges
	cfg.XDPoS = &params.XDPoSConfig{}
	cfg.Eip1559Block = big.NewInt(0) // XDC gates header.BaseFee on this, not London
	return &cfg
}

// newBulkSyncTestChain opens a follower chain on db in XDC bulk-sync mode, the
// only configuration in which canMaskMissingState is true.
func newBulkSyncTestChain(t *testing.T, db ethdb.Database, gspec *Genesis) *BlockChain {
	t.Helper()
	cfg := DefaultConfig()
	cfg.SnapshotLimit = 0 // read state through the trie, so a missing node surfaces
	chain, err := NewBlockChain(db, gspec, ethash.NewFaker(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	prev := XdcBulkSyncMode.Load()
	XdcBulkSyncMode.Store(true)
	t.Cleanup(func() { XdcBulkSyncMode.Store(prev); chain.Stop() })
	if !chain.canMaskMissingState() {
		t.Fatal("precondition: canMaskMissingState() = false")
	}
	return chain
}

// TestBulkSyncRejectsStateRootMismatch (#1426): in bulk sync, a block whose
// state root does not match execution on a COMPLETE parent state is a bad
// block, as in canonical XDPoSChain. It must not be canonized and must not
// latch checkpointSyncNoState.
func TestBulkSyncRejectsStateRootMismatch(t *testing.T) {
	gspec := &Genesis{Config: xdcTestConfig()}
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, func(int, *BlockGen) {})

	header := blocks[0].Header()
	header.Root = common.HexToHash("0xbad0000000000000000000000000000000000000000000000000000000000bad")
	forged := types.NewBlockWithHeader(header).WithBody(*blocks[0].Body())

	db := rawdb.NewMemoryDatabase()
	chain := newBulkSyncTestChain(t, db, gspec)

	if _, err := chain.InsertChain(types.Blocks{forged}); err == nil {
		t.Fatal("block with a wrong state root on complete state was imported in bulk sync")
	} else if !strings.Contains(err.Error(), "invalid merkle root") {
		t.Fatalf("rejected for the wrong reason: %v", err)
	}
	if chain.checkpointSyncNoState.Load() || rawdb.ReadCheckpointSyncNoState(db) {
		t.Fatal("checkpointSyncNoState latched on a state root mismatch")
	}
	if n := chain.CurrentBlock().Number.Uint64(); n != 0 {
		t.Fatalf("head = %d, want 0 (forged block canonized)", n)
	}
}

// TestBulkSyncMasksMissingTrieNode: the A.94 mask still applies when the
// parent trie is genuinely incomplete (root present, deeper nodes missing),
// the partial fast-sync pivot it exists for.
func TestBulkSyncMasksMissingTrieNode(t *testing.T) {
	var (
		a     = common.HexToAddress("0xaaaa")
		b     = common.HexToAddress("0xbbbb")
		gspec = &Genesis{
			Config: xdcTestConfig(),
			Alloc:  types.GenesisAlloc{a: {Balance: big.NewInt(1)}, b: {Balance: big.NewInt(1)}},
		}
	)
	// Block 1 pays its reward to b, so executing it reads b's account.
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, func(_ int, g *BlockGen) { g.SetCoinbase(b) })

	db := rawdb.NewMemoryDatabase()
	root := gspec.MustCommit(db, triedb.NewDatabase(db, triedb.HashDefaults)).Root()

	// Drop every account-trie node below the root: HasState(root) still holds,
	// so this is the partial-trie (A.94) case, not the missing-parent (A.93) one.
	tr, err := trie.New(trie.StateTrieID(root), triedb.NewDatabase(db, triedb.HashDefaults))
	if err != nil {
		t.Fatal(err)
	}
	var dropped int
	for it := tr.MustNodeIterator(nil); it.Next(true); {
		if h := it.Hash(); h != (common.Hash{}) && h != root {
			rawdb.DeleteLegacyTrieNode(db, h)
			dropped++
		}
	}
	if dropped == 0 {
		t.Fatal("precondition: no trie node below the root to drop")
	}

	chain := newBulkSyncTestChain(t, db, gspec)
	if _, err := chain.InsertChain(types.Blocks{blocks[0]}); err != nil {
		t.Fatalf("missing-trie-node mismatch not masked in bulk sync: %v", err)
	}
	if !chain.checkpointSyncNoState.Load() || !rawdb.ReadCheckpointSyncNoState(db) {
		t.Fatal("checkpointSyncNoState not set after masking a missing trie node")
	}
}
