// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-07 (#2060): unit tests of ChainConfig.CheckXDCCompatible.

package params

import (
	"math/big"
	"testing"
)

func TestCheckXDCCompatible(t *testing.T) {
	base := func() *ChainConfig {
		return &ChainConfig{
			TIPSigningBlock: big.NewInt(100),
			Gas2500xBlock:   big.NewInt(500),
			XDPoS: &XDPoSConfig{
				Epoch: 900, Gap: 450,
				V2: &V2{
					SwitchBlock: big.NewInt(1000),
					AllConfigs: map[uint64]*V2Config{
						0:     {SwitchRound: 0, TimeoutPeriod: 10, CertThreshold: 0.667},
						90000: {SwitchRound: 90000, TimeoutPeriod: 20},
					},
				},
			},
		}
	}
	for _, tc := range []struct {
		name    string
		head    int64
		change  func(c *ChainConfig)
		wantErr bool
	}{
		{"unchanged", 5000, func(c *ChainConfig) {}, false},
		{"passed fork moved", 5000, func(c *ChainConfig) { c.TIPSigningBlock = big.NewInt(101) }, true},
		{"passed fork removed", 5000, func(c *ChainConfig) { c.TIPSigningBlock = nil }, true},
		{"future fork moved", 400, func(c *ChainConfig) { c.Gas2500xBlock = big.NewInt(600) }, false},
		{"fork moved to a passed block", 5000, func(c *ChainConfig) { c.Gas2500xBlock = big.NewInt(300) }, true},
		{"epoch changed", 5000, func(c *ChainConfig) { c.XDPoS.Epoch = 450 }, true},
		{"gap changed", 5000, func(c *ChainConfig) { c.XDPoS.Gap = 225 }, true},
		{"V2 switch block moved", 5000, func(c *ChainConfig) { c.XDPoS.V2.SwitchBlock = big.NewInt(1100) }, true},
		{"passed V2 config changed", 5000, func(c *ChainConfig) { c.XDPoS.V2.AllConfigs[0].TimeoutPeriod = 11 }, true},
		{"passed V2 config removed", 5000, func(c *ChainConfig) { delete(c.XDPoS.V2.AllConfigs, 0) }, true},
		{"future V2 config changed", 5000, func(c *ChainConfig) { c.XDPoS.V2.AllConfigs[90000].TimeoutPeriod = 25 }, false},
		{"new V2 config entry", 5000, func(c *ChainConfig) { c.XDPoS.V2.AllConfigs[200000] = &V2Config{TimeoutPeriod: 5} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored, next := base(), base()
			tc.change(next)
			err := stored.CheckXDCCompatible(next, big.NewInt(tc.head))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
		})
	}

	// A field that is nil or zero in the stored config is not compared, even
	// when the new config sets it at a block the chain already passed.
	stored, next := base(), base()
	stored.Gas2500xBlock = nil
	stored.XDPoS.Gap = 0
	stored.XDPoS.V2.AllConfigs[0].TimeoutPeriod = 0
	if err := stored.CheckXDCCompatible(next, big.NewInt(5000)); err != nil {
		t.Fatalf("fields missing in the stored config: %v", err)
	}
}
