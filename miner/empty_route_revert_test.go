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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// TestApplyStaleEmptyRouteTxNoPanic checks XDC-HUNT-24: on chain 5151 an empty
// route tx (to 0x92) with a stale nonce must be skipped with an error. The nonce
// check ran after Finalise, which drops every snapshot, so reverting to the
// pre-tx snapshot panicked the miner.
func TestApplyStaleEmptyRouteTxNoPanic(t *testing.T) {
	cfg := *params.TestChainConfig
	cfg.ChainID = big.NewInt(5151)
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900}
	cfg.TIPXDCXBlock = big.NewInt(1)
	cfg.TIPXDCXReceiverDisableBlock = big.NewInt(100)

	key, _ := crypto.GenerateKey()
	from := crypto.PubkeyToAddress(key.PublicKey)
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	statedb.SetNonce(from, 1, tracing.NonceChangeUnspecified)

	header := &types.Header{Number: big.NewInt(10), GasLimit: 50_000_000, Difficulty: big.NewInt(1)}
	env := &environment{state: statedb, gasPool: core.NewGasPool(header.GasLimit), header: header}
	raw := types.NewTransaction(0, common.TradingStateAddrBinary, new(big.Int), 100000, new(big.Int), nil)
	tx, err := types.SignTx(raw, types.MakeSigner(&cfg, header.Number, header.Time), key)
	if err != nil {
		t.Fatal(err)
	}
	if core.RouteTransaction(&cfg, header.Number, tx) != core.TxRouteEmpty {
		t.Fatal("test setup: tx is not on the empty route")
	}
	m := &Miner{chainConfig: &cfg}
	if _, _, err := m.applyTransaction(context.Background(), env, tx); err == nil {
		t.Fatal("stale empty route tx applied, want nonce error")
	}
	if n := statedb.GetNonce(from); n != 1 {
		t.Fatalf("sender nonce = %d, want 1", n)
	}
}
