// Copyright 2026 The XDC Network Authors
// Regression test for #1839: XDPOS2 receipts served by a modern node must
// decode with the XDPOS2 receipts decoder (consensus encoding, with bloom).

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

func TestServeReceiptsXDPOS2Decodes(t *testing.T) {
	signer := types.HomesteadSigner{}
	generator := func(i int, block *core.BlockGen) {
		if i%2 == 0 { // every other block carries a transfer; the rest stay empty
			tx, _ := types.SignTx(types.NewTransaction(block.TxNonce(testAddr), testAddr, big.NewInt(1), params.TxGas, block.BaseFee(), nil), signer, testKey)
			block.AddTx(tx)
		}
	}
	backend := newTestBackendWithGenerator(6, false, false, generator)
	defer backend.close()

	var query GetReceiptsRequest
	for i := uint64(1); i <= backend.chain.CurrentBlock().Number.Uint64(); i++ {
		query = append(query, backend.chain.GetBlockByNumber(i).Hash())
	}
	served := ServiceGetReceiptsQueryXDPOS2(backend.chain, query)
	if len(served) != len(query) {
		t.Fatalf("served %d blocks, want %d", len(served), len(query))
	}
	// Decode the reply exactly as handleReceiptsXDPOS2 does.
	payload, err := rlp.EncodeToBytes(served)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []types.Receipts
	if err := rlp.DecodeBytes(payload, &decoded); err != nil {
		t.Fatalf("XDPOS2 decoder rejected a modern node's receipts: %v", err)
	}
	for i, rs := range decoded {
		header := backend.chain.GetHeaderByHash(query[i])
		if got := types.DeriveSha(rs, trie.NewStackTrie(nil)); got != header.ReceiptHash {
			t.Fatalf("block %d: receipt root %x, want %x", header.Number, got, header.ReceiptHash)
		}
	}
}
