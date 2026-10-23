// Copyright 2026 The XDC Network Authors
// Tests that the CertiK fork-gated rules stay off on registered networks.

package params

import (
	"math/big"
	"testing"
)

// TestCertikRulesPlaceholderOnRegisteredNetworks checks that mainnet and
// Apothem carry only the far-future placeholder for the CertiK rules (#1721,
// #1722, #1723) until the real blocks are scheduled together with legacy.
func TestCertikRulesPlaceholderOnRegisteredNetworks(t *testing.T) {
	placeholder := big.NewInt(99_999_999_999)
	for name, cfg := range map[string]*ChainConfig{
		"mainnet": XDCMainnetChainConfig,
		"apothem": XDCApothemChainConfig,
	} {
		for field, b := range map[string]*big.Int{
			"SigningTxBoundBlock": cfg.SigningTxBoundBlock,
			"ValidatorOrderBlock": cfg.ValidatorOrderBlock,
			"ParoleDedupBlock":    cfg.ParoleDedupBlock,
		} {
			if b == nil || b.Cmp(placeholder) != 0 {
				t.Errorf("%s: %s = %v, want placeholder %v", name, field, b, placeholder)
			}
		}
		n := big.NewInt(200_000_000)
		if cfg.IsSigningTxBound(n) || cfg.IsValidatorOrder(n) || cfg.IsParoleDedup(n) {
			t.Errorf("%s: a CertiK rule is active at block %v", name, n)
		}
	}
}
