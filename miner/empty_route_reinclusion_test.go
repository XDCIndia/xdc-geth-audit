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

// TestMinerEmptyRouteTxNoReinclusion verifies that an empty-route tx to 0x91-0x94
// that was included in block N is NOT included again in block N+1, even though
// ApplyEmptyTransaction never bumps the sender's nonce (Fixes #1702).
//
// Fails on origin/main: the second block contains the tx again.
// Passes with the fix: GetCanonicalTransaction finds the tx and skips it.

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func TestMinerEmptyRouteTxNoReinclusion(t *testing.T) {
	// Chain config: IsTIPXDCXReceiver active from genesis (TIPXDCX=0) and
	// effectively never disabled (TIPXDCXReceiverDisable=1e17), matching the
	// 5151/5050/5551 devnet profiles where the window stays open forever.
	cfg := *params.TestChainConfig
	cfg.TIPXDCXBlock = big.NewInt(0)
	cfg.TIPXDCXReceiverDisableBlock = new(big.Int).SetUint64(uint64(1e17))

	b := newTestWorkerBackend(t, &cfg, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
	w := New(b, testConfig, ethash.NewFaker())

	signer := types.LatestSigner(&cfg)
	// Empty-route tx to 0x91 (XDCXAddrBinary); Gas=TxGas so the pool admits it.
	emptyTx, err := types.SignTx(
		types.NewTransaction(0, common.XDCXAddrBinary, new(big.Int), params.TxGas,
			big.NewInt(2*params.InitialBaseFee), nil),
		signer, testBankKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if errs := b.txPool.Add([]*types.Transaction{emptyTx}, true); errs[0] != nil {
		t.Fatalf("pool.Add: %v", errs[0])
	}

	genesis := b.chain.CurrentBlock()
	ts := uint64(time.Now().Unix())

	// Block 1 must contain the empty-route tx (test precondition).
	res1 := w.generateWork(context.Background(), &generateParams{
		parentHash: genesis.Hash(),
		timestamp:  ts,
		coinbase:   testUserAddress,
	}, false)
	if res1.err != nil {
		t.Fatalf("block 1 generateWork: %v", res1.err)
	}
	found := false
	for _, tx := range res1.block.Transactions() {
		if tx.Hash() == emptyTx.Hash() {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("test precondition: block 1 did not include the empty-route tx")
	}

	// Insert block 1 into the canonical chain so the tx lookup entry is written.
	if _, err := b.chain.InsertChain(types.Blocks{res1.block}); err != nil {
		t.Fatalf("InsertChain block 1: %v", err)
	}

	// Block 2: the empty-route tx is still pending (nonce never bumped).
	// With the fix, GetCanonicalTransaction finds the tx and the miner skips it.
	block1 := b.chain.CurrentBlock()
	res2 := w.generateWork(context.Background(), &generateParams{
		parentHash: block1.Hash(),
		timestamp:  block1.Time + 1,
		coinbase:   testUserAddress,
	}, false)
	if res2.err != nil {
		t.Fatalf("block 2 generateWork: %v", res2.err)
	}
	for _, tx := range res2.block.Transactions() {
		if tx.Hash() == emptyTx.Hash() {
			t.Error("block 2 re-includes empty-route tx that was already in block 1 (re-inclusion bug)")
		}
	}
}

// TestMinerEmptyRouteTxIncludedOnce checks that the tx IS in block 1 (sanity),
// covering the positive path (the fix must not prevent first inclusion).
func TestMinerEmptyRouteTxIncludedOnce(t *testing.T) {
	cfg := *params.TestChainConfig
	cfg.TIPXDCXBlock = big.NewInt(0)
	cfg.TIPXDCXReceiverDisableBlock = new(big.Int).SetUint64(uint64(1e17))

	b := newTestWorkerBackend(t, &cfg, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
	w := New(b, testConfig, ethash.NewFaker())

	signer := types.LatestSigner(&cfg)
	emptyTx, err := types.SignTx(
		types.NewTransaction(0, common.TradingStateAddrBinary, new(big.Int), params.TxGas,
			big.NewInt(2*params.InitialBaseFee), nil),
		signer, testBankKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if errs := b.txPool.Add([]*types.Transaction{emptyTx}, true); errs[0] != nil {
		t.Fatalf("pool.Add: %v", errs[0])
	}

	genesis := b.chain.CurrentBlock()
	res := w.generateWork(context.Background(), &generateParams{
		parentHash: genesis.Hash(),
		timestamp:  uint64(time.Now().Unix()),
		coinbase:   testUserAddress,
	}, false)
	if res.err != nil {
		t.Fatalf("generateWork: %v", res.err)
	}
	for _, tx := range res.block.Transactions() {
		if tx.Hash() == emptyTx.Hash() {
			return // found — pass
		}
	}
	t.Error("empty-route tx was not included in the first block")
}

// testEmptyRouteTx routes transactions via core.RouteTransaction; verify the
// addresses used in this test actually route to TxRouteEmpty.
func TestMinerEmptyRouteTxRouting(t *testing.T) {
	cfg := *params.TestChainConfig
	cfg.TIPXDCXBlock = big.NewInt(0)
	cfg.TIPXDCXReceiverDisableBlock = new(big.Int).SetUint64(uint64(1e17))

	addrs := []common.Address{
		common.XDCXAddrBinary,
		common.TradingStateAddrBinary,
		common.XDCXLendingAddressBinary,
		common.XDCXLendingFinalizedTradeAddressBinary,
	}
	blockNum := big.NewInt(1)
	for _, addr := range addrs {
		tx := types.NewTransaction(0, addr, new(big.Int), params.TxGas, big.NewInt(1), nil)
		got := core.RouteTransaction(&cfg, blockNum, tx)
		if got != core.TxRouteEmpty {
			t.Errorf("addr %s: got route %v, want TxRouteEmpty", addr, got)
		}
	}
}
