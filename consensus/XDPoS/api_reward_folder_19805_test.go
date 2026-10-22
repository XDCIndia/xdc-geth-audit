// Copyright (c) 2026 XDC Network
// F-2026-19805: GetRewardByAccount must read the folder that --store-reward
// sets (common.StoreRewardFolder). It read an XDPoS package variable that was
// never assigned, so the RPC always reported the folder as not configured.
//
// Run: go test ./consensus/XDPoS/ -run TestGetRewardByAccountUsesStoreRewardFlag -count=1

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

func TestGetRewardByAccountUsesStoreRewardFlag(t *testing.T) {
	dir := t.TempDir()
	old := common.StoreRewardFolder
	common.StoreRewardFolder = dir // what cmd/utils does for --store-reward
	defer func() { common.StoreRewardFolder = old }()

	signer := common.HexToAddress("0x11c3637d2848ebdc190d56d8b81348c21446e866")
	owner := common.HexToAddress("0x5eca17cd31964f436c420a2092dc1c3bfc4b15dd")
	reward := `{"` + signer.Hex() + `":{"` + owner.Hex() + `":{"reward":57077640000000000000}}}`
	if err := os.WriteFile(filepath.Join(dir, "1800.0xabc"), []byte(reward), 0o644); err != nil {
		t.Fatal(err)
	}

	chain := &stubChain{currentHeader: &types.Header{Number: big.NewInt(2000)}}
	api := makeTestAPI(makeTestXDPoSWithV2(), chain)
	res, err := api.GetRewardByAccount(owner, rpc.BlockNumber(0), rpc.LatestBlockNumber)
	if err != nil {
		t.Fatalf("GetRewardByAccount with --store-reward set: %v", err)
	}
	if len(res.Rewards) != 1 || res.Rewards[0].BlockNumber != 1800 || res.Rewards[0].Reward != "57077640000000000000" {
		t.Fatalf("rewards = %+v, want one 57.07764 XDC entry at block 1800", res.Rewards)
	}
}
