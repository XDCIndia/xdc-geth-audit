// Copyright 2026 The XDC Network Authors
// Benchmark and unit tests for XDC #1645 TD backfill.
// Run benchmarks: go test -bench=BenchmarkXDCTDBackfill -benchtime=1x -run=^$ -v ./core/rawdb/
// Run unit tests:  go test -run=TestXDCTDBackfill ./core/rawdb/

package rawdb

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
)

// peakVmRSSKB reads VmRSS (current) from /proc/self/status in kB.
func peakVmRSSKB() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, _ := strconv.ParseInt(fields[1], 10, 64)
				return v
			}
		}
	}
	return -1
}

// syntheticXDCChain writes N canonical headers (no TD) into db.
// Genesis carries difficulty=1 with TD stored; all others carry difficulty=2 with no TD.
// Returns the genesis hash, head hash, and expected cumulative TD at head.
func syntheticXDCChain(db ethdb.Database, blocks int) (genHash, headHash common.Hash, wantTD *big.Int) {
	batch := db.NewBatch()

	// Use deterministic but unique hashes to avoid collisions across benchmark iterations.
	salt := rand.Uint64()
	genHeader := &types.Header{
		Number:     big.NewInt(0),
		Difficulty: big.NewInt(1),
		GasLimit:   8_000_000,
		Time:       salt,
		Extra:      []byte("genesis"),
	}
	genHash = genHeader.Hash()
	WriteHeader(batch, genHeader)
	WriteCanonicalHash(batch, genHash, 0)
	WriteTd(batch, genHash, 0, genHeader.Difficulty) // genesis TD must be seeded
	batch.Write()

	cumTD := new(big.Int).Set(genHeader.Difficulty)
	parentHash := genHash
	batch = db.NewBatch()
	for i := 1; i <= blocks; i++ {
		h := &types.Header{
			Number:     big.NewInt(int64(i)),
			ParentHash: parentHash,
			Difficulty: big.NewInt(2),
			GasLimit:   8_000_000,
			Time:       salt + uint64(i)*2,
		}
		hash := h.Hash()
		WriteHeader(batch, h)
		WriteCanonicalHash(batch, hash, uint64(i))
		// Intentionally omit WriteTd — simulates old-binary import.
		cumTD = new(big.Int).Add(cumTD, h.Difficulty)
		parentHash = hash
		headHash = hash
		if batch.ValueSize() >= ethdb.IdealBatchSize {
			batch.Write()
			batch.Reset()
		}
	}
	batch.Write()
	wantTD = cumTD
	return
}

// xdcTDBackfillDirect mirrors the core.BlockChain.xdcTDBackfill logic without
// needing a full BlockChain.  Used for unit tests and benchmarks in isolation.
//
// headFn returns the current canonical head block number and may be called
// multiple times. The function loops until headFn() returns a block with
// stored TD or stopping is set.
func xdcTDBackfillDirect(db ethdb.Database, headFn func() uint64, stopping *atomic.Bool) (written uint64) {
	const retryDelay = 10 * time.Millisecond

	// Genesis must already have TD written (syntheticXDCChain does this).
	genHash := ReadCanonicalHash(db, 0)
	if genHash == (common.Hash{}) || ReadTd(db, genHash, 0) == nil {
		return
	}

	for !stopping.Load() {
		headNum := headFn()
		headHash := ReadCanonicalHash(db, headNum)
		if headHash != (common.Hash{}) && ReadTd(db, headHash, headNum) != nil {
			return // head already has TD
		}

		// Establish anchor from last persisted progress.
		anchorNum := uint64(0)
		anchorHash := genHash
		if startNum := ReadTdBackfillProgress(db); startNum > 0 {
			if h := ReadCanonicalHash(db, startNum); h != (common.Hash{}) {
				if ReadTd(db, h, startNum) != nil {
					anchorNum = startNum
					anchorHash = h
				}
			}
		}

		// Seed in-memory running TD from anchor (guaranteed in DB).
		runTD := new(big.Int).Set(ReadTd(db, anchorHash, anchorNum))
		prevHash := anchorHash

		batch := db.NewBatch()
		lastWritten := anchorNum

		for num := anchorNum + 1; num <= headNum; num++ {
			if stopping.Load() {
				if batch.ValueSize() > 0 {
					WriteTdBackfillProgress(batch, lastWritten)
					batch.Write() //nolint:errcheck
				}
				return
			}
			hash := ReadCanonicalHash(db, num)
			if hash == (common.Hash{}) {
				break // canonical gap
			}
			hdr := ReadHeader(db, hash, num)
			if hdr == nil {
				break
			}
			// Reorg detection.
			if hdr.ParentHash != prevHash {
				break
			}
			// Sync to existing TD if a concurrent writer already stored it.
			if existing := ReadTd(db, hash, num); existing != nil {
				runTD = new(big.Int).Set(existing)
				prevHash = hash
				lastWritten = num
				continue
			}
			// Use in-memory runTD (not a DB read) to avoid reading unflushed batch.
			blockTD := new(big.Int).Add(runTD, hdr.Difficulty)
			WriteTd(batch, hash, num, blockTD)
			written++
			runTD = blockTD
			prevHash = hash
			lastWritten = num
			if batch.ValueSize() >= ethdb.IdealBatchSize {
				WriteTdBackfillProgress(batch, num)
				batch.Write()
				batch.Reset()
			}
		}
		if batch.ValueSize() > 0 {
			WriteTdBackfillProgress(batch, lastWritten)
			batch.Write()
		}

		if lastWritten < headNum {
			time.Sleep(retryDelay)
		}
	}
	return
}

// TestXDCTDBackfillCatchUp verifies the outer catch-up loop: blocks appended
// to the canonical chain while the backfill is running are covered without a
// restart, and the final TD is the correct cumulative sum.
//
// Reliability note: headFn returns phase2 from the start so the backfill
// immediately tries to cover 1..phase2. It hits a canonical gap at phase1+1
// (not yet written) and sleeps retryDelay (10 ms). The goroutine writes
// phase1+1..phase2 within that window (its own sleep is 5 ms < 10 ms). On the
// next outer-loop iteration the backfill covers the newly added blocks.
func TestXDCTDBackfillCatchUp(t *testing.T) {
	const (
		phase1 = 50  // blocks present before backfill starts
		phase2 = 100 // total blocks (phase1 + 50 appended concurrently)
		diff   = 2   // difficulty per non-genesis block
	)

	db := NewMemoryDatabase()
	defer db.Close()

	// Build phase-1 chain (blocks 0..phase1, no TD except genesis).
	_, headHash, _ := syntheticXDCChain(db, phase1)

	// headFn always returns phase2 so the backfill aims for the full target
	// from the first iteration — it will see a canonical gap at phase1+1 and
	// sleep, giving the goroutine time to write the remaining blocks.
	headFn := func() uint64 { return uint64(phase2) }

	// Goroutine: after a brief pause, extend the canonical chain to phase2.
	var extended atomic.Bool
	go func() {
		time.Sleep(5 * time.Millisecond)
		batch := db.NewBatch()
		parentHash := headHash
		for i := phase1 + 1; i <= phase2; i++ {
			h := &types.Header{
				Number:     big.NewInt(int64(i)),
				ParentHash: parentHash,
				Difficulty: big.NewInt(diff),
				GasLimit:   8_000_000,
				Time:       uint64(i) * 3, // distinct from phase-1 timestamps (*2)
			}
			hash := h.Hash()
			WriteHeader(batch, h)
			WriteCanonicalHash(batch, hash, uint64(i))
			// Intentionally no WriteTd — backfill must supply it.
			parentHash = hash
			if batch.ValueSize() >= ethdb.IdealBatchSize {
				batch.Write()
				batch.Reset()
			}
		}
		batch.Write()
		extended.Store(true)
	}()

	var stopping atomic.Bool
	done := make(chan uint64, 1)
	go func() { done <- xdcTDBackfillDirect(db, headFn, &stopping) }()

	select {
	case written := <-done:
		if !extended.Load() {
			t.Fatal("backfill finished before phase-2 blocks were appended — test did not exercise the catch-up path")
		}
		// Expected TD at head: genesis(1) + phase2*diff = 1 + 100*2 = 201
		wantTD := big.NewInt(1 + int64(phase2)*diff)
		finalHash := ReadCanonicalHash(db, uint64(phase2))
		if finalHash == (common.Hash{}) {
			t.Fatal("canonical hash missing for final head")
		}
		gotTD := ReadTd(db, finalHash, uint64(phase2))
		if gotTD == nil {
			t.Fatal("TD missing for final head after backfill")
		}
		if gotTD.Cmp(wantTD) != 0 {
			t.Errorf("final TD = %s, want %s", gotTD, wantTD)
		}
		t.Logf("backfill wrote %d TDs, final TD = %s", written, gotTD)
	case <-time.After(5 * time.Second):
		stopping.Store(true)
		t.Fatal("backfill did not converge within 5 s")
	}
}

func BenchmarkXDCTDBackfill5M(b *testing.B) {
	benchmarkXDCTDBackfill(b, 5_000_000)
}

func BenchmarkXDCTDBackfill10M(b *testing.B) {
	benchmarkXDCTDBackfill(b, 10_000_000)
}

func benchmarkXDCTDBackfill(b *testing.B, blocks int) {
	b.Helper()

	db := NewMemoryDatabase()
	defer db.Close()

	b.Log("populating synthetic chain:", blocks, "blocks")
	_, _, wantTD := syntheticXDCChain(db, blocks)

	var msBase runtime.MemStats
	runtime.ReadMemStats(&msBase)
	rssBase := peakVmRSSKB()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 {
			// Re-populate for repeated runs (clear TDs written in prior iteration).
			syntheticXDCChain(db, blocks)
		}

		b.StartTimer()
		t0 := time.Now()
		var stopping atomic.Bool
		written := xdcTDBackfillDirect(db, func() uint64 { return uint64(blocks) }, &stopping)
		elapsed := time.Since(t0)
		b.StopTimer()

		blocksPerSec := float64(written) / elapsed.Seconds()
		rssPeak := peakVmRSSKB()

		fmt.Printf("\n=== XDCTDBackfill %d blocks ===\n", blocks)
		fmt.Printf("  written:    %d blocks\n", written)
		fmt.Printf("  elapsed:    %v\n", elapsed)
		fmt.Printf("  throughput: %.0f blocks/s\n", blocksPerSec)
		fmt.Printf("  RSS base:   %d kB  peak: %d kB  delta: %d kB\n", rssBase, rssPeak, rssPeak-rssBase)
		fmt.Printf("  wantTD:     %s\n", wantTD)

		progress := ReadTdBackfillProgress(db)
		if progress != uint64(blocks) {
			b.Errorf("progress key = %d, want %d", progress, blocks)
		}
	}
}
