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

// Tests for MakeSigner gating type-1/2 on IsEIP1559 (not IsLondon) for XDC.
// Canonical XDPoSChain v2.9.3-testnet core/types/transaction_signing.go:41-55.

package types

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestXDCMakeSignerPreEIP1559 verifies that MakeSigner returns EIP155Signer in
// the [London=76,321,000, EIP1559=98,800,200) window on XDC mainnet.
//
// On origin/main without the fix: block 80,000,000 is past LondonBlock, so
// MakeSigner returns LondonSigner (type-2 enabled) — wrong.
// After the fix: returns EIP155Signer (no typed txs) matching canonical.
func TestXDCMakeSignerPreEIP1559(t *testing.T) {
	cfg := params.XDCMainnetChainConfig
	// Block in [London=76,321,000, EIP1559=98,800,200): IsLondon=true, IsEIP1559=false.
	blockNum := big.NewInt(80_000_000)

	signer := MakeSigner(cfg, blockNum, 0)

	want := NewEIP155Signer(cfg.ChainID)
	if !signer.Equal(want) {
		t.Fatalf("MakeSigner at block 80,000,000 (pre-EIP1559) = %T, want EIP155Signer (fix not applied?)", signer)
	}
}

// TestXDCMakeSignerPostEIP1559 verifies that MakeSigner returns LondonSigner
// at-and-after Eip1559Block on XDC mainnet.
func TestXDCMakeSignerPostEIP1559(t *testing.T) {
	cfg := params.XDCMainnetChainConfig
	blockNum := new(big.Int).Add(cfg.Eip1559Block, big.NewInt(1))

	signer := MakeSigner(cfg, blockNum, 0)

	// At EIP1559 but no Cancun yet: LondonSigner.
	want := NewLondonSigner(cfg.ChainID)
	if !signer.Equal(want) {
		t.Fatalf("MakeSigner at post-EIP1559 block = %T, want LondonSigner", signer)
	}
}
