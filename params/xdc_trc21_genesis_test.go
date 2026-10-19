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

// Tests that TRC21GasPriceForBlock reads Gas50xBlock and TIPTRC21FeeBlock
// from the genesis config rather than the package-level globals. Refs #1549.

package params

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestTRC21GasPriceForBlockFromGenesisConfig verifies that TRC21GasPriceForBlock
// uses the genesis-level Gas50xBlock and TIPTRC21FeeBlock (not the global
// common.BlockNumberGas50x / common.TIPTRC21Fee) to select the gas-price tier.
//
// For a custom chain with gas50xBlock=2200, at block 2200 the 12.5 gwei tier
// must be returned. On origin/main this FAILS because the code reads
// common.BlockNumberGas50x (mainnet: 80,370,000), so 2200 >= 80370000 is false
// and the function returns TRC21GasPriceBefore instead of GasPrice50x.
//
// The test also verifies that mainnet/Apothem/devnet values are unchanged
// (their per-chain globals equal the genesis fields for registered chains).
func TestTRC21GasPriceForBlockFromGenesisConfig(t *testing.T) {
	// Custom config matching net2002 genesis gas50xBlock=2200 tipTRC21FeeBlock=2200.
	// common.BlockNumberGas50x for chain 2002 (unregistered) stays at 80,370,000.
	cfg := &ChainConfig{
		ChainID:          big.NewInt(2002),
		Gas50xBlock:      big.NewInt(2200),
		TIPTRC21FeeBlock: big.NewInt(2200),
	}

	t.Run("at Gas50xBlock returns GasPrice50x", func(t *testing.T) {
		got := TRC21GasPriceForBlock(cfg, big.NewInt(2200))
		if got.Cmp(common.GasPrice50x) != 0 {
			t.Errorf("block 2200: got %v, want GasPrice50x %v (global BlockNumberGas50x=%v — should not be used for custom genesis)",
				got, common.GasPrice50x, common.BlockNumberGas50x)
		}
	})

	t.Run("after Gas50xBlock returns GasPrice50x", func(t *testing.T) {
		got := TRC21GasPriceForBlock(cfg, big.NewInt(12134))
		if got.Cmp(common.GasPrice50x) != 0 {
			t.Errorf("block 12134: got %v, want GasPrice50x %v", got, common.GasPrice50x)
		}
	})

	t.Run("before Gas50xBlock but after TIPTRC21FeeBlock returns TRC21GasPrice", func(t *testing.T) {
		// cfg with only TIPTRC21FeeBlock=100 and no Gas50xBlock
		cfg2 := &ChainConfig{
			ChainID:          big.NewInt(2002),
			TIPTRC21FeeBlock: big.NewInt(100),
		}
		got := TRC21GasPriceForBlock(cfg2, big.NewInt(200))
		if got.Cmp(common.TRC21GasPrice) != 0 {
			t.Errorf("block 200 (after TRC21FeeBlock=100, no Gas50xBlock): got %v, want TRC21GasPrice %v",
				got, common.TRC21GasPrice)
		}
	})

	// Mainnet/Apothem/devnet must be unaffected: their global constants match
	// the registered chain values already set by CopyXDCConstants. Verify that
	// XDCMainnetChainConfig (no Gas50xBlock field set) still returns GasPrice50x
	// at the mainnet global BlockNumberGas50x.
	t.Run("mainnet unaffected: Gas50xBlock nil falls back to global", func(t *testing.T) {
		// XDCMainnetChainConfig.Gas50xBlock is nil → falls back to common.BlockNumberGas50x (80370000).
		mainnet := XDCMainnetChainConfig
		atGlobal := new(big.Int).Set(common.BlockNumberGas50x)
		got := TRC21GasPriceForBlock(mainnet, atGlobal)
		if got.Cmp(common.GasPrice50x) != 0 {
			t.Errorf("mainnet at BlockNumberGas50x=%v: got %v, want GasPrice50x %v",
				atGlobal, got, common.GasPrice50x)
		}
		// Below Gas50xBlock but above TIPTRC21Fee → TRC21GasPrice
		below := new(big.Int).Sub(common.BlockNumberGas50x, big.NewInt(1))
		above := new(big.Int).Add(common.TIPTRC21Fee, big.NewInt(1))
		if below.Cmp(above) > 0 {
			got = TRC21GasPriceForBlock(mainnet, below)
			if got.Cmp(common.TRC21GasPrice) != 0 {
				t.Errorf("mainnet below Gas50x, above TIPTRC21Fee: got %v, want TRC21GasPrice %v",
					got, common.TRC21GasPrice)
			}
		}
	})
}
