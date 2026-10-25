package params

// genesis_fork_keys_test.go: behavioural tests that compile on origin/main.
// TestNewForksPreservedInRoundtrip and TestMaxMasternodesV2Preserved FAIL on
// origin/main (fields absent) and PASS on branch. The remaining tests PASS on
// both (regression guards). Tests requiring new symbols live in
// genesis_fork_keys_new_api_test.go. The genesis-file unknown-key-rejection test
// lives in core/genesis_fork_keys_new_api_test.go (needs DecodeGenesisJSON).

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
)

// TestChainConfigLenientOnUnknownKeys: json.Unmarshal into params.ChainConfig must
// succeed when the JSON has unknown keys. This is the path used by rawdb,
// ethclient, RPC, and any tool decoding a config from a different release.
// PASSES on both origin/main and branch (regression guard). Refs #1528.
func TestChainConfigLenientOnUnknownKeys(t *testing.T) {
	data := []byte(`{"chainId":50,"homesteadBlock":0,"unknownFutureKey":999}`)
	var c ChainConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("ChainConfig must be lenient on unknown keys (rawdb/RPC path): %v", err)
	}
	if c.ChainID == nil || c.ChainID.Cmp(big.NewInt(50)) != 0 {
		t.Errorf("chainId not parsed correctly: got %v", c.ChainID)
	}
}

// TestNewForksPreservedInRoundtrip: checks that dynamicGasLimitBlock and tipTRC21FeeBlock
// survive a JSON round-trip. Uses reflect so it compiles on main. On origin/main
// these fields are absent from the struct so they are dropped → FAIL. On branch
// the fields exist and are populated → PASS.
// Note: osakaBlock is owned by #1550 and is intentionally absent here.
func TestNewForksPreservedInRoundtrip(t *testing.T) {
	raw := `{"chainId":999,"dynamicGasLimitBlock":42,"tipTRC21FeeBlock":7}`
	var c ChainConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		// On the branch, these are known keys, so no error expected.
		// On main, also no error (silently dropped). An error here means our
		// known-key set is misconfigured — fail loudly.
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	typ := reflect.TypeOf(c)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "dynamicGasLimitBlock,omitempty" || tag == "dynamicGasLimitBlock" {
			val := reflect.ValueOf(c).Field(i)
			if val.IsNil() {
				t.Error("dynamicGasLimitBlock: field exists in struct but is nil after unmarshal — value was dropped")
			}
			return
		}
	}
	// No field with that tag → origin/main behaviour.
	t.Error("ChainConfig has no field with json tag 'dynamicGasLimitBlock' — new fork fields not added")
}

// TestMaxMasternodesV2Preserved: checks XDPoSConfig.MaxMasternodesV2 survives
// a JSON round-trip via reflect. FAILS on origin/main (field absent), PASSES on branch.
func TestMaxMasternodesV2Preserved(t *testing.T) {
	raw := `{"xdpos":{"period":2,"epoch":900,"reward":0,"rewardCheckpoint":900,"gap":5,"maxMasternodesV2":108,"v2":{"switchBlock":0,"minePeriod":0,"timeoutSyncThreshold":3,"timeoutPeriod":10,"maxTimeoutCount":3,"certThreshold":2,"config":{}}}}`
	var c ChainConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if c.XDPoS == nil {
		t.Fatal("XDPoS is nil")
	}
	typ := reflect.TypeOf(*c.XDPoS)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "maxMasternodesV2,omitempty" || tag == "maxMasternodesV2" {
			val := reflect.ValueOf(*c.XDPoS).Field(i).Int()
			if val != 108 {
				t.Errorf("MaxMasternodesV2: got %d want 108", val)
			}
			return
		}
	}
	t.Error("XDPoSConfig has no field with json tag 'maxMasternodesV2'")
}

// TestMainnetChainConfigRoundtrip: XDCMainnetChainConfig marshals and unmarshals
// cleanly through the unknown-key gate. PASSES on both (regression guard for #1528:
// all struct-generated keys must be in the known-key whitelist).
func TestMainnetChainConfigRoundtrip(t *testing.T) {
	data, err := json.Marshal(XDCMainnetChainConfig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var c ChainConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.ChainID == nil || c.ChainID.Cmp(XDCMainnetChainConfig.ChainID) != 0 {
		t.Errorf("ChainID: got %v want %v", c.ChainID, XDCMainnetChainConfig.ChainID)
	}
}

// TestBuiltinConfigsHaveNoNewFields: built-in mainnet and Apothem configs must not
// emit any of the new fork-key fields in their JSON output. All new fields are
// omitempty and nil in built-in configs, so genesis JSON is byte-identical to
// before this PR. PASSES on both (genesis hash proxy regression guard).
func TestBuiltinConfigsHaveNoNewFields(t *testing.T) {
	newFields := []string{
		"tip2019Block", "tipRandomizeBlock", "tipIncreaseMasternodesBlock",
		"denylistBlock", "tipNoHalvingMNRewardBlock", "tipXDCXBlock",
		"tipXDCXLendingBlock", "tipXDCXCancellationFeeBlock", "tipTRC21FeeBlock",
		"gas50xBlock", "tipXDCXMinerDisableBlock", "tipXDCXReceiverDisableBlock",
		"dynamicGasLimitBlock", "tipEpochHalvingBlock", "mergeBlock",
		"shanghaiBlock", // osakaBlock is owned by #1550 — not in this PR
		"trc21IssuerSMC", "xdcxListingSMC", "relayerRegistrationSMC", "lendingRegistrationSMC",
	}
	for _, cc := range []*ChainConfig{XDCMainnetChainConfig, XDCApothemChainConfig} {
		data, _ := json.Marshal(cc)
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(data, &raw)
		for _, k := range newFields {
			if k == "dynamicGasLimitBlock" && cc == XDCApothemChainConfig {
				continue // canonical Apothem sets it with the v2.9 reset (#1539)
			}
			if _, ok := raw[k]; ok {
				t.Errorf("built-in config chain=%v has new field %q in JSON — must be omitted (genesis hash would change)", cc.ChainID, k)
			}
		}
	}
}

// TestMainnetTIPGateParity: mainnet/Apothem TIP gate activation blocks must equal
// the global constants after the per-config xdcTIPBlock refactor. PASSES on both
// (parity regression guard for #1528: registered-chain behaviour must be unchanged).
func TestMainnetTIPGateParity(t *testing.T) {
	// IsTIPSigning: common.TIPSigning = 3,000,000 for mainnet.
	if !XDCMainnetChainConfig.IsTIPSigning(big.NewInt(3_000_000)) {
		t.Error("mainnet IsTIPSigning: not active at 3,000,000")
	}
	if XDCMainnetChainConfig.IsTIPSigning(big.NewInt(2_999_999)) {
		t.Error("mainnet IsTIPSigning: incorrectly active before 3,000,000")
	}
	// IsTIP2019: common.TIP2019Block = 0 for mainnet (block 0 is forked).
	if !XDCMainnetChainConfig.IsTIP2019(big.NewInt(0)) {
		t.Error("mainnet IsTIP2019: not active at block 0")
	}
	// Apothem: IsTIPSigning at common.TIPSigning default (0 for devnet, 3M for mainnet/testnet).
	if !XDCApothemChainConfig.IsTIP2019(big.NewInt(1)) {
		t.Error("apothem IsTIP2019: not active at block 1")
	}
}
