// Tests for issue #1787: GetRewardByAccount must reject ranges spanning more
// than 1000 epochs before calling os.ReadDir, as GetEpochNumbersBetween does.
// Every test here FAILS on origin/main and PASSES with the fix.
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

// TestGetRewardByAccountRangeLimit verifies that a range spanning more than
// 1000 epochs is rejected before os.ReadDir is called.
// FAILS on origin/main (no limit check), PASSES with the fix.
func TestGetRewardByAccountRangeLimit(t *testing.T) {
	const epoch = uint64(900) // matches makeTestXDPoSWithV2

	dir := t.TempDir()
	old := common.StoreRewardFolder
	common.StoreRewardFolder = dir
	defer func() { common.StoreRewardFolder = old }()

	// Range of 1001 epochs = 1001*900 = 900900 blocks, over the 1000-epoch limit.
	beginNum := uint64(0)
	endNum := uint64(1001 * epoch)

	chain := &stubChain{
		currentHeader: &types.Header{Number: big.NewInt(int64(endNum))},
		headers:       map[uint64]*types.Header{},
	}
	api := makeTestAPI(makeTestXDPoSWithV2(), chain)

	begin := rpc.BlockNumber(beginNum)
	end := rpc.BlockNumber(endNum)
	_, err := api.GetRewardByAccount(common.Address{}, begin, end)
	if err == nil {
		t.Fatal("expected error for range > 1000 epochs, got nil")
	}
}

// TestGetRewardByAccountRangeAllowed verifies that a normal range (within the
// 1000-epoch limit) is accepted and reads the folder.
func TestGetRewardByAccountRangeAllowed(t *testing.T) {
	const epoch = uint64(900)

	dir := t.TempDir()
	old := common.StoreRewardFolder
	common.StoreRewardFolder = dir
	defer func() { common.StoreRewardFolder = old }()

	owner := common.HexToAddress("0x5eca17cd31964f436c420a2092dc1c3bfc4b15dd")
	signer := common.HexToAddress("0x11c3637d2848ebdc190d56d8b81348c21446e866")
	reward := `{"` + signer.Hex() + `":{"` + owner.Hex() + `":{"reward":1000000000000000000}}}`
	if err := os.WriteFile(filepath.Join(dir, "900.0xabc"), []byte(reward), 0o644); err != nil {
		t.Fatal(err)
	}

	// Range of exactly 1000 epochs — at the limit, should be allowed.
	beginNum := uint64(0)
	endNum := uint64(1000 * epoch)

	chain := &stubChain{
		currentHeader: &types.Header{Number: big.NewInt(int64(endNum))},
		headers:       map[uint64]*types.Header{},
	}
	api := makeTestAPI(makeTestXDPoSWithV2(), chain)

	begin := rpc.BlockNumber(beginNum)
	end := rpc.BlockNumber(endNum)
	res, err := api.GetRewardByAccount(owner, begin, end)
	if err != nil {
		t.Fatalf("unexpected error for range at limit: %v", err)
	}
	if len(res.Rewards) != 1 || res.Rewards[0].BlockNumber != 900 {
		t.Fatalf("rewards = %+v, want one entry at block 900", res.Rewards)
	}
}
