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

// TestSigningTxCapPerBlock checks F-2026-19467: the miner stops packing 0x89
// BlockSigners signing transactions once the per-block cap (2 × V2 MaxMasternodes)
// is reached, even though signing txs do not consume gas from the pool.
func TestSigningTxCapPerBlock(t *testing.T) {
	const maxMN = 5        // small value so the cap is easy to hit in a test
	const want = 2 * maxMN // expected cap: 10

	// Activate TIPSigning from genesis so signing txs are routed through
	// ApplySigningTransaction on the ethash-backed test chain.
	old := common.TIPSigning
	common.TIPSigning = big.NewInt(0)
	t.Cleanup(func() { common.TIPSigning = old })

	// Build a chain config with a small V2 MaxMasternodes so the cap is reachable
	// without creating thousands of signing txs.
	cfg := *params.TestChainConfig
	cfg.XDPoS = &params.XDPoSConfig{V2: &params.V2{CurrentConfig: &params.V2Config{MaxMasternodes: maxMN}}}

	b := newTestWorkerBackend(t, &cfg, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 0)
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

	// Create want+5 valid signing txs from distinct keys so each has a fresh
	// nonce and all pass the data-length and block-number filters.
	signer := types.LatestSigner(&cfg)
	pending := make(map[common.Address][]*txpool.LazyTransaction)
	for i := 0; i < want+5; i++ {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		raw := contracts.CreateTxSign(genesis.Number, genesis.Hash(), 0, common.BlockSignersBinary)
		tx, err := types.SignTx(raw, signer, key)
		if err != nil {
			t.Fatal(err)
		}
		addr := crypto.PubkeyToAddress(key.PublicKey)
		pending[addr] = []*txpool.LazyTransaction{{
			Hash:      tx.Hash(),
			Tx:        tx,
			Time:      tx.Time(),
			Gas:       tx.Gas(),
			GasFeeCap: uint256.MustFromBig(tx.GasFeeCap()),
			GasTipCap: uint256.MustFromBig(tx.GasTipCap()),
		}}
	}

	plain := txorder.NewTransactionsByPriceAndNonce(env.signer, pending, nil)
	blobs := txorder.NewTransactionsByPriceAndNonce(env.signer, nil, nil)
	if err := w.commitTransactions(context.Background(), env, plain, blobs, nil); err != nil {
		t.Fatal(err)
	}

	if env.signingTxCount != want {
		t.Errorf("signingTxCount = %d, want %d (cap = 2 × V2 MaxMasternodes = 2 × %d)", env.signingTxCount, want, maxMN)
	}
	if len(env.txs) != want {
		t.Errorf("committed txs = %d, want %d", len(env.txs), want)
	}
}
