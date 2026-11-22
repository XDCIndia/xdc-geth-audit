// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-07 (#2060): the built-in XDC configs, written to the database as JSON
// and read back by the next start, must be compatible with themselves.

package params

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestCheckXDCCompatibleBuiltinConfigsRoundTrip(t *testing.T) {
	for name, cfg := range map[string]*ChainConfig{
		"mainnet":   XDCMainnetChainConfig,
		"apothem":   XDCApothemChainConfig,
		"devnet":    XDCDevnetChainConfig,
		"devnet551": XDCDevnet551ChainConfig,
	} {
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var stored ChainConfig
		if err := json.Unmarshal(data, &stored); err != nil {
			t.Fatal(err)
		}
		for _, head := range []int64{1, 1_000_000, 90_000_000, 200_000_000} {
			if err := stored.CheckXDCCompatible(cfg, big.NewInt(head)); err != nil {
				t.Errorf("%s at head %d: %v", name, head, err)
			}
		}
	}
}
