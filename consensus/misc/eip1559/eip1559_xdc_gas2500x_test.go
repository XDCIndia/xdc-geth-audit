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

package eip1559

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

var gas2500xBaseFee = big.NewInt(625_000_000_000)

// TestCalcBaseFeeFollowsGasPriceSchedule mirrors XinFinOrg/XDPoSChain #2516: the
// XDC base fee is the tier of the block being built, so the activation block
// already carries the Gas2500x price. CalcBaseFee takes the parent here.
func TestCalcBaseFeeFollowsGasPriceSchedule(t *testing.T) {
	apothem := params.XDCApothemChainConfig
	for _, tc := range []struct {
		name   string
		cfg    *params.ChainConfig
		parent int64
		want   *big.Int
	}{
		{"apothem, child below gas2500x", apothem, 83_599_998, twelvePointFiveBillion()},
		{"apothem, child is the gas2500x block", apothem, 83_599_999, gas2500xBaseFee},
		{"apothem, above gas2500x", apothem, 86_899_316, gas2500xBaseFee},
		{"mainnet, unscheduled", params.XDCMainnetChainConfig, 107_000_000, twelvePointFiveBillion()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcBaseFee(tc.cfg, &types.Header{Number: big.NewInt(tc.parent), GasLimit: 420_000_000})
			if got.Cmp(tc.want) != 0 {
				t.Fatalf("have %v want %v", got, tc.want)
			}
		})
	}
}

// TestVerifyEIP1559HeaderAtGas2500x checks header validation across the Apothem
// Gas2500x fork block: the fork block must carry 625 gwei, the block before it
// 12.5 gwei, and the old price is rejected from the fork on.
func TestVerifyEIP1559HeaderAtGas2500x(t *testing.T) {
	cfg := params.XDCApothemChainConfig
	header := func(n int64, fee *big.Int) *types.Header {
		return &types.Header{Number: big.NewInt(n), GasLimit: 420_000_000, BaseFee: fee}
	}
	for _, tc := range []struct {
		name    string
		n       int64
		fee     *big.Int
		wantErr bool
	}{
		{"last block before the fork, old price", 83_599_999, twelvePointFiveBillion(), false},
		{"last block before the fork, new price", 83_599_999, gas2500xBaseFee, true},
		{"fork block, new price", 83_600_000, gas2500xBaseFee, false},
		{"fork block, old price", 83_600_000, twelvePointFiveBillion(), true},
		{"after the fork, new price", 83_600_001, gas2500xBaseFee, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := header(tc.n-1, twelvePointFiveBillion())
			err := VerifyEIP1559Header(cfg, parent, header(tc.n, tc.fee))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
