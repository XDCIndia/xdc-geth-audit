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

// TestXDCFeeRecipient pins who receives the transaction fee on the consensus
// path: after TIPTRC21Fee the coinbase owner gets gasUsed*gasPrice, otherwise
// the block coinbase gets gasUsed*tip (the tip is the full price before the
// EIP-1559 block, where BaseFee is nil).
func TestXDCFeeRecipient(t *testing.T) {
	var (
		config   = params.XDCMainnetChainConfig
		sender   = common.HexToAddress("0x5555555555555555555555555555555555555555")
		to       = common.HexToAddress("0x6666666666666666666666666666666666666666")
		owner    = common.HexToAddress("0x7777777777777777777777777777777777777777")
		coinbase = common.HexToAddress("0x8888888888888888888888888888888888888888")
		price    = uint256.NewInt(params.GWei)
	)
	run := func(number *big.Int, owners ...common.Address) (*ExecutionResult, *state.StateDB) {
		statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		if err != nil {
			t.Fatal(err)
		}
		statedb.AddBalance(sender, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
		evm := vm.NewEVM(vm.BlockContext{
			CanTransfer: CanTransfer,
			Transfer:    Transfer,
			Coinbase:    coinbase,
			BlockNumber: number,
			GasLimit:    84_000_000,
			Difficulty:  big.NewInt(1),
		}, statedb, config, vm.Config{})
		msg := &Message{
			From:      sender,
			To:        &to,
			Value:     uint256.NewInt(1),
			GasLimit:  21_000,
			GasPrice:  price,
			GasFeeCap: price,
			GasTipCap: price,
		}
		res, err := ApplyMessage(evm, msg, NewGasPool(84_000_000), owners...)
		if err != nil {
			t.Fatal(err)
		}
		return res, statedb
	}
	fee := func(res *ExecutionResult) *uint256.Int {
		return new(uint256.Int).Mul(uint256.NewInt(res.UsedGas), price)
	}
	afterTIP := new(big.Int).Add(common.TIPTRC21Fee, common.Big1)

	t.Run("after TIPTRC21Fee with owner", func(t *testing.T) {
		res, statedb := run(afterTIP, owner)
		if got := statedb.GetBalance(owner); !got.Eq(fee(res)) {
			t.Fatalf("owner got %v, want %v", got, fee(res))
		}
		if got := statedb.GetBalance(coinbase); !got.IsZero() {
			t.Fatalf("coinbase got %v, want 0", got)
		}
	})
	t.Run("at TIPTRC21Fee with owner", func(t *testing.T) {
		res, statedb := run(new(big.Int).Set(common.TIPTRC21Fee), owner)
		if got := statedb.GetBalance(coinbase); !got.Eq(fee(res)) {
			t.Fatalf("coinbase got %v, want %v", got, fee(res))
		}
		if got := statedb.GetBalance(owner); !got.IsZero() {
			t.Fatalf("owner got %v, want 0", got)
		}
	})
	t.Run("after TIPTRC21Fee without owner", func(t *testing.T) {
		// Canonical (XDPoSChain v2.9.2 core/state_transition.go:507-510)
		// credits only the owner after TIPTRC21Fee; with none, nothing.
		_, statedb := run(afterTIP)
		if got := statedb.GetBalance(coinbase); !got.IsZero() {
			t.Fatalf("coinbase got %v, want 0", got)
		}
	})
}

// TestXDCSpecialTxNoFeeCredit pins that special transactions (0x89/0x90) credit
// no fee once EIP-1559 is active, matching XDPoSChain v2.9.2
// core/state_transition.go:505. A 0-price special tx would otherwise compute
// effectiveTip = 0 - baseFee, which wraps in uint256 (issue #1455).
func TestXDCSpecialTxNoFeeCredit(t *testing.T) {
	var (
		config   = params.XDCMainnetChainConfig
		sender   = common.HexToAddress("0x5555555555555555555555555555555555555555")
		normal   = common.HexToAddress("0x6666666666666666666666666666666666666666")
		owner    = common.HexToAddress("0x7777777777777777777777777777777777777777")
		coinbase = common.HexToAddress("0x8888888888888888888888888888888888888888")
		baseFee  = big.NewInt(12_500_000_000) // 12.5 gwei
		number   = new(big.Int).Add(config.Eip1559Block, common.Big1)
	)
	run := func(to common.Address, price *uint256.Int, owners ...common.Address) (*ExecutionResult, *state.StateDB) {
		statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		if err != nil {
			t.Fatal(err)
		}
		statedb.AddBalance(sender, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
		evm := vm.NewEVM(vm.BlockContext{
			CanTransfer: CanTransfer,
			Transfer:    Transfer,
			Coinbase:    coinbase,
			BlockNumber: number,
			BaseFee:     baseFee,
			GasLimit:    84_000_000,
			Difficulty:  big.NewInt(1),
		}, statedb, config, vm.Config{})
		if !evm.ChainConfig().IsLondon(number) {
			t.Fatalf("London not active at %v", number)
		}
		msg := &Message{
			From:      sender,
			To:        &to,
			Value:     new(uint256.Int),
			GasLimit:  100_000,
			GasPrice:  price,
			GasFeeCap: price,
			GasTipCap: price,
		}
		res, err := ApplyMessage(evm, msg, NewGasPool(84_000_000), owners...)
		if err != nil {
			t.Fatal(err)
		}
		return res, statedb
	}
	for _, special := range []common.Address{common.BlockSignersBinary, common.RandomizeSMCBinary} {
		for _, owners := range [][]common.Address{nil, {owner}} {
			_, statedb := run(special, new(uint256.Int), owners...)
			for _, addr := range []common.Address{coinbase, owner} {
				if got := statedb.GetBalance(addr); !got.IsZero() {
					t.Errorf("special tx to %v (owners %v): %v balance %v, want 0", special, owners, addr, got)
				}
				if statedb.Exist(addr) {
					t.Errorf("special tx to %v (owners %v): %v touched", special, owners, addr)
				}
			}
		}
	}
	// A normal priced tx is still credited. This block is past TIPTRC21Fee, so
	// canonical credits the coinbase owner the full gasUsed*price, and nothing
	// at all when no owner is registered (XDPoSChain v2.9.2
	// core/state_transition.go:507-510).
	price := uint256.NewInt(20_000_000_000)
	res, statedb := run(normal, price, owner)
	want := new(uint256.Int).Mul(uint256.NewInt(res.UsedGas), price)
	if got := statedb.GetBalance(owner); !got.Eq(want) {
		t.Fatalf("normal tx: owner got %v, want %v", got, want)
	}
	if got := statedb.GetBalance(coinbase); !got.IsZero() {
		t.Fatalf("normal tx: coinbase got %v, want 0", got)
	}
	_, statedb = run(normal, price)
	if got := statedb.GetBalance(coinbase); !got.IsZero() {
		t.Fatalf("normal tx without owner: coinbase got %v, want 0", got)
	}
}
