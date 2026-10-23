// Copyright 2026 The go-ethereum Authors
// CertiK XDC-11 / #1722: epoch-switch Validators must be in calcMasternodes order.
//
// Before ValidatorOrderBlock (or when the field is nil) an order mismatch is
// only logged (diagnostic F-2026-19815). After the fork, verifyHeader returns
// an error for a mismatched order.
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestValidatorOrder

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestValidatorOrder_NilField: IsValidatorOrder returns false when
// ValidatorOrderBlock is nil (all current networks). This test FAILS on
// origin/main (IsValidatorOrder is not defined) and passes after this PR.
func TestValidatorOrder_NilField(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(5151),
		ValidatorOrderBlock: nil, // disabled
	}
	if cfg.IsValidatorOrder(big.NewInt(1_000_000)) {
		t.Fatal("nil ValidatorOrderBlock: IsValidatorOrder should return false")
	}
}

// TestValidatorOrder_BeforeFork: IsValidatorOrder returns false for blocks
// before the configured fork number.
func TestValidatorOrder_BeforeFork(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(5151),
		ValidatorOrderBlock: big.NewInt(100),
	}
	if cfg.IsValidatorOrder(big.NewInt(99)) {
		t.Fatal("IsValidatorOrder should be false at block 99 when fork is 100")
	}
}

// TestValidatorOrder_AtFork: IsValidatorOrder returns true at the fork block.
// This test FAILS on origin/main and passes after this PR.
func TestValidatorOrder_AtFork(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(5151),
		ValidatorOrderBlock: big.NewInt(100),
	}
	if !cfg.IsValidatorOrder(big.NewInt(100)) {
		t.Fatal("IsValidatorOrder should be true at block 100 when fork is 100")
	}
	if !cfg.IsValidatorOrder(big.NewInt(200)) {
		t.Fatal("IsValidatorOrder should be true at block 200 when fork is 100")
	}
}

// TestValidatorOrder_NilConfig: IsValidatorOrder on a nil config returns false
// without panicking.
func TestValidatorOrder_NilConfig(t *testing.T) {
	var cfg *params.ChainConfig
	if cfg.IsValidatorOrder(big.NewInt(1)) {
		t.Fatal("nil ChainConfig: IsValidatorOrder should return false")
	}
}
