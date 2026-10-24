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
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
)

// TestSlowSubscriberDoesNotBlockReset verifies CertiK XDC-15: a txFeed
// subscriber that never reads must not stall pool resets.
//
// Before the fix, runReorg closed done only after txFeed.Send returned.
// txFeed.Send blocks until every subscriber has consumed the event, so a
// non-reading subscriber held the reset waiter indefinitely.
//
// After the fix, done is closed before txFeed.Send, so requestReset returns
// as soon as pool state work is complete, regardless of subscriber speed.
func TestSlowSubscriberDoesNotBlockReset(t *testing.T) {
	t.Parallel()

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	blockchain := newTestBlockChain(params.TestChainConfig, 10_000_000, statedb, new(event.Feed))

	pool := New(testTxPoolConfig, blockchain)
	if err := pool.Init(testTxPoolConfig.PriceLimit, blockchain.CurrentBlock(), newReserver()); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Subscribe with a zero-buffer channel (synchronous) that is never read.
	// Before the fix this causes txFeed.Send to block indefinitely.
	blocked := make(chan core.NewTxsEvent) // zero buffer, never read
	sub := pool.SubscribeTransactions(blocked, true)
	defer sub.Unsubscribe()

	// Add a transaction to give the next reorg something to announce.
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)
	testAddBalance(pool, addr, big.NewInt(1_000_000))
	tx := transaction(0, 100_000, key)

	// addRemoteSync blocks until the reorg triggered by the add is complete
	// (including done being closed).  With the fix this finishes promptly
	// because done closes before txFeed.Send.  Without the fix this hangs
	// because txFeed.Send blocks on the zero-buffer subscriber above.
	done := make(chan struct{})
	go func() {
		defer close(done)
		pool.addRemoteSync(tx)
	}()

	select {
	case <-done:
		// addRemoteSync returned promptly — fix is working.
	case <-time.After(3 * time.Second):
		t.Fatal("addRemoteSync blocked for >3s: txFeed.Send is holding up the pool reset (CertiK XDC-15)")
	}

	// Drain the blocked channel in the background so sub.Unsubscribe() can
	// complete (Unsubscribe waits for any in-progress send to finish).
	go func() {
		for range blocked {
		}
	}()
}
