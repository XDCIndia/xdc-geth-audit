// Copyright (c) 2026 XDC Network Authors
//
// The error value of the XDC Gas2500x tier min-gas-price rejection; kept apart
// from legacypool_xdc_tier_gas_test.go because it names the new sentinel.

package legacypool

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/core/txpool"
)

// TestXDCTierMinGasPrice_BelowFloorError checks that the below-floor rejection
// carries txpool.ErrUnderMinGasPrice, as canonical XDPoSChain's pool does.
func TestXDCTierMinGasPrice_BelowFloorError(t *testing.T) {
	t.Parallel()

	if err := addBelowFloor(t); !errors.Is(err, txpool.ErrUnderMinGasPrice) {
		t.Fatalf("want ErrUnderMinGasPrice, got %v", err)
	}
}
