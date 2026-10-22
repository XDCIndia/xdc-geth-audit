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

package legacypool

// Tests that a pending empty-route tx (to 0x91-0x94) is removed from the pool
// once it is confirmed in the canonical chain, even though ApplyEmptyTransaction
// never bumps the sender's nonce (Fixes #1702).
//
// Fails on origin/main: the tx remains pending after Reset.
// Passes with the fix: reset() walks the new head block and drops the tx.

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
)

// emptyRouteBlockChain wraps testBlockChain so that GetBlock can return a
// specific block (simulating the newly confirmed head that contains our tx).
type emptyRouteBlockChain struct {
	*testBlockChain
	headBlock *types.Block // returned when GetBlock is called with headBlock.Hash()
}

func (c *emptyRouteBlockChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	if c.headBlock != nil && c.headBlock.Hash() == hash {
		return c.headBlock
	}
	return c.testBlockChain.GetBlock(hash, number)
}

func (c *emptyRouteBlockChain) CurrentBlock() *types.Header {
	if c.headBlock != nil {
		return c.headBlock.Header()
	}
	return c.testBlockChain.CurrentBlock()
}

// xdcEmptyRouteCfg is a minimal XDC chain config where IsXDC() = true and
// IsTIPXDCXReceiver is active at every block:
//   - TIPXDCX = 0 (enabled from genesis)
//   - TIPXDCXReceiverDisable = 1e17 (never disabled, matches devnets 5151/5050/5551)
func xdcEmptyRouteCfg() *params.ChainConfig {
	cpy := *params.TestChainConfig
	cpy.XDPoS = &params.XDPoSConfig{Epoch: 900}
	cpy.Ethash = nil
	cpy.LondonBlock = common.Big0
	cpy.BerlinBlock = common.Big0
	return &cpy
}

// gas50xFloor is the Gas50x tier minimum: XDCBaseFee (12.5 gwei).
// Gas2500x (625 gwei) may kick in on some global initializations, so we
// use a price comfortably above both tiers.
var emptyRouteTxGasPrice = new(big.Int).Mul(
	new(big.Int).SetUint64(params.XDCBaseFee), // 12.5 gwei
	big.NewInt(60), // ×60 = 750 gwei > Gas2500x (625 gwei)
)

// TestPoolEmptyRouteTxDroppedAfterInclusion checks that a pending empty-route
// tx is removed from the pool once the head advances to a block that contains it.
func TestPoolEmptyRouteTxDroppedAfterInclusion(t *testing.T) {
	setXDCXReceiverWindow(t)
	cfg := xdcEmptyRouteCfg()

	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	// Fund generously so balance is never the rejection reason.
	statedb.AddBalance(addr, uint256.MustFromBig(new(big.Int).SetUint64(1e18)), tracing.BalanceChangeUnspecified)

	bc := &emptyRouteBlockChain{
		testBlockChain: newTestBlockChain(cfg, 10_000_000, statedb, new(event.Feed)),
	}

	pcfg := DefaultConfig
	pcfg.Journal = ""
	pcfg.PriceLimit = 0

	pool := New(pcfg, bc)
	if err := pool.Init(0, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatalf("pool.Init: %v", err)
	}
	<-pool.initDoneCh
	defer pool.Close()

	signer := types.LatestSigner(cfg)
	emptyTx, err := types.SignTx(
		types.NewTransaction(0, common.XDCXAddrBinary, new(big.Int), 100_000, emptyRouteTxGasPrice, nil),
		signer, key,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Add the empty-route tx to the pool — it should be pending.
	if errs := pool.Add([]*types.Transaction{emptyTx}, true); errs[0] != nil {
		t.Fatalf("pool.Add: %v", errs[0])
	}
	if pending, _ := pool.Stats(); pending == 0 {
		t.Fatal("precondition: tx not pending after Add")
	}

	// Simulate a new canonical head that contains the empty-route tx.
	// The emptyRouteBlockChain.GetBlock returns this block for its hash, so
	// reset() can find and inspect it.
	genesis := types.NewBlock(bc.testBlockChain.CurrentBlock(), nil, nil, trie.NewStackTrie(nil))
	newHeader := &types.Header{
		ParentHash: genesis.Hash(),
		Number:     big.NewInt(1),
		GasLimit:   10_000_000,
	}
	newBlock := types.NewBlock(newHeader, &types.Body{Transactions: types.Transactions{emptyTx}}, nil, trie.NewStackTrie(nil))
	bc.headBlock = newBlock

	// Reset: simple advance (new head is direct child of old head).
	// The pool must notice the tx is in the new head block and drop it.
	<-pool.requestReset(genesis.Header(), newBlock.Header())

	pending, _ := pool.Stats()
	if pending != 0 {
		t.Errorf("expected 0 pending txs after reset, got %d (empty-route tx not dropped)", pending)
	}
}

// setXDCXReceiverWindow opens the XDCx receiver window from genesis
// (TIPXDCX=0, TIPXDCXReceiverDisable=1e17, as on the 5151/5050/5551 devnets)
// for the duration of the test.
func setXDCXReceiverWindow(t *testing.T) {
	oldX, oldD := common.TIPXDCX, common.TIPXDCXReceiverDisable
	common.TIPXDCX = big.NewInt(0)
	common.TIPXDCXReceiverDisable = new(big.Int).SetUint64(uint64(1e17))
	t.Cleanup(func() { common.TIPXDCX, common.TIPXDCXReceiverDisable = oldX, oldD })
}
