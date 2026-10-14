// Copyright 2021 The go-ethereum Authors
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

package eip1559

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// FeeMarketActive reports whether the EIP-1559 fee market is in force at num on
// the given chain — i.e. whether a header at num must carry a BaseFee.
//
// For XDC chains the gate is IsEIP1559, because XDPoSChain splits the London EVM
// upgrade from the fee market: LondonBlock enables the London opcodes/EIP-3529,
// while Eip1559Block — which may be far later, or absent entirely on a private
// net — is what introduces header.BaseFee. For upstream chains the two coincide,
// so the gate is IsLondon.
//
// Block producers MUST use this rather than IsLondon when deciding whether to
// stamp header.BaseFee: on an XDC chain with London active but Eip1559Block nil
// or later, an IsLondon-gated producer emits a BaseFee that every other client on
// the net rejects ("invalid baseFee: have <n>, want <nil>"). Refs ethOne#62.
func FeeMarketActive(config *params.ChainConfig, num *big.Int) bool {
	if config.XDPoS != nil {
		return config.IsEIP1559(num)
	}
	return config.IsLondon(num)
}

// feeMarketActive is the unexported spelling used within this package.
func feeMarketActive(config *params.ChainConfig, num *big.Int) bool {
	return FeeMarketActive(config, num)
}

// VerifyEIP1559Header verifies some header attributes which were changed in EIP-1559,
// - gas limit check
// - basefee check
func VerifyEIP1559Header(config *params.ChainConfig, parent, header *types.Header) error {
	// XDC: if EIP-1559 is not active for this block, the header MUST NOT carry
	// a BaseFee. Upstream chains short-circuit before reaching this function
	// (callers only invoke VerifyEIP1559Header when IsLondon(header.Number) is
	// already true), so the early-return only fires for the XDC pre-fork path.
	if config.XDPoS != nil && !config.IsEIP1559(header.Number) {
		if header.BaseFee != nil {
			return fmt.Errorf("invalid baseFee: have %s, want <nil>", header.BaseFee)
		}
		return nil
	}

	// Verify that the gas limit remains within allowed bounds.
	//
	// XDC: on XDC networks the gas limit does NOT double at the EIP-1559 fork
	// boundary (the chain keeps the same configured limit). Standard Ethereum's
	// elasticity-multiplier doubling applies only to upstream chains.
	parentGasLimit := parent.GasLimit
	if config.XDPoS == nil && !config.IsLondon(parent.Number) {
		parentGasLimit = parent.GasLimit * config.ElasticityMultiplier()
	}
	if err := misc.VerifyGaslimit(parentGasLimit, header.GasLimit); err != nil {
		return err
	}
	// Verify the header is not malformed
	if header.BaseFee == nil {
		return errors.New("header is missing baseFee")
	}
	// XDC: the base fee is a pure function of the block number (the gas price
	// tier active at header.Number, params.XDCBaseFeeForBlock), so it is checked
	// exactly and without consulting the parent. This also covers the two cases
	// that used to be special-cased here: the EIP-1559 fork boundary (the parent
	// has no base fee) and checkpoint-sync parents with a synthetic GasUsed=0
	// (#541). Both were only needed while the check derived the fee from the
	// parent, and both would have accepted a Gas2500x fork block still carrying
	// the previous tier's price.
	if config.XDPoS != nil {
		if want := params.XDCBaseFeeForBlock(config, header.Number); header.BaseFee.Cmp(want) != 0 {
			return fmt.Errorf("invalid baseFee: have %s, want %s", header.BaseFee, want)
		}
		return nil
	}
	// Verify the parent header is not malformed
	if feeMarketActive(config, parent.Number) && parent.BaseFee == nil {
		return errors.New("parent header is missing baseFee")
	}
	// Verify the baseFee is correct based on the parent header.
	expectedBaseFee := CalcBaseFee(config, parent)
	if header.BaseFee.Cmp(expectedBaseFee) != 0 {
		return fmt.Errorf("invalid baseFee: have %s, want %s, parentBaseFee %s, parentGasUsed %d",
			header.BaseFee, expectedBaseFee, parent.BaseFee, parent.GasUsed)
	}
	return nil
}

// CalcBaseFee calculates the basefee of the header.
//
// XDC: on XDC chains the base fee is the gas price tier of the block being
// built or verified (params.XDCBaseFeeForBlock: 12.5 gwei, 625 gwei from
// Gas2500x), ignoring parent gas inputs. The tier is resolved at the child's
// number, parent.Number+1, so the block that activates a tier fork already
// carries the new price (XinFinOrg/XDPoSChain #2516). The standard Ethereum
// dynamic formula applies on non-XDC chains.
func CalcBaseFee(config *params.ChainConfig, parent *types.Header) *big.Int {
	if config.XDPoS != nil {
		// Pre-fork callers (txpool, miner) may consult this with a pre-fork
		// parent; returning the tier price preserves the non-nil contract.
		return params.XDCBaseFeeForBlock(config, new(big.Int).Add(parent.Number, common.Big1))
	}
	// Upstream Ethereum chains use the standard dynamic formula.
	//
	// If the current block is the first EIP-1559 block, return the InitialBaseFee.
	if !config.IsLondon(parent.Number) {
		return new(big.Int).SetUint64(params.InitialBaseFee)
	}

	parentGasTarget := parent.GasLimit / config.ElasticityMultiplier()
	// If the parent gasUsed is the same as the target, the baseFee remains unchanged.
	if parent.GasUsed == parentGasTarget {
		return new(big.Int).Set(parent.BaseFee)
	}

	var (
		num   = new(big.Int)
		denom = new(big.Int)
	)

	if parent.GasUsed > parentGasTarget {
		// If the parent block used more gas than its target, the baseFee should increase.
		// max(1, parentBaseFee * gasUsedDelta / parentGasTarget / baseFeeChangeDenominator)
		num.SetUint64(parent.GasUsed - parentGasTarget)
		num.Mul(num, parent.BaseFee)
		num.Div(num, denom.SetUint64(parentGasTarget))
		num.Div(num, denom.SetUint64(config.BaseFeeChangeDenominator()))
		if num.Cmp(common.Big1) < 0 {
			return num.Add(parent.BaseFee, common.Big1)
		}
		return num.Add(parent.BaseFee, num)
	} else {
		// Otherwise if the parent block used less gas than its target, the baseFee should decrease.
		// max(0, parentBaseFee * gasUsedDelta / parentGasTarget / baseFeeChangeDenominator)
		num.SetUint64(parentGasTarget - parent.GasUsed)
		num.Mul(num, parent.BaseFee)
		num.Div(num, denom.SetUint64(parentGasTarget))
		num.Div(num, denom.SetUint64(config.BaseFeeChangeDenominator()))

		baseFee := num.Sub(parent.BaseFee, num)
		if baseFee.Cmp(common.Big0) < 0 {
			baseFee = common.Big0
		}
		return baseFee
	}
}
