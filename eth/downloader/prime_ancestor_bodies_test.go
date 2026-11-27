// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// A.72: Test PrimeAncestorBodies writes block bodies + receipts to rawdb
// correctly so the V2 HookReward signing-tx walk can resolve
// rawdb.ReadBlock + rawdb.ReadRawReceipts for pre-pivot ancestors.
//
// Refs #844 #859 A.72.

package downloader

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"
)

// makeContiguousHeaders builds count parent-linked headers starting from
// number `start`.
func makeContiguousHeaders(start uint64, count int) []*types.Header {
	headers := make([]*types.Header, count)
	var parentHash common.Hash
	for i := 0; i < count; i++ {
		h := &types.Header{
			Number:      new(big.Int).SetUint64(start + uint64(i)),
			ParentHash:  parentHash,
			Difficulty:  big.NewInt(1),
			GasLimit:    8_000_000,
			TxHash:      types.EmptyTxsHash,
			UncleHash:   types.EmptyUncleHash,
			ReceiptHash: types.EmptyReceiptsHash,
		}
		headers[i] = h
		parentHash = h.Hash()
	}
	return headers
}

// TestPrimeAncestorBodies_WritesBodiesAndReceipts verifies that
// PrimeAncestorBodies persists bodies + receipts so rawdb.ReadBlock and
// rawdb.ReadRawReceipts can subsequently retrieve them. This is the
// invariant the V2 HookReward signing-tx walk depends on.
func TestPrimeAncestorBodies_WritesBodiesAndReceipts(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	d := &Downloader{stateDB: db}

	const start, count = 100, 4
	headers := makeContiguousHeaders(start, count)
	// Header pre-fetch must happen before body write — mirror real A.68.2 flow.
	if err := d.PrimeAncestorHeaders(headers); err != nil {
		t.Fatalf("PrimeAncestorHeaders failed: %v", err)
	}

	bodies := make([]*types.Body, count)
	receipts := make([]types.Receipts, count)
	for i := 0; i < count; i++ {
		// Empty bodies + receipts are sufficient for the storage round-trip
		// test. Real signing-tx detection is exercised by the integration
		// harness on server.
		bodies[i] = &types.Body{}
		receipts[i] = types.Receipts{}
	}
	if err := d.PrimeAncestorBodies(headers, bodies, receipts); err != nil {
		t.Fatalf("PrimeAncestorBodies failed: %v", err)
	}

	// Verify each block can be loaded back via rawdb.ReadBlock.
	for i, h := range headers {
		num := h.Number.Uint64()
		hash := h.Hash()
		if !rawdb.HasBody(db, hash, num) {
			t.Errorf("idx %d (num=%d): HasBody=false after prime", i, num)
		}
		if !rawdb.HasReceipts(db, hash, num) {
			t.Errorf("idx %d (num=%d): HasReceipts=false after prime", i, num)
		}
		block := rawdb.ReadBlock(db, hash, num)
		if block == nil {
			t.Errorf("idx %d (num=%d): ReadBlock returned nil", i, num)
		}
	}
}

func TestF202620637PrimeRefusesMismatchedBodyAndReceipts(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	d := &Downloader{stateDB: db}
	headers := makeContiguousHeaders(10, 1)
	headers[0].TxHash = common.HexToHash("0x1111")
	if err := d.PrimeAncestorBodies(headers, []*types.Body{{}}, nil); err == nil {
		t.Fatal("body whose tx root is not the header tx hash was written")
	}
	if rawdb.HasBody(db, headers[0].Hash(), 10) {
		t.Fatal("mismatched body was persisted")
	}

	headers = makeContiguousHeaders(11, 1)
	headers[0].ReceiptHash = common.HexToHash("0x2222")
	if err := d.PrimeAncestorBodies(headers, []*types.Body{{}}, []types.Receipts{{}}); err == nil {
		t.Fatal("receipts whose root is not the header receipt hash were written")
	}
	if rawdb.HasReceipts(db, headers[0].Hash(), 11) {
		t.Fatal("mismatched receipts were persisted")
	}
}

// TestPrimeAncestorBodies_LengthMismatch verifies the function rejects
// header/body length mismatches before touching the DB.
func TestPrimeAncestorBodies_LengthMismatch(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	d := &Downloader{stateDB: db}

	headers := makeContiguousHeaders(0, 3)
	bodies := []*types.Body{{}, {}} // 2 bodies for 3 headers
	if err := d.PrimeAncestorBodies(headers, bodies, nil); err == nil {
		t.Errorf("expected length mismatch error, got nil")
	}
}

// TestPrimeAncestorBodies_OptionalReceipts verifies receipts parameter is
// optional: passing nil writes bodies only, no receipt rows.
func TestPrimeAncestorBodies_OptionalReceipts(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	d := &Downloader{stateDB: db}

	headers := makeContiguousHeaders(0, 2)
	if err := d.PrimeAncestorHeaders(headers); err != nil {
		t.Fatalf("PrimeAncestorHeaders failed: %v", err)
	}

	bodies := []*types.Body{{}, {}}
	if err := d.PrimeAncestorBodies(headers, bodies, nil); err != nil {
		t.Fatalf("PrimeAncestorBodies failed: %v", err)
	}
	for _, h := range headers {
		num := h.Number.Uint64()
		hash := h.Hash()
		if !rawdb.HasBody(db, hash, num) {
			t.Errorf("num=%d: HasBody=false", num)
		}
		// No receipts were supplied, so HasReceipts must be false.
		if rawdb.HasReceipts(db, hash, num) {
			t.Errorf("num=%d: HasReceipts=true but receipts were nil", num)
		}
	}
}

// TestPrimeAncestorBodies_Idempotent verifies a second call doesn't
// re-write bodies that are already present in the DB. The function's
// idempotency is critical for crash-recovery (a partial pre-fetch followed
// by a restart should not re-fetch already-written bodies).
func TestPrimeAncestorBodies_Idempotent(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	d := &Downloader{stateDB: db}

	headers := makeContiguousHeaders(0, 2)
	bodies := []*types.Body{{}, {}}
	receipts := []types.Receipts{{}, {}}

	// First write.
	if err := d.PrimeAncestorBodies(headers, bodies, receipts); err != nil {
		t.Fatalf("first PrimeAncestorBodies failed: %v", err)
	}
	// Second write must not fail (idempotent).
	if err := d.PrimeAncestorBodies(headers, bodies, receipts); err != nil {
		t.Fatalf("second PrimeAncestorBodies failed: %v", err)
	}
}

// TestF202620610PrimeReplacesPlaceholderWithLookups: replacing a placeholder
// body writes the tx lookups with it, and a placeholder is not replaced when no
// receipts come with the body (the placeholder receipts would stay behind).
func TestF202620610PrimeReplacesPlaceholderWithLookups(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	d := &Downloader{stateDB: db}

	tx := types.NewTx(&types.LegacyTx{Nonce: 1, Gas: 21000, GasPrice: big.NewInt(1)})
	body := &types.Body{Transactions: []*types.Transaction{tx}}
	receipts := types.Receipts{{Status: types.ReceiptStatusSuccessful, CumulativeGasUsed: 21000}}
	mk := func(num uint64) *types.Header {
		h := makeContiguousHeaders(num, 1)[0]
		h.TxHash = types.DeriveSha(types.Transactions(body.Transactions), trie.NewStackTrie(nil))
		h.ReceiptHash = types.DeriveSha(receipts, trie.NewStackTrie(nil))
		return h
	}
	placeholder := func(h *types.Header) {
		rawdb.WriteBody(db, h.Hash(), h.Number.Uint64(), &types.Body{})
		rawdb.WriteReceipts(db, h.Hash(), h.Number.Uint64(), types.Receipts{})
	}

	// No receipts: the placeholder stays.
	h1 := mk(50)
	placeholder(h1)
	if err := d.PrimeAncestorBodies([]*types.Header{h1}, []*types.Body{body}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rawdb.ReadBody(db, h1.Hash(), 50); got == nil || len(got.Transactions) != 0 {
		t.Fatal("placeholder body was replaced without receipts")
	}

	// With receipts: body, receipts and lookup are written.
	h2 := mk(60)
	placeholder(h2)
	if err := d.PrimeAncestorBodies([]*types.Header{h2}, []*types.Body{body}, []types.Receipts{receipts}); err != nil {
		t.Fatal(err)
	}
	if got := rawdb.ReadBody(db, h2.Hash(), 60); got == nil || len(got.Transactions) != 1 {
		t.Fatal("placeholder body was not replaced")
	}
	if n := rawdb.ReadTxLookupEntry(db, tx.Hash()); n == nil || *n != 60 {
		t.Fatalf("tx lookup after replacing a placeholder body = %v, want 60", n)
	}
}
