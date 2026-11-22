// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-14 (#2067): XDC has no type 3 (blob) transactions. Canonical
// XDPoSChain cannot decode one, so a block that holds one is invalid there.
// ValidateBody must reject it on every route, the 0x89 signing route
// included (which skips the blob checks of the state transition).

package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
)

func hunt14Block(to common.Address) *types.Block {
	tx := types.NewTx(&types.BlobTx{
		To:         to,
		Gas:        100_000,
		GasFeeCap:  uint256.NewInt(1),
		GasTipCap:  uint256.NewInt(1),
		BlobFeeCap: uint256.NewInt(1),
		BlobHashes: []common.Hash{{0x01, 0x01}},
	})
	zero := uint64(0)
	header := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   30_000_000,
		BaseFee:    big.NewInt(1),
		BlobGasUsed: func() *uint64 {
			u := uint64(params.BlobTxBlobGasPerBlob)
			return &u
		}(),
		ExcessBlobGas: &zero,
	}
	return types.NewBlock(header, &types.Body{Transactions: types.Transactions{tx}}, nil, trie.NewStackTrie(nil))
}

func TestHunt14BlobTxRejectedOnXDC(t *testing.T) {
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), &Genesis{Config: params.AllEthashProtocolChanges}, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	xdc := *params.AllEthashProtocolChanges
	xdc.XDPoS = &params.XDPoSConfig{Epoch: 900}
	for name, to := range map[string]common.Address{
		"signing route": common.BlockSignersBinary,
		"empty route":   common.XDCXAddrBinary,
		"evm route":     {0x42},
	} {
		err := NewBlockValidator(&xdc, bc).ValidateBody(hunt14Block(to))
		if !errors.Is(err, ErrTxTypeNotSupported) {
			t.Errorf("%s: want ErrTxTypeNotSupported, got %v", name, err)
		}
	}
	// Control: the same block on a non-XDC chain is not rejected for its type.
	err = NewBlockValidator(params.AllEthashProtocolChanges, bc).ValidateBody(hunt14Block(common.Address{0x42}))
	if errors.Is(err, ErrTxTypeNotSupported) {
		t.Errorf("non-XDC chain: unexpected %v", err)
	}
}
