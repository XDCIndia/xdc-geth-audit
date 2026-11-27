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

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// TestDemoteRefusedByQueueLeavesLookup checks XDC-HUNT-47: when
// demoteUnexecutables moves a pending tx to the queue and the queue refuses it
// (a better tx has the same nonce there), the tx must leave the lookup too.
// Before the fix it stayed in pool.all with no entry in pending or queued.
func TestDemoteRefusedByQueueLeavesLookup(t *testing.T) {
	pool, key := setupPool()
	defer pool.Close()

	from := crypto.PubkeyToAddress(key.PublicKey)
	pool.mu.Lock()
	pool.currentState.AddBalance(from, uint256.NewInt(50000), tracing.BalanceChangeUnspecified)

	x := pricedTransaction(0, 100000, big.NewInt(1), key)  // cost 100100: not affordable, dropped
	a := pricedTransaction(1, 21000, big.NewInt(1), key)   // affordable, but a gap in front: demoted
	b := pricedTransaction(1, 21000, big.NewInt(100), key) // queued at the same nonce, much better than a

	list := newList(true)
	for _, tx := range []*types.Transaction{x, a} {
		if inserted, _ := list.Add(tx, pool.config.PriceBump); !inserted {
			t.Fatal("test setup: pending add failed")
		}
		pool.all.Add(tx)
		pool.priced.Put(tx)
	}
	pool.pending[from] = list
	if _, err := pool.queue.add(b); err != nil {
		t.Fatal(err)
	}
	pool.all.Add(b)
	pool.priced.Put(b)
	pool.pendingNonces.set(from, 2)

	pool.demoteUnexecutables(nil)
	pool.mu.Unlock()

	if err := validatePoolInternals(pool); err != nil {
		t.Fatal(err)
	}
	if pool.all.Get(a.Hash()) != nil {
		t.Fatal("refused tx stayed in the lookup")
	}
	if pool.all.Get(b.Hash()) == nil {
		t.Fatal("queued tx left the lookup")
	}
}
