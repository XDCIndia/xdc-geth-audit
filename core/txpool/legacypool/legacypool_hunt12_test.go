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
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
)

// TestTRC21CapacityBuiltPerHead checks XDC-HUNT-12: the TRC21 fee capacity map
// is built when the pool is initialised and on each reset, and an admitted tx
// reuses it. Before the fix the map was rebuilt from state for each admission,
// under the pool lock.
func TestTRC21CapacityBuiltPerHead(t *testing.T) {
	senderKey, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)
	recipient := common.HexToAddress("0x0000000000000000000000000000000000000002")

	minFee := new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil)
	tokenBalance := new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)
	oneXDC := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	capacity1 := new(big.Int).Mul(big.NewInt(100), oneXDC)
	capacity2 := new(big.Int).Mul(big.NewInt(200), oneXDC)

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	setupTRC21State(statedb, sender, tokenBalance, capacity1, minFee)

	bc := newTestBlockChain(trc21TestConfig, 10000000, statedb, new(event.Feed))
	pool := New(testTxPoolConfig, bc)
	if err := pool.Init(testTxPoolConfig.PriceLimit, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatal(err)
	}
	<-pool.initDoneCh
	defer pool.Close()

	capacityOf := func() *big.Int {
		pool.mu.RLock()
		defer pool.mu.RUnlock()
		return pool.trc21Capacity[trc21TokenAddr]
	}
	if got := capacityOf(); got == nil || got.Cmp(capacity1) != 0 {
		t.Fatalf("capacity after Init = %v, want %v", got, capacity1)
	}

	// Admit a tx. It must not rebuild the map.
	pool.mu.RLock()
	before := reflect.ValueOf(pool.trc21Capacity).Pointer()
	pool.mu.RUnlock()
	tx, _ := types.SignTx(
		types.NewTransaction(0, trc21TokenAddr, big.NewInt(0), 150000, big.NewInt(1000e9), trc21TransferData(recipient)),
		types.HomesteadSigner{}, senderKey)
	if err := pool.addRemoteSync(tx); err != nil {
		t.Fatalf("TRC21 tx not admitted: %v", err)
	}
	pool.mu.RLock()
	after := reflect.ValueOf(pool.trc21Capacity).Pointer()
	pool.mu.RUnlock()
	if before != after {
		t.Fatal("the TRC21 capacity map was rebuilt by an admission")
	}

	// A new head rebuilds it from the new state.
	pool.mu.Lock()
	setupTRC21State(statedb, sender, tokenBalance, capacity2, minFee)
	pool.mu.Unlock()
	<-pool.requestReset(nil, nil)
	if got := capacityOf(); got == nil || got.Cmp(capacity2) != 0 {
		t.Fatalf("capacity after reset = %v, want %v", got, capacity2)
	}
}
