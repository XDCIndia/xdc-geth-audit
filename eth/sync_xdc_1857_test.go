// Copyright 2026 The XDC Network Authors
// Test for #1857: one XDPOS2 header request in flight per peer.

package eth

import (
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
)

// Two goroutines fetch different ranges from the same XDPOS2 peer at once.
// XDPOS2 replies carry no request id, so without per-peer serialisation each
// requester can drain or consume the other's reply.
func TestFetchHeaderBatchConcurrentRequestersGetOwnReply(t *testing.T) {
	peer, remote := makeXDCPeerPair(t)
	s := &xdcSyncer{quitCh: make(chan struct{})}

	// Fake remote: when two requests arrive within 300 ms it answers them in
	// reverse order, as a busy peer can. A requester must still end up with
	// the batch it asked for.
	reqs := make(chan uint64, 4)
	go func() {
		for {
			msg, err := remote.ReadMsg()
			if err != nil {
				return
			}
			var req ethproto.GetBlockHeadersRequest
			if err := msg.Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			reqs <- req.Origin.Number
		}
	}()
	reply := func(origin uint64) {
		hs := make([]*types.Header, 5)
		for i := range hs {
			hs[i] = makeHeader(origin + uint64(i))
		}
		peer.XDCHeaderResp <- hs
	}
	go func() {
		for first := range reqs {
			select {
			case second := <-reqs:
				reply(second)
				reply(first)
			case <-time.After(300 * time.Millisecond):
				reply(first)
			}
		}
	}()

	var wg sync.WaitGroup
	got := make(map[uint64]uint64)
	var mu sync.Mutex
	for _, from := range []uint64{100, 500} {
		wg.Add(1)
		go func(from uint64) {
			defer wg.Done()
			hs := s.fetchHeaderBatch(peer, from, 5)
			mu.Lock()
			defer mu.Unlock()
			if len(hs) > 0 {
				got[from] = hs[0].Number.Uint64()
			}
		}(from)
	}
	wg.Wait()
	for _, from := range []uint64{100, 500} {
		if got[from] != from {
			t.Errorf("request from %d got batch starting at %d", from, got[from])
		}
	}
}
