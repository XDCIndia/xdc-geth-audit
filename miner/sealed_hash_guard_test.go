// Copyright 2026 XDC Network
// Tests for the sealed-block hash guard in maybeBroadcastImportedSignTx (#1472).

package miner

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestMaybeBroadcastSkipsSealedBlock verifies that maybeBroadcastImportedSignTx
// returns early (without accessing w.miner) when the block hash matches
// lastSealedHash, preventing a second sign tx at the next nonce.
//
// Fails on origin/main: lastSealedHash field does not exist (compile error).
// Passes with fix: function returns at the hash guard before dereferencing
// w.miner (which is nil in this test), so no panic.
func TestMaybeBroadcastSkipsSealedBlock(t *testing.T) {
	// Build a minimal block whose number is a MergeSignRange multiple (15) so
	// that — without the hash guard — the next gate (isSignBlock) would be
	// entered and would dereference w.miner.chainConfig (nil), causing a panic.
	header := &types.Header{
		Number:     big.NewInt(int64(common.MergeSignRange)), // 15
		Difficulty: common.Big1,
		GasLimit:   21000,
	}
	block := types.NewBlockWithHeader(header)

	// Create a minimal XdcWorker: non-zero coinbase (so the first early-return
	// doesn't fire), nil miner (so any code reaching isSignBlock panics), and
	// lastSealedHash set to the block's hash.
	w := &XdcWorker{
		coinbase:       common.HexToAddress("0xAAAA"),
		miner:          nil,
		lastSealedHash: block.Hash(),
	}

	// Must not panic: the hash guard returns early before accessing w.miner.
	w.maybeBroadcastImportedSignTx(block)
}
