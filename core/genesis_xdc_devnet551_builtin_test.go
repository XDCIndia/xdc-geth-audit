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
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// TestXDCDevnet551BuiltinMatchesCanonical checks every value of the built-in
// chain-551 genesis and chain config against canonical XDPoSChain v2.9.3-testnet
// (params/config_networks.go:471 DevnetChainConfig, core/genesis.go:925
// DefaultDevnetGenesisBlock).
func TestXDCDevnet551BuiltinMatchesCanonical(t *testing.T) {
	g := DefaultXDCDevnet551GenesisBlock()
	want := common.HexToHash(canonical551GenesisHash)
	if got := g.ToBlock().Hash(); got != want {
		t.Fatalf("551 built-in genesis hash = %s, want %s", got.Hex(), canonical551GenesisHash)
	}
	if params.XDCDevnet551GenesisHash != want {
		t.Fatalf("params.XDCDevnet551GenesisHash = %s, want %s", params.XDCDevnet551GenesisHash.Hex(), canonical551GenesisHash)
	}
	if g.GasLimit != 4700000 || g.Timestamp != 1785709299 || g.Nonce != 0 || g.Difficulty.Int64() != 1 || len(g.Alloc) != 8 {
		t.Fatalf("551 genesis header = gasLimit %d time %d nonce %d difficulty %v alloc %d, want 4700000 1785709299 0 1 8",
			g.GasLimit, g.Timestamp, g.Nonce, g.Difficulty, len(g.Alloc))
	}
	c := g.Config
	if c != params.XDCDevnet551ChainConfig || c.ChainID.Int64() != 551 {
		t.Fatalf("551 genesis config is not XDCDevnet551ChainConfig (chainId %v)", c.ChainID)
	}
	if err := c.CheckConfigForkOrder(); err != nil {
		t.Fatalf("CheckConfigForkOrder: %v", err)
	}
	checkForks(t, "551 built-in", []forkWant{
		{"Homestead", c.HomesteadBlock, 0},
		{"DAOFork", c.DAOForkBlock, -1},
		{"EIP150", c.EIP150Block, 0},
		{"EIP155", c.EIP155Block, 0},
		{"EIP158", c.EIP158Block, 0},
		{"Byzantium", c.ByzantiumBlock, 0},
		{"Berlin", c.BerlinBlock, 0},
		{"London", c.LondonBlock, 0},
		{"Merge", c.MergeBlock, 0},
		{"Shanghai", c.ShanghaiBlock, 0},
		{"TIP2019", c.TIP2019Block, 0},
		{"TIPSigning", c.TIPSigningBlock, 0},
		{"TIPRandomize", c.TIPRandomizeBlock, 0},
		{"TIPIncreaseMasternodes", c.TIPIncreaseMasternodesBlock, 0},
		{"Denylist", c.DenylistBlock, 0},
		{"TIPNoHalvingMNReward", c.TIPNoHalvingMNRewardBlock, 0},
		{"TIPXDCX", c.TIPXDCXBlock, 0},
		{"TIPXDCXLending", c.TIPXDCXLendingBlock, 0},
		{"TIPXDCXCancellationFee", c.TIPXDCXCancellationFeeBlock, 0},
		{"TIPTRC21Fee", c.TIPTRC21FeeBlock, 0},
		{"Gas50x", c.Gas50xBlock, 0},
		{"Gas2500x", c.Gas2500xBlock, 1385100},
		{"TIPXDCXMinerDisable", c.TIPXDCXMinerDisableBlock, 0},
		{"TIPXDCXReceiverDisable", c.TIPXDCXReceiverDisableBlock, 0},
		{"EIP-1559", c.Eip1559Block, 25000},
		{"Cancun", c.CancunBlock, 25000},
		{"Prague", c.PragueBlock, 50000},
		{"Osaka", c.OsakaBlock, -1},
		{"DynamicGasLimit", c.DynamicGasLimitBlock, 50000},
		{"TIPFixedReward", c.TIPFixedRewardBlock, 50000},
		{"TIPUpgradePenalty", c.TIPUpgradePenaltyBlock, 50000},
		{"TIPEpochHalving", c.TIPEpochHalvingBlock, -1},
	})
	x := c.XDPoS
	if x.Period != 2 || x.Epoch != 900 || x.Reward != 1026 || x.RewardCheckpoint != 900 || x.Gap != 450 || x.MaxMasternodesV2 != 18 ||
		x.FoudationWalletAddr != common.HexToAddress("0xf8b10955abe83acfdae2dbd4b5d351fc5e1c3672") {
		t.Errorf("551 XDPoS = %v foundation %s maxMasternodesV2 %d, want period 2 epoch 900 reward 1026 checkpoint 900 gap 450, 0xf8b10955abe83acfdae2dbd4b5d351fc5e1c3672, 18",
			x, x.FoudationWalletAddr.Hex(), x.MaxMasternodesV2)
	}
	if x.V2.SwitchBlock.Int64() != 2700 || x.V2.SwitchEpoch != 3 || x.V2.CurrentConfig != x.V2.AllConfigs[0] {
		t.Errorf("551 V2 switch = block %v epoch %d, want block 2700 epoch 3 with round-0 current config", x.V2.SwitchBlock, x.V2.SwitchEpoch)
	}
	if !reflect.DeepEqual(x.V2.AllConfigs, canonical551V2Configs) {
		t.Errorf("551 V2 configs differ from canonical:\n have %s\n want %s", jsonOf(x.V2.AllConfigs), jsonOf(canonical551V2Configs))
	}
}
