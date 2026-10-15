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

package legacypool

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestListSpecialTxReplacement pins the list-level same-nonce rules: a special
// (block-signing) tx claims its nonce back from a regular tx (XinFinOrg/XDPoSChain
// #2363), a regular tx never evicts a special tx, special-for-special stays on
// the priced path (the pool only admits it once the incumbent expired, see
// TestXDCExpiredSigningTxReplacement), and regular-for-regular needs the bump.
func TestListSpecialTxReplacement(t *testing.T) {
	other := common.HexToAddress("0x1234")
	regular := func(price int64) *types.Transaction {
		return types.NewTransaction(0, other, big.NewInt(0), 21000, big.NewInt(price), nil)
	}
	special := func() *types.Transaction {
		return types.NewTransaction(0, common.BlockSignersBinary, big.NewInt(0), 21000, big.NewInt(0), nil)
	}
	const bump = 10

	for _, tc := range []struct {
		name     string
		old, new *types.Transaction
		replaced bool
	}{
		{"special evicts a pricier regular tx", regular(1_000_000), special(), true},
		{"regular cannot evict a pending special tx", special(), regular(1_000_000), false},
		{"special-for-special stays priced", special(), special(), false},
		{"regular replaces regular with the bump", regular(100), regular(111), true},
		{"regular below the bump is rejected", regular(100), regular(105), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newList(true)
			if ok, _ := l.Add(tc.old, bump); !ok {
				t.Fatal("seeding the list failed")
			}
			ok, prev := l.Add(tc.new, bump)
			if ok != tc.replaced {
				t.Fatalf("Add accepted = %v, want %v", ok, tc.replaced)
			}
			if tc.replaced && prev != tc.old {
				t.Fatalf("replaced tx = %v, want the old one", prev)
			}
		})
	}
}
