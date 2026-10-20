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

// EVM-level CALL tests for XDCx precompiles 0x1e / 0x28 / 0x29 / 0x2a.
//
// These tests call into the precompile addresses from Go (not from EVM bytecode)
// and assert exact gas consumed, the error flag, and the return bytes. They
// compile and run on both origin/main and this branch; the assertions are
// calibrated to pass only when the precompile is registered.
//
// Canonical reference: XDPoSChain v2.9.3-testnet core/vm/contracts.go.
// Active window (mainnet): [0, TIPXDCXMinerDisable=80,370,000).

package vm

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// xdcxPrecompileAddr returns the well-known XDCx precompile addresses by index:
//
//	0 = 0x1e (ringSignatureVerifier, 100,000 gas)
//	1 = 0x28 (bulletproofVerifier, 100,000 gas)
//	2 = 0x29 (XDCXLastPrice, 1 gas)
//	3 = 0x2a (XDCXEpochPrice, 1 gas)
var xdcxPrecompileAddrs = [4]common.Address{
	common.BytesToAddress([]byte{0x1e}),
	common.BytesToAddress([]byte{0x28}),
	common.BytesToAddress([]byte{0x29}),
	common.BytesToAddress([]byte{0x2a}),
}

// newXDCxEVM builds a minimal EVM for XDC mainnet at the given block number.
// The StateDB has a funded caller account; no accounts exist at the XDCx
// precompile addresses, so a call there on origin/main (where they are not
// registered as precompiles) hits an empty account and costs 0 gas.
func newXDCxEVM(blockNum *big.Int) (*EVM, *state.StateDB) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	caller := common.BytesToAddress([]byte("xdcx-test-caller"))
	statedb.AddBalance(caller, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
	statedb.Finalise(params.XDCMainnetChainConfig.Rules(blockNum, false, 0))

	ctx := BlockContext{
		CanTransfer: func(db StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db StateDB, sender, recipient common.Address, amount *uint256.Int, _ *params.Rules) {
			db.SubBalance(sender, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(recipient, amount, tracing.BalanceChangeTransfer)
		},
		BlockNumber: new(big.Int).Set(blockNum),
		Difficulty:  big.NewInt(1),
	}
	return NewEVM(ctx, statedb, params.XDCMainnetChainConfig, Config{}), statedb
}

// xdcxGasUsed computes how much of an initial GasBudget was consumed by a call.
// GasBudget.Used(initial) computes (initial.Exec+State) - (receiver.Exec+State),
// so the leftover is the receiver and the starting point is the argument.
func xdcxGasUsed(initial, leftOver GasBudget) uint64 {
	return leftOver.Used(initial)
}

// TestXDCxPricePrecompilesEVM verifies EVM-level CALL behavior for the two
// price precompiles (0x29 = XDCXLastPrice, 0x2a = XDCXEpochPrice) at a block
// inside [0, TIPXDCXMinerDisable=80,370,000).
//
// Canonical XDPoSChain: RequiredGas=1, returns 32 zero bytes.
//
// On origin/main without the fix: no precompile is registered at 0x29/0x2a.
// A call to a non-existent account with value=0 under EIP-158 returns
// immediately without consuming any execution gas (gas unchanged). Asserting
// gasUsed==1 therefore FAILS on main.
//
// On this branch: the precompile is registered; ChargeExecution(1) is called,
// Run returns 32 zero bytes, and gasUsed==1.
func TestXDCxPricePrecompilesEVM(t *testing.T) {
	// Block 50,000,000: IsXDC=true, IsByzantium/Istanbul/London=true,
	// IsEIP1559=false, TIPXDCXMinerDisable not yet reached.
	blockNum := big.NewInt(50_000_000)
	evm, _ := newXDCxEVM(blockNum)
	caller := common.BytesToAddress([]byte("xdcx-test-caller"))

	for _, tc := range []struct {
		name string
		addr common.Address
	}{
		{"XDCXLastPrice (0x29)", common.BytesToAddress([]byte{0x29})},
		{"XDCXEpochPrice (0x2a)", common.BytesToAddress([]byte{0x2a})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := NewGasBudget(200_000, 0)
			ret, leftOver, err := evm.Call(caller, tc.addr, nil, initial, new(uint256.Int))

			gasUsed := xdcxGasUsed(initial, leftOver)

			// Canonical: RequiredGas=1, Run returns 32 zero bytes, no error.
			// On main (no precompile): gasUsed=0, err=nil, ret=nil.
			if err != nil {
				t.Fatalf("%s: unexpected error: %v (on main this would be nil — different failure mode)", tc.name, err)
			}
			if gasUsed != 1 {
				t.Fatalf("%s: gasUsed=%d, want 1 (on main without fix: 0)", tc.name, gasUsed)
			}
			if len(ret) != 32 {
				t.Fatalf("%s: return length=%d, want 32 (on main: 0)", tc.name, len(ret))
			}
			if !bytes.Equal(ret, make([]byte, 32)) {
				t.Fatalf("%s: return not zero bytes: %x", tc.name, ret)
			}
		})
	}
}

// TestXDCxVerifierPrecompilesEVMRegistered verifies that the two verifier
// precompiles (0x1e = ringSignatureVerifier, 0x28 = bulletproofVerifier)
// are registered as precompiles in the [0, TIPXDCXMinerDisable) window on XDC.
//
// The test passes nil input, which triggers a deserialization error in both
// precompiles. The key behavioral difference is in gas:
//   - Branch: RequiredGas=100,000 is charged, then Run fails; the exceptional
//     halt burns all remaining execution gas (ExitHalt). Total gas consumed = 200,000.
//   - Main: no precompile, empty account, value=0, EIP-158 → returns immediately,
//     gas unchanged, gasUsed=0.
//
// The test asserts err != nil AND gasUsed > 0. On main gasUsed=0 → FAIL.
func TestXDCxVerifierPrecompilesEVMRegistered(t *testing.T) {
	blockNum := big.NewInt(50_000_000)
	evm, _ := newXDCxEVM(blockNum)
	caller := common.BytesToAddress([]byte("xdcx-test-caller"))

	for _, tc := range []struct {
		name        string
		addr        common.Address
		requiredGas uint64
	}{
		{"ringSignatureVerifier (0x1e)", common.BytesToAddress([]byte{0x1e}), 100_000},
		{"bulletproofVerifier (0x28)", common.BytesToAddress([]byte{0x28}), 100_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := NewGasBudget(200_000, 0)
			_, leftOver, err := evm.Call(caller, tc.addr, nil, initial, new(uint256.Int))

			gasUsed := xdcxGasUsed(initial, leftOver)

			// On main (no precompile): gasUsed=0, err=nil.
			// On branch: precompile registered, RequiredGas charged, Run fails on
			// nil input, ExitHalt burns all execution gas → gasUsed=200,000, err≠nil.
			if err == nil {
				t.Fatalf("%s: expected error (precompile rejects nil input), got nil — on main this is nil too (no precompile registered)", tc.name)
			}
			if gasUsed == 0 {
				t.Fatalf("%s: gasUsed=0, want >0 (precompile must charge RequiredGas=%d; on main gasUsed=0 because no precompile is registered)", tc.name, tc.requiredGas)
			}
		})
	}
}

// TestXDCxPrecompilesEVMPostMinerDisable verifies that all four XDCx precompile
// addresses behave as empty accounts at and after TIPXDCXMinerDisable (80,370,000
// on mainnet), on both origin/main and this branch.
//
// This is a regression guard: both sides should agree (gasUsed=0, no error,
// empty return) after the disable block. If a future change accidentally re-adds
// XDCx precompiles to a post-disable set, this test catches it on the branch.
func TestXDCxPrecompilesEVMPostMinerDisable(t *testing.T) {
	// Block 80,370,001: at and after TIPXDCXMinerDisable (80,370,000 mainnet).
	blockNum := big.NewInt(80_370_001)
	evm, _ := newXDCxEVM(blockNum)
	caller := common.BytesToAddress([]byte("xdcx-test-caller"))

	for _, addr := range xdcxPrecompileAddrs {
		t.Run(addr.Hex(), func(t *testing.T) {
			initial := NewGasBudget(200_000, 0)
			ret, leftOver, err := evm.Call(caller, addr, nil, initial, new(uint256.Int))

			gasUsed := xdcxGasUsed(initial, leftOver)

			// After MinerDisable, both main and branch treat these as empty accounts.
			if err != nil {
				t.Fatalf("addr %s post-disable: unexpected error: %v", addr.Hex(), err)
			}
			if gasUsed != 0 {
				t.Fatalf("addr %s post-disable: gasUsed=%d, want 0 (must be empty account after disable)", addr.Hex(), gasUsed)
			}
			if len(ret) != 0 {
				t.Fatalf("addr %s post-disable: return length=%d, want 0", addr.Hex(), len(ret))
			}
		})
	}
}
