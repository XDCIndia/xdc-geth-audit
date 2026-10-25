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

// Tests for EIP-3529 refund-cap gating on IsEIP1559 not IsLondon on XDC chains.
// Canonical XDPoSChain v2.9.3-testnet core/state_transition.go:594-603.

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

// TestXDCRefundCapGating verifies that in the [London, EIP1559) window on XDC
// mainnet, the storage-clear refund is capped at gasUsed/2 (RefundQuotient=2),
// not at gasUsed/5 (RefundQuotientEIP3529=5).
//
// Canonical XDPoSChain v2.9.3-testnet gates the /5 cap on IsEIP1559, not
// IsLondon. On XDC mainnet London=76,321,000 and EIP1559=98,800,200, so the
// window [76,321,000, 98,800,200) must use /2.
//
// On origin/main without the fix: the /5 cap applies from IsLondon=76,321,000,
// so gasUsed is higher than canonical for any tx that clears a storage slot
// with sufficient gas available.
// After the fix: the /2 cap applies until EIP1559, matching canonical.
func TestXDCRefundCapGating(t *testing.T) {
	var (
		config = params.XDCMainnetChainConfig
		// Block in [London=76,321,000, EIP1559=98,800,200): IsLondon=true, IsEIP1559=false.
		blockNum = big.NewInt(80_000_000)
		sender   = common.HexToAddress("0x1111111111111111111111111111111111111111")
		coinbase = common.HexToAddress("0x2222222222222222222222222222222222222222")
		gasPrice = uint256.NewInt(1) // minimal gas price
	)

	// Contract that clears one storage slot (SSTORE slot 0 = 0):
	//   PUSH1 0x00; PUSH1 0x00; SSTORE; STOP
	// At Istanbul SSTORE prices (EIP-2929 NOT active in this window):
	//   clearing a nonzero slot costs 5000 gas and adds 15000 to the refund counter.
	contractCode := []byte{0x60, 0x00, 0x60, 0x00, 0x55, 0x00}
	contractAddr := common.HexToAddress("0x3333333333333333333333333333333333333333")

	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	// Fund the sender and deploy the contract with slot 0 set to nonzero.
	statedb.AddBalance(sender, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
	statedb.CreateAccount(contractAddr)
	statedb.SetCode(contractAddr, contractCode, tracing.CodeChangeUnspecified)
	statedb.SetState(contractAddr, common.Hash{}, common.BigToHash(big.NewInt(1))) // slot 0 = 1

	rules := config.Rules(blockNum, false, 0)
	statedb.Finalise(rules)

	evm := vm.NewEVM(vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		Coinbase:    coinbase,
		BlockNumber: blockNum,
		GasLimit:    84_000_000,
		Difficulty:  big.NewInt(1),
	}, statedb, config, vm.Config{})

	gas := uint64(50_000)
	msg := &Message{
		From:      sender,
		To:        &contractAddr,
		Value:     new(uint256.Int),
		GasLimit:  gas,
		GasPrice:  gasPrice,
		GasFeeCap: gasPrice,
		GasTipCap: gasPrice,
	}
	res, err := ApplyMessage(evm, msg, NewGasPool(84_000_000))
	if err != nil {
		t.Fatal(err)
	}

	// Gas breakdown at block 80,000,000 (xdcLondonInstructionSet, no EIP-2929):
	//   intrinsic:  21,000
	//   PUSH1×2:        6
	//   SSTORE clear: 5,000 (Istanbul, slot current=1→0, refund += 15,000)
	//   STOP:           0
	//   subtotal:  26,006
	//   refund counter = 15,000
	//
	// Canonical (/2 cap): refund = min(15000, 26006/2) = min(15000, 13003) = 13003
	//   gasUsed = 26006 - 13003 = 13003
	//
	// Old behaviour (/5 cap): refund = min(15000, 26006/5) = min(15000, 5201) = 5201
	//   gasUsed = 26006 - 5201 = 20805

	gasBeforeRefund := uint64(26_006)
	wantRefund := gasBeforeRefund / params.RefundQuotient // /2 = 13003
	wantUsed := gasBeforeRefund - wantRefund              // 13003

	if res.UsedGas != wantUsed {
		// Detect the old /5 behaviour explicitly for a clearer failure message.
		oldRefund := gasBeforeRefund / params.RefundQuotientEIP3529 // /5 = 5201
		oldUsed := gasBeforeRefund - oldRefund                      // 20805
		if res.UsedGas == oldUsed {
			t.Fatalf("gasUsed=%d matches old /5 cap (fix not applied); want %d (/2 cap, canonical)", res.UsedGas, wantUsed)
		}
		t.Fatalf("gasUsed=%d, want %d (canonical /2 cap)", res.UsedGas, wantUsed)
	}
}
