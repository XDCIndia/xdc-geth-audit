// Copyright (c) 2026 XDC Network

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/trie"
)

// TestF202620610ReceiptRootRequired checks that snapBlockComplete rejects a
// stored receipt list whose root differs from header.ReceiptHash, even when
// its length equals the transaction count.
func TestF202620610ReceiptRootRequired(t *testing.T) {
	// A typed transaction and a log: the storage form of the receipt keeps
	// neither the type nor the bloom, and the root still has to match.
	tx := types.NewTx(&types.DynamicFeeTx{Nonce: 1, GasFeeCap: big.NewInt(1), Gas: 21000, To: &common.Address{1}})
	receipts := types.Receipts{{
		Type: types.DynamicFeeTxType, Status: types.ReceiptStatusSuccessful, CumulativeGasUsed: 21000,
		Logs: []*types.Log{{Address: common.Address{2}, Topics: []common.Hash{{3}}}},
	}}
	receipts[0].Bloom = types.CreateBloom(receipts[0])
	receiptRoot := types.DeriveSha(receipts, trie.NewStackTrie(nil))

	build := func(rcptHash common.Hash) (ethdb.Database, *types.Header) {
		db := rawdb.NewMemoryDatabase()
		h := &types.Header{
			Number:      big.NewInt(5),
			TxHash:      types.DeriveSha(types.Transactions{tx}, trie.NewStackTrie(nil)),
			UncleHash:   types.EmptyUncleHash,
			ReceiptHash: rcptHash,
		}
		rawdb.WriteHeader(db, h)
		rawdb.WriteCanonicalHash(db, h.Hash(), 5)
		rawdb.WriteBody(db, h.Hash(), 5, &types.Body{Transactions: types.Transactions{tx}})
		rawdb.WriteReceipts(db, h.Hash(), 5, receipts)
		return db, h
	}

	db, h := build(common.HexToHash("0xdead"))
	if snapBlockComplete(db, h.Hash(), 5, h.TxHash) {
		t.Fatal("receipts that do not hash to header.ReceiptHash counted as complete")
	}
	db, h = build(receiptRoot)
	if !snapBlockComplete(db, h.Hash(), 5, h.TxHash) {
		t.Fatal("receipts matching header.ReceiptHash not counted as complete")
	}
}
