// Copyright 2024 The XDC Network Authors

package rawdb

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// TestBFTSafetyState_RoundTrip verifies that the three BFT local safety state
// values survive a write+read round-trip.  This test FAILS on origin/main
// (functions do not exist) and PASSES after the F-2026-19774 fix.
func TestBFTSafetyState_RoundTrip(t *testing.T) {
	db := NewMemoryDatabase()

	// --- highestVotedRound ---
	if got := ReadBFTHighestVotedRound(db); got != 0 {
		t.Errorf("ReadBFTHighestVotedRound: want 0 (no key), got %d", got)
	}
	WriteBFTHighestVotedRound(db, 42)
	if got := ReadBFTHighestVotedRound(db); got != 42 {
		t.Errorf("ReadBFTHighestVotedRound: want 42, got %d", got)
	}
	// Overwrite with a higher value.
	WriteBFTHighestVotedRound(db, 99)
	if got := ReadBFTHighestVotedRound(db); got != 99 {
		t.Errorf("ReadBFTHighestVotedRound: want 99, got %d", got)
	}

	// --- highestSelfMinedRound ---
	if got := ReadBFTHighestSelfMinedRound(db); got != 0 {
		t.Errorf("ReadBFTHighestSelfMinedRound: want 0 (no key), got %d", got)
	}
	WriteBFTHighestSelfMinedRound(db, types.Round(17))
	if got := ReadBFTHighestSelfMinedRound(db); got != 17 {
		t.Errorf("ReadBFTHighestSelfMinedRound: want 17, got %d", got)
	}

	// --- lockQC ---
	if got := ReadBFTLockQC(db); got != nil {
		t.Errorf("ReadBFTLockQC: want nil (no key), got %+v", got)
	}
	qc := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Round:  types.Round(7),
			Number: big.NewInt(700),
			Hash:   common.HexToHash("0xdeadbeef"),
		},
		GapNumber: 450,
	}
	WriteBFTLockQC(db, qc)
	got := ReadBFTLockQC(db)
	if got == nil {
		t.Fatal("ReadBFTLockQC: want non-nil QC, got nil")
	}
	if got.ProposedBlockInfo.Round != qc.ProposedBlockInfo.Round {
		t.Errorf("lock QC round: want %d, got %d", qc.ProposedBlockInfo.Round, got.ProposedBlockInfo.Round)
	}
	if got.GapNumber != qc.GapNumber {
		t.Errorf("lock QC gap: want %d, got %d", qc.GapNumber, got.GapNumber)
	}
	if got.ProposedBlockInfo.Hash != qc.ProposedBlockInfo.Hash {
		t.Errorf("lock QC hash: want %s, got %s", qc.ProposedBlockInfo.Hash, got.ProposedBlockInfo.Hash)
	}
}

// TestBFTSafetyState_LockNotLowered verifies a later write of an equal or
// lower round does not replace the stored lock.
func TestBFTSafetyState_LockNotLowered(t *testing.T) {
	db := NewMemoryDatabase()
	higher := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Round: 2, Number: big.NewInt(20), Hash: common.Hash{0x02}},
	}
	lower := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Round: 1, Number: big.NewInt(10), Hash: common.Hash{0x01}},
	}
	WriteBFTLockQC(db, higher)
	WriteBFTLockQC(db, lower)
	got := ReadBFTLockQC(db)
	if got == nil || got.ProposedBlockInfo.Round != 2 {
		t.Fatalf("stored lock round = %v, want 2", got)
	}
	same := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Round: 2, Number: big.NewInt(21), Hash: common.Hash{0x03}},
	}
	WriteBFTLockQC(db, same)
	got = ReadBFTLockQC(db)
	if got.ProposedBlockInfo.Hash != higher.ProposedBlockInfo.Hash {
		t.Fatalf("equal round replaced the stored lock hash: got %s", got.ProposedBlockInfo.Hash)
	}
}

// TestBFTSafetyState_NilLockQC verifies WriteBFTLockQC is a no-op for nil.
func TestBFTSafetyState_NilLockQC(t *testing.T) {
	db := NewMemoryDatabase()
	WriteBFTLockQC(db, nil) // must not panic or write anything
	if got := ReadBFTLockQC(db); got != nil {
		t.Errorf("ReadBFTLockQC: want nil after nil write, got %+v", got)
	}
}
