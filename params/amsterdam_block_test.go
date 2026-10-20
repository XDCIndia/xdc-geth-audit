// Tests for the XDC block-gated Amsterdam fork (AmsterdamBlock).
//
// Amsterdam on XDC (net2030 target: 31,500) uses a block-number gate rather
// than the upstream timestamp-based AmsterdamTime. These tests verify:
//   - Mainnet and Apothem carry only the far-future AmsterdamBlock placeholder
//     (99,999,999,999, #1644); devnet and the default private profile never
//     get AmsterdamBlock set (IsAmsterdam returns false unless genesis sets it).
//   - IsAmsterdamBlock and IsAmsterdam respond correctly to a set block.
//   - IsAmsterdam on XDC ignores any AmsterdamTime and uses the block gate.
//   - Amsterdam must follow Osaka in CheckConfigForkOrder.
//   - Rules.IsAmsterdam is false for XDC before the block, true at/after.
//
// Canonical XDPoSChain v3.0.0-testnet has NO Amsterdam. These are
// modern-only, self-consistent checks — not canonical parity tests.
package params_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// amstNet returns a minimal XDC ChainConfig with an OsakaBlock and
// AmsterdamBlock set for testing.
func amstNet(osakaBlock, amsterdamBlock int64) *params.ChainConfig {
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(2030),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		Eip1559Block:        big.NewInt(3600),
		CancunBlock:         big.NewInt(4050),
		PragueBlock:         big.NewInt(6300),
		XDPoS: &params.XDPoSConfig{
			Period: 2,
			Epoch:  900,
			V2: &params.V2{
				SwitchBlock: big.NewInt(2700),
				SwitchEpoch: 3,
				AllConfigs:  params.MainnetV2Configs,
			},
		},
	}
	if osakaBlock >= 0 {
		cfg.OsakaBlock = big.NewInt(osakaBlock)
	}
	if amsterdamBlock >= 0 {
		cfg.AmsterdamBlock = big.NewInt(amsterdamBlock)
	}
	return cfg
}

// TestAmsterdamBlockNotSetOnRegisteredNetworks verifies that the built-in
// mainnet, Apothem and devnet configs carry no AmsterdamBlock — Amsterdam
// must be off by default on every existing XDC chain.
func TestAmsterdamBlockNotSetOnRegisteredNetworks(t *testing.T) {
	for name, cfg := range map[string]*params.ChainConfig{
		"mainnet": params.XDCMainnetChainConfig,
		"apothem": params.XDCApothemChainConfig,
		"devnet":  params.XDCDevnetChainConfig,
	} {
		// Mainnet and Apothem may only carry the far-future placeholder (#1644).
		placeholder := name != "devnet" && cfg.AmsterdamBlock != nil && cfg.AmsterdamBlock.Cmp(big.NewInt(99_999_999_999)) == 0
		if cfg.AmsterdamBlock != nil && !placeholder {
			t.Errorf("%s: AmsterdamBlock should be nil or the 99,999,999,999 placeholder (Amsterdam not yet scheduled), got %v", name, cfg.AmsterdamBlock)
		}
		// IsAmsterdam must return false at any realistic height, regardless of time.
		for _, n := range []int64{1_000_000, 200_000_000} {
			if cfg.IsAmsterdam(big.NewInt(n), ^uint64(0)) {
				t.Errorf("%s: IsAmsterdam(%d, maxUint64) returned true; must be false until Amsterdam is scheduled", name, n)
			}
		}
	}
}

// TestIsAmsterdamBlockActivation checks that IsAmsterdamBlock and IsAmsterdam
// activate exactly at (and after) the configured block number on an XDC chain.
func TestIsAmsterdamBlockActivation(t *testing.T) {
	const (
		osakaAt     = int64(6750)
		amsterdamAt = int64(31500)
	)
	cfg := amstNet(osakaAt, amsterdamAt)

	for _, tt := range []struct {
		num  int64
		want bool
	}{
		{0, false},
		{31499, false},
		{31500, true},
		{31501, true},
		{1_000_000, true},
	} {
		num := big.NewInt(tt.num)
		// IsAmsterdamBlock (direct)
		if got := cfg.IsAmsterdamBlock(num); got != tt.want {
			t.Errorf("IsAmsterdamBlock(%d): got %v, want %v", tt.num, got, tt.want)
		}
		// IsAmsterdam on XDC must use the block gate (timestamp is irrelevant).
		// We pass time=0 and time=max to prove timestamp is ignored.
		for _, ts := range []uint64{0, ^uint64(0)} {
			if got := cfg.IsAmsterdam(num, ts); got != tt.want {
				t.Errorf("IsAmsterdam(%d, ts=%d): got %v, want %v", tt.num, ts, got, tt.want)
			}
		}
	}
}

// TestAmsterdamTimeIgnoredOnXDC proves that AmsterdamTime has no effect on XDC
// chains — only AmsterdamBlock matters.
func TestAmsterdamTimeIgnoredOnXDC(t *testing.T) {
	cfg := amstNet(6750, 31500)
	// Set a non-nil AmsterdamTime; it must not affect the outcome (the XDC
	// path dispatches to IsAmsterdamBlock, not isTimestampForked).
	ts := uint64(0)
	cfg.AmsterdamTime = &ts

	num := big.NewInt(100) // before AmsterdamBlock=31500
	if cfg.IsAmsterdam(num, 0) {
		t.Error("IsAmsterdam returned true for block<31500 even with AmsterdamTime=0; XDC must use block gate")
	}

	num = big.NewInt(31500)
	if !cfg.IsAmsterdam(num, 0) {
		t.Error("IsAmsterdam returned false at block 31500 with AmsterdamBlock=31500")
	}
}

// TestAmsterdamBlockAfterOsaka verifies that CheckConfigForkOrder rejects a
// config where AmsterdamBlock < OsakaBlock.
func TestAmsterdamBlockAfterOsaka(t *testing.T) {
	cfg := amstNet(6750, 31500)
	if err := cfg.CheckConfigForkOrder(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	// Amsterdam before Osaka — must error.
	bad := amstNet(31500, 1000)
	if err := bad.CheckConfigForkOrder(); err == nil {
		t.Error("expected error for Amsterdam < Osaka, got nil")
	}
}

// TestRulesAmsterdamXDC checks that Rules.IsAmsterdam is false before the
// fork block and true at/after it, and is not affected by isMerge=false.
func TestRulesAmsterdamXDC(t *testing.T) {
	cfg := amstNet(6750, 31500)

	before := big.NewInt(31499)
	at := big.NewInt(31500)

	// isMerge=false simulates XDC callers (txpool, non-merge chains).
	rBefore := cfg.Rules(before, false, 0)
	rAt := cfg.Rules(at, false, 0)

	if rBefore.IsAmsterdam {
		t.Error("Rules.IsAmsterdam should be false before AmsterdamBlock")
	}
	if !rAt.IsAmsterdam {
		t.Error("Rules.IsAmsterdam should be true at AmsterdamBlock, even with isMerge=false")
	}
}
