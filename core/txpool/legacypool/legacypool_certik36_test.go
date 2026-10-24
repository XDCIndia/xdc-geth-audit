// Copyright 2024 The go-ethereum Authors
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

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// failingStateBlockChain wraps testBlockChain and returns an error from StateAt
// when the requested root matches failRoot.  All other roots succeed.
type failingStateBlockChain struct {
	*testBlockChain
	failRoot common.Hash
}

func (bc *failingStateBlockChain) StateAt(root common.Hash, number *big.Int, time uint64) (*state.StateDB, error) {
	if root == bc.failRoot {
		return nil, errors.New("state unavailable: simulated CertiK XDC-36 test failure")
	}
	return bc.statedb, nil
}

// TestRunReorgSkipsPostProcessingOnFailedReset verifies CertiK XDC-36:
// when pool.reset() fails to open the new head's state, runReorg must not
// run head-specific post-processing (demotion, nonce forwarding, base-fee
// repricing) against the stale currentState.
//
// Before the fix, demoteUnexecutables ran with the stale currentState even
// when reset returned without updating it.  The test injects a stale
// currentState where addr.nonce = 5; with the bug, all three pending txs
// (nonces 0–2) are evicted as "too old".  With the fix they survive.
func TestRunReorgSkipsPostProcessingOnFailedReset(t *testing.T) {
	t.Parallel()

	badRoot := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	inner := newTestBlockChain(params.TestChainConfig, 10_000_000, statedb, new(event.Feed))
	blockchain := &failingStateBlockChain{testBlockChain: inner, failRoot: badRoot}

	pool := New(testTxPoolConfig, blockchain)
	if err := pool.Init(testTxPoolConfig.PriceLimit, blockchain.CurrentBlock(), newReserver()); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)
	testAddBalance(pool, addr, big.NewInt(1_000_000))

	// Add 3 sequential txs (nonces 0, 1, 2) — all should go to pending.
	for j := 0; j < 3; j++ {
		if err := pool.addRemoteSync(transaction(uint64(j), 100_000, key)); err != nil {
			t.Fatalf("add tx nonce=%d: %v", j, err)
		}
	}
	pool.mu.RLock()
	pending0, _ := pool.stats()
	pool.mu.RUnlock()
	if pending0 != 3 {
		t.Fatalf("pre-condition: want 3 pending txs, got %d", pending0)
	}

	// Inject a stale currentState where addr.nonce = 5.  When demoteUnexecutables
	// runs with this state, txs with nonces 0, 1, 2 (all < 5) are evicted.
	// The fix prevents demoteUnexecutables from running on a failed reset.
	elevatedStatedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	elevatedStatedb.SetNonce(addr, 5, tracing.NonceChangeUnspecified)
	elevatedStatedb.AddBalance(addr, new(uint256.Int).SetUint64(1_000_000), tracing.BalanceChangeUnspecified)
	pool.mu.Lock()
	pool.currentState = elevatedStatedb
	pool.mu.Unlock()

	// Reset to a head whose root makes StateAt fail.
	badHead := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: common.Big0,
		GasLimit:   10_000_000,
		Root:       badRoot,
	}
	pool.Reset(blockchain.CurrentBlock(), badHead)

	// After the failed reset the pool must still have 3 pending txs.
	// Before the fix demoteUnexecutables ran with the elevated stale state
	// (nonce=5), evicting all three as "too old" (CertiK XDC-36).
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	pending1, _ := pool.stats()
	if pending1 != 3 {
		t.Errorf("want 3 pending txs after failed reset, got %d — CertiK XDC-36: post-processing ran on stale state", pending1)
	}
}
