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
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/txorder"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// TestCommitTransactionsSigningTxFilter checks the canonical miner filter for
// 0x89 txs (#1414): a tx with data shorter than 68 bytes, or one signing a
// block number >= the block being built, is left out; a valid one is kept.
func TestCommitTransactionsSigningTxFilter(t *testing.T) {
	old := common.TIPSigning
	common.TIPSigning = big.NewInt(0)
	t.Cleanup(func() { common.TIPSigning = old })

	b := newTestWorkerBackend(t, params.TestChainConfig, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
	w := New(b, testConfig, ethash.NewFaker())
	genesis := b.chain.CurrentBlock()
	env, err := w.prepareWork(context.Background(), &generateParams{
		parentHash: genesis.Hash(),
		timestamp:  uint64(time.Now().Unix()),
		coinbase:   testUserAddress,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer env.discard()

	signer := types.LatestSigner(params.TestChainConfig)
	pending := make(map[common.Address][]*txpool.LazyTransaction)
	add := func(tx *types.Transaction) *types.Transaction {
		key, _ := crypto.GenerateKey()
		tx, err := types.SignTx(tx, signer, key)
		if err != nil {
			t.Fatal(err)
		}
		pending[crypto.PubkeyToAddress(key.PublicKey)] = []*txpool.LazyTransaction{{
			Hash: tx.Hash(), Tx: tx, Time: tx.Time(), Gas: tx.Gas(),
			GasFeeCap: uint256.MustFromBig(tx.GasFeeCap()), GasTipCap: uint256.MustFromBig(tx.GasTipCap()),
		}}
		return tx
	}
	short := contracts.CreateTxSign(genesis.Number, genesis.Hash(), 0, common.BlockSignersBinary)
	add(types.NewTransaction(0, common.BlockSignersBinary, new(big.Int), short.Gas(), new(big.Int), short.Data()[:67]))
	add(contracts.CreateTxSign(env.header.Number, genesis.Hash(), 0, common.BlockSignersBinary))
	valid := add(contracts.CreateTxSign(genesis.Number, genesis.Hash(), 0, common.BlockSignersBinary))

	plain := txorder.NewTransactionsByPriceAndNonce(env.signer, pending, nil)
	blobs := txorder.NewTransactionsByPriceAndNonce(env.signer, nil, nil)
	if err := w.commitTransactions(context.Background(), env, plain, blobs, nil); err != nil {
		t.Fatal(err)
	}
	if len(env.txs) != 1 || env.txs[0].Hash() != valid.Hash() {
		got := make([]common.Hash, len(env.txs))
		for i, tx := range env.txs {
			got[i] = tx.Hash()
		}
		t.Fatalf("committed txs = %v, want only %v", got, valid.Hash())
	}
}
