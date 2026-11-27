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

package legacypool

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// TestShortDataTxToBlockSignersRejected checks XDC-HUNT-23: a paid tx to 0x89
// with fewer than 68 bytes of calldata is never mined, so it must not enter the
// pool. A tx with a full sign call still does.
func TestShortDataTxToBlockSignersRejected(t *testing.T) {
	pool, closePool := xdcSpecialPool(t)
	defer closePool()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	pool.IsSigner = func(a common.Address) bool { return a == addr }
	pool.currentState.AddBalance(addr, uint256.NewInt(1_000_000_000_000_000_000), tracing.BalanceChangeUnspecified)

	// A paying tx, so the tip check passes; only the short calldata is wrong.
	short := types.NewTransaction(0, common.BlockSignersBinary, big.NewInt(0), 100000, big.NewInt(2_000_000_000), []byte{0x01, 0x02, 0x03, 0x04})
	signed, err := types.SignTx(short, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	if errs := pool.Add([]*types.Transaction{signed}, true); !errors.Is(errs[0], txpool.ErrSpecialTxShortData) {
		t.Fatalf("short calldata err = %v, want ErrSpecialTxShortData", errs[0])
	}
	if p, q := pool.Stats(); p+q != 0 {
		t.Fatalf("pool holds %d pending and %d queued, want none", p, q)
	}

	sign := makeSpecialTx(0, common.BlockSignersBinary, key)
	if errs := pool.Add([]*types.Transaction{sign}, true); errs[0] != nil {
		t.Fatalf("sign selector: %v", errs[0])
	}
}
