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
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

func addrRange(from, to byte) []common.Address {
	var out []common.Address
	for b := from; b <= to; b++ {
		out = append(out, common.BytesToAddress([]byte{b}))
	}
	return out
}

func sortedKeys(set PrecompiledContracts) []common.Address {
	keys := make([]common.Address, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b common.Address) int { return a.Cmp(b) })
	return keys
}

func sortedAddrs(addrs []common.Address) []common.Address {
	out := slices.Clone(addrs)
	slices.SortFunc(out, func(a, b common.Address) int { return a.Cmp(b) })
	return out
}

// TestXDCCancunHasNo0x0a pins XDPoSChain parity on the live networks: from the
// Cancun block on, mainnet and Apothem keep XDPoSChain's EIP1559 set (0x01-0x09),
// so 0x0a is neither a precompile nor warmed by the access list.
func TestXDCCancunHasNo0x0a(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *params.ChainConfig
		cancun int64
	}{
		{"mainnet", params.XDCMainnetChainConfig, 98_802_000},
		{"apothem", params.XDCApothemChainConfig, 71_551_800},
	} {
		rules := tc.config.Rules(big.NewInt(tc.cancun), true, 0)
		if !rules.IsXDC || !rules.IsCancun || rules.IsPrague {
			t.Fatalf("%s: rules at Cancun block: IsXDC=%v IsCancun=%v IsPrague=%v", tc.name, rules.IsXDC, rules.IsCancun, rules.IsPrague)
		}
		want := addrRange(0x01, 0x09)
		if got := sortedKeys(*activePrecompiledContracts(rules)); !slices.Equal(got, want) {
			t.Errorf("%s: precompile set %v, want %v", tc.name, got, want)
		}
		if got := sortedAddrs(ActivePrecompiles(rules)); !slices.Equal(got, want) {
			t.Errorf("%s: warmed addresses %v, want %v", tc.name, got, want)
		}
	}
}

// TestXDCPragueOsaka0x0aStub pins XDPoSChain v2.9.2-testnet: 0x0a is an
// always-failing stub priced at the point-evaluation gas, next to BLS12-381
// (0x0b-0x11) on Prague and additionally P256VERIFY (0x100) on Osaka.
func TestXDCPragueOsaka0x0aStub(t *testing.T) {
	prague := params.Rules{IsXDC: true, IsBerlin: true, IsCancun: true, IsPrague: true}
	osaka := prague
	osaka.IsOsaka = true

	wantPrague := addrRange(0x01, 0x11)
	wantOsaka := append(slices.Clone(wantPrague), common.BytesToAddress([]byte{0x01, 0x00}))
	for _, tc := range []struct {
		name  string
		rules params.Rules
		want  []common.Address
	}{
		{"prague", prague, wantPrague},
		{"osaka", osaka, wantOsaka},
	} {
		set := *activePrecompiledContracts(tc.rules)
		if got := sortedKeys(set); !slices.Equal(got, tc.want) {
			t.Errorf("%s: precompile set %v, want %v", tc.name, got, tc.want)
		}
		if got := sortedAddrs(ActivePrecompiles(tc.rules)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: warmed addresses %v, want %v", tc.name, got, tc.want)
		}
		stub := set[kzgPointEvaluationAddress]
		if stub != xdcKZGStub {
			t.Fatalf("%s: 0x0a is %T, want the XDC stub", tc.name, stub)
		}
		for _, n := range []int{0, 1, 191, 192, 200} {
			input := make([]byte, n)
			if gas := stub.RequiredGas(input); gas != params.BlobTxPointEvaluationPrecompileGas {
				t.Errorf("%s: gas(%d bytes) = %d", tc.name, n, gas)
			}
			if _, err := stub.Run(input); err == nil {
				t.Errorf("%s: stub accepted %d-byte input", tc.name, n)
			}
		}
	}
}

// TestNonXDCKeepsKZG guards that only XDC chains are rerouted.
func TestNonXDCKeepsKZG(t *testing.T) {
	rules := params.Rules{IsBerlin: true, IsCancun: true}
	if _, ok := (*activePrecompiledContracts(rules))[kzgPointEvaluationAddress].(*kzgPointEvaluation); !ok {
		t.Fatal("non-XDC Cancun lost the KZG point evaluation precompile")
	}
	if !slices.Contains(ActivePrecompiles(rules), kzgPointEvaluationAddress) {
		t.Fatal("non-XDC Cancun no longer warms 0x0a")
	}
}
