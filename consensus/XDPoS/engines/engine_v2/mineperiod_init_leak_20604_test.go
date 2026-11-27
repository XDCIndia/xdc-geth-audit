// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"runtime"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// F-2026-20604: a node that does not mine has no reader on minePeriodCh. Every
// initial() run must hand over the mine period without parking a goroutine.
func TestF202620604InitialNoReaderNoGoroutineLeak(t *testing.T) {
	eng, _ := newA894Engine(t)
	t.Cleanup(func() { eng.timeoutWorker.Stop() })
	eng.minePeriodCh = make(chan int, 1)

	head := buildV2Header(t, 5, 101, 0, common.Hash{0x99}, nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{5: head}}

	run := func() {
		eng.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{}}
		eng.isInitialized = false
		eng.lock.Lock()
		err := eng.initial(chain, head)
		eng.lock.Unlock()
		if err != nil {
			t.Fatalf("initial: %v", err)
		}
	}
	run() // warm up
	before := runtime.NumGoroutine()
	for i := 0; i < 200; i++ {
		run()
	}
	time.Sleep(20 * time.Millisecond)
	if leaked := runtime.NumGoroutine() - before; leaked > 5 {
		t.Fatalf("leaked %d goroutines over 200 initial() runs with no reader", leaked)
	}
	select {
	case got := <-eng.minePeriodCh:
		if got != 2 {
			t.Fatalf("channel holds %d, want mine period 2", got)
		}
	default:
		t.Fatal("channel empty")
	}
}
