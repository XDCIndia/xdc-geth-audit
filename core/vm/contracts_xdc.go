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
	return nil
}
