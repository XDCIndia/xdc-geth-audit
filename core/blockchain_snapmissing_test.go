// Copyright (c) 2026 XDC Network
// F-2026-20648: an epoch switch that fails with ErrV2SnapshotMissing (the gap
// snapshot is not seeded yet) is missing data, not a bad block: no bad-block
// record is written and the same block imports once the snapshot exists.

package core

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	xdcutils "github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// failingEngine fails header verification with err while err is set.
type failingEngine struct {
	consensus.Engine
	err atomic.Pointer[error]
}

func (e *failingEngine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	p := e.err.Load()
	if p == nil {
		return e.Engine.VerifyHeaders(chain, headers)
	}
	results := make(chan error, len(headers))
	for range headers {
		results <- *p
	}
	return make(chan struct{}), results
}

func TestSnapshotMissingIsNotABadBlock(t *testing.T) {
	gspec := &Genesis{Config: params.AllEthashProtocolChanges, Difficulty: common.Big1, GasLimit: 8_000_000}
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 2, nil)

	eng := &failingEngine{Engine: ethash.NewFaker()}
	db := rawdb.NewMemoryDatabase()
	chain, err := NewBlockChain(db, gspec, eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()

	// Missing gap snapshot: the error comes back, and nothing is recorded as bad.
	missing := error(errors.Join(xdcutils.ErrV2SnapshotMissing, errors.New("at gap 1")))
	eng.err.Store(&missing)
	if _, err := chain.InsertChain(blocks[:1]); !errors.Is(err, xdcutils.ErrV2SnapshotMissing) {
		t.Fatalf("want ErrV2SnapshotMissing, got %v", err)
	}
	if bad := rawdb.ReadAllBadBlocks(db); len(bad) != 0 {
		t.Fatalf("%d bad-block records after ErrV2SnapshotMissing", len(bad))
	}

	// A real validation failure is still a bad block.
	invalid := error(consensus.ErrInvalidNumber)
	eng.err.Store(&invalid)
	if _, err := chain.InsertChain(blocks[:1]); !errors.Is(err, consensus.ErrInvalidNumber) {
		t.Fatalf("want ErrInvalidNumber, got %v", err)
	}
	if bad := rawdb.ReadAllBadBlocks(db); len(bad) != 1 {
		t.Fatalf("%d bad-block records after a real failure, want 1", len(bad))
	}

	// The snapshot is seeded: the same block imports.
	eng.err.Store(nil)
	if n, err := chain.InsertChain(blocks[:1]); err != nil || n != 1 {
		t.Fatalf("import after seeding: n=%d err=%v", n, err)
	}
	if chain.CurrentBlock().Hash() != blocks[0].Hash() {
		t.Fatal("head did not advance")
	}
}

// F-2026-19463: missing penalty data is retried the same way.
func TestPenaltyDataUnavailableIsNotABadBlock(t *testing.T) {
	gspec := &Genesis{Config: params.AllEthashProtocolChanges, Difficulty: common.Big1, GasLimit: 8_000_000}
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, nil)

	eng := &failingEngine{Engine: ethash.NewFaker()}
	db := rawdb.NewMemoryDatabase()
	chain, err := NewBlockChain(db, gspec, eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()

	unavailable := error(errors.Join(xdcutils.ErrPenaltyDataUnavailable, errors.New("no block body")))
	eng.err.Store(&unavailable)
	if _, err := chain.InsertChain(blocks); !errors.Is(err, xdcutils.ErrPenaltyDataUnavailable) {
		t.Fatalf("want ErrPenaltyDataUnavailable, got %v", err)
	}
	if bad := rawdb.ReadAllBadBlocks(db); len(bad) != 0 {
		t.Fatalf("%d bad-block records after ErrPenaltyDataUnavailable", len(bad))
	}
}
