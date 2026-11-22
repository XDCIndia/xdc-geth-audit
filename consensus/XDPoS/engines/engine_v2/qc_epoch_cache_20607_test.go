// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestF202620607RejectedQCDoesNotCacheEpochUnderUncheckedHash checks that a
// certificate whose proposed-block hash was not checked does not leave the
// parent epoch in the epoch cache under that hash.
func TestF202620607RejectedQCDoesNotCacheEpochUnderUncheckedHash(t *testing.T) {
	eng, _ := newA894Engine(t)
	parent := buildV2Header(t, 10, 5, 0, common.Hash{0x11}, nil)
	parentEpoch := &types.EpochSwitchInfo{
		Masternodes:    []common.Address{{0x01}},
		MasternodesLen: 1,
		EpochSwitchBlockInfo: &types.BlockInfo{
			Hash:   parent.ParentHash,
			Round:  0,
			Number: big.NewInt(9),
		},
	}
	eng.epochSwitches.Add(parent.ParentHash, parentEpoch)

	arbitrary := common.Hash{0xab, 0xcd}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{10: parent}}

	got, err := eng.getEpochSwitchInfoWithParents(chain, parent, arbitrary, nil)
	if err != nil {
		t.Fatalf("epoch lookup: %v", err)
	}
	if got == nil || len(got.Masternodes) == 0 {
		t.Fatal("epoch lookup returned no masternodes")
	}
	if _, ok := eng.epochSwitches.Get(arbitrary); ok {
		t.Fatal("epoch lookup stored the parent epoch under the unchecked block hash")
	}

	qc := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: arbitrary, Round: 6, Number: big.NewInt(11)},
		GapNumber:         0,
	}
	if err := eng.verifyQC(chain, qc, parent, nil); err == nil {
		t.Fatal("certificate with no signatures was accepted")
	}
	if _, ok := eng.epochSwitches.Get(arbitrary); ok {
		t.Fatal("rejected certificate stored the parent epoch under the unchecked block hash")
	}
}
