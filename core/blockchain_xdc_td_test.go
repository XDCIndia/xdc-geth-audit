// Copyright 2026 The go-ethereum Authors
// Tests and benchmarks for xdcTDBackfill using a real *BlockChain fixture.
// These live in package core (not core_test) so they can call unexported methods.

package core

import (
	"fmt"
	"math/big"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

// syntheticXDCBackfillChain creates a BlockChain with a genesis block and then
// writes N synthetic canonical headers (no TD) directly to its DB, bypassing
// InsertChain. The chain's in-memory head pointer is updated to the synthetic
// tip, matching what CurrentBlock() returns after a real import would.
//
// The caller should defer bc.Stop().
func syntheticXDCBackfillChain(tb testing.TB, blocks int) (bc *BlockChain, headHash common.Hash, wantTD *big.Int) {
	tb.Helper()
	gspec := &Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	db := rawdb.NewMemoryDatabase()

	var err error
	// NewBlockChain commits the genesis block internally.
	bc, err = NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		tb.Fatalf("NewBlockChain: %v", err)
	}
	tb.Cleanup(bc.Stop)

	// genesis TD is 1 (gspec.Difficulty = common.Big1); synthetic blocks add 2.
	genesis := bc.Genesis()
	cumTD := new(big.Int).Set(genesis.Difficulty())
	parentHash := genesis.Hash()
	var headHeader *types.Header

	batch := db.NewBatch()
	for i := 1; i <= blocks; i++ {
		h := &types.Header{
			Number:     big.NewInt(int64(i)),
			ParentHash: parentHash,
			Difficulty: big.NewInt(2),
			GasLimit:   8_000_000,
			Time:       uint64(i) * 2,
		}
		hash := h.Hash()
		rawdb.WriteHeader(batch, h)                            // also writes hash→number mapping
		rawdb.WriteBody(batch, hash, uint64(i), &types.Body{}) // needed so bc.Stop() can retrieve blocks
		rawdb.WriteCanonicalHash(batch, hash, uint64(i))
		// Intentionally omit WriteTd — xdcTDBackfill must supply it.
		cumTD.Add(cumTD, h.Difficulty)
		parentHash = hash
		headHeader = h
		headHash = hash
		if batch.ValueSize() >= ethdb.IdealBatchSize {
			if err := batch.Write(); err != nil {
				tb.Fatalf("batch.Write: %v", err)
			}
			batch.Reset()
		}
	}
	if batch.ValueSize() > 0 {
		if err := batch.Write(); err != nil {
			tb.Fatalf("batch.Write: %v", err)
		}
	}

	// Update the blockchain's in-memory head pointer so CurrentBlock() returns
	// the synthetic tip.  currentBlock is the unexported field in BlockChain.
	bc.currentBlock.Store(headHeader)
	wantTD = new(big.Int).Set(cumTD)
	return
}

// TestXDCTDBackfillSinglePass asserts that xdcTDBackfill covers a 10 k-block
// chain in a single forward pass.  If the in-memory runTD accumulator is
// missing and the backfill reads parentTD from the DB (which hasn't been
// flushed yet), it would advance only ~one block per 10 ms retry — completing
// 10 000 blocks would take ~100 s and trigger the timeout.
func TestXDCTDBackfillSinglePass(t *testing.T) {
	const blocks = 10_000
	bc, headHash, wantTD := syntheticXDCBackfillChain(t, blocks)

	done := make(chan struct{})
	go func() {
		bc.xdcTDBackfill()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("backfill did not complete within 10 s — likely multi-pass regression (1 block/10 ms)")
	}

	gotTD := rawdb.ReadTd(bc.db, headHash, uint64(blocks))
	if gotTD == nil {
		t.Fatal("TD missing at final head after backfill")
	}
	if gotTD.Cmp(wantTD) != 0 {
		t.Errorf("TD = %s, want %s", gotTD, wantTD)
	}
}

// TestXDCTDBackfillCatchUpReal verifies the outer catch-up loop on a real
// BlockChain.  The backfill starts with headNum pointing to phase2 (the full
// target), but canonical hashes exist only for phase1 blocks initially.  The
// backfill hits a canonical gap and sleeps (10 ms).  A goroutine writes the
// remaining phase1+1..phase2 blocks within that window (5 ms sleep) and
// advances the BlockChain head pointer.  The outer loop then covers the new
// blocks in a second pass.
func TestXDCTDBackfillCatchUpReal(t *testing.T) {
	const (
		phase1 = 50
		phase2 = 100
	)

	bc, headHash1, _ := syntheticXDCBackfillChain(t, phase1)

	// Point the in-memory head at a synthetic phase2 block (TD = nil in DB)
	// so the backfill immediately aims for block phase2 from the first iteration.
	bc.currentBlock.Store(&types.Header{Number: big.NewInt(int64(phase2))})

	var (
		finalHash common.Hash
		extended  atomic.Bool
	)
	go func() {
		time.Sleep(5 * time.Millisecond)
		batch := bc.db.NewBatch()
		parentHash := headHash1
		var lastHeader *types.Header
		for i := phase1 + 1; i <= phase2; i++ {
			h := &types.Header{
				Number:     big.NewInt(int64(i)),
				ParentHash: parentHash,
				Difficulty: big.NewInt(2),
				GasLimit:   8_000_000,
				Time:       uint64(i) * 3, // distinct timestamps from phase-1 (*2)
			}
			hash := h.Hash()
			rawdb.WriteHeader(batch, h)
			rawdb.WriteBody(batch, hash, uint64(i), &types.Body{})
			rawdb.WriteCanonicalHash(batch, hash, uint64(i))
			parentHash = hash
			finalHash = hash
			lastHeader = h
		}
		batch.Write()
		bc.currentBlock.Store(lastHeader) // advance in-memory head to real tip
		extended.Store(true)
	}()

	done := make(chan struct{})
	go func() {
		bc.xdcTDBackfill()
		close(done)
	}()

	select {
	case <-done:
		if !extended.Load() {
			t.Fatal("backfill finished before phase-2 extension — catch-up path not exercised")
		}
		wantTD := big.NewInt(1 + int64(phase2)*2) // genesis(1) + phase2×diff(2)
		gotTD := rawdb.ReadTd(bc.db, finalHash, uint64(phase2))
		if gotTD == nil {
			t.Fatal("TD missing at phase-2 head after catch-up")
		}
		if gotTD.Cmp(wantTD) != 0 {
			t.Errorf("TD = %s, want %s", gotTD, wantTD)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("backfill did not converge within 10 s")
	}
}

// BenchmarkXDCTDBackfillReal measures xdcTDBackfill throughput on a real
// BlockChain with a 1 M-block synthetic chain (no stored TD).
// Run: go test -bench=BenchmarkXDCTDBackfillReal -benchtime=1x -run=^$ -v ./core/
func BenchmarkXDCTDBackfillReal(b *testing.B) {
	const blocks = 1_000_000

	b.Log("populating synthetic chain:", blocks, "blocks")
	bc, headHash, _ := syntheticXDCBackfillChain(b, blocks)

	var msBase runtime.MemStats
	runtime.ReadMemStats(&msBase)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		t0 := time.Now()
		bc.xdcTDBackfill()
		elapsed := time.Since(t0)
		b.StopTimer()

		written := uint64(0)
		if td := rawdb.ReadTd(bc.db, headHash, uint64(blocks)); td != nil {
			written = uint64(blocks) // all blocks covered
		}
		bps := float64(written) / elapsed.Seconds()

		var msPeak runtime.MemStats
		runtime.ReadMemStats(&msPeak)
		allocDelta := msPeak.TotalAlloc - msBase.TotalAlloc

		fmt.Printf("\n=== XDCTDBackfillReal %d blocks ===\n", blocks)
		fmt.Printf("  elapsed:    %v\n", elapsed)
		fmt.Printf("  throughput: %.0f blocks/s\n", bps)
		fmt.Printf("  alloc delta: %d KB\n", allocDelta/1024)
	}
}
