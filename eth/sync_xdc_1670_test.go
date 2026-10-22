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

package eth

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"
)

// TestInvalidHeadSyncStall1670 reproduces the scenario from issue #1670: a
// single peer advertises a higher head whose block fails InsertChain with a
// definitive consensus error (wrong difficulty here, invalid merkle root in the
// live incident). After fullSyncOnce the node must:
//   - NOT import the bad block
//   - Taint the delivering peer so rankedPeers() stops selecting it (breaks the
//     chainmu starvation: the miner's TryLock only wins when the syncer is not
//     holding chainmu over bad-block execution)
//   - Record the bad block hash in consensusFailedBlocks (non-mainnet)
//   - Clear XdcBulkSyncMode so the miner can seal the valid successor (non-mainnet,
//     node is at tip: local head is fresh — genesis timestamp set to now)
//
// Since Hacken F-2026-19724 replaced the peer-controlled short-batch signal
// (len < 192) with the local headFresh signal, the genesis timestamp must be
// recent so the at-tip detector fires on local state, not peer data.
func TestInvalidHeadSyncStall1670(t *testing.T) {
	// Start at block 0 (local=0) with the remote chain having 3 blocks.
	// The bad peer will serve block 1 with a mutated (wrong) difficulty.
	// Use a fresh genesis timestamp so headFresh=true: the local head (genesis)
	// is within atTipFreshnessWindow and bulk mode can be cleared.
	n := newXDCFakeNetTime(t, 0, 3, uint64(time.Now().Unix()))

	// Build a bad block 1: same parent as the canonical block 1 but wrong difficulty.
	canonical := n.remote[0]
	h := types.CopyHeader(canonical.Header())
	h.Difficulty = new(big.Int).Add(h.Difficulty, big.NewInt(1))
	bad := types.NewBlockWithHeader(h).WithBody(*canonical.Body())

	// Serve only the bad block at origin 1. In the live incident the bad peer
	// had the invalid block as its head — a single-block batch — so the
	// contiguity pre-check inside InsertChain never fires; InsertChain gets
	// exactly [bad_block] and returns the difficulty / merkle-root error that
	// isDefinitiveInvalidBlockError recognises.
	fp := n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
		if origin == 1 {
			return []*types.Header{bad.Header()}
		}
		return n.canonicalHeaders(origin, amount)
	}, bad)

	s := n.newTestXDCSyncer(t)
	core.XdcBulkSyncMode.Store(true)

	s.fullSyncOnce()

	// The invalid block must not have been imported.
	if head := n.chain.CurrentBlock().Number.Uint64(); head != 0 {
		t.Fatalf("invalid block imported: head=%d, want 0", head)
	}

	// XdcBulkSyncMode must be cleared so the miner can seal.
	// (chain is AllEthashProtocolChanges, chainID=1337 ≠ 50; headFresh=true because
	// genesis timestamp is set to time.Now() above — Hacken F-2026-19724 fix)
	if got := core.XdcBulkSyncMode.Load(); got {
		t.Error("XdcBulkSyncMode still true after consensus error — miner cannot seal")
	}

	// The delivering peer must be tainted so the next cycle skips it.
	if !s.isPeerTainted(fp.ID()) {
		t.Error("bad peer not tainted — chainmu will be held again next cycle")
	}

	// The bad block hash must be recorded so it is filtered even after taint expires.
	if !s.isConsensusFailedBlock(bad.Hash()) {
		t.Error("bad block hash not in consensusFailedBlocks — will be re-imported after taint TTL")
	}
}

// TestIsDefinitiveInvalidBlockError verifies the allowlist of errors that prove
// a block is invalid (isDefinitiveInvalidBlockError returns true) and the
// non-definitive errors that must NOT taint the delivering peer.
//
// Non-definitive: local state gaps (MissingNodeError), already-known blocks
// (ErrKnownBlock), liveness failures (errChainStopped, errInsertionInterrupted),
// context cancellation, ErrPrunedAncestor, and generic I/O errors.
//
// Definitive: wrong difficulty, invalid merkle root (no MissingNodeError), bad
// seal (unauthorized), gas/receipt mismatch, and all exported consensus
// sentinels from the XDPoS utils package.
func TestIsDefinitiveInvalidBlockError(t *testing.T) {
	missingNode := &trie.MissingNodeError{Owner: common.Hash{1}, Path: []byte{2}}

	tests := []struct {
		name       string
		err        error
		definitive bool
	}{
		// Non-definitive: local/transient — must NOT taint the peer.
		{"ErrKnownBlock", core.ErrKnownBlock, false},
		{"MissingNodeError direct", missingNode, false},
		{"MissingNodeError wrapped in invalid-merkle-root", fmt.Errorf("invalid merkle root (remote: %x local: %x) dberr: %w", common.Hash{1}, common.Hash{2}, missingNode), false},
		{"errChainStopped-like", errors.New("blockchain is stopped"), false},
		{"context.Canceled", context.Canceled, false},
		{"ErrPrunedAncestor", consensus.ErrPrunedAncestor, false},
		{"generic io error", errors.New("io error"), false},
		// Definitive: the block is invalid — must taint the peer.
		{"wrong difficulty (format)", errors.New("invalid difficulty: 5 does not equal 4"), true},
		{"invalid merkle root no dberr", errors.New("invalid merkle root (remote: abc local: def) dberr: <nil>"), true},
		{"bad seal (unauthorized)", errors.New("unauthorized"), true},
		{"invalid gas used", errors.New("invalid gas used (remote: 100 local: 200)"), true},
		{"invalid bloom", errors.New("invalid bloom (remote: ff local: 00)"), true},
		{"invalid receipt root hash", errors.New("invalid receipt root hash (remote: abc local: def)"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isDefinitiveInvalidBlockError(tc.err)
			if got != tc.definitive {
				t.Errorf("isDefinitiveInvalidBlockError(%v) = %v, want %v", tc.err, got, tc.definitive)
			}
		})
	}
}
