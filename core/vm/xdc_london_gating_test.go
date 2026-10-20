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

// Tests for EIP-3541 (reject 0xEF code) gating on IsEIP1559 not IsLondon on
// XDC chains. Canonical XDPoSChain v2.9.3-testnet core/vm/evm.go:537-539.

package vm

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// xdcLondonPreEIP1559Block is a block in [London=76,321,000, EIP1559=98,800,200)
// on XDC mainnet: IsLondon=true, IsEIP1559=false.
var xdcLondonPreEIP1559Block = big.NewInt(80_000_000)

// xdcPostEIP1559Block is a block after EIP1559 on XDC mainnet.
var xdcPostEIP1559Block = new(big.Int).Add(params.XDCMainnetChainConfig.Eip1559Block, big.NewInt(1))

func newXDCTestEVM(statedb StateDB, blockNum *big.Int) *EVM {
	cfg := params.XDCMainnetChainConfig
	ctx := BlockContext{
		CanTransfer: func(db StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db StateDB, sender, recipient common.Address, amount *uint256.Int, _ *params.Rules) {
			db.SubBalance(sender, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(recipient, amount, tracing.BalanceChangeTransfer)
		},
		BlockNumber: blockNum,
		GasLimit:    1_000_000,
		Difficulty:  big.NewInt(1),
	}
	return NewEVM(ctx, statedb, cfg, Config{})
}

// deployEFInitcode is initcode that deploys a 1-byte contract starting with 0xEF.
// PUSH1 0xef; PUSH1 0x00; MSTORE8; PUSH1 0x01; PUSH1 0x00; RETURN
// Result runtime code: [0xef]
var deployEFInitcode = []byte{0x60, 0xef, 0x60, 0x00, 0x53, 0x60, 0x01, 0x60, 0x00, 0xf3}

// TestXDCEIP3541GatingPreEIP1559 verifies that deploying code starting with 0xEF
// succeeds in the [London, EIP1559) window on XDC mainnet, matching canonical
// XDPoSChain (EIP-3541 is gated on IsEIP1559, not IsLondon).
//
// On origin/main without the fix: Create fails with ErrInvalidCode because
// IsLondon=true activates the EF-code rejection too early.
// After the fix: Create succeeds because IsEIP1559=false in this window.
func TestXDCEIP3541GatingPreEIP1559(t *testing.T) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	deployer := common.HexToAddress("0xdeadbeef")
	statedb.CreateAccount(deployer)
	statedb.AddBalance(deployer, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
	statedb.Finalise(params.Rules{IsEIP158: true})

	evm := newXDCTestEVM(statedb, xdcLondonPreEIP1559Block)
	_, _, _, err := evm.Create(
		deployer,
		deployEFInitcode,
		NewGasBudget(500_000, 0),
		new(uint256.Int),
	)
	// Canonical: deployment succeeds in the [London, EIP1559) window.
	if err != nil {
		t.Fatalf("pre-EIP1559 block: Create with 0xEF initcode got err=%v, want nil (fix not applied?)", err)
	}
}

// TestXDCEIP3541GatingPostEIP1559 verifies that deploying code starting with 0xEF
// fails after the EIP-1559 block on XDC mainnet (EIP-3541 is now active).
func TestXDCEIP3541GatingPostEIP1559(t *testing.T) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	deployer := common.HexToAddress("0xdeadbeef")
	statedb.CreateAccount(deployer)
	statedb.AddBalance(deployer, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
	statedb.Finalise(params.Rules{IsEIP158: true})

	evm := newXDCTestEVM(statedb, xdcPostEIP1559Block)
	_, _, _, err := evm.Create(
		deployer,
		deployEFInitcode,
		NewGasBudget(500_000, 0),
		new(uint256.Int),
	)
	if err != ErrInvalidCode {
		t.Fatalf("post-EIP1559 block: Create with 0xEF initcode got err=%v, want ErrInvalidCode", err)
	}
}
