package hooks

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// deferredListSetup builds a chain with `blocks` empty blocks and the V1 hooks
// on a mainnet-like config. A checkpoint whose parent is unknown is deferred.
func deferredListSetup(t *testing.T, blocks int) (*XDPoS.XDPoS, *core.BlockChain) {
	t.Helper()
	db := rawdb.NewMemoryDatabase()
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	if blocks > 0 {
		_, chain, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), blocks, nil)
		if _, err := bc.InsertChain(chain); err != nil {
			t.Fatal(err)
		}
	}
	chainConfig := *params.AllEthashProtocolChanges
	chainConfig.ChainID = big.NewInt(50)
	chainConfig.XDPoS = &params.XDPoSConfig{Epoch: 900, Gap: 450, Period: 2}
	adaptor := XDPoS.New(chainConfig.XDPoS, db)
	AttachConsensusV1Hooks(adaptor, bc, &chainConfig)
	return adaptor, bc
}

func deferredCheckpoint(i int) *types.Header {
	return &types.Header{
		Number:     big.NewInt(int64(900 * (i + 1))),
		ParentHash: common.Hash{0xff, byte(i)}, // never written: the check is deferred
	}
}

// hasDeferred reports whether a recheck of h finds an entry. A recheck that
// finds one runs the hook again, which counts a skip (the parent is still
// missing); one that finds none does nothing.
func hasDeferred(t *testing.T, adaptor *XDPoS.XDPoS, h *types.Header) bool {
	t.Helper()
	before := hookVerifyMNsMissingStateSkips.Snapshot().Count()
	if err := adaptor.RecheckV1Validators(h); err != nil {
		t.Fatal(err)
	}
	return hookVerifyMNsMissingStateSkips.Snapshot().Count() > before
}

// A full list of checkpoints that still wait for body validation must not lose
// one silently: the 33rd is an error, and all 32 entries stay (F-2026-19462).
func TestV1DeferredFullIsAnErrorWhenNoEntryIsStale(t *testing.T) {
	adaptor, _ := deferredListSetup(t, 0)
	const max = 32
	for i := 0; i < max; i++ {
		if err := adaptor.HookVerifyMNs(deferredCheckpoint(i), []common.Address{{0x01}}); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	if err := adaptor.HookVerifyMNs(deferredCheckpoint(max), []common.Address{{0x01}}); err == nil {
		t.Fatal("33rd pending checkpoint was accepted: its validator check is dropped")
	}
	for _, i := range []int{0, 1, max - 1} {
		if !hasDeferred(t, adaptor, deferredCheckpoint(i)) {
			t.Fatalf("pending checkpoint %d lost its entry", i)
		}
	}
}

// Entries at or below the head are stale (their block was passed without body
// validation). The oldest of them makes room for a new checkpoint, so header-only
// sync of V1 history keeps going. Entries above the head stay.
func TestV1DeferredFullEvictsOnlyEntriesAtOrBelowHead(t *testing.T) {
	adaptor, bc := deferredListSetup(t, 1800)
	if got := bc.CurrentHeader().Number.Uint64(); got != 1800 {
		t.Fatalf("head = %d, want 1800", got)
	}
	const max = 32
	for i := 0; i < max; i++ {
		if err := adaptor.HookVerifyMNs(deferredCheckpoint(i), []common.Address{{0x01}}); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	// Checkpoints 900 and 1800 are at or below the head: two new ones fit.
	for i := max; i < max+2; i++ {
		if err := adaptor.HookVerifyMNs(deferredCheckpoint(i), []common.Address{{0x01}}); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	if err := adaptor.HookVerifyMNs(deferredCheckpoint(max+2), []common.Address{{0x01}}); err == nil {
		t.Fatal("no stale entry is left, yet the checkpoint was accepted")
	}
	if hasDeferred(t, adaptor, deferredCheckpoint(0)) || hasDeferred(t, adaptor, deferredCheckpoint(1)) {
		t.Fatal("a stale entry was kept")
	}
	if !hasDeferred(t, adaptor, deferredCheckpoint(2)) || !hasDeferred(t, adaptor, deferredCheckpoint(max+1)) {
		t.Fatal("an entry above the head was dropped")
	}
}
