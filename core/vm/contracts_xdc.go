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

package vm

import (
	"errors"
	"maps"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// XDC: XDC has no blob ecosystem and does not implement the EIP-4844 point
// evaluation precompile at 0x0a (XDPoSChain v2.9.2-testnet, core/vm/contracts.go).
//
//   - Cancun (mainnet from 98,802,000, Apothem from 71,551,800): XDPoSChain keeps
//     its EIP1559 set, which has no 0x0a at all. A call there is a call to an
//     empty account (succeeds, cold access), so 0x0a must be neither registered
//     nor warmed.
//   - Prague and Osaka: 0x0a holds an always-failing stub priced like the real
//     precompile, so a call burns the frame instead of faking success.
//
// Each set is its own variable: the pointer is the set's identity in the
// precompile result cache.
var (
	PrecompiledContractsXDCCancun PrecompiledContracts
	PrecompiledContractsXDCPrague PrecompiledContracts
	PrecompiledContractsXDCOsaka  PrecompiledContracts

	PrecompiledAddressesXDCCancun []common.Address
	PrecompiledAddressesXDCPrague []common.Address
	PrecompiledAddressesXDCOsaka  []common.Address
)

var kzgPointEvaluationAddress = common.BytesToAddress([]byte{0x0a})

// xdcKZGStub mirrors XDPoSChain's kzgStub: it costs the point evaluation gas
// and fails on every input with a plain error, so evm.Call consumes all gas.
var xdcKZGStub = &unsupportedPrecompile{
	name: "KZG_POINT_EVALUATION_UNSUPPORTED",
	gas:  params.BlobTxPointEvaluationPrecompileGas,
	err:  errors.New("kzg point evaluation is not supported on XDC"),
}

type unsupportedPrecompile struct {
	name string
	gas  uint64
	err  error
}

func (c *unsupportedPrecompile) RequiredGas(input []byte) uint64  { return c.gas }
func (c *unsupportedPrecompile) Run(input []byte) ([]byte, error) { return nil, c.err }
func (c *unsupportedPrecompile) Name() string                     { return c.name }

func init() {
	PrecompiledContractsXDCCancun = maps.Clone(PrecompiledContractsCancun)
	delete(PrecompiledContractsXDCCancun, kzgPointEvaluationAddress)

	PrecompiledContractsXDCPrague = maps.Clone(PrecompiledContractsPrague)
	PrecompiledContractsXDCPrague[kzgPointEvaluationAddress] = xdcKZGStub

	PrecompiledContractsXDCOsaka = maps.Clone(PrecompiledContractsOsaka)
	PrecompiledContractsXDCOsaka[kzgPointEvaluationAddress] = xdcKZGStub

	for k := range PrecompiledContractsXDCCancun {
		PrecompiledAddressesXDCCancun = append(PrecompiledAddressesXDCCancun, k)
	}
	for k := range PrecompiledContractsXDCPrague {
		PrecompiledAddressesXDCPrague = append(PrecompiledAddressesXDCPrague, k)
	}
	for k := range PrecompiledContractsXDCOsaka {
		PrecompiledAddressesXDCOsaka = append(PrecompiledAddressesXDCOsaka, k)
	}
}

// xdcPrecompiledContracts returns the XDC set for the Cancun-and-later rules,
// or nil when upstream's selection applies unchanged.
func xdcPrecompiledContracts(rules params.Rules) *PrecompiledContracts {
	if !rules.IsXDC {
		return nil
	}
	switch {
	case rules.IsOsaka:
		return &PrecompiledContractsXDCOsaka
	case rules.IsPrague:
		return &PrecompiledContractsXDCPrague
	case rules.IsCancun:
		return &PrecompiledContractsXDCCancun
	}
	return nil
}

// xdcPrecompiles is the address-list counterpart of xdcPrecompiledContracts,
// so the access list warms exactly the addresses the EVM treats as precompiles.
func xdcPrecompiles(rules params.Rules) []common.Address {
	if !rules.IsXDC {
		return nil
	}
	switch {
	case rules.IsOsaka:
		return PrecompiledAddressesXDCOsaka
	case rules.IsPrague:
		return PrecompiledAddressesXDCPrague
	case rules.IsCancun:
		return PrecompiledAddressesXDCCancun
	}
	// XDCx precompiles (0x1e, 0x28, 0x29, 0x2a) are only active before
	// TIPXDCXMinerDisable. EIP-2929 access-list warming is first active at
	// EIP1559 on XDC, by which point XDCx is already disabled. So there is
	// no period where both access-list warming AND XDCx precompiles overlap,
	// and the XDCx addresses never need to appear in this list.
	return nil
}

// XDCx precompile addresses.
// Active on XDC chains for blocks in [4, TIPXDCXMinerDisable).
// Canonical XDPoSChain v2.9.3-testnet core/vm/contracts.go.
var (
	xdcxRingVerifierAddress = common.BytesToAddress([]byte{0x1e})
	xdcxBulletproofAddress  = common.BytesToAddress([]byte{0x28})
	xdcxLastPriceAddress    = common.BytesToAddress([]byte{0x29})
	xdcxEpochPriceAddress   = common.BytesToAddress([]byte{0x2a})
)

// xdcxLastPrice is precompile 0x29 (XDCXLastPrice). Charges 1 gas and returns
// 32 zero bytes. Canonical XDPoSChain reads last traded price from XDCx trading
// state; on nodes without that state it also returns 32 zero bytes. This
// stateless form matches that fallback.
// Canonical: core/vm/contracts.go XDCXLastPrice.Run, v2.9.3-testnet.
type xdcxLastPrice struct{}

func (c *xdcxLastPrice) RequiredGas(input []byte) uint64  { return 1 }
func (c *xdcxLastPrice) Run(input []byte) ([]byte, error) { return make([]byte, 32), nil }
func (c *xdcxLastPrice) Name() string                     { return "XDCX_LAST_PRICE" }

// xdcxEpochPrice is precompile 0x2a (XDCXEpochPrice). Same gas and return value
// as xdcxLastPrice; reads the epoch settlement price in canonical XDPoSChain.
// Canonical: core/vm/contracts.go XDCXEpochPrice.Run, v2.9.3-testnet.
type xdcxEpochPrice struct{}

func (c *xdcxEpochPrice) RequiredGas(input []byte) uint64  { return 1 }
func (c *xdcxEpochPrice) Run(input []byte) ([]byte, error) { return make([]byte, 32), nil }
func (c *xdcxEpochPrice) Name() string                     { return "XDCX_EPOCH_PRICE" }

// xdcxExtras is the set of four XDCx precompile entries that augment the
// standard sets for XDC blocks before TIPXDCXMinerDisable.
var xdcxExtras = PrecompiledContracts{
	xdcxRingVerifierAddress: &ringSignatureVerifier{},
	xdcxBulletproofAddress:  &bulletproofVerifier{},
	xdcxLastPriceAddress:    &xdcxLastPrice{},
	xdcxEpochPriceAddress:   &xdcxEpochPrice{},
}

// XDC-specific precompile sets for the blocks before TIPXDCXMinerDisable.
// Each is the corresponding standard set plus the four XDCx entries.
// Keeping them as separate variables lets the precompile result cache
// distinguish between them.
var (
	// PrecompiledContractsXDCByzantiumXDCx is used for XDC blocks in
	// [0, Istanbul) that are also before TIPXDCXMinerDisable.
	// On all current XDC chains Istanbul=0, so this set is unreachable
	// in practice but included for completeness.
	PrecompiledContractsXDCByzantiumXDCx PrecompiledContracts

	// PrecompiledContractsXDCIstanbulXDCx is used for XDC blocks in
	// [Istanbul, Berlin) that are also before TIPXDCXMinerDisable.
	// On XDC mainnet Istanbul=0 and Berlin=76,321,000, so this covers
	// blocks [0, 76,321,000).
	PrecompiledContractsXDCIstanbulXDCx PrecompiledContracts

	// PrecompiledContractsXDCBerlinXDCx is used for XDC blocks in
	// [Berlin, TIPXDCXMinerDisable). ModExp uses pre-EIP-2565 pricing
	// because XDC defers EIP-2565 to Eip1559Block (issue #716).
	// On XDC mainnet this covers blocks [76,321,000, 80,370,000).
	PrecompiledContractsXDCBerlinXDCx PrecompiledContracts
)

func init() {
	// Build the XDCx-augmented sets by cloning the base sets and adding
	// the four XDCx precompile entries.
	merge := func(base PrecompiledContracts) PrecompiledContracts {
		m := maps.Clone(base)
		for k, v := range xdcxExtras {
			m[k] = v
		}
		return m
	}
	PrecompiledContractsXDCByzantiumXDCx = merge(PrecompiledContractsByzantium)
	PrecompiledContractsXDCIstanbulXDCx = merge(PrecompiledContractsIstanbul)
	// XDCBerlin has eip2565:false; merge that, not the upstream Berlin.
	PrecompiledContractsXDCBerlinXDCx = merge(PrecompiledContractsXDCBerlin)
}
