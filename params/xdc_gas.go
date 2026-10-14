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

package params

import "math/big"

// XDC gas price schedule, ported from XinFinOrg/XDPoSChain v2.9.1-testnet
// params/gas.go (#2516, #2695). One tier price is shared by the chain-default
// gas price and the EIP-1559 header base fee:
//
//	XDCBaseFee (12.5 gwei)        the Gas50x tier; every XDC EIP-1559 header so far
//	XDCBaseFee * 50 (625 gwei)    the Gas2500x tier (2500 x the 0.25 gwei baseline)
//
// The base fee is a pure function of the block number: it does not depend on
// the parent's gas usage.

// gas2500xMultiplier scales the Gas50x tier (XDCBaseFee) to the Gas2500x tier.
const gas2500xMultiplier = 50

// XDCBaseFeeForBlock returns the EIP-1559 base fee of the XDC block with the
// given number. It does not check Eip1559Block; callers must do so.
func XDCBaseFeeForBlock(c *ChainConfig, number *big.Int) *big.Int {
	fee := new(big.Int).SetUint64(XDCBaseFee)
	if c.IsGas2500x(number) {
		fee.Mul(fee, big.NewInt(gas2500xMultiplier))
	}
	return fee
}

// XDCBaseFeeForOpcode is what the BASEFEE opcode reports on an XDC block whose
// header carries no base fee: the London to EIP-1559 window, where the opcode is
// live but the header field is not. It stays pinned to the Gas50x tier so that a
// later tier fork cannot rewrite what those already executed blocks saw.
func XDCBaseFeeForOpcode() *big.Int {
	return new(big.Int).SetUint64(XDCBaseFee)
}
