// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// TestF202619460OwnSendAfterStopDoesNotBlock checks that an own-message send
// started after StopBroadcast returns instead of blocking on the unbuffered
// broadcast channel.
func TestF202619460OwnSendAfterStopDoesNotBlock(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.StopBroadcast()

	done := make(chan struct{})
	go func() {
		eng.deliverBroadcast(&types.Vote{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("own-message send blocked after StopBroadcast")
	}
}
