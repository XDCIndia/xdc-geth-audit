// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestF202619601BlockAccessListHashIsInTheSeal checks that one proposer seal
// cannot cover two headers that differ only in BlockAccessListHash.
func TestF202619601BlockAccessListHashIsInTheSeal(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	balA := common.Hash{0x01}
	balB := common.Hash{0x02}
	h := &types.Header{
		Number:              big.NewInt(1),
		Difficulty:          big.NewInt(1),
		Extra:               []byte{0x02},
		UncleHash:           types.EmptyUncleHash,
		TxHash:              types.EmptyTxsHash,
		ReceiptHash:         types.EmptyReceiptsHash,
		BlockAccessListHash: &balA,
		Coinbase:            crypto.PubkeyToAddress(key.PublicKey),
	}
	sig, err := crypto.Sign(sigHashWith(h, true).Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	h.Validator = sig
	twin := types.CopyHeader(h)
	twin.BlockAccessListHash = &balB
	pub, err := crypto.Ecrecover(sigHashWith(twin, true).Bytes(), h.Validator)
	if err != nil {
		t.Fatal(err)
	}
	var addr common.Address
	copy(addr[:], crypto.Keccak256(pub[1:])[12:])
	if addr == h.Coinbase {
		t.Fatal("seal over one BlockAccessListHash recovered the signer for a different value")
	}
}
