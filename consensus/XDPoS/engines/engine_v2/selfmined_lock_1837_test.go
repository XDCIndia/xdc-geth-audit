// Copyright 2026 The XDC Network Authors
// Race test for review R4-W02: Seal's highestSelfMinedRound write vs the
// yourturn read under x.lock.
// Run: go test -race -count=1 -run TestMarkSelfMinedRace ./consensus/XDPoS/engines/engine_v2/

package engine_v2

import (
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestMarkSelfMinedRace(t *testing.T) {
	x := &XDPoS_v2{db: rawdb.NewMemoryDatabase()}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= 500; i++ {
			x.markSelfMined(types.Round(i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			x.lock.RLock()
			_ = x.highestSelfMinedRound
			x.lock.RUnlock()
		}
	}()
	wg.Wait()
	if x.highestSelfMinedRound != 500 {
		t.Fatalf("highestSelfMinedRound = %d, want 500", x.highestSelfMinedRound)
	}
	if got := rawdb.ReadBFTHighestSelfMinedRound(x.db); got != 500 {
		t.Fatalf("persisted round = %d, want 500", got)
	}
}
