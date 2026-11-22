// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-07 (#2060): startup compares the XDC fork blocks of the stored chain
// config with the new one. A moved fork that the chain already passed refuses to
// start; a field the stored config does not have is not a mismatch (no rewind,
// no error).

package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
)

func TestSetupGenesisXDCForkFields(t *testing.T) {
	mk := func(tip, denylist *big.Int) *Genesis {
		return &Genesis{
			Config: &params.ChainConfig{
				HomesteadBlock:  big.NewInt(0),
				Ethash:          &params.EthashConfig{},
				TIPSigningBlock: tip,
				DenylistBlock:   denylist,
			},
			Alloc: types.GenesisAlloc{{1}: {Balance: big.NewInt(1)}},
		}
	}
	// run commits `old`, advances the chain to block 4 and sets up with `next`.
	run := func(old, next *Genesis) (*params.ChainConfig, *params.ConfigCompatError, error) {
		db := rawdb.NewMemoryDatabase()
		tdb := triedb.NewDatabase(db, newDbConfig(rawdb.HashScheme))
		old.Commit(db, tdb, nil)
		bc, _ := NewBlockChain(db, old, ethash.NewFullFaker(), DefaultConfig().WithStateScheme(rawdb.HashScheme))
		defer bc.Stop()
		_, blocks, _ := GenerateChainWithGenesis(old, ethash.NewFaker(), 4, nil)
		if _, err := bc.InsertChain(blocks); err != nil {
			t.Fatal(err)
		}
		cfg, _, compatErr, err := SetupGenesisBlock(db, tdb, next)
		return cfg, compatErr, err
	}

	// A fork the chain has passed (block 2, head 4) moves to block 3: refuse to start.
	_, compatErr, err := run(mk(big.NewInt(2), nil), mk(big.NewInt(3), nil))
	if err == nil || !strings.Contains(err.Error(), "TIPSigningBlock changed from 2 to 3") {
		t.Fatalf("moved passed fork: err = %v, want a TIPSigningBlock mismatch", err)
	}
	if compatErr != nil {
		t.Fatalf("moved passed fork: unexpected rewind %v", compatErr)
	}

	// The stored config has no DenylistBlock (older binary); the new one sets it
	// at a block already passed. Not a mismatch: no error and no rewind.
	cfg, compatErr, err := run(mk(big.NewInt(2), nil), mk(big.NewInt(2), big.NewInt(1)))
	if err != nil || compatErr != nil {
		t.Fatalf("field missing in the stored config: err = %v, compatErr = %v, want none", err, compatErr)
	}
	if cfg.DenylistBlock == nil || cfg.DenylistBlock.Int64() != 1 {
		t.Fatalf("new field not adopted: %v", cfg.DenylistBlock)
	}

	// A fork block that is still ahead of the head may move.
	if _, compatErr, err = run(mk(big.NewInt(9), nil), mk(big.NewInt(12), nil)); err != nil || compatErr != nil {
		t.Fatalf("future fork moved: err = %v, compatErr = %v, want none", err, compatErr)
	}
}
