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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
)

// Canonical devnet 551: XinFinOrg/XDPoSChain v2.9.3-testnet
// params/config_networks.go:55 (DevnetGenesisHash), :189 (DevnetV2Configs) and
// :471 (DevnetChainConfig).
const canonical551GenesisHash = "0x50e63112ebf55bba9115ea875eedc3f6c62b42440d23786323e2678a9848b7ac"

var canonical551V2Configs = map[uint64]*params.V2Config{
	0: {MaxMasternodes: 18, CertThreshold: 0.667, TimeoutSyncThreshold: 3, TimeoutPeriod: 10, MinePeriod: 2,
		ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 1.0}},
	45000: {SwitchRound: 45000, MaxMasternodes: 10, MaxProtectorNodes: 3, MaxObserverNodes: 10, CertThreshold: 0.667,
		TimeoutSyncThreshold: 3, TimeoutPeriod: 10, MinePeriod: 2, ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 1.0},
		MasternodeReward: 63.42, ProtectorReward: 50.27, ObserverReward: 25.13,
		MinimumMinerBlockPerEpoch: 5, LimitPenaltyEpoch: 5, MinimumSigningTx: 30},
	1683000: {SwitchRound: 1683000, MaxMasternodes: 10, MaxProtectorNodes: 3, MaxObserverNodes: 10, CertThreshold: 0.667,
		TimeoutSyncThreshold: 3, TimeoutPeriod: 10, MinePeriod: 2, ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 1.0},
		MasternodeReward: 63.4196, ProtectorReward: 50.7357, ObserverReward: 25.3678,
		MinimumMinerBlockPerEpoch: 5, LimitPenaltyEpoch: 5, MinimumSigningTx: 30},
}

type forkWant struct {
	name string
	got  *big.Int
	want int64 // -1: canonical nil
}

func checkForks(t *testing.T, label string, forks []forkWant) {
	t.Helper()
	for _, f := range forks {
		switch {
		case f.want < 0 && f.got != nil:
			t.Errorf("%s %s = %v, want nil", label, f.name, f.got)
		case f.want >= 0 && (f.got == nil || f.got.Int64() != f.want):
			t.Errorf("%s %s = %v, want %d", label, f.name, f.got, f.want)
		}
	}
}

// TestXDCDevnet551SetupGenesisMatchesCanonical starts a node from the canonical
// 551 genesis with a sparse config (only chainId and the XDPoS block, as a
// shipped genesis JSON carries) and checks that the genesis hash and the fork
// schedule the node resolves are canonical's.
func TestXDCDevnet551SetupGenesisMatchesCanonical(t *testing.T) {
	raw, err := os.ReadFile("xdc_genesis/devnet551.json")
	if err != nil {
		t.Fatalf("canonical 551 genesis: %v", err)
	}
	var g Genesis
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	g.Config = &params.ChainConfig{
		ChainID: big.NewInt(551), HomesteadBlock: big.NewInt(0), EIP150Block: big.NewInt(0),
		EIP155Block: big.NewInt(0), EIP158Block: big.NewInt(0), ByzantiumBlock: big.NewInt(0),
		XDPoS: &params.XDPoSConfig{Period: 2, Epoch: 900, Reward: 1026, RewardCheckpoint: 900, Gap: 450,
			FoudationWalletAddr: common.HexToAddress("0xf8b10955abe83acfdae2dbd4b5d351fc5e1c3672")},
	}
	db := rawdb.NewMemoryDatabase()
	if _, hash, _, err := SetupGenesisBlock(db, triedb.NewDatabase(db, nil), &g); err != nil {
		t.Fatalf("SetupGenesisBlock: %v", err)
	} else if hash != common.HexToHash(canonical551GenesisHash) {
		t.Fatalf("551 genesis hash = %s, want %s", hash.Hex(), canonical551GenesisHash)
	}
	cfg, _, err := LoadChainConfig(db, nil)
	if err != nil {
		t.Fatalf("LoadChainConfig: %v", err)
	}
	checkForks(t, "551 resolved", []forkWant{
		{"Berlin", cfg.BerlinBlock, 0},
		{"London", cfg.LondonBlock, 0},
		{"EIP-1559", cfg.Eip1559Block, 25000},
		{"Cancun", cfg.CancunBlock, 25000},
		{"Prague", cfg.PragueBlock, 50000},
		{"TIPUpgradeReward", cfg.TIPUpgradeRewardBlock, 50000},
		{"TIPUpgradePenalty", cfg.TIPUpgradePenaltyBlock, 50000},
		{"Gas2500x", cfg.Gas2500xBlock, 1385100},
		{"Osaka", cfg.OsakaBlock, -1},
	})
	v2 := cfg.XDPoS.V2
	if v2 == nil {
		t.Fatal("551 resolved V2 config is nil")
	}
	if v2.SwitchBlock == nil || v2.SwitchBlock.Int64() != 2700 || v2.SwitchEpoch != 3 {
		t.Errorf("551 resolved V2 switch = block %v epoch %d, want block 2700 epoch 3", v2.SwitchBlock, v2.SwitchEpoch)
	}
	if !reflect.DeepEqual(v2.AllConfigs, canonical551V2Configs) {
		t.Errorf("551 resolved V2 configs differ from canonical:\n have %s\n want %s", jsonOf(v2.AllConfigs), jsonOf(canonical551V2Configs))
	}
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func sha256JSON(v any) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(jsonOf(v))))
}

// TestXDCRegisteredGenesisProfilesUnchanged pins mainnet (50), Apothem (51) and
// legacy devnet (5551): built-in genesis hash, the built-in chain config, and
// the config a node resolves from the built-in genesis.
func TestXDCRegisteredGenesisProfilesUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name        string
		genesis     func() *Genesis
		cfg         *params.ChainConfig
		hash        string
		cfgSHA      string
		resolvedSHA string
	}{
		{"mainnet", DefaultXDCMainnetGenesisBlock, params.XDCMainnetChainConfig,
			"0x4a9d748bd78a8d0385b67788c2435dcdb914f98a96250b68863a1f8b7642d6b1",
			// amsterdamBlock 99,999,999,999: far-future placeholder (#1644).
			"e2d0cf678f18867257b30db4359a6df2dc75e5b6fa05cc548fa998e7349cae35",
			"e2d0cf678f18867257b30db4359a6df2dc75e5b6fa05cc548fa998e7349cae35"},
		{"apothem", DefaultXDCApothemGenesisBlock, params.XDCApothemChainConfig,
			"0xbdea512b4f12ff1135ec92c00dc047ffb93890c2ea1aa0eefe9b013d80640075",
			// dynamicGasLimitBlock 83,600,000 added for the v2.9 reset (#1539),
			// pragueBlock 84,333,000 from XDPoSChain v3.0.0-testnet-1 (#1673),
			// amsterdamBlock 99,999,999,999 far-future placeholder (#1644).
			"647af3eb97871d95f3cf6912185802fe4be3eb4a9857fe1d0a80560267177824",
			"647af3eb97871d95f3cf6912185802fe4be3eb4a9857fe1d0a80560267177824"},
		{"devnet5551", DefaultXDCDevnetGenesisBlock, params.XDCDevnetChainConfig,
			"0xb8be003946a9c2688e9f1e255a5567c3d144293ccde2ffb38452a5840081b402",
			"fff564302f00f0707421fa0214a3abf5444189c319b81aa5dcd365f8def165c2",
			"752e4ac336b22149cc00be37c91e8eb3b5c413c7fdcb14bdb13f716664d8a34b"},
	} {
		if got := tc.genesis().ToBlock().Hash(); got != common.HexToHash(tc.hash) {
			t.Errorf("%s genesis hash = %s, want %s", tc.name, got.Hex(), tc.hash)
		}
		if got := sha256JSON(tc.cfg); got != tc.cfgSHA {
			t.Errorf("%s built-in chain config changed: sha256 %s, want %s", tc.name, got, tc.cfgSHA)
		}
		db := rawdb.NewMemoryDatabase()
		if _, _, _, err := SetupGenesisBlock(db, triedb.NewDatabase(db, nil), tc.genesis()); err != nil {
			t.Fatalf("%s SetupGenesisBlock: %v", tc.name, err)
		}
		cfg, _, err := LoadChainConfig(db, nil)
		if err != nil {
			t.Fatalf("%s LoadChainConfig: %v", tc.name, err)
		}
		if got := sha256JSON(cfg); got != tc.resolvedSHA {
			t.Errorf("%s resolved chain config changed: sha256 %s, want %s", tc.name, got, tc.resolvedSHA)
		}
	}
}
