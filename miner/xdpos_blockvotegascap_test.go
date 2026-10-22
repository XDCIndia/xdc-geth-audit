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
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// buildWithGasCap fills a block from a pool holding pendingTxs plus extra
// 21,000-gas transfers, with the given --miner.blockvotegascap, and returns
// the block's gas used, tx count and header gas limit.
func buildWithGasCap(t *testing.T, gasCap uint64, extra int) (gasUsed uint64, txs int, gasLimit uint64) {
	return buildWithGasCapTxGas(t, gasCap, extra, params.TxGas)
}

// buildWithGasCapTxGas is buildWithGasCap with the extra transfers declaring
// txGas (they all use 21,000).
func buildWithGasCapTxGas(t *testing.T, gasCap uint64, extra int, txGas uint64) (gasUsed uint64, txs int, gasLimit uint64) {
	t.Helper()
	w, b := newTestWorker(t, params.TestChainConfig, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)

	signer := types.LatestSigner(params.TestChainConfig)
	nonce := uint64(len(pendingTxs))
	var more []*types.Transaction
	for i := 0; i < extra; i++ {
		more = append(more, types.MustSignNewTx(testBankKey, signer, &types.LegacyTx{
			Nonce:    nonce + uint64(i),
			To:       &common.Address{0x42},
			Value:    big.NewInt(1),
			Gas:      txGas,
			GasPrice: big.NewInt(params.InitialBaseFee),
		}))
	}
	b.txPool.Add(more, true)

	env, err := w.prepareWork(context.Background(), &generateParams{
		timestamp: uint64(time.Now().Unix()),
		coinbase:  testBankAddress,
	}, false)
	if err != nil {
		t.Fatalf("prepareWork: %v", err)
	}
	applyBlockVoteGasCap(env, gasCap)
	if err := w.fillTransactions(context.Background(), new(atomic.Int32), env); err != nil {
		t.Fatalf("fillTransactions: %v", err)
	}
	for _, r := range env.receipts {
		gasUsed += r.GasUsed
	}
	return gasUsed, len(env.txs), env.header.GasLimit
}

// TestBlockVoteGasCap checks #1665: with --miner.blockvotegascap set, the
// builder packs at most the cap's worth of gas, and the header gas limit (the
// only consensus-visible limit) is unchanged.
func TestBlockVoteGasCap(t *testing.T) {
	const extra = 20 // pool holds len(pendingTxs)+20 txs of ~21,000 gas each

	fullGas, fullTxs, fullLimit := buildWithGasCap(t, 0, extra)
	if fullTxs != len(pendingTxs)+extra {
		t.Fatalf("uncapped build: %d txs, want all %d", fullTxs, len(pendingTxs)+extra)
	}

	gasCap := 5 * params.TxGas
	gasUsed, txs, limit := buildWithGasCap(t, gasCap, extra)
	// The cap is soft: packing stops once gas used reaches it, so the last
	// transaction may take the block over it by less than one transaction.
	if gasUsed >= gasCap+params.TxGas {
		t.Errorf("capped build used %d gas, cap %d (+ at most one tx)", gasUsed, gasCap)
	}
	if txs >= fullTxs || txs == 0 {
		t.Errorf("capped build packed %d txs (uncapped %d); want fewer but not zero", txs, fullTxs)
	}
	if limit != fullLimit {
		t.Errorf("header gas limit changed by the cap: %d, want %d", limit, fullLimit)
	}

	// A cap at or above the header gas limit is a no-op.
	gasUsed2, txs2, _ := buildWithGasCap(t, fullLimit, extra)
	if txs2 != fullTxs || gasUsed2 != fullGas {
		t.Errorf("cap >= gas limit changed the block: %d txs / %d gas, want %d / %d", txs2, gasUsed2, fullTxs, fullGas)
	}
}

// TestBlockVoteGasCap_OversizedDeclaredGas checks that a transaction declaring
// more gas than the cap is still included. Shrinking the gas pool to the cap
// skipped such a transaction, and the rest of its sender's queue, on every
// capped validator, so a transfer with an over-estimated gas limit was never
// mined.
func TestBlockVoteGasCap_OversizedDeclaredGas(t *testing.T) {
	gasCap := 5 * params.TxGas
	// Each extra transfer declares 1,000,000 gas (> cap) but uses 21,000.
	_, txs, _ := buildWithGasCapTxGas(t, gasCap, 20, 1_000_000)
	if txs == 0 {
		t.Fatal("no transaction included: oversized declared gas blocked the block")
	}
	if txs <= len(pendingTxs) {
		t.Errorf("only %d txs included; the oversized-declared transfers were skipped", txs)
	}
}
