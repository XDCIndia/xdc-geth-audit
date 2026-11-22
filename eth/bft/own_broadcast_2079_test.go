package bft

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// An own message from the engine must go to the Own callback, not through the
// forward queue that relayed messages fill (#2079).
func TestOwnMessagesUseOwnCallback(t *testing.T) {
	got := make(chan interface{}, 1)
	b := New(BroadcastFns{
		Vote:     func(*types.Vote) { t.Error("own vote went through the relay callback") },
		Timeout:  func(*types.Timeout) {},
		SyncInfo: func(*types.SyncInfo) {},
		Own:      func(msg interface{}) { got <- msg },
	}, fakeChain{}, func() uint64 { return 100 })
	ch := make(chan interface{})
	b.SetBroadcastCh(ch)
	b.Start()
	defer b.Stop()

	vote := newVote(5)
	ch <- vote
	select {
	case m := <-got:
		if m != interface{}(vote) {
			t.Fatalf("Own got %v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("Own callback was not called")
	}
}
