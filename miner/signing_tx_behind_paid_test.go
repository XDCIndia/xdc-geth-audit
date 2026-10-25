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

// Regression test for Hacken finding F-2026-19727 (residual): a zero-price
// signing tx at nonce N+1 behind a paid tx at nonce N from the same account
// was silently omitted from every sealed block.
//
// Root cause: splitSpecialTxs extracted the signing tx into its own batch and
// committed it first (before nonce N), which failed with nonce-too-high and
// lost the tx entirely.  newTxWithMinerFee then also rejected it via
// ErrGasFeeCapTooLow when Shift reached it from the normal batch.
//
// Fix: splitSpecialTxs only splits the uninterrupted leading special-tx prefix
// per account; newTxWithMinerFee skips the fee-cap check for special txs and
// assigns tip=0.

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

// TestSignTxBehindPaidNonce checks that a zero-price signing tx at nonce N+1
// is included in the block even when the same account has a paid tx at nonce N.
// Before the fix, only the paid tx was included.
func TestSignTxBehindPaidNonce(t *testing.T) {
	// Enable the signing-tx fast path (ApplySigningTransaction) from block 0.
	old := common.TIPSigning
	common.TIPSigning = big.NewInt(0)
	t.Cleanup(func() { common.TIPSigning = old })

	b := newTestWorkerBackend(t, params.TestChainConfig, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
	w := New(b, testConfig, ethash.NewFaker())

	signer := types.LatestSigner(params.TestChainConfig)
	genesis := b.chain.CurrentBlock()

	// Nonce 0: normal paid tx (gas price > base fee so it clears the fee cap).
	paidTx := types.MustSignNewTx(testBankKey, signer, &types.LegacyTx{
		Nonce:    0,
		To:       &testUserAddress,
		Value:    big.NewInt(1000),
		Gas:      params.TxGas,
		GasPrice: big.NewInt(2 * params.InitialBaseFee),
	})
	// Nonce 1: zero-price signing tx to BlockSigners (0x89) — the residual case.
	signTx, err := types.SignTx(
		contracts.CreateTxSign(genesis.Number, genesis.Hash(), 1, common.BlockSignersBinary),
		signer, testBankKey,
	)
	if err != nil {
		t.Fatalf("sign signing tx: %v", err)
	}

	for i, e := range b.txPool.Add([]*types.Transaction{paidTx, signTx}, true) {
		if e != nil {
			t.Fatalf("pool.Add tx[%d]: %v", i, e)
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
		t.Fatal("test precondition: block carries no base fee (EIP-1559 not active)")
	}

	txs := res.block.Transactions()
	if len(txs) != 2 {
		hashes := make([]common.Hash, len(txs))
		for i, tx := range txs {
			hashes[i] = tx.Hash()
		}
		t.Fatalf("block has %d tx(s) %v; want 2 [paid=%v, sign=%v]",
			len(txs), hashes, paidTx.Hash(), signTx.Hash())
	}
	if txs[0].Hash() != paidTx.Hash() {
		t.Errorf("txs[0] = %v, want paid tx %v", txs[0].Hash(), paidTx.Hash())
	}
	if txs[1].Hash() != signTx.Hash() {
		t.Errorf("txs[1] = %v, want sign tx %v", txs[1].Hash(), signTx.Hash())
	}
}
