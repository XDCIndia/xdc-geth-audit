// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-11 (#2064): a 0x89 signing tx whose fee or value fields are wider
// than 256 bits must not be rejected by Process. Canonical XDPoSChain has no
// width check on its message conversion and the signing route never reads
// those fields, so canonical accepts the block.

package core

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// hunt11Process runs one tx as the only transaction of block 1 through
// Process and returns the error.
func hunt11Process(t *testing.T, to common.Address, gasPrice, value *big.Int) error {
	t.Helper()
	cfg := *params.AllEthashProtocolChanges
	cfg.TIPSigningBlock = big.NewInt(0)
	key, _ := crypto.GenerateKey()
	from := crypto.PubkeyToAddress(key.PublicKey)
	gspec := &Genesis{
		Config: &cfg,
		Alloc:  types.GenesisAlloc{from: {Balance: big.NewInt(1e18)}},
	}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	tx, err := types.SignTx(types.NewTx(&types.LegacyTx{
		To: &to, Gas: 100_000, GasPrice: gasPrice, Value: value, Data: make([]byte, 36),
	}), types.LatestSigner(&cfg), key)
	if err != nil {
		t.Fatal(err)
	}
	parent := bc.CurrentBlock()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   parent.GasLimit,
		Time:       parent.Time + 1,
		BaseFee:    parent.BaseFee,
	}
	block := types.NewBlockWithHeader(header).WithBody(types.Body{Transactions: types.Transactions{tx}})
	statedb, err := bc.State()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewStateProcessor(bc).Process(context.Background(), block, statedb, nil, nil, vm.Config{}, nil)
	return err
}

func TestHunt11SignRouteWideFeeFields(t *testing.T) {
	wide := new(big.Int).Lsh(big.NewInt(1), 256) // 257 bits
	if err := hunt11Process(t, common.BlockSignersBinary, wide, wide); err != nil {
		t.Fatalf("0x89 tx with 257-bit gas price and value rejected: %v", err)
	}
}

// The EVM route keeps rejecting the same tx (canonical rejects it too, for
// lack of balance).
func TestHunt11EVMRouteWideFeeStillRejected(t *testing.T) {
	wide := new(big.Int).Lsh(big.NewInt(1), 256)
	if err := hunt11Process(t, common.Address{0x42}, wide, big.NewInt(0)); err == nil {
		t.Fatal("EVM-route tx with 257-bit gas price accepted")
	}
}
