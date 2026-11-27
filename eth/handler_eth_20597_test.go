// Copyright 2026 XDC Network
// Tests for issue #1844: required-block challenge on XDPOS2 peers.
//
// On origin/main, the challenge goroutine waits on resCh which is never
// fulfilled for XDPOS2 peers (their header replies go to peer.XDCHeaderResp).
// After syncChallengeTimeout the goroutine calls removePeer, evicting every
// XDPOS2 peer — including ones on the canonical chain. The fix adds an XDPOS2
// branch that reads from peer.XDCHeaderResp with a bounded timeout and only
// drops the peer on an explicit hash mismatch.
//
// Review ID: SYNC-04 (Medium).

package eth

import (
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// TestXDPOS2RequiredBlockForeignBatch: a foreign-number batch must not end the
// v100 challenge; the later wrong-hash reply at the challenge number still
// drops the peer. Refs F-2026-20597.
func TestXDPOS2RequiredBlockForeignBatch(t *testing.T) {
	runXDPOS2RequiredBlockTest(t, "foreign-then-mismatch")
}

// TestXDPOS2RequiredBlockWithSyncerReading: a syncer reader waits on the peer's
// shared header channel when the wrong-hash challenge reply arrives. The reply
// goes to the challenge only, so the peer is still dropped. Refs F-2026-20597.
func TestXDPOS2RequiredBlockWithSyncerReading(t *testing.T) {
	runXDPOS2RequiredBlockTest(t, "syncer-reading")
}

// runXDPOS2RequiredBlockTest is the shared body. scenario is one of:
//   - "match"    push correct header → peer kept
//   - "mismatch" push wrong-hash header → peer dropped (quickly, not via timeout)
//   - "timeout"  push nothing → peer kept after syncChallengeTimeout
func runXDPOS2RequiredBlockTest(t *testing.T, scenario string) {
	t.Helper()

	// Short timeout so tests finish quickly.
	oldTimeout := syncChallengeTimeout
	syncChallengeTimeout = 150 * time.Millisecond
	defer func() { syncChallengeTimeout = oldTimeout }()

	// Construct a header whose hash becomes the required block hash.
	// Using Number=1, Difficulty=1 gives a deterministic hash.
	reqHeader := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
	}
	reqNum := reqHeader.Number.Uint64()
	reqHash := reqHeader.Hash()

	// For the mismatch scenario we need a header that hashes differently.
	wrongHeader := &types.Header{
		Number:     big.NewInt(int64(reqNum)),
		Difficulty: big.NewInt(999), // different → different hash
	}
	if wrongHeader.Hash() == reqHash {
		t.Fatal("test setup: wrongHeader hash collides with reqHash")
	}

	// Build a test handler with the required block pinned.
	th := newTestHandlerWithBlocks(0, ethconfig.FullSync)
	defer th.close()
	th.handler.requiredBlocks = map[uint64]common.Hash{reqNum: reqHash}

	// Create an XDPOS2 peer connected via an in-memory message pipe.
	// p2p.NewPeerPipe closes appRW when Disconnect() is called, so we can
	// detect disconnect by watching netRW.ReadMsg() for an error.
	appRW, netRW := p2p.MsgPipe()
	defer appRW.Close()
	defer netRW.Close()

	p2pPeer := p2p.NewPeerPipe(enode.ID{42}, "xdpos2-test", nil, appRW)
	handlerPeer := ethproto.NewPeer(ethproto.XDPOS2, p2pPeer, appRW, nil, nil, nil)
	defer handlerPeer.Close()

	// disconnectedCh is closed when netRW.ReadMsg() returns an error — the
	// signal that p2p.Peer.Disconnect was called (appRW closed).
	disconnectedCh := make(chan struct{})

	// handlerRunning is closed once the handler function is executing, i.e.
	// handshake finished and required-block goroutines are spawned.
	handlerRunning := make(chan struct{})

	// The syncer reads XDCHeaderResp of every connected peer. This reader
	// stands in for a syncer header fetch in flight when the challenge reply
	// arrives: the challenge reply must never reach it (F-2026-20597).
	var (
		syncerSaw  atomic.Int32
		readerStop = make(chan struct{})
	)
	defer close(readerStop)
	go func() {
		for {
			select {
			case <-handlerPeer.XDCHeaderResp:
				syncerSaw.Add(1)
			case <-readerStop:
				return
			}
		}
	}()

	// Run runEthPeer in the background. The handler runs the real eth message
	// loop, so a header reply takes the same route as on a live connection.
	peerErrCh := make(chan error, 1)
	go func() {
		err := th.handler.runEthPeer(handlerPeer, func(p *ethproto.Peer) error {
			close(handlerRunning)
			return ethproto.Handle((*ethHandler)(th.handler), p)
		})
		peerErrCh <- err
	}()

	// Net side: complete the XDPOS2 handshake, then drain messages and watch
	// for disconnect.
	genesis := th.chain.Genesis()
	latest := th.chain.CurrentHeader()

	handshakeDone := make(chan struct{})
	go func() {
		defer close(disconnectedCh)

		// handshake62: both sides send and receive StatusMsg concurrently.
		hsErrc := make(chan error, 2)
		go func() {
			hsErrc <- p2p.Send(netRW, ethproto.StatusMsg, &ethproto.StatusPacket62{
				ProtocolVersion: ethproto.XDPOS2,
				NetworkID:       1,
				TD:              new(big.Int).Set(latest.Difficulty),
				Head:            latest.Hash(),
				Genesis:         genesis.Hash(),
			})
		}()
		go func() {
			msg, err := netRW.ReadMsg()
			if err != nil {
				hsErrc <- err
				return
			}
			msg.Discard()
			hsErrc <- nil
		}()
		for range 2 {
			if err := <-hsErrc; err != nil {
				return // pipe closed during handshake
			}
		}
		close(handshakeDone)

		// Answer the challenge (GetBlockHeaders) per scenario and drain every
		// other message (e.g. BlockRangeUpdateMsg from blockRangeLoop).
		for {
			msg, err := netRW.ReadMsg()
			if err != nil {
				return // pipe closed = peer disconnected
			}
			code := msg.Code
			msg.Discard()
			if code != ethproto.GetBlockHeadersMsg {
				continue
			}
			reply := func(h ...*types.Header) { go p2p.Send(netRW, ethproto.BlockHeadersMsg, h) }
			switch scenario {
			case "match":
				reply(reqHeader)
			case "mismatch", "syncer-reading":
				reply(wrongHeader)
			case "foreign-then-mismatch":
				// A batch for another number lands first. The peer leaves the
				// challenge queued; the wrong-hash reply at the challenge
				// number arrives later and drops the peer.
				reply(&types.Header{Number: big.NewInt(int64(reqNum) + 7)})
				time.Sleep(20 * time.Millisecond)
				reply(wrongHeader)
			}
		}
	}()

	// Wait for handshake and handler to be running (goroutines spawned).
	select {
	case <-handshakeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("XDPOS2 handshake timed out")
	}
	select {
	case <-handlerRunning:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start running after handshake")
	}

	// Allow the challenge goroutine to resolve (timeout + generous margin).
	time.Sleep(syncChallengeTimeout + 120*time.Millisecond)

	// Evaluate expectations before the pipe is closed.
	disconnected := false
	select {
	case <-disconnectedCh:
		disconnected = true
	default:
	}
	if n := syncerSaw.Load(); n != 0 {
		t.Fatalf("scenario %q: the challenge reply reached the syncer's header channel (%d batches)", scenario, n)
	}

	// Shut down the handler.
	netRW.Close()
	select {
	case <-peerErrCh:
	case <-time.After(3 * time.Second):
		t.Fatal("runEthPeer did not exit after the pipe closed")
	}

	switch scenario {
	case "match", "timeout":
		if disconnected {
			t.Fatalf("scenario %q: XDPOS2 peer was disconnected but should have been kept", scenario)
		}
	case "mismatch", "foreign-then-mismatch", "syncer-reading":
		if !disconnected {
			t.Fatalf("scenario %q: XDPOS2 peer was NOT disconnected despite required block hash mismatch", scenario)
		}
	}
}
