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

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// TestFinalizeSimulatedV2HeaderNoPanic checks XDC-HUNT-40: eth_simulateV1 calls
// Finalize with a header reader that is not a consensus.ChainReader and a
// simulated header with no extra field. The V2 branch is skipped, and the
// epoch-switch check on the empty extra field used to panic. Finalize must
// return, with no reward step.
func TestFinalizeSimulatedV2HeaderNoPanic(t *testing.T) {
	cfg := &params.XDPoSConfig{Period: 2, Epoch: 900, Gap: 450, V2: &params.V2{SwitchBlock: big.NewInt(900)}}
	db := rawdb.NewMemoryDatabase()
	engine := New(cfg, db)
	engine.SetEngineV2(engine_v2.New(&params.ChainConfig{ChainID: big.NewInt(2012), XDPoS: cfg}, db, nil, nil))
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(1000), Difficulty: big.NewInt(1)} // above SwitchBlock 900, empty Extra
	// Only the header-reader methods, as simChainHeadReader: it is not a ChainReader.
	chain := struct{ consensus.ChainHeaderReader }{&stubChain{currentHeader: header, headers: map[uint64]*types.Header{}}}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Finalize panicked: %v", r)
		}
	}()
	engine.Finalize(chain, header, statedb, &types.Body{})
}
