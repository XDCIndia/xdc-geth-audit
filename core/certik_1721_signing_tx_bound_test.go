// Copyright 2026 The go-ethereum Authors
// CertiK XDC-04 / XDC-09 / #1721: signing-tx count and block-size bounds.
//
// Before SigningTxBoundBlock (or when the field is nil) behaviour is unchanged.
// After the fork, ValidateBody rejects blocks with more than 2*MaxMasternodesV2
// signing transactions or whose RLP-encoded size exceeds params.MaxBlockSize.
//
// Run: go test -count=1 ./core/ -run TestSigningTxBound

package core

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// signingTxBoundCfg builds a minimal XDC ChainConfig with SigningTxBoundBlock
// set to boundBlock (nil = disabled). MaxMasternodesV2 is set to 5 for a tight
// test cap of 2*5 = 10 signing txs. TIPSigning is active from block 0 so that
// RouteTransaction classifies 0x89 txs as TxRouteSign.
func signingTxBoundCfg(boundBlock *big.Int) *params.ChainConfig {
	return &params.ChainConfig{
		ChainID:         big.NewInt(5151),
		TIPSigningBlock: big.NewInt(0), // active from genesis
		XDPoS: &params.XDPoSConfig{
			Epoch:            900,
			MaxMasternodesV2: 5, // cap = 2*5 = 10
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
			},
		},
		SigningTxBoundBlock: boundBlock,
	}
}

// makeSigningTx returns a signing tx (to BlockSignersBinary) with the given nonce.
func makeSigningTx(nonce uint64) *types.Transaction {
	return types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &common.BlockSignersBinary,
		Gas:      0,
		GasPrice: big.NewInt(0),
		Data:     make([]byte, 36),
	})
}

// makeSigningBoundBlock builds a block with the given transactions and number.
func makeSigningBoundBlock(num int64, txs types.Transactions) *types.Block {
	header := &types.Header{
		Number:     big.NewInt(num),
		Difficulty: big.NewInt(1),
		GasLimit:   100_000_000,
	}
	return types.NewBlock(header, &types.Body{Transactions: txs}, nil, trie.NewStackTrie(nil))
}

// TestSigningTxBound_NilField: when SigningTxBoundBlock is nil (all current
// networks) checkSigningTxBound is a no-op, even for 100 signing txs.
func TestSigningTxBound_NilField(t *testing.T) {
	cfg := signingTxBoundCfg(nil) // disabled

	var txs types.Transactions
	for i := 0; i < 100; i++ {
		txs = append(txs, makeSigningTx(uint64(i)))
	}
	block := makeSigningBoundBlock(10, txs)
	if err := checkSigningTxBound(cfg, block); err != nil {
		t.Fatalf("nil SigningTxBoundBlock: unexpected error: %v", err)
	}
}

// TestSigningTxBound_BeforeFork: blocks before the fork number are not checked.
func TestSigningTxBound_BeforeFork(t *testing.T) {
	cfg := signingTxBoundCfg(big.NewInt(100)) // fork at block 100

	var txs types.Transactions
	for i := 0; i < 20; i++ { // 20 > 2*5=10 cap
		txs = append(txs, makeSigningTx(uint64(i)))
	}
	block := makeSigningBoundBlock(50, txs) // block 50 < fork 100
	if err := checkSigningTxBound(cfg, block); err != nil {
		t.Fatalf("before fork: unexpected error at block 50 (fork=100): %v", err)
	}
}

// TestSigningTxBound_TooManySigningTxs: after the fork, a block with more than
// 2*MaxMasternodesV2 signing txs must be rejected.
// This test FAILS on origin/main (checkSigningTxBound does not exist) and
// PASSES after this PR.
func TestSigningTxBound_TooManySigningTxs(t *testing.T) {
	cfg := signingTxBoundCfg(big.NewInt(100)) // fork at block 100

	var txs types.Transactions
	for i := 0; i < 11; i++ { // 11 > 2*5=10
		txs = append(txs, makeSigningTx(uint64(i)))
	}
	block := makeSigningBoundBlock(150, txs) // block 150 > fork 100
	err := checkSigningTxBound(cfg, block)
	if err == nil {
		t.Fatal("expected error for too many signing txs, got nil")
	}
	if !strings.HasPrefix(err.Error(), "too many signing") {
		t.Fatalf("expected 'too many signing transactions' error, got: %v", err)
	}
}

// TestSigningTxBound_ExactlyAtCap: exactly 2*MaxMasternodesV2 signing txs is
// allowed (cap is exclusive: count > maxSignTxs).
func TestSigningTxBound_ExactlyAtCap(t *testing.T) {
	cfg := signingTxBoundCfg(big.NewInt(100))

	var txs types.Transactions
	for i := 0; i < 10; i++ { // exactly 2*5=10 — must not fire
		txs = append(txs, makeSigningTx(uint64(i)))
	}
	block := makeSigningBoundBlock(150, txs)
	if err := checkSigningTxBound(cfg, block); err != nil {
		t.Fatalf("exactly at cap: unexpected error: %v", err)
	}
}

// TestSigningTxBound_OversizedBlock: after the fork, a block whose RLP-encoded
// size exceeds params.MaxBlockSize must be rejected with ErrBlockOversized.
// This test FAILS on origin/main and PASSES after this PR.
func TestSigningTxBound_OversizedBlock(t *testing.T) {
	cfg := signingTxBoundCfg(big.NewInt(0)) // fork from genesis

	bigData := make([]byte, params.MaxBlockSize+1)
	tx := types.NewTx(&types.LegacyTx{
		Gas:      21_000,
		GasPrice: big.NewInt(1),
		Data:     bigData,
	})
	block := makeSigningBoundBlock(1, types.Transactions{tx})
	if block.Size() <= params.MaxBlockSize {
		t.Skip("block not actually oversized (RLP header overhead insufficient)")
	}
	err := checkSigningTxBound(cfg, block)
	if !errors.Is(err, ErrBlockOversized) {
		t.Fatalf("oversized block after SigningTxBoundBlock: got %v, want ErrBlockOversized", err)
	}
}
