// Copyright (c) 2026 XDC Network

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

// TestF202619457OtherCalldataPaysTheTip checks that a zero-tip call to
// BlockSigners with calldata other than sign is not fee-exempt.
func TestF202619457OtherCalldataPaysTheTip(t *testing.T) {
	pool, closePool := xdcSpecialPool(t)
	defer closePool()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	pool.IsSigner = func(a common.Address) bool { return a == addr }
	pool.currentState.AddBalance(addr, uint256.NewInt(1_000_000_000_000_000_000), tracing.BalanceChangeUnspecified)

	other := types.NewTransaction(0, common.BlockSignersBinary, big.NewInt(0), 100000, big.NewInt(0), []byte{0x01, 0x02, 0x03, 0x04})
	signed, err := types.SignTx(other, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	errs := pool.Add([]*types.Transaction{signed}, true)
	if !errors.Is(errs[0], txpool.ErrTxGasPriceTooLow) {
		t.Fatalf("other calldata err = %v, want ErrTxGasPriceTooLow", errs[0])
	}

	sign := makeSpecialTx(0, common.BlockSignersBinary, key)
	if errs = pool.Add([]*types.Transaction{sign}, true); errs[0] != nil {
		t.Fatalf("sign selector: %v", errs[0])
	}
}
