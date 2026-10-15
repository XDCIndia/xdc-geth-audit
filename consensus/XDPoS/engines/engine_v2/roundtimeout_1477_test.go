// Copyright (c) 2024 XDC Network
// Test for #1477: setNewRound must update per-round timeout config.

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestSetNewRoundUpdatesTimeoutConfig verifies that setNewRound picks up the
// per-round V2 config so the pacemaker uses the correct TimeoutPeriod and not
// the round-0 default.  Fails on origin/main (no UpdateConfig call in
// setNewRound); passes with the fix.
func TestSetNewRoundUpdatesTimeoutConfig(t *testing.T) {
	const (
		period0   = 10 // TimeoutPeriod for round 0 row
		period100 = 60 // TimeoutPeriod for the row starting at round 100
		testRound = 101
	)

	cfg0 := &params.V2Config{
		SwitchRound:      0,
		MinePeriod:       2,
		TimeoutPeriod:    period0,
		ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 2.0, MaxExponent: 6},
		CertThreshold:    0.667,
	}
	cfg100 := &params.V2Config{
		SwitchRound:      100,
		MinePeriod:       2,
		TimeoutPeriod:    period100,
		ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 2.0, MaxExponent: 6},
		CertThreshold:    0.667,
	}

	chainCfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch:            900,
			RewardCheckpoint: 900,
			Gap:              5,
			V2: &params.V2{
				SwitchBlock:   big.NewInt(0),
				CurrentConfig: cfg0,
				AllConfigs: map[uint64]*params.V2Config{
					0:   cfg0,
					100: cfg100,
				},
			},
		},
	}

	// createEngine builds the config index internally; pass nil channels so the
	// goroutine that drains minePeriodCh (if any) is skipped cleanly.
	eng := createEngine(chainCfg, nil, nil, nil)

	// Before setNewRound: CurrentConfig must be the round-0 row.
	if got := eng.config.V2.CurrentConfig.TimeoutPeriod; got != period0 {
		t.Fatalf("pre-condition: expected TimeoutPeriod %d, got %d", period0, got)
	}

	// Advance to a round that falls into the second config row.
	eng.setNewRound(nil, testRound)

	// After setNewRound: CurrentConfig must reflect the row active at round 101.
	got := eng.config.V2.CurrentConfig.TimeoutPeriod
	if got != period100 {
		t.Errorf("after setNewRound(%d): TimeoutPeriod = %d, want %d (fix #1477)",
			testRound, got, period100)
	}
}
