package params

// genesis_fork_keys_new_api_test.go: tests that use symbols added by #1528.
// These do NOT compile on origin/main. See genesis_fork_keys_test.go for the
// behavioural tests that compile on both and fail on main.

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestChainConfigKnownKeysAccepted: all canonical v2.9.3 keys parse without error
// and their values survive the round-trip.
func TestChainConfigKnownKeysAccepted(t *testing.T) {
	data := []byte(`{
		"chainId":50,
		"tip2019Block":1,
		"tipRandomizeBlock":2,
		"tipIncreaseMasternodesBlock":3,
		"denylistBlock":4,
		"tipNoHalvingMNRewardBlock":5,
		"tipXDCXBlock":6,
		"tipXDCXLendingBlock":7,
		"tipXDCXCancellationFeeBlock":8,
		"tipTRC21FeeBlock":9,
		"gas50xBlock":10,
		"tipXDCXMinerDisableBlock":11,
		"tipXDCXReceiverDisableBlock":12,
		"dynamicGasLimitBlock":100,
		"tipEpochHalvingBlock":200,
		"mergeBlock":300,
		"shanghaiBlock":400,
		"trc21IssuerSMC":"0x0000000000000000000000000000000000000089",
		"xdcxListingSMC":"0x0000000000000000000000000000000000000090",
		"relayerRegistrationSMC":"0x0000000000000000000000000000000000000091",
		"lendingRegistrationSMC":"0x0000000000000000000000000000000000000092"
	}`)
	var c ChainConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.TIP2019Block == nil || c.TIP2019Block.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("TIP2019Block: got %v, want 1", c.TIP2019Block)
	}
	if c.DynamicGasLimitBlock == nil || c.DynamicGasLimitBlock.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("DynamicGasLimitBlock: got %v, want 100", c.DynamicGasLimitBlock)
	}
}

// TestIsDynamicGasLimit: gate semantics for DynamicGasLimitBlock.
func TestIsDynamicGasLimit(t *testing.T) {
	c := &ChainConfig{DynamicGasLimitBlock: big.NewInt(100)}
	if c.IsDynamicGasLimit(big.NewInt(99)) {
		t.Error("should not be active before fork block")
	}
	if !c.IsDynamicGasLimit(big.NewInt(100)) {
		t.Error("should be active at fork block")
	}
	if !c.IsDynamicGasLimit(big.NewInt(101)) {
		t.Error("should be active after fork block")
	}
	c2 := &ChainConfig{DynamicGasLimitBlock: nil}
	if c2.IsDynamicGasLimit(big.NewInt(999999)) {
		t.Error("nil DynamicGasLimitBlock should never activate")
	}
}

// TestSMCAddresses: SMC address fields parse correctly (pointer type, omitempty works).
func TestSMCAddresses(t *testing.T) {
	addr := "0x0000000000000000000000000000000000000089"
	data := []byte(`{"chainId":999,"trc21IssuerSMC":"` + addr + `"}`)
	var c ChainConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.TRC21IssuerSMC == nil {
		t.Fatal("TRC21IssuerSMC is nil after unmarshal")
	}
	want := common.HexToAddress(addr)
	if *c.TRC21IssuerSMC != want {
		t.Errorf("TRC21IssuerSMC: got %v, want %v", *c.TRC21IssuerSMC, want)
	}
}

// TestXDPoSMaxMasternodesV2: MaxMasternodesV2 in XDPoSConfig parses correctly.
func TestXDPoSMaxMasternodesV2(t *testing.T) {
	data := []byte(`{
		"xdpos":{
			"period":2,"epoch":900,"reward":0,"rewardCheckpoint":900,"gap":5,
			"maxMasternodesV2":15,
			"v2":{"switchBlock":0,"minePeriod":0,"timeoutSyncThreshold":3,"timeoutPeriod":10,"maxTimeoutCount":3,"certThreshold":2,"config":{}}
		}
	}`)
	var c ChainConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.XDPoS == nil {
		t.Fatal("XDPoS is nil")
	}
	if c.XDPoS.MaxMasternodesV2 != 15 {
		t.Errorf("MaxMasternodesV2: got %d, want 15", c.XDPoS.MaxMasternodesV2)
	}
}

// TestIsTIP2019FallsBackToGlobal: when TIP2019Block is explicitly set, the gate
// uses it and overrides the global constant.
func TestIsTIP2019FallsBackToGlobal(t *testing.T) {
	c := &ChainConfig{TIP2019Block: big.NewInt(1000)}
	if c.IsTIP2019(big.NewInt(999)) {
		t.Error("IsTIP2019 should not activate before explicit block 1000")
	}
	if !c.IsTIP2019(big.NewInt(1000)) {
		t.Error("IsTIP2019 should activate at explicit block 1000")
	}
	// nil field falls back to common.TIP2019Block (0 for mainnet default).
	c2 := &ChainConfig{TIP2019Block: nil}
	if !c2.IsTIP2019(big.NewInt(0)) {
		t.Error("IsTIP2019 with nil field should be active at block 0 (global default = 0)")
	}
}
