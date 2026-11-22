// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestF202620636RestartMustNotLowerPersistedVoteLock is the regression for a
// restart that runs processQC on the head certificate before the persisted
// lock is loaded. The stored lock must keep the higher round, in the database
// and in memory. This is also the remaining part of F-2026-19774.
func TestF202620636RestartMustNotLowerPersistedVoteLock(t *testing.T) {
	eng, db := newA894Engine(t)
	parent := buildV2Header(t, 2, 2, 1, common.Hash{0x11}, nil)
	head := buildV2Header(t, 3, 3, 2, parent.Hash(), nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{
		2: parent,
		3: head,
	}}

	stored := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   common.Hash{0x22},
			Round:  2,
			Number: big.NewInt(2),
		},
		GapNumber: 0,
	}
	rawdb.WriteBFTLockQC(db, stored)

	eng.isInitialized = false
	eng.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{}}
	eng.currentRound = 0
	eng.lockQuorumCert = nil

	eng.lock.Lock()
	err := eng.initial(chain, head)
	eng.lock.Unlock()
	if err != nil {
		t.Fatalf("initial: %v", err)
	}

	got := rawdb.ReadBFTLockQC(db)
	if got == nil || got.ProposedBlockInfo.Round != 2 {
		t.Fatalf("database lock round = %v, want 2", roundOf(got))
	}
	if eng.lockQuorumCert == nil || eng.lockQuorumCert.ProposedBlockInfo.Round != 2 {
		t.Fatalf("memory lock round = %v, want 2", roundOf(eng.lockQuorumCert))
	}
}

func roundOf(qc *types.QuorumCert) interface{} {
	if qc == nil || qc.ProposedBlockInfo == nil {
		return nil
	}
	return qc.ProposedBlockInfo.Round
}
