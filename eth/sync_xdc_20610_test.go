// Copyright 2026 The go-ethereum Authors
// F-2026-20610: the empty body and empty receipts that the fast-sync anchor
// writes for every header are not a finished block when the header carries
// transactions.

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestF202620610AnchorPlaceholderIsNotAFinishedBlock(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	h := &types.Header{
		Number:    big.NewInt(5),
		TxHash:    types.EmptyTxsHash,
		UncleHash: types.EmptyUncleHash,
	}
	hash := h.Hash()
	num := h.Number.Uint64()

	if snapBlockComplete(db, hash, num, h.TxHash) {
		t.Fatal("not complete before the header is written")
	}
	rawdb.WriteHeader(db, h)
	rawdb.WriteCanonicalHash(db, hash, num)
	if snapBlockComplete(db, hash, num, h.TxHash) {
		t.Fatal("an empty-tx block is not complete without its body record")
	}
	rawdb.WriteBody(db, hash, num, &types.Body{})
	if !snapBlockComplete(db, hash, num, h.TxHash) {
		t.Fatal("an empty-tx block is complete with its header and empty body")
	}

	// The anchor placeholder: empty body and empty receipts for a header that
	// names transactions.
	nonEmptyTxHash := common.HexToHash("0x1111")
	if snapBlockComplete(db, hash, num, nonEmptyTxHash) {
		t.Fatal("an empty body is not a block with a non-empty tx hash")
	}
	rawdb.WriteReceipts(db, hash, num, types.Receipts{})
	if snapBlockComplete(db, hash, num, nonEmptyTxHash) {
		t.Fatal("empty body and empty receipts are not a block with a non-empty tx hash")
	}
}
