// Copyright 2026 XDC Network
// Tests for the blacklist check in StateProcessor.Process (#1483).

package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/beacon"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// TestProcessRejectsBlacklistedRecipient verifies that StateProcessor.Process
// returns an error when a block contains a transaction to a blacklisted address
// once BlackListHFNumber is active.
//
// Fails on origin/main: no blacklist check in Process; InsertChain returns a
// state-root mismatch error instead of a blacklist error.
// Passes with fix: Process returns "blacklisted" error before state-root check.
func TestProcessRejectsBlacklistedRecipient(t *testing.T) {
	// Override BlackListHFNumber to 0 so every block number triggers the check.
	oldBL := common.BlackListHFNumber
	common.BlackListHFNumber = 0
	t.Cleanup(func() { common.BlackListHFNumber = oldBL })

	var (
		config  = params.TestChainConfig
		signer  = types.LatestSigner(config)
		key1, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		from    = crypto.PubkeyToAddress(key1.PublicKey)
		// First address in common.blacklist (common/types.go).
		blacklistedTo = common.HexToAddress("0x5248bfb72fd4f234e062d3e9bb76f08643004fcd")
	)

	db := rawdb.NewMemoryDatabase()
	gspec := &Genesis{
		Config: config,
		Alloc: withSystemContracts(types.GenesisAlloc{
			from: {Balance: new(big.Int).Mul(big.NewInt(1e18), big.NewInt(10))},
		}),
	}
	blockchain, err := NewBlockChain(db, gspec, beacon.New(ethash.NewFaker()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blockchain.Stop()

	// Sign a tx from a clean (non-blacklisted) key to the blacklisted recipient.
	tx, err := types.SignTx(
		types.NewTransaction(0, blacklistedTo, big.NewInt(0), params.TxGas, big.NewInt(875000000), nil),
		signer, key1,
	)
	if err != nil {
		t.Fatal(err)
	}

	// GenerateBadBlock produces a block with a fabricated state root.
	// Process will be called by InsertChain and must return before that root
	// is verified (Process errors propagate before any state-root check).
	block := GenerateBadBlock(gspec.ToBlock(), beacon.New(ethash.NewFaker()), []*types.Transaction{tx}, config, false)

	_, insertErr := blockchain.InsertChain(types.Blocks{block})
	if insertErr == nil {
		t.Fatal("expected error for tx to blacklisted address, got nil")
	}
	if !strings.Contains(insertErr.Error(), "blacklisted") {
		t.Fatalf("expected blacklist error, got: %v", insertErr)
	}
}
