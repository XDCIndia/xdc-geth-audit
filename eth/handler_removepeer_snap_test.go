// Copyright 2026 XDC Network

package eth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/eth/ethconfig"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// TestRemovePeerDuringSnapRunning: during SNAP_RUNNING only the useless-peer drop
// (header skeleton) is skipped; removePeer, used for protocol faults, still
// disconnects (XDC-HUNT-04).
func TestRemovePeerDuringSnapRunning(t *testing.T) {
	th := newTestHandlerWithBlocks(0, ethconfig.FullSync)
	defer th.close()
	old := th.handler.xdcSync
	defer func() { th.handler.xdcSync = old }()
	th.handler.xdcSync = &xdcSyncer{}
	th.handler.xdcSync.snapPhase.Store(int32(phaseSnapRunning))

	newPeer := func(id byte) (*ethproto.Peer, *p2p.MsgPipeRW, func()) {
		appRW, netRW := p2p.MsgPipe()
		p := ethproto.NewPeer(ethproto.XDPOS2, p2p.NewPeerPipe(enode.ID{id}, "t", nil, appRW), appRW, nil, nil, nil)
		if err := th.handler.peers.registerPeer(p, nil); err != nil {
			t.Fatal(err)
		}
		return p, netRW, func() { p.Close(); appRW.Close(); netRW.Close() }
	}
	// disconnected reports whether Disconnect closed the pipe.
	disconnected := func(netRW *p2p.MsgPipeRW) bool {
		done := make(chan struct{})
		go func() { netRW.ReadMsg(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(300 * time.Millisecond):
			return false
		}
	}

	p1, net1, close1 := newPeer(1)
	defer close1()
	th.handler.removeUselessPeer(p1.ID())
	if disconnected(net1) {
		t.Fatal("useless peer was disconnected during SNAP_RUNNING")
	}

	p2, net2, close2 := newPeer(2)
	defer close2()
	th.handler.removePeer(p2.ID())
	if !disconnected(net2) {
		t.Fatal("removePeer did not disconnect during SNAP_RUNNING")
	}

	// Outside SNAP_RUNNING the useless peer is dropped too.
	th.handler.xdcSync.snapPhase.Store(int32(phaseFullSync))
	th.handler.removeUselessPeer(p1.ID())
	if !disconnected(net1) {
		t.Fatal("useless peer was not disconnected outside SNAP_RUNNING")
	}
}
