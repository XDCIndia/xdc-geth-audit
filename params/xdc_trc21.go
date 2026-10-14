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

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// TRC21GasPriceForBlock is the gas price of a TRC21 token-fee transaction (a
// transaction to a token registered in common.TRC21IssuerSMC) at the given
// block: it replaces the transaction's own gas price. Mirrors XDPoSChain
// tx.AsMessage (v2.7.1) and params.GetGasPriceForTRC21 (v2.9.1-testnet), which
// share the gas price tier with the EIP-1559 base fee:
//
//	Gas2500x             625 gwei (the Gas2500x tier)
//	>= BlockNumberGas50x 12.5 gwei
//	>  TIPTRC21Fee       0.25 gwei (common.TRC21GasPrice)
//	otherwise            2500 wei  (common.TRC21GasPriceBefore)
func TRC21GasPriceForBlock(c *ChainConfig, number *big.Int) *big.Int {
	switch {
	case c.IsGas2500x(number):
		return XDCBaseFeeForBlock(c, number)
	case number.Cmp(common.BlockNumberGas50x) >= 0:
		return new(big.Int).Set(common.GasPrice50x)
	case number.Cmp(common.TIPTRC21Fee) > 0:
		return new(big.Int).Set(common.TRC21GasPrice)
	default:
		return new(big.Int).Set(common.TRC21GasPriceBefore)
	}
}

// TRC21GasFee is what a TRC21 token-fee transaction that used gas takes from
// its token's fee capacity (XDPoSChain common.GetGasFee).
func TRC21GasFee(c *ChainConfig, number *big.Int, gas uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(gas), TRC21GasPriceForBlock(c, number))
}
