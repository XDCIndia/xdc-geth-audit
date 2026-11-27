// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-45 (#2098): tests for the historical balance rule ported from
// XDPoSChain core/state_processor_historical_bypass_test.go, plus one test that
// goes through ApplyTransactionWithEVM.

package core

import (
	"context"
	"math/big"
	"sort"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func newHistoricalBypassStateDB(t *testing.T) *state.StateDB {
	t.Helper()
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatalf("failed to create state db: %v", err)
	}
	return statedb
}

func TestHistoricalBalanceBypassApply(t *testing.T) {
	if len(historicalBalanceBypassByBlock) != 60 {
		t.Fatalf("table has %d rules, canonical has 60", len(historicalBalanceBypassByBlock))
	}
	blocks := make([]uint64, 0, len(historicalBalanceBypassByBlock))
	for block := range historicalBalanceBypassByBlock {
		blocks = append(blocks, block)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i] < blocks[j] })

	for _, block := range blocks {
		t.Run(new(big.Int).SetUint64(block).String(), func(t *testing.T) {
			statedb := newHistoricalBypassStateDB(t)
			rule := historicalBalanceBypassByBlock[block]

			statedb.SetBalance(rule.addr, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
			applyHistoricalBalanceBypass(statedb, new(big.Int).SetUint64(block), rule.addr)

			if have := statedb.GetBalance(rule.addr).ToBig(); have.Cmp(rule.balance) != 0 {
				t.Fatalf("wrong balance after bypass: have %v want %v", have, rule.balance)
			}
		})
	}
}

func TestHistoricalBalanceBypassSkip(t *testing.T) {
	first := historicalBalanceBypassByBlock[9073579].addr
	tests := []struct {
		name        string
		blockNumber *big.Int
		from        common.Address
	}{
		{"nil block number", nil, first},
		{"block above max", new(big.Int).SetUint64(maxHistoricalBalanceBypassBlock + 1), first},
		{"block without rule", big.NewInt(1), first},
		{"address mismatch", big.NewInt(9073579), common.HexToAddress("0x1111111111111111111111111111111111111111")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statedb := newHistoricalBypassStateDB(t)
			statedb.SetBalance(first, uint256.NewInt(7), tracing.BalanceChangeUnspecified)
			applyHistoricalBalanceBypass(statedb, tt.blockNumber, tt.from)
			if have := statedb.GetBalance(first); !have.Eq(uint256.NewInt(7)) {
				t.Fatalf("balance changed unexpectedly: have %v want 7", have)
			}
		})
	}
}

// TestHistoricalBalanceRuleOnTransaction: at mainnet block 9,073,579 the rule
// sender has 1 XDC from normal execution and sends 20,000 XDC. Without the rule
// the tx fails with insufficient funds (the block would be rejected); with it the
// sender holds the table balance first, as on canonical.
func TestHistoricalBalanceRuleOnTransaction(t *testing.T) {
	var (
		config = params.XDCMainnetChainConfig
		number = big.NewInt(9073579)
		rule   = historicalBalanceBypassByBlock[9073579]
		to     = common.HexToAddress("0x6666666666666666666666666666666666666666")
		price  = uint256.NewInt(params.GWei)
		value  = uint256.MustFromBig(toXdcAmount(20000))
	)
	statedb := newHistoricalBypassStateDB(t)
	statedb.SetBalance(rule.addr, uint256.MustFromBig(toXdcAmount(1)), tracing.BalanceChangeUnspecified)
	evm := vm.NewEVM(vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		BlockNumber: number,
		GasLimit:    84_000_000,
		Difficulty:  big.NewInt(1),
	}, statedb, config, vm.Config{})
	msg := &Message{From: rule.addr, To: &to, Value: value, GasLimit: 21_000, GasPrice: price, GasFeeCap: price, GasTipCap: price}
	tx := types.NewTx(&types.LegacyTx{To: &to, Gas: 21_000, GasPrice: price.ToBig(), Value: value.ToBig()})

	receipt, _, err := ApplyTransactionWithEVM(context.Background(), msg, NewGasPool(84_000_000), statedb, number, common.Hash{1}, 0, tx, evm)
	if err != nil {
		t.Fatalf("tx at block 9073579 rejected: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("receipt status %d", receipt.Status)
	}
	want := new(big.Int).Sub(rule.balance, value.ToBig())
	want.Sub(want, new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), price.ToBig()))
	if have := statedb.GetBalance(rule.addr).ToBig(); have.Cmp(want) != 0 {
		t.Fatalf("sender balance %v, want %v", have, want)
	}
}
