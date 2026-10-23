// Copyright 2026 The XDC Network Authors
// Tests for #1854: nil-QC guard in isVoteBlamed and the bounded ancestor walk.

package engine_v2

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// A vote whose proposed block carries no QC must return an error, not panic.
func TestIsVoteBlamedNilQC(t *testing.T) {
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
	blk := &types.Header{Number: big.NewInt(10), Extra: mustEncodeExtra(t, &types.ExtraFields_v2{Round: 5})}
	chain.add(blk)
	vote := &types.Vote{ProposedBlockInfo: &types.BlockInfo{Hash: blk.Hash(), Round: 5, Number: big.NewInt(10)}}
	committed := make([]types.QuorumCert, NUM_OF_FORENSICS_QC)
	for i := range committed {
		committed[i] = types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Round: types.Round(3 - i)}}
	}
	if _, _, err := NewForensics().isVoteBlamed(chain, committed, vote); err == nil {
		t.Fatal("expected an error for a proposed block without a QC")
	}
}

// The ancestor walk stops at forensicsMaxAncestorWalk instead of reading
// every header back to the committed round.
func TestFindAncestorQcThroughRoundBounded(t *testing.T) {
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
	const n = forensicsMaxAncestorWalk + 200
	prev := &types.Header{Number: big.NewInt(0), Extra: mustEncodeExtra(t, &types.ExtraFields_v2{Round: 0})}
	chain.add(prev)
	for i := uint64(1); i <= n; i++ {
		qc := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Hash: prev.Hash(), Round: types.Round(i - 1), Number: new(big.Int).SetUint64(i - 1)}}
		h := &types.Header{Number: new(big.Int).SetUint64(i), Extra: mustEncodeExtra(t, &types.ExtraFields_v2{Round: types.Round(i), QuorumCert: qc})}
		chain.add(h)
		prev = h
	}
	committed := make([]types.QuorumCert, NUM_OF_FORENSICS_QC)
	for i := range committed {
		committed[i] = types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Round: types.Round(NUM_OF_FORENSICS_QC - 1 - i)}}
	}
	incoming := []types.QuorumCert{{ProposedBlockInfo: &types.BlockInfo{Hash: prev.Hash(), Round: types.Round(n), Number: new(big.Int).SetUint64(n)}}}
	_, _, _, err := NewForensics().findAncestorQcThroughRound(chain, committed, incoming)
	if err == nil || !strings.Contains(err.Error(), "no ancestor QC within") {
		t.Fatalf("expected the walk cap error, got %v", err)
	}
}
