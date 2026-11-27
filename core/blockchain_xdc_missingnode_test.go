// Copyright 2026 XDC Network

package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// missingNodeValidator reports a missing trie node from ValidateState, the way
// block_validator wraps statedb.Error() inside "invalid merkle root".
type missingNodeValidator struct{ Validator }

func (v missingNodeValidator) ValidateState(*types.Block, *state.StateDB, *ProcessResult, bool) error {
	return fmt.Errorf("invalid merkle root: %w", &trie.MissingNodeError{NodeHash: common.Hash{0x01}})
}

// TestMissingTrieNodeIsNotABadBlock: a missing trie node below the root means the
// local state is incomplete. The import fails with the error type intact (the
// syncer uses it to avoid marking honest peers), the head does not move, and no
// bad-block record is written for a block that is in fact good (XDC-HUNT-55).
func TestMissingTrieNodeIsNotABadBlock(t *testing.T) {
	gspec := &Genesis{Config: params.TestChainConfig}
	db := rawdb.NewMemoryDatabase()
	chain, err := NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)
	chain.SetBlockValidatorAndProcessorForTesting(missingNodeValidator{chain.validator}, chain.processor)

	for i := 0; i < 3; i++ {
		_, err = chain.InsertChain(blocks)
		var missing *trie.MissingNodeError
		if !errors.As(err, &missing) {
			t.Fatalf("try %d: error %v does not keep the MissingNodeError type", i, err)
		}
	}
	if n := chain.CurrentBlock().Number.Uint64(); n != 0 {
		t.Fatalf("head moved to %d", n)
	}
	if bad := rawdb.ReadAllBadBlocks(db); len(bad) != 0 {
		t.Fatalf("%d bad-block records written for a missing trie node", len(bad))
	}
}
