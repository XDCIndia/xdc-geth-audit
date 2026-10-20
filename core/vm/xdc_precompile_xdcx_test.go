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

// Tests for the four XDCx precompiles (0x1e, 0x28, 0x29, 0x2a) that are active
// on XDC chains for blocks in [4, TIPXDCXMinerDisable).
// Canonical XDPoSChain v2.9.3-testnet core/vm/contracts.go.

package vm

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// xdcxRules builds a params.Rules for XDC with Berlin active and the
// IsXDCxMinerDisable flag set as requested.
func xdcxRules(minerDisabled bool) params.Rules {
	return params.Rules{
		IsXDC:              true,
		IsXDCxMinerDisable: minerDisabled,
		IsByzantium:        true,
		IsIstanbul:         true,
		IsBerlin:           true,
	}
}

// TestXDCxPrecompilesPresent verifies that the four XDCx precompile addresses
// are registered in the active set before TIPXDCXMinerDisable (the window where
// canonical XDPoSChain enables them).
//
// On origin/main without the fix: activePrecompiledContracts returns
// PrecompiledContractsXDCBerlin for XDC+Berlin blocks, which contains only
// 0x01–0x09. Looking up 0x1e returns nil, and this test fails.
func TestXDCxPrecompilesPresent(t *testing.T) {
	rules := xdcxRules(false) // before TIPXDCXMinerDisable
	active := ActivePrecompiledContracts(rules)

	for _, addr := range []common.Address{
		xdcxRingVerifierAddress, // 0x1e
		xdcxBulletproofAddress,  // 0x28
		xdcxLastPriceAddress,    // 0x29
		xdcxEpochPriceAddress,   // 0x2a
	} {
		if _, ok := active[addr]; !ok {
			t.Errorf("XDCx precompile %s missing from active set before TIPXDCXMinerDisable", addr.Hex())
		}
	}
}

// TestXDCxPrecompilesAbsentAfterMinerDisable verifies that the four XDCx
// precompile addresses are removed from the active set at and after
// TIPXDCXMinerDisable, matching canonical XDPoSChain behaviour.
func TestXDCxPrecompilesAbsentAfterMinerDisable(t *testing.T) {
	rules := xdcxRules(true) // at or after TIPXDCXMinerDisable
	active := ActivePrecompiledContracts(rules)

	for _, addr := range []common.Address{
		xdcxRingVerifierAddress, // 0x1e
		xdcxBulletproofAddress,  // 0x28
		xdcxLastPriceAddress,    // 0x29
		xdcxEpochPriceAddress,   // 0x2a
	} {
		if _, ok := active[addr]; ok {
			t.Errorf("XDCx precompile %s unexpectedly present after TIPXDCXMinerDisable", addr.Hex())
		}
	}
}

// TestXDCxLastPriceStateless verifies that the 0x29 precompile (XDCXLastPrice)
// charges 1 gas and returns 32 zero bytes regardless of input. Canonical
// XDPoSChain core/vm/contracts.go XDCXLastPrice.Run returns 32 zero bytes when
// no trading state is attached.
func TestXDCxLastPriceStateless(t *testing.T) {
	c := &xdcxLastPrice{}

	if g := c.RequiredGas(nil); g != 1 {
		t.Fatalf("RequiredGas = %d, want 1", g)
	}
	out, err := c.Run([]byte("any input"))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(out) != 32 {
		t.Fatalf("output length = %d, want 32", len(out))
	}
	if !bytes.Equal(out, make([]byte, 32)) {
		t.Fatalf("output not zero bytes: %x", out)
	}
}

// TestXDCxEpochPriceStateless mirrors TestXDCxLastPriceStateless for the
// 0x2a precompile (XDCXEpochPrice).
func TestXDCxEpochPriceStateless(t *testing.T) {
	c := &xdcxEpochPrice{}

	if g := c.RequiredGas(nil); g != 1 {
		t.Fatalf("RequiredGas = %d, want 1", g)
	}
	out, err := c.Run(nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(out) != 32 {
		t.Fatalf("output length = %d, want 32", len(out))
	}
	if !bytes.Equal(out, make([]byte, 32)) {
		t.Fatalf("output not zero bytes: %x", out)
	}
}

// TestXDCxPrecompilesIstanbulWindow verifies that XDCx precompiles are also
// registered in the Istanbul (pre-Berlin) window on XDC chains.
func TestXDCxPrecompilesIstanbulWindow(t *testing.T) {
	// Istanbul but not Berlin, XDCx not yet disabled.
	rules := params.Rules{
		IsXDC:              true,
		IsXDCxMinerDisable: false,
		IsByzantium:        true,
		IsIstanbul:         true,
		IsBerlin:           false,
	}
	active := ActivePrecompiledContracts(rules)

	for _, addr := range []common.Address{
		xdcxRingVerifierAddress,
		xdcxBulletproofAddress,
		xdcxLastPriceAddress,
		xdcxEpochPriceAddress,
	} {
		if _, ok := active[addr]; !ok {
			t.Errorf("XDCx precompile %s missing from Istanbul-window active set", addr.Hex())
		}
	}
}
