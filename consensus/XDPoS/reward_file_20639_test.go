// Copyright (c) 2026 XDC Network

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

func TestF202620639RewardByAccountSeparatesSignerAndHolder(t *testing.T) {
	dir := t.TempDir()
	old := common.StoreRewardFolder
	common.StoreRewardFolder = dir
	defer func() { common.StoreRewardFolder = old }()

	signer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	body := `{
		"signers": {"` + signer.Hex() + `": {"Sign": 12, "Reward": 100}},
		"rewards": {"` + signer.Hex() + `": {"` + owner.Hex() + `": 40, "` + signer.Hex() + `": 60}},
		"signSlots": 9
	}`
	if err := os.WriteFile(filepath.Join(dir, "1800.0xabc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRewardFileObject([]byte(body))
	if err != nil {
		t.Fatalf("file with signSlots did not decode: %v", err)
	}
	if decoded["signSlots"] == nil || decoded["signers"] == nil {
		t.Fatalf("decoded record = %#v", decoded)
	}

	chain := &stubChain{currentHeader: &types.Header{Number: big.NewInt(2000)}}
	api := makeTestAPI(makeTestXDPoSWithV2(), chain)

	signerRes, err := api.GetRewardByAccount(signer, rpc.BlockNumber(0), rpc.LatestBlockNumber)
	if err != nil {
		t.Fatal(err)
	}
	if len(signerRes.Rewards) != 1 || signerRes.Rewards[0].Reward != "100" {
		t.Fatalf("signer reward = %+v, want 100 once", signerRes.Rewards)
	}
	ownerRes, err := api.GetRewardByAccount(owner, rpc.BlockNumber(0), rpc.LatestBlockNumber)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerRes.Rewards) != 1 || ownerRes.Rewards[0].Reward != "40" {
		t.Fatalf("owner reward = %+v, want 40", ownerRes.Rewards)
	}

	if err := os.WriteFile(filepath.Join(dir, "1801.0xdef"), []byte(`{"signers":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := api.GetRewardByAccount(signer, rpc.BlockNumber(0), rpc.LatestBlockNumber); err == nil {
		t.Fatal("a reward file that does not decode returned no error")
	}
}
