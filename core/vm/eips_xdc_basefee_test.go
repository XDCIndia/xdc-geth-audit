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
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestOpBaseFeeXDC pins BASEFEE on XDC chains (XinFinOrg/XDPoSChain #2516): the
// header base fee when the header has one, and the Gas50x tier price in the
// London to EIP-1559 window, where the header carries none. The window used to
// hand a nil base fee to SetFromBig.
func TestOpBaseFeeXDC(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseFee *big.Int
		want    uint64
	}{
		{"london window, header has no base fee", nil, params.XDCBaseFee},
		{"gas50x header", new(big.Int).SetUint64(params.XDCBaseFee), params.XDCBaseFee},
		{"gas2500x header", big.NewInt(625_000_000_000), 625_000_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				evm   = NewEVM(BlockContext{BaseFee: tc.baseFee}, nil, params.XDCApothemChainConfig, Config{})
				stack = newStackForTesting()
				pc    = uint64(0)
			)
			if _, err := opBaseFee(&pc, evm, &ScopeContext{nil, stack, nil}); err != nil {
				t.Fatal(err)
			}
			if got := stack.pop(); !got.IsUint64() || got.Uint64() != tc.want {
				t.Fatalf("BASEFEE = %v, want %d", &got, tc.want)
			}
		})
	}
}
