// Copyright (c) 2026 XDC Network
// Tests for rejection of empty persisted gap snapshots (CertiK XDC-35).
//
// loadSnapshot must return an error for a snapshot with no NextEpochCandidates
// so the existing rebuild path in getSnapshot runs instead of returning an
// empty masternode set that causes vote and timeout checks to fail.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestLoadSnapshot_Empty -count=1

package engine_v2

import (
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
)

// TestLoadSnapshot_EmptyCandidatesRejected verifies that loadSnapshot treats a
// stored snapshot with no NextEpochCandidates as missing (returns an error) so
// the caller's rebuild path runs instead of returning an empty masternode set.
//
// Before fix: loadSnapshot returns (snap, nil) for an empty snapshot → FAIL
// After fix:  loadSnapshot returns (nil, err)                         → PASS
func TestLoadSnapshot_EmptyCandidatesRejected(t *testing.T) {
	dir := t.TempDir()
	db, err := leveldb.New(dir, 256, 0, "", false)
	if err != nil {
		t.Fatalf("can't create temporary database: %v", err)
	}
	ldb := rawdb.NewDatabase(db)

	// Store a snapshot with no candidates (the pathological case from v2.8.0/v2.8.2-testnet).
	empty := newSnapshot(900, common.Hash{0x90}, nil) // NextEpochCandidates = nil
	if err := storeSnapshot(empty, ldb); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	snap, err := loadSnapshot(ldb, empty.Hash)
	if err == nil {
		// Before fix: err is nil; snap.NextEpochCandidates is empty.
		t.Fatalf("loadSnapshot accepted empty snapshot (XDC-35): got snap with %d candidates, want error",
			len(snap.NextEpochCandidates))
	}
	// After fix: err is non-nil; snap must be nil.
	if snap != nil {
		t.Fatalf("loadSnapshot returned non-nil snap alongside error: %v", err)
	}
}

// TestLoadSnapshot_NonEmptyCandidatesAccepted verifies that a snapshot with
// valid NextEpochCandidates is still returned correctly (no regression).
func TestLoadSnapshot_NonEmptyCandidatesAccepted(t *testing.T) {
	dir := t.TempDir()
	db, err := leveldb.New(dir, 256, 0, "", false)
	if err != nil {
		panic(fmt.Sprintf("can't create temporary database: %v", err))
	}
	ldb := rawdb.NewDatabase(db)

	candidates := []common.Address{{0x01}, {0x02}, {0x03}}
	snap := newSnapshot(900, common.Hash{0x91}, candidates)
	if err := storeSnapshot(snap, ldb); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	loaded, err := loadSnapshot(ldb, snap.Hash)
	if err != nil {
		t.Fatalf("loadSnapshot rejected valid snapshot: %v", err)
	}
	if len(loaded.NextEpochCandidates) != len(candidates) {
		t.Fatalf("loaded %d candidates, want %d", len(loaded.NextEpochCandidates), len(candidates))
	}
}
