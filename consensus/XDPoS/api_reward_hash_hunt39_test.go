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
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

const hunt39Reward = `{"0x11c3637d2848ebdc190d56d8b81348c21446e866":{"0x5eca17cd31964f436c420a2092dc1c3bfc4b15dd":{"reward":1000000000000000000}}}`

// TestGetRewardByAccountUsesCanonicalHashFileOnly checks XDC-HUNT-39: a miner
// leaves a reward file under a pre-seal hash next to the file of the real block.
// The reader selected files by block number only and counted the epoch twice.
// It must read only the file named with the canonical hash of that block, and
// skip a file of a block it does not have.
func TestGetRewardByAccountUsesCanonicalHashFileOnly(t *testing.T) {
	dir := t.TempDir()
	old := common.StoreRewardFolder
	common.StoreRewardFolder = dir
	defer func() { common.StoreRewardFolder = old }()

	owner := common.HexToAddress("0x5eca17cd31964f436c420a2092dc1c3bfc4b15dd")
	canonical := &types.Header{Number: big.NewInt(1800), Extra: []byte{2}}
	files := []string{
		"1800." + canonical.Hash().Hex(),             // the block
		"1800." + common.HexToHash("0xbad1").Hex(),   // pre-seal hash, never a block
		"1800." + common.HexToHash("0xbad2").Hex(),   // side chain block
		"2700." + common.HexToHash("0xc0ffee").Hex(), // no such block on this node
		".tmp-1800-12345",                            // temp file of an atomic write
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(hunt39Reward), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	chain := &stubChain{
		currentHeader: &types.Header{Number: big.NewInt(3000)},
		headers:       map[uint64]*types.Header{1800: canonical},
	}
	api := makeTestAPI(makeTestXDPoSWithV2(), chain)
	res, err := api.GetRewardByAccount(owner, rpc.BlockNumber(0), rpc.LatestBlockNumber)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rewards) != 1 || res.Rewards[0].BlockNumber != 1800 {
		t.Fatalf("rewards = %+v, want exactly one entry at block 1800", res.Rewards)
	}
}
