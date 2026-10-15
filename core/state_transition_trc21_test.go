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

package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// TestTRC21TokenFeeMessage checks the XDPoSChain token-fee semantics of the
// state transition: the sender pays only the value (no gas buy, no refund), the
// coinbase owner is still credited gasUsed*gasPrice, and a capacity that does not
// cover the gas is rejected.
func TestTRC21TokenFeeMessage(t *testing.T) {
	var (
		config = params.XDCMainnetChainConfig
		number = big.NewInt(40_000_000) // > TIPTRC21Fee (38,383,838), < Gas50x
		sender = common.HexToAddress("0x5555555555555555555555555555555555555555")
		token  = common.HexToAddress("0x6666666666666666666666666666666666666666")
		owner  = common.HexToAddress("0x7777777777777777777777777777777777777777")
		price  = params.TRC21GasPriceForBlock(config, number)
	)
	if price.Cmp(common.TRC21GasPrice) != 0 {
		t.Fatalf("TRC21 gas price at %v = %v, want %v", number, price, common.TRC21GasPrice)
	}
	run := func(capacity *big.Int) (*ExecutionResult, *state.StateDB, error) {
		statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		if err != nil {
			t.Fatal(err)
		}
		statedb.AddBalance(sender, uint256.NewInt(1_000), tracing.BalanceChangeUnspecified)
		evm := vm.NewEVM(vm.BlockContext{
			CanTransfer: CanTransfer,
			Transfer:    Transfer,
			BlockNumber: number,
			GasLimit:    84_000_000,
			Difficulty:  big.NewInt(1),
		}, statedb, config, vm.Config{})
		gasPrice, _ := uint256.FromBig(price)
		msg := &Message{
			From:            sender,
			To:              &token,
			Value:           uint256.NewInt(10),
			GasLimit:        100_000,
			GasPrice:        gasPrice,
			GasFeeCap:       gasPrice,
			GasTipCap:       gasPrice,
			BalanceTokenFee: capacity,
		}
		res, err := ApplyMessage(evm, msg, NewGasPool(84_000_000), owner)
		return res, statedb, err
	}

	t.Run("capacity covers the gas", func(t *testing.T) {
		res, statedb, err := run(big.NewInt(1e18))
		if err != nil {
			t.Fatal(err)
		}
		if got := statedb.GetBalance(sender).Uint64(); got != 990 {
			t.Fatalf("sender balance = %d, want 990 (only the value is paid)", got)
		}
		want := new(big.Int).Mul(new(big.Int).SetUint64(res.UsedGas), price)
		if got := statedb.GetBalance(owner).ToBig(); got.Cmp(want) != 0 {
			t.Fatalf("owner credited %v, want gasUsed*price = %v", got, want)
		}
	})
	t.Run("capacity below gas limit * price", func(t *testing.T) {
		tooLow := new(big.Int).Sub(new(big.Int).Mul(big.NewInt(100_000), price), common.Big1)
		if _, _, err := run(tooLow); !errors.Is(err, ErrInsufficientFunds) {
			t.Fatalf("err = %v, want ErrInsufficientFunds", err)
		}
	})
}
