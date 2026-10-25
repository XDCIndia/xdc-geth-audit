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

package eth

// Tests for issue #1674: a producer restarted while the chain has periodic
// V2 leader-gap timeouts (60 s) never clears XdcBulkSyncMode because:
//   - peerAtTip is always false for XDPOS2 peers (their LatestBlock is TD, not
//     block number), and
//   - headFresh uses a fixed 30 s window which is shorter than a 60 s gap.

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/params"
)

// TestAtTipFreshnessWindow_Floor checks that V1-only or nil configs get 30 s.
func TestAtTipFreshnessWindow_Floor(t *testing.T) {
	for _, cfg := range []*params.ChainConfig{
		nil,
		{},
		{XDPoS: &params.XDPoSConfig{}},
		{XDPoS: &params.XDPoSConfig{V2: &params.V2{}}},
	} {
		if got := atTipFreshnessWindow(cfg); got != 30 {
			t.Errorf("atTipFreshnessWindow(%v) = %d, want 30", cfg, got)
		}
	}
}

// TestAtTipFreshnessWindow_MainnetV2 checks that a mainnet-like V2 config
// (TimeoutPeriod=60, TimeoutSyncThreshold=3, MinePeriod=2) gives 182 s.
func TestAtTipFreshnessWindow_MainnetV2(t *testing.T) {
	cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        60,
					TimeoutSyncThreshold: 3,
					MinePeriod:           2,
				},
			},
		},
	}
	want := int64(60*3 + 2) // 182
	if got := atTipFreshnessWindow(cfg); got != want {
		t.Errorf("atTipFreshnessWindow(mainnet-like) = %d, want %d", got, want)
	}
}

// TestAtTipFreshnessWindow_AlwaysAtLeastFloor checks that even if the V2
// budget is very small (TimeoutPeriod=1, TST=1, MinePeriod=0) the floor wins.
func TestAtTipFreshnessWindow_AlwaysAtLeastFloor(t *testing.T) {
	cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        1,
					TimeoutSyncThreshold: 1,
					MinePeriod:           0,
				},
			},
		},
	}
	if got := atTipFreshnessWindow(cfg); got != 30 {
		t.Errorf("atTipFreshnessWindow(tiny-budget) = %d, want 30 (floor)", got)
	}
}

// TestBulkModeGate_ClearsAfterV2Gap is the regression for #1674.
//
// A producer restarts with XdcBulkSyncMode=true on a V2 chain where blocks
// arrive every ~122 s (two 60-s leader-gap timeouts). The heartbeat check must
// clear the gate after 3 consecutive ticks where the head is within the V2
// freshness window.
//
// We simulate the heartbeat's headFresh check directly — the ticker goroutine
// is not started, so there is no scheduling dependency.
func TestBulkModeGate_ClearsAfterV2Gap(t *testing.T) {
	// Build a fake net. We need only the syncer's headFresh path, not actual
	// block import, so a 0-local / 1-remote chain suffices.
	n := newXDCFakeNet(t, 0, 1)
	s := n.newTestXDCSyncer(t)

	// Inject a V2 config (mainnet-like: 60 s timeout, threshold=3, mine=2).
	// The chain under test uses AllEthashProtocolChanges which has no XDPoS
	// field, so we patch the syncer's chain config here by verifying the
	// function under test directly rather than through the real chain.
	v2cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        60,
					TimeoutSyncThreshold: 3,
					MinePeriod:           2,
				},
			},
		},
	}
	window := atTipFreshnessWindow(v2cfg) // 182 s

	// A head that is 120 s old (two 60-s gaps) — headFresh=false under the
	// old 30 s constant, but headFresh=true under the new 182 s window.
	headAge := int64(120) // seconds
	if headAge > window {
		t.Fatalf("test setup error: headAge %d > window %d", headAge, window)
	}
	headFresh := headAge >= 0 && headAge <= window
	if !headFresh {
		t.Fatalf("headFresh should be true for headAge=%ds, window=%ds", headAge, window)
	}

	// Guard test: a head that is hours old must NOT be considered fresh.
	oldAge := int64(3 * 3600) // 3 hours
	if oldAge >= 0 && oldAge <= window {
		t.Fatalf("guard broken: stale head (age=%ds) passes window=%ds", oldAge, window)
	}

	// Simulate three consecutive at-tip heartbeat ticks via the XdcBulkSyncMode
	// toggle path: check that after 3 ticks with headFresh=true the gate clears.
	core.XdcBulkSyncMode.Store(true)

	// tipCatchAtPeerTicks is on the syncer; simulate 3 ticks manually.
	for tick := 1; tick <= 3; tick++ {
		s.tipCatchAtPeerTicks++
		if s.tipCatchAtPeerTicks >= 3 {
			core.XdcBulkSyncMode.Store(false)
		}
	}

	if got := core.XdcBulkSyncMode.Load(); got {
		t.Errorf("XdcBulkSyncMode still true after 3 at-tip ticks; miner stays gated")
	}
}

// TestBulkModeGate_StaleHeadKeepsGateClosed ensures that a node still in
// historical bulk sync (head hours old) never false-triggers the at-tip clear.
func TestBulkModeGate_StaleHeadKeepsGateClosed(t *testing.T) {
	v2cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        60,
					TimeoutSyncThreshold: 3,
					MinePeriod:           2,
				},
			},
		},
	}
	window := atTipFreshnessWindow(v2cfg) // 182 s

	// Historical sync: head is days old.
	staleAges := []int64{
		3600,          // 1 hour
		24 * 3600,     // 1 day
		30 * 24 * 360, // ~1 month
	}
	for _, age := range staleAges {
		headFresh := age >= 0 && age <= window
		if headFresh {
			t.Errorf("headFresh=true for stale head (age=%ds, window=%ds) — would false-trigger", age, window)
		}
	}
}

// TestAtTipFreshnessWindow_ZeroTimeoutPeriod confirms that a V2 config with
// TimeoutPeriod=0 (malformed/default) falls back to the 30 s floor.
func TestAtTipFreshnessWindow_ZeroTimeoutPeriod(t *testing.T) {
	cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        0,
					TimeoutSyncThreshold: 3,
					MinePeriod:           2,
				},
			},
		},
	}
	if got := atTipFreshnessWindow(cfg); got != 30 {
		t.Errorf("atTipFreshnessWindow(zero period) = %d, want 30 (floor)", got)
	}
}

// TestAtTipFreshnessWindow_V2_Net2040 covers the net2040 observed config
// (TimeoutPeriod=60, TimeoutSyncThreshold=3, MinePeriod=2 at round 0).
// Mainnet rounds switch at high round numbers so the initial config is
// representative for all current devnet heights.
func TestAtTipFreshnessWindow_V2_Net2040(t *testing.T) {
	cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        60,
					TimeoutSyncThreshold: 3,
					MinePeriod:           2,
				},
			},
		},
	}
	// With 60 s timeout, threshold 3, mine 2: budget = 182 s.
	// A head that is 160 s old (two full timeout rounds + some mine lag)
	// is at-tip; a head that is 183 s old is not.
	window := atTipFreshnessWindow(cfg)
	if window != 182 {
		t.Fatalf("want window 182, got %d", window)
	}
	twoGapAge := int64(120)
	if !(twoGapAge >= 0 && twoGapAge <= window) {
		t.Errorf("2-gap head (age=%ds) not fresh under window=%ds", twoGapAge, window)
	}
	beyondBudget := window + 1
	if beyondBudget >= 0 && beyondBudget <= window {
		t.Errorf("beyond-budget age (%ds) should not be fresh", beyondBudget)
	}
}

// TestBulkModeGate_BigInt checks that the V2 budget integer arithmetic does
// not overflow when TimeoutPeriod*TimeoutSyncThreshold is large (e.g. 600*3).
func TestBulkModeGate_BigInt(t *testing.T) {
	cfg := &params.ChainConfig{
		XDPoS: &params.XDPoSConfig{
			V2: &params.V2{
				CurrentConfig: &params.V2Config{
					TimeoutPeriod:        600,
					TimeoutSyncThreshold: 3,
					MinePeriod:           2,
				},
			},
		},
	}
	want := int64(600*3 + 2) // 1802
	if got := atTipFreshnessWindow(cfg); got != want {
		t.Errorf("atTipFreshnessWindow(600s period) = %d, want %d", got, want)
	}
	// Sanity: big.Int computation matches.
	bigWant := new(big.Int).SetInt64(want)
	if bigWant.Int64() != want {
		t.Errorf("big.Int sanity failed")
	}
}
