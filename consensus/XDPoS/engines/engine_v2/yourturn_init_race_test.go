// Copyright (c) 2024 XDC Network
// Race-detection test for #1748: YourTurn must initialise under write lock.
//
// Run: go test -race -count=1 -run TestYourTurn_ConcurrentInitRace ./consensus/XDPoS/engines/engine_v2/

package engine_v2

import (
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestYourTurn_ConcurrentInitRace calls YourTurn from many goroutines on an
// uninitialised engine.
//
// Bug (origin/main): YourTurn holds only RLock while calling initial(), so N
// goroutines can execute initial() concurrently and write isInitialized (and
// other fields) simultaneously — a data race the -race flag detects.
//
// Fix: initial() is called under Lock with a double-checked re-test so only
// one goroutine executes it; the others observe isInitialized==true and skip.
func TestYourTurn_ConcurrentInitRace(t *testing.T) {
	eng, _ := newA894Engine(t)

	// Pre-seed the QC hash so initial()'s "already initialized" fast-path
	// fires: it writes isInitialized=true and returns without touching the
	// chain.  This keeps the test hermetic (no real chain needed) while still
	// triggering the concurrent write that the bug allows.
	eng.highestQuorumCert.ProposedBlockInfo.Hash = common.HexToHash("0x01")
	eng.isInitialized = false

	// parent.Time == now → waitedTime ≈ 0 < minePeriod(2), so YourTurn
	// returns after the init block without calling yourturnAligned.
	parent := &types.Header{
		Number: big.NewInt(1),
		Time:   uint64(time.Now().Unix()),
	}
	chain := &mockChainForA894{}

	const N = 20
	ready := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-ready                                       // barrier: all goroutines start simultaneously
			eng.YourTurn(chain, parent, common.Address{}) //nolint:errcheck
		}()
	}
	close(ready)
	wg.Wait()
}
