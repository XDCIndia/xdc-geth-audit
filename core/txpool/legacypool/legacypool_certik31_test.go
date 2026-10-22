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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
)

// TestEvictedTxsNotAnnounced verifies CertiK XDC-31: transactions evicted by
// truncatePending must not be included in the txFeed event sent to subscribers.
// Before the fix, promoted txs that truncatePending removed from pool.all were
// still announced; peers would mark them as known and skip re-submissions.
func TestEvictedTxsNotAnnounced(t *testing.T) {
	t.Parallel()

	// Small pool: GlobalSlots=4, AccountSlots=1.  Four accounts each submitting
	// 4 sequential txs promote 16 txs total; truncatePending then evicts 12 of
	// them, leaving exactly GlobalSlots=4 in pool.all.
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	blockchain := newTestBlockChain(params.TestChainConfig, 10_000_000, statedb, new(event.Feed))

	config := testTxPoolConfig
	config.GlobalSlots = 4
	config.AccountSlots = 1
	config.GlobalQueue = 100

	pool := New(config, blockchain)
	if err := pool.Init(config.PriceLimit, blockchain.CurrentBlock(), newReserver()); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Subscribe before adding transactions so we capture every event.
	// Buffer 200 >> 16 txs ensures txFeed.Send never blocks on this channel.
	ch := make(chan core.NewTxsEvent, 200)
	sub := pool.SubscribeTransactions(ch, true)
	defer sub.Unsubscribe()

	// Fund 4 accounts and build 4 sequential txs each (nonces 0-3).
	const (
		numAccounts = 4
		txsPerAcct  = 4
	)
	var txs types.Transactions
	for i := 0; i < numAccounts; i++ {
		key, _ := crypto.GenerateKey()
		addr := crypto.PubkeyToAddress(key.PublicKey)
		testAddBalance(pool, addr, big.NewInt(1_000_000))
		for j := 0; j < txsPerAcct; j++ {
			txs = append(txs, transaction(uint64(j), 100_000, key))
		}
	}

	pool.addRemotesSync(txs)

	// Events may be delivered after addRemotesSync returns (the pool no longer
	// holds a reset on slow subscribers), so wait for the first batch and then
	// collect until the feed is idle.
	announced := make(map[common.Hash]struct{})
	wait := 2 * time.Second
	for {
		select {
		case ev := <-ch:
			for _, tx := range ev.Txs {
				announced[tx.Hash()] = struct{}{}
			}
			wait = 100 * time.Millisecond
			continue
		case <-time.After(wait):
		}
		break
	}

	// Sanity: truncatePending must have left fewer txs than submitted, and at
	// least some announcements must have been made.
	if len(announced) == 0 {
		t.Fatal("no transactions were announced — test setup is broken")
	}

	// After the fix, every announced tx must still be in pool.all.  Before the
	// fix, evicted txs remained in the announced set (CertiK XDC-31).
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	for hash := range announced {
		if pool.all.Get(hash) == nil {
			t.Errorf("tx %s was announced but is not in pool.all (evicted without filtering) — CertiK XDC-31", hash)
		}
	}
}
