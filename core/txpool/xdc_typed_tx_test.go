// Copyright 2026 The go-ethereum Authors
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

// Tests for XDC txpool rejecting type-1/2 txs before Eip1559Block.
// Canonical XDPoSChain v2.9.3-testnet core/txpool/validation.go:81-83.

package txpool

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// TestXDCTxpoolRejectsTypedPreEIP1559 verifies that ValidateTransaction rejects
// type-1 and type-2 transactions at a block in [London, EIP1559) on XDC mainnet.
//
// On origin/main without the fix: type-2 txs pass the London gate (IsLondon=true)
// and are accepted — wrong.
// After the fix: both types are rejected with ErrTxTypeNotSupported.
func TestXDCTxpoolRejectsTypedPreEIP1559(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	cfg := params.XDCMainnetChainConfig
	// Block in [London=76,321,000, EIP1559=98,800,200).
	head := &types.Header{
		Number:     big.NewInt(80_000_000),
		GasLimit:   84_000_000,
		Time:       1,
		Difficulty: big.NewInt(1),
	}

	signer := types.NewEIP155Signer(cfg.ChainID)
	opts := &ValidationOptions{
		Config:  cfg,
		Accept:  0xFF,
		MaxSize: 32 * 1024,
		MinTip:  big.NewInt(0),
	}

	// type-2 (DynamicFeeTx)
	tx2 := types.MustSignNewTx(key, types.NewLondonSigner(cfg.ChainID), &types.DynamicFeeTx{
		ChainID:   cfg.ChainID,
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
	})
	err = ValidateTransaction(tx2, head, signer, opts)
	if !errors.Is(err, core.ErrTxTypeNotSupported) {
		t.Fatalf("type-2 tx at pre-EIP1559 block: got %v, want ErrTxTypeNotSupported (fix not applied?)", err)
	}

	// type-1 (AccessListTx)
	tx1 := types.MustSignNewTx(key, types.NewEIP2930Signer(cfg.ChainID), &types.AccessListTx{
		ChainID: cfg.ChainID,
		Nonce:   0,
		Gas:     21000,
	})
	err = ValidateTransaction(tx1, head, signer, opts)
	if !errors.Is(err, core.ErrTxTypeNotSupported) {
		t.Fatalf("type-1 tx at pre-EIP1559 block: got %v, want ErrTxTypeNotSupported (fix not applied?)", err)
	}
}

// TestXDCTxpoolAcceptsTypedPostEIP1559 verifies that type-2 txs are accepted
// at-and-after Eip1559Block on XDC mainnet (ensuring no regression).
func TestXDCTxpoolAcceptsTypedPostEIP1559(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	cfg := params.XDCMainnetChainConfig
	postEIP1559 := new(big.Int).Add(cfg.Eip1559Block, big.NewInt(1))
	baseFee := big.NewInt(12_500_000_000)
	head := &types.Header{
		Number:     postEIP1559,
		GasLimit:   84_000_000,
		BaseFee:    baseFee,
		Time:       1,
		Difficulty: big.NewInt(1),
	}

	londonSigner := types.NewLondonSigner(cfg.ChainID)
	opts := &ValidationOptions{
		Config:  cfg,
		Accept:  0xFF,
		MaxSize: 32 * 1024,
		MinTip:  big.NewInt(0),
	}

	tx2 := types.MustSignNewTx(key, londonSigner, &types.DynamicFeeTx{
		ChainID:   cfg.ChainID,
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: new(big.Int).Add(baseFee, big.NewInt(1)),
		Gas:       100_000,
	})
	if err := ValidateTransaction(tx2, head, londonSigner, opts); err != nil {
		t.Fatalf("type-2 tx at post-EIP1559 block should be accepted: %v", err)
	}
}
