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

package hooks

import (
	"math/big"
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestLegacyStandbyPool checks the devnet 5551 protector/observer pool: the
// parent-state candidates (including ones that joined during the rewarded
// epoch), ranked by stake, without h's penalties and masternodes.
func TestLegacyStandbyPool(t *testing.T) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	smc := common.MasternodeVotingSMCBinary
	slot8 := common.BigToHash(big.NewInt(8)) // candidates[]
	addr := func(b byte) common.Address { return common.BytesToAddress([]byte{b}) }
	// a: masternode, b: penalised, c..e: standby with caps 10, 30, 20.
	cands := []common.Address{addr(0xa), addr(0xb), addr(0xc), addr(0xd), {}, addr(0xe)}
	caps := map[common.Address]int64{addr(0xa): 50, addr(0xb): 40, addr(0xc): 10, addr(0xd): 30, addr(0xe): 20}
	statedb.SetState(smc, slot8, common.BigToHash(big.NewInt(int64(len(cands)))))
	for i, c := range cands {
		statedb.SetState(smc, state.GetLocDynamicArrAtElement(slot8, uint64(i), 1), common.BytesToHash(c.Bytes()))
		loc := state.GetLocMappingAtKey(common.BytesToHash(c.Bytes()), 1) // validatorsState[c]
		loc.Add(loc, big.NewInt(1))                                       // .cap
		statedb.SetState(smc, common.BigToHash(loc), common.BigToHash(big.NewInt(caps[c])))
	}
	h := &types.Header{Number: big.NewInt(194867), Penalties: addr(0xb).Bytes()}

	got := legacyStandbyPool(statedb, h, []common.Address{addr(0xa)})
	want := []common.Address{addr(0xd), addr(0xe), addr(0xc)}
	if !slices.Equal(got, want) {
		t.Fatalf("legacyStandbyPool = %v, want %v", got, want)
	}
}
