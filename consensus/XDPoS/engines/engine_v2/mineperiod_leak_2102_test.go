package engine_v2

import (
	"runtime"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// A node that does not mine has no reader on minePeriodCh. setNewRound must
// not leave one blocked goroutine per round, and the channel must hold
// the latest period once a worker starts reading.
func TestSetNewRoundNoReaderNoGoroutineLeak(t *testing.T) {
	ch := make(chan int, 1)
	eng := createEngine(minimalChainConfig(), nil, ch, make(chan types.Round, 1))

	before := runtime.NumGoroutine()
	for i := 1; i <= 2000; i++ {
		eng.setNewRound(nil, types.Round(i))
	}
	time.Sleep(20 * time.Millisecond)
	if leaked := runtime.NumGoroutine() - before; leaked > 5 {
		t.Fatalf("leaked %d goroutines over 2000 rounds with no reader", leaked)
	}

	eng.notifyMinePeriod(7)
	select {
	case got := <-ch:
		if got != 7 {
			t.Fatalf("channel holds stale period %d, want 7", got)
		}
	default:
		t.Fatal("channel empty")
	}
}
