// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestF202619773CommittedBlockSurvivesRestart checks that the committed-block
// marker is read back after the in-memory value is cleared, which is what a
// process restart does.
func TestF202619773CommittedBlockSurvivesRestart(t *testing.T) {
	eng, db := newA894Engine(t)
	info := &types.BlockInfo{
		Hash:   common.Hash{0x42},
		Number: big.NewInt(42),
		Round:  7,
	}
	rawdb.WriteBFTCommittedBlock(db, info)
	eng.highestCommitBlock = nil
	eng.loadBFTSafetyState()
	got := eng.GetLatestCommittedBlockInfo()
	if got == nil || got.Hash != info.Hash || got.Number.Cmp(info.Number) != 0 {
		t.Fatalf("committed block after load = %+v, want number 42 hash %s", got, info.Hash)
	}
}
