// Copyright 2026 XDC Network
// Unit test for the resolvePostUpgradeLimitPenaltyEpoch helper (fix for #1471).
//
// Fails on origin/main: function does not exist (compile error).
// Passes with fix: function exists and returns correct values.

package hooks

import (
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

func TestResolvePostUpgradeLimitPenaltyEpoch(t *testing.T) {
	// nil config → fallback 1
	if got := resolvePostUpgradeLimitPenaltyEpoch(nil); got != 1 {
		t.Fatalf("nil cfg: want 1, got %d", got)
	}
	// V2Config with LimitPenaltyEpoch=5 (apothem) → 5
	if got := resolvePostUpgradeLimitPenaltyEpoch(&params.V2Config{LimitPenaltyEpoch: 5}); got != 5 {
		t.Fatalf("cfg.LimitPenaltyEpoch=5: want 5, got %d", got)
	}
	// V2Config with LimitPenaltyEpoch=0 (unset) → fallback 1
	if got := resolvePostUpgradeLimitPenaltyEpoch(&params.V2Config{LimitPenaltyEpoch: 0}); got != 1 {
		t.Fatalf("cfg.LimitPenaltyEpoch=0: want 1 (fallback), got %d", got)
	}
	// Non-zero arbitrary value passes through
	if got := resolvePostUpgradeLimitPenaltyEpoch(&params.V2Config{LimitPenaltyEpoch: 3}); got != 3 {
		t.Fatalf("cfg.LimitPenaltyEpoch=3: want 3, got %d", got)
	}
}
