// Copyright (c) 2026 XDC Network Authors

package legacypool

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// TestF202620477NonZeroCostSigningTxDoesNotPanicOnForward is the regression
// for a masternode signing transaction with a non-zero cost. Admission must
// reject it, and the signer fast path must keep list.totalcost in step with
// the transactions it stores so Forward does not underflow.
func TestF202620477NonZeroCostSigningTxDoesNotPanicOnForward(t *testing.T) {
	t.Parallel()

	pool, closePool := xdcSpecialPool(t)
	defer closePool()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	pool.IsSigner = func(a common.Address) bool { return a == addr }

	t.Run("admission rejects non-zero value", func(t *testing.T) {
		tx := pricedSigningTx(t, key, 0, big.NewInt(1), big.NewInt(0))
		errs := pool.Add([]*types.Transaction{tx}, true)
		if !errors.Is(errs[0], txpool.ErrSpecialTxNonZeroCost) {
			t.Fatalf("Add err = %v, want ErrSpecialTxNonZeroCost", errs[0])
		}
	})
	t.Run("admission rejects non-zero gas price", func(t *testing.T) {
		tx := pricedSigningTx(t, key, 0, big.NewInt(0), big.NewInt(1))
		errs := pool.Add([]*types.Transaction{tx}, true)
		if !errors.Is(errs[0], txpool.ErrSpecialTxNonZeroCost) {
			t.Fatalf("Add err = %v, want ErrSpecialTxNonZeroCost", errs[0])
		}
	})

	// The fast path itself must not panic if a non-zero-cost signing tx is
	// inserted and the nonce then moves past it (block import).
	tx := pricedSigningTx(t, key, 0, big.NewInt(1), big.NewInt(0))
	pool.mu.Lock()
	defer pool.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Forward panicked: %v", r)
		}
	}()
	if _, err := pool.promoteSpecialTx(addr, tx); err != nil {
		t.Fatalf("promoteSpecialTx: %v", err)
	}
	list := pool.pending[addr]
	if list.totalcost.Cmp(uint256.MustFromBig(tx.Cost())) != 0 {
		t.Fatalf("totalcost after insert = %s, want %s", list.totalcost, tx.Cost())
	}
	removed := list.Forward(tx.Nonce() + 1)
	if len(removed) != 1 {
		t.Fatalf("Forward removed %d txs, want 1", len(removed))
	}
	if !list.totalcost.IsZero() {
		t.Fatalf("totalcost after Forward = %s, want 0", list.totalcost)
	}
}

func pricedSigningTx(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, value, gasPrice *big.Int) *types.Transaction {
	t.Helper()
	data := common.Hex2Bytes(common.HexSignMethod)
	blockNum := new(big.Int).SetUint64(1)
	input := append(data, common.LeftPadBytes(blockNum.Bytes(), 32)...)
	input = append(input, common.LeftPadBytes(blockNum.Bytes(), 32)...)
	raw := types.NewTransaction(nonce, common.BlockSignersBinary, value, 21000, gasPrice, input)
	tx, err := types.SignTx(raw, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
