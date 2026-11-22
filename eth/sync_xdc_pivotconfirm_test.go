// Copyright 2026 XDC Network

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/p2p/enode"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func pivotHdr(n int64, root byte) *types.Header {
	return &types.Header{Number: big.NewInt(n), Root: common.Hash{root}}
}

// TestConfirmPivot: a pivot header served by one peer is adopted only when a
// second peer serves the same header; a lying source cannot set the pivot or
// its state root alone (XDC-HUNT-32).
func TestConfirmPivot(t *testing.T) {
	honest, forged := pivotHdr(5000, 1), pivotHdr(5000, 0xbd)
	answers := func(hs ...*types.Header) func(int) *types.Header {
		return func(i int) *types.Header { return hs[i] }
	}
	for _, tc := range []struct {
		name     string
		pivot    *types.Header
		others   []*types.Header
		wantOK   bool
		wantConf int
	}{
		{"no other peer", forged, nil, false, -1},
		{"lying source, honest second peer", forged, []*types.Header{honest}, false, 0},
		{"lying source, nobody answers", forged, []*types.Header{nil, nil}, false, -1},
		{"honest source confirmed", honest, []*types.Header{honest}, true, -1},
		{"first peer silent, second confirms", honest, []*types.Header{nil, honest}, true, -1},
		{"wrong number is not an answer", honest, []*types.Header{pivotHdr(4999, 1), honest}, true, -1},
		{"second peer disagrees", honest, []*types.Header{forged, honest}, false, 0},
	} {
		ok, conflict := confirmPivot(tc.pivot, len(tc.others), answers(tc.others...))
		if ok != tc.wantOK || conflict != tc.wantConf {
			t.Errorf("%s: got (%v, %d), want (%v, %d)", tc.name, ok, conflict, tc.wantOK, tc.wantConf)
		}
	}
}

// pivotNet builds a fake net whose remote chain is longer than the snap pivot
// backoff, with peers that serve either the true chain or a forged pivot header.
func pivotNet(t *testing.T) (*xdcFakeNet, *xdcSyncer, uint64, *types.Header) {
	t.Helper()
	n := newXDCFakeNet(t, 0, int(snapPivotBackoff)+100)
	s := n.newTestXDCSyncer(t)
	tip := n.remote[len(n.remote)-1].Header()
	pivotNum := tip.Number.Uint64() - snapPivotBackoff
	forged := types.CopyHeader(n.remote[pivotNum-1].Header())
	forged.Root = common.Hash{0xbd}
	return n, s, pivotNum, forged
}

func (n *xdcFakeNet) addSnapPeer(t *testing.T, id byte, forged *types.Header) {
	t.Helper()
	tip := n.remote[len(n.remote)-1].Header()
	serve := func(origin uint64, amount int) []*types.Header {
		if origin == 0 { // by-hash request: the peer's head
			return []*types.Header{tip}
		}
		if forged != nil && origin == forged.Number.Uint64() {
			return []*types.Header{forged}
		}
		return n.canonicalHeaders(origin, amount)
	}
	n.addPeer(t, id, serve)
	n.handler.peers.peer(enode.ID{id}.String()).snapExt = &snapPeer{snap.NewFakePeer(snap.SNAP1, enode.ID{id}.String(), nil)}
}

// TestFetchPeerHeadHeaderNeedsSecondPeer: a single lying peer cannot set the snap
// pivot or its root; two agreeing peers can (XDC-HUNT-32).
func TestFetchPeerHeadHeaderNeedsSecondPeer(t *testing.T) {
	// One peer alone, even honest: no confirmation, no pivot.
	n, s, _, _ := pivotNet(t)
	n.addSnapPeer(t, 1, nil)
	if h := s.fetchPeerHeadHeader(); h != nil {
		t.Fatalf("pivot %d selected from a single peer", h.Number)
	}
	// A liar and an honest peer disagree: whichever is the source, no pivot.
	n, s, _, forged := pivotNet(t)
	n.addSnapPeer(t, 1, forged)
	n.addSnapPeer(t, 2, nil)
	if h := s.fetchPeerHeadHeader(); h != nil {
		t.Fatalf("pivot %d (root %x) selected while two peers disagree", h.Number, h.Root)
	}
	// Two honest peers confirm each other.
	n, s, pivotNum, _ := pivotNet(t)
	n.addSnapPeer(t, 1, nil)
	n.addSnapPeer(t, 2, nil)
	h := s.fetchPeerHeadHeader()
	if h == nil || h.Number.Uint64() != pivotNum || h.Hash() != n.remote[pivotNum-1].Hash() {
		t.Fatalf("honest pair did not select the pivot: %v", h)
	}
}
