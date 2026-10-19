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

package core

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/triedb"
)

// TestXDCDevnetSetupGenesisV2RoundConfigs checks that a node started with the
// built-in devnet 5551 genesis gets the round-50000 V2 config
// (MinimumMinerBlockPerEpoch 5, LimitPenaltyEpoch 5, MinimumSigningTx 30).
// Live block 51,813 (round 50,400) penalises 13 masternodes with it; with the
// round-0 config only, a follower penalises 2 and rejects the block with
// "validators not legit".
func TestXDCDevnetSetupGenesisV2RoundConfigs(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	cfg, _, _, err := SetupGenesisBlock(db, triedb.NewDatabase(db, nil), DefaultXDCDevnetGenesisBlock())
	if err != nil {
		t.Fatalf("SetupGenesisBlock: %v", err)
	}
	v2 := cfg.XDPoS.V2
	if _, ok := v2.AllConfigs[50000]; !ok {
		t.Fatalf("devnet V2 AllConfigs has no round-50000 entry: %d entries", len(v2.AllConfigs))
	}
	v2.BuildConfigIndex()
	c := v2.Config(50400)
	if c.MinimumMinerBlockPerEpoch != 5 || c.LimitPenaltyEpoch != 5 || c.MinimumSigningTx != 30 {
		t.Fatalf("V2 config at round 50400 = {minBlocks %d, limitPenaltyEpoch %d, minSigningTx %d}, want {5, 5, 30}",
			c.MinimumMinerBlockPerEpoch, c.LimitPenaltyEpoch, c.MinimumSigningTx)
	}
}
