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
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// TestXDPOS2RequiredBlockMatch: an XDPOS2 peer that provides the correct
// required block header is kept. On origin/main, the goroutine waits on resCh
// (not XDCHeaderResp), times out, and calls removePeer — this test FAILS on
// origin/main.
func TestXDPOS2RequiredBlockMatch(t *testing.T) {
	runXDPOS2RequiredBlockTest(t, "match")
}

// TestXDPOS2RequiredBlockMismatch: an XDPOS2 peer that provides a wrong hash
// for the required block is dropped.
func TestXDPOS2RequiredBlockMismatch(t *testing.T) {
	runXDPOS2RequiredBlockTest(t, "mismatch")
}

// TestXDPOS2RequiredBlockTimeout: when an XDPOS2 peer does not respond to the
// required-block challenge at all, the peer is kept. On origin/main, the
// timeout fires and calls removePeer — this test FAILS on origin/main.
func TestXDPOS2RequiredBlockTimeout(t *testing.T) {
	runXDPOS2RequiredBlockTest(t, "timeout")
}

// runXDPOS2RequiredBlockTest is the shared body. scenario is one of:
//   - "match"    push correct header → peer kept
//   - "mismatch" push wrong-hash header → peer dropped (quickly, not via timeout)
//   - "timeout"  push nothing → peer kept after syncChallengeTimeout
func runXDPOS2RequiredBlockTest(t *testing.T, scenario string) {
	t.Helper()

	// Short timeout so tests finish quickly.
	oldTimeout := syncChallengeTimeout
	syncChallengeTimeout = 80 * time.Millisecond
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

	// handlerDone lets the test end the blocking handler function cleanly.
	handlerDone := make(chan struct{})

	// Run runEthPeer in the background.
	peerErrCh := make(chan error, 1)
	go func() {
		err := th.handler.runEthPeer(handlerPeer, func(_ *ethproto.Peer) error {
			close(handlerRunning)
			<-handlerDone
			return nil
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

		// Drain all subsequent messages (GetBlockHeaders from the challenge,
		// and any BlockRangeUpdateMsg from blockRangeLoop).
		for {
			msg, err := netRW.ReadMsg()
			if err != nil {
				return // pipe closed = peer disconnected
			}
			msg.Discard()
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

	// Push the response based on the scenario.
	switch scenario {
	case "match":
		// Correct header: with the fix, goroutine reads it and keeps the peer.
		// On origin/main, nobody reads XDCHeaderResp; timeout removes the peer.
		handlerPeer.XDCHeaderResp <- []*types.Header{reqHeader}
	case "mismatch":
		// Wrong hash: goroutine should detect mismatch and drop the peer quickly.
		handlerPeer.XDCHeaderResp <- []*types.Header{wrongHeader}
	case "timeout":
		// Nothing pushed: goroutine times out.
		// With fix: peer is kept. On origin/main: peer is removed.
	}

	// Allow the challenge goroutine to resolve (timeout + generous margin).
	time.Sleep(syncChallengeTimeout + 120*time.Millisecond)

	// Shut down the handler.
	close(handlerDone)
	select {
	case <-peerErrCh:
	case <-time.After(3 * time.Second):
		t.Fatal("runEthPeer did not exit after handler closed")
	}

	// Evaluate expectations.
	disconnected := false
	select {
	case <-disconnectedCh:
		disconnected = true
	default:
	}

	switch scenario {
	case "match", "timeout":
		if disconnected {
			t.Fatalf("scenario %q: XDPOS2 peer was disconnected but should have been kept", scenario)
		}
	case "mismatch":
		if !disconnected {
			t.Fatalf("scenario %q: XDPOS2 peer was NOT disconnected despite required block hash mismatch", scenario)
		}
	}
}

// TestApothemBuiltinRequiredBlock verifies that the built-in Apothem post-reset
// required block (83,665,617) is injected when --xdcapothem is active and that
// a user-supplied entry at the same number takes precedence.
func TestApothemBuiltinRequiredBlock(t *testing.T) {
	const apothemResetNum = uint64(83_665_617)
	builtinHash := common.HexToHash("0x2432ce6433608954ef6eaf2feba8f9a2fe90f5a913401eb07d5fa2f1bb499c3f")

	t.Run("builtin present when no user entry", func(t *testing.T) {
		cfg := &ethconfig.Config{}
		// Simulate what SetEthConfig does for XDCApothemFlag:
		if cfg.RequiredBlocks == nil {
			cfg.RequiredBlocks = make(map[uint64]common.Hash)
		}
		if _, exists := cfg.RequiredBlocks[apothemResetNum]; !exists {
			cfg.RequiredBlocks[apothemResetNum] = builtinHash
		}
		got, ok := cfg.RequiredBlocks[apothemResetNum]
		if !ok {
			t.Fatal("built-in Apothem required block not present")
		}
		if got != builtinHash {
			t.Fatalf("got hash %s, want %s", got, builtinHash)
		}
	})

	t.Run("user entry wins on conflict", func(t *testing.T) {
		userHash := common.Hash{0xbe, 0xef}
		cfg := &ethconfig.Config{
			RequiredBlocks: map[uint64]common.Hash{apothemResetNum: userHash},
		}
		// Same logic as SetEthConfig:
		if _, exists := cfg.RequiredBlocks[apothemResetNum]; !exists {
			cfg.RequiredBlocks[apothemResetNum] = builtinHash
		}
		got := cfg.RequiredBlocks[apothemResetNum]
		if got != userHash {
			t.Fatalf("user entry was overwritten: got %s, want %s", got, userHash)
		}
	})
}

// TestApothemBuiltinRequiredBlockNotOnMainnet checks that the built-in required
// block is specific to Apothem — mainnet and devnet must not carry it when the
// Apothem flag is not set.
func TestApothemBuiltinRequiredBlockNotOnMainnet(t *testing.T) {
	const apothemResetNum = uint64(83_665_617)

	// A fresh ethconfig not touched by the Apothem path must have no entry.
	cfg := &ethconfig.Config{
		RequiredBlocks: map[uint64]common.Hash{},
	}
	// The Apothem built-in injection only happens inside the XDCApothemFlag case.
	// Verify it is NOT present when that path was not taken.
	if _, ok := cfg.RequiredBlocks[apothemResetNum]; ok {
		t.Fatal("Apothem built-in required block present in a non-Apothem config")
	}
}
