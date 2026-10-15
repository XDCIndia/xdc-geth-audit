// Copyright (c) 2024 XDC Network
// Test for #1478: ForensicsProcessor must be built and wired.

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestForensicsProcessorBuilt verifies that createEngine initialises a non-nil
// ForensicsProcessor.  Fails on origin/main (field absent); passes with the fix.
func TestForensicsProcessorBuilt(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch:            900,
			RewardCheckpoint: 900,
			Gap:              5,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
				CurrentConfig: &params.V2Config{
					MinePeriod:    2,
					TimeoutPeriod: 10,
					ExpTimeoutConfig: params.ExpTimeoutConfig{
						Base:        2.0,
						MaxExponent: 6,
					},
					CertThreshold: 0.667,
				},
				AllConfigs: map[uint64]*params.V2Config{},
			},
		},
	}
	eng := createEngine(cfg, nil, nil, nil)
	if eng.ForensicsProcessor == nil {
		t.Fatal("ForensicsProcessor is nil after createEngine — fix #1478 not applied")
	}
}
