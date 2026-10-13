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

package miner

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// TestFillTransactionsZeroPriceSignTx checks that a 0-price signing tx to
// 0x89 is committed even when the header carries a base fee, and that it does
// not take the rest of its account's txs out of the block (#1428).
func TestFillTransactionsZeroPriceSignTx(t *testing.T) {
	// Signing txs take the gasless path from TIPSigning on (mainnet reality).
	old := common.TIPSigning
	common.TIPSigning = big.NewInt(0)
	t.Cleanup(func() { common.TIPSigning = old })

	b := newTestWorkerBackend(t, params.TestChainConfig, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
	w := New(b, testConfig, ethash.NewFaker())

	signer := types.LatestSigner(params.TestChainConfig)
	genesis := b.chain.CurrentBlock()
	signTx, err := types.SignTx(contracts.CreateTxSign(genesis.Number, genesis.Hash(), 0, common.BlockSignersBinary), signer, testBankKey)
	if err != nil {
		t.Fatal(err)
	}
	normalTx := types.MustSignNewTx(testBankKey, signer, &types.LegacyTx{
		Nonce:    1,
		To:       &testUserAddress,
		Value:    big.NewInt(1000),
		Gas:      params.TxGas,
		GasPrice: big.NewInt(2 * params.InitialBaseFee),
	})
	for i, err := range b.txPool.Add([]*types.Transaction{signTx, normalTx}, true) {
		if err != nil {
			t.Fatalf("pool add tx %d: %v", i, err)
		}
	}
	res := w.generateWork(context.Background(), &generateParams{
		parentHash: genesis.Hash(),
		timestamp:  uint64(time.Now().Unix()),
		coinbase:   testUserAddress,
	}, false)
	if res.err != nil {
		t.Fatalf("generateWork: %v", res.err)
	}
	if res.block.BaseFee() == nil {
		t.Fatal("test precondition: header has no base fee")
	}
	txs := res.block.Transactions()
	if len(txs) != 2 || txs[0].Hash() != signTx.Hash() || txs[1].Hash() != normalTx.Hash() {
		got := make([]common.Hash, len(txs))
		for i, tx := range txs {
			got[i] = tx.Hash()
		}
		t.Fatalf("block txs = %v, want [sign %v, normal %v]", got, signTx.Hash(), normalTx.Hash())
	}
}
