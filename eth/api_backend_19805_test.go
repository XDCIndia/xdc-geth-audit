// Copyright (c) 2026 XDC Network

package eth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestF19805RewardByHashWithoutFolder checks that GetRewardByHash does not
// read <number>.<hash> from the working directory when --store-reward is unset.
func TestF19805RewardByHashWithoutFolder(t *testing.T) {
	n := newXDCFakeNet(t, 3, 3)
	head := n.chain.CurrentBlock()
	dir := t.TempDir()
	name := head.Number.String() + "." + head.Hash().Hex()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"signSlots": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	old := common.StoreRewardFolder
	common.StoreRewardFolder = ""
	defer func() { common.StoreRewardFolder = old }()

	api := &EthAPIBackend{eth: &Ethereum{blockchain: n.chain}}
	if got := api.GetRewardByHash(head.Hash()); len(got) != 0 {
		t.Fatalf("read a reward file from the working directory: %v", got)
	}
}
