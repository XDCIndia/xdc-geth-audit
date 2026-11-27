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

package state

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// TestEmptyIgnoresWrappedBalance checks XDC-HUNT-48: an account whose true
// balance is exactly 2^256 has a zero 256-bit Balance field but a non-nil
// BigBalance. Canonical XDPoSChain keeps the full big.Int balance, so such an
// account is not empty and is not deleted by EIP-158 cleanup.
func TestEmptyIgnoresWrappedBalance(t *testing.T) {
	var (
		addr   = common.HexToAddress("0x9a3787688fd210ec8f8d0224c6c50b8178d75bc0")
		maxU   = new(uint256.Int).SetAllOne()
		twoE56 = new(big.Int).Lsh(big.NewInt(1), 256)
	)
	db := NewDatabaseForTesting()
	state, _ := New(types.EmptyRootHash, db)
	state.SetBalance(addr, maxU, tracing.BalanceChangeUnspecified)
	state.AddBalance(addr, uint256.NewInt(1), tracing.BalanceChangeUnspecified)

	if got := state.GetBigBalance(addr); got == nil || got.Cmp(twoE56) != 0 {
		t.Fatalf("BigBalance = %v, want 2^256", got)
	}
	if state.Empty(addr) {
		t.Fatal("account with balance 2^256 reported empty")
	}
	// EIP-158 cleanup must keep the account.
	state.Finalise(params.Rules{IsEIP158: true})
	if !state.Exist(addr) {
		t.Fatal("account with balance 2^256 deleted by Finalise")
	}
	root, err := state.Commit(params.Rules{IsEIP158: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(root, db)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Exist(addr) {
		t.Fatal("account with balance 2^256 missing after commit")
	}
	if got := reopened.GetBigBalance(addr); got == nil || got.Cmp(twoE56) != 0 {
		t.Fatalf("BigBalance after commit = %v, want 2^256", got)
	}
}
