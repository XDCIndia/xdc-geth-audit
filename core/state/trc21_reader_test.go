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

package state

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
)

func addrKey(a common.Address) common.Hash { return common.BytesToHash(a.Bytes()) }

// seedTRC21 lays out the issuer and token storage the way the TRC21 issuer and
// token contracts do (XDPoSChain SlotTRC21Issuer / SlotTRC21Token).
func seedTRC21(t *testing.T, tokens map[common.Address]*big.Int) *StateDB {
	t.Helper()
	statedb, err := New(types.EmptyRootHash, NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	issuer := common.TRC21IssuerSMC
	tokensSlot := common.BigToHash(big.NewInt(int64(slotTRC21IssuerTokens)))
	statedb.SetState(issuer, tokensSlot, common.BigToHash(big.NewInt(int64(len(tokens)+1))))
	i := uint64(0)
	// A zero entry in the tokens array (a removed token) must be skipped.
	statedb.SetState(issuer, GetLocDynamicArrAtElement(tokensSlot, i, 1), common.Hash{})
	for token, capacity := range tokens {
		i++
		statedb.SetState(issuer, GetLocDynamicArrAtElement(tokensSlot, i, 1), addrKey(token))
		statedb.SetState(issuer, common.BigToHash(GetLocMappingAtKey(addrKey(token), slotTRC21IssuerTokensState)), common.BigToHash(capacity))
	}
	return statedb
}

func TestTRC21FeeCapacityRoundTrip(t *testing.T) {
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	statedb := seedTRC21(t, map[common.Address]*big.Int{token: big.NewInt(1_000_000)})

	capacity := GetTRC21FeeCapacityFromState(statedb)
	if len(capacity) != 1 || capacity[token].Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("capacity = %v, want {%v: 1000000}", capacity, token)
	}

	// Paying 300 from the capacity lowers it in the issuer storage and takes the
	// XDC from the issuer's balance.
	issuer := common.TRC21IssuerSMC
	statedb.AddBalance(issuer, uint256.NewInt(10_000), tracing.BalanceChangeUnspecified)
	UpdateTRC21Fee(statedb, map[common.Address]*big.Int{token: big.NewInt(999_700)}, big.NewInt(300))

	if got := GetTRC21FeeCapacityFromState(statedb)[token]; got.Cmp(big.NewInt(999_700)) != 0 {
		t.Fatalf("capacity after update = %v, want 999700", got)
	}
	if got := statedb.GetBalance(issuer); got.Uint64() != 9_700 {
		t.Fatalf("issuer balance = %v, want 9700", got)
	}
}

func TestUpdateTRC21FeeNoTokensIsNoop(t *testing.T) {
	statedb := seedTRC21(t, nil)
	issuer := common.TRC21IssuerSMC
	statedb.AddBalance(issuer, uint256.NewInt(10_000), tracing.BalanceChangeUnspecified)
	UpdateTRC21Fee(statedb, map[common.Address]*big.Int{}, big.NewInt(300))
	if got := statedb.GetBalance(issuer); got.Uint64() != 10_000 {
		t.Fatalf("issuer balance = %v, want untouched 10000", got)
	}
}

func TestPayFeeWithTRC21TxFail(t *testing.T) {
	var (
		token      = common.HexToAddress("0x2222222222222222222222222222222222222222")
		sender     = common.HexToAddress("0x3333333333333333333333333333333333333333")
		tokenIssue = common.HexToAddress("0x4444444444444444444444444444444444444444")
	)
	statedb, err := New(types.EmptyRootHash, NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	balanceKey := func(a common.Address) common.Hash {
		return common.BigToHash(GetLocMappingAtKey(addrKey(a), slotTRC21TokenBalances))
	}
	statedb.SetState(token, GetLocSimpleVariable(slotTRC21TokenIssuer), addrKey(tokenIssue))
	statedb.SetState(token, GetLocSimpleVariable(slotTRC21TokenMinFee), common.BigToHash(big.NewInt(50)))

	for _, tc := range []struct {
		name                   string
		balance                int64
		wantSender, wantIssuer int64
	}{
		{"balance above minFee pays minFee", 80, 30, 50},
		{"balance below minFee pays what is left", 30, 0, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statedb.SetState(token, balanceKey(sender), common.BigToHash(big.NewInt(tc.balance)))
			PayFeeWithTRC21TxFail(statedb, sender, token)
			if got := statedb.GetState(token, balanceKey(sender)).Big().Int64(); got != tc.wantSender {
				t.Errorf("sender token balance = %d, want %d", got, tc.wantSender)
			}
			if got := statedb.GetState(token, balanceKey(tokenIssue)).Big().Int64(); got != tc.wantIssuer {
				t.Errorf("issuer token balance = %d, want %d", got, tc.wantIssuer)
			}
		})
	}
}
