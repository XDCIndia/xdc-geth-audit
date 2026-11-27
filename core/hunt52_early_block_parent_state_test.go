// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-52 (#2109): ValidateBody tolerates a missing parent state below
// block 10,000 only where ProcessBlock may mask missing state (bulk sync, not an
// archive node, not a producer). Everywhere else the block is refused with
// ErrPrunedAncestor.

package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

func TestHunt52EarlyBlockMissingParentState(t *testing.T) {
	cfg := *params.AllEthashProtocolChanges
	cfg.XDPoS = &params.XDPoSConfig{Epoch: 900}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), &Genesis{Config: &cfg}, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	// Block 1 is stored without its state, block 2 is the block to validate.
	genesis := bc.Genesis()
	mk := func(parent *types.Block) *types.Block {
		h := &types.Header{
			ParentHash: parent.Hash(),
			Number:     new(big.Int).Add(parent.Number(), big.NewInt(1)),
			Difficulty: big.NewInt(1),
			GasLimit:   parent.GasLimit(),
			Time:       parent.Time() + 2,
			BaseFee:    parent.BaseFee(),
		}
		return types.NewBlock(h, &types.Body{}, nil, trie.NewStackTrie(nil))
	}
	b1 := mk(genesis)
	rawdb.WriteBlock(bc.db, b1)
	b2 := mk(b1)
	if bc.HasBlockAndState(b1.Hash(), 1) || !bc.HasBlock(b1.Hash(), 1) {
		t.Fatal("test setup: block 1 must exist without state")
	}

	saved := XdcBulkSyncMode.Load()
	defer XdcBulkSyncMode.Store(saved)

	XdcBulkSyncMode.Store(false)
	if err := bc.validator.ValidateBody(b2); !errors.Is(err, consensus.ErrPrunedAncestor) {
		t.Fatalf("outside bulk sync: got %v, want consensus.ErrPrunedAncestor", err)
	}
	XdcBulkSyncMode.Store(true)
	if err := bc.validator.ValidateBody(b2); errors.Is(err, consensus.ErrPrunedAncestor) {
		t.Fatalf("in bulk sync the early block must be allowed, got %v", err)
	}
}
