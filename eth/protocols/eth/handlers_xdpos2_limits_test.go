// Copyright 2026 XDC Network

package eth

import (
	"bytes"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/tracker"
	"github.com/ethereum/go-ethereum/rlp"
)

type recBackend struct {
	Backend
	got *NodeDataPacket
}

func (b *recBackend) Handle(_ *Peer, pkt Packet) error {
	b.got = pkt.(*NodeDataPacket)
	return nil
}

// TestHandleNodeDataItemCap: a NodeData message with more than MaxNodeDataItems
// items is refused before it is decoded into per-item slices (XDC-HUNT-18).
func TestHandleNodeDataItemCap(t *testing.T) {
	enc := func(n int) []byte {
		items := make([][]byte, n)
		for i := range items {
			items[i] = []byte{byte(i)}
		}
		b, err := rlp.EncodeToBytes(items)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	be := &recBackend{}
	if err := handleNodeData(be, rawDecoder{enc(MaxNodeDataItems)}, nil); err != nil {
		t.Fatalf("message at the limit refused: %v", err)
	}
	if be.got == nil || len(*be.got) != MaxNodeDataItems || !bytes.Equal((*be.got)[5], []byte{5}) {
		t.Fatalf("backend did not get the %d items", MaxNodeDataItems)
	}
	be.got = nil
	if err := handleNodeData(be, rawDecoder{enc(MaxNodeDataItems + 1)}, nil); err == nil {
		t.Fatal("message above the limit accepted")
	}
	if be.got != nil {
		t.Fatal("oversized message reached the backend")
	}
}

func newReceiptsTestPeer() *Peer {
	peer := &Peer{
		id:             "0123456789abcdef",
		version:        XDPOS2,
		XDCReceiptResp: make(chan []types.Receipts, 4),
		resDispatch:    make(chan *response, 4),
	}
	peer.tracker = tracker.New(p2p.Cap{Name: "eth", Version: uint(XDPOS2)}, peer.id, time.Minute)
	go func() {
		for op := range peer.resDispatch {
			op.fail <- errDanglingResponse
		}
	}()
	return peer
}

// TestHandleReceiptsXDPOS2RequiresRequest: a Receipts message with no request in
// flight is not decoded and a peer that keeps sending them is disconnected; a
// reply to a request is decoded once, and a list with more blocks than the
// serving limit is refused (XDC-HUNT-50).
func TestHandleReceiptsXDPOS2RequiresRequest(t *testing.T) {
	peer := newReceiptsTestPeer()
	defer peer.tracker.Stop()

	one, err := rlp.EncodeToBytes([]types.Receipts{{}})
	if err != nil {
		t.Fatal(err)
	}
	// Garbage that would fail to decode: an unsolicited message is not decoded,
	// so it is not an error either.
	garbage := rawDecoder{[]byte{0xc1, 0xff}}
	for i := 0; i < maxUnsolicitedReceipts; i++ {
		if err := handleReceiptsXDPOS2(nil, garbage, peer); err != nil {
			t.Fatalf("unsolicited message %d: %v", i, err)
		}
	}
	if err := handleReceiptsXDPOS2(nil, garbage, peer); err == nil {
		t.Fatal("peer not disconnected after repeated unsolicited receipts")
	}
	select {
	case <-peer.XDCReceiptResp:
		t.Fatal("unsolicited receipts were delivered")
	default:
	}

	// A request resets the count; its reply is decoded and delivered once.
	peer.legacyReceiptsReqCount.Store(1)
	peer.unsolicitedReceipts.Store(0)
	handleReceiptsXDPOS2(nil, rawDecoder{one}, peer) // dispatch error is irrelevant here
	select {
	case got := <-peer.XDCReceiptResp:
		if len(got) != 1 {
			t.Fatalf("delivered %d blocks, want 1", len(got))
		}
	default:
		t.Fatal("reply to a request was not delivered")
	}
	if peer.legacyReceiptsReqCount.Load() != 0 {
		t.Fatal("request not consumed by its reply")
	}

	// More blocks than the serving side would ever send.
	big := make([]types.Receipts, maxReceiptsServe+1)
	payload, err := rlp.EncodeToBytes(big)
	if err != nil {
		t.Fatal(err)
	}
	peer.legacyReceiptsReqCount.Store(1)
	if err := handleReceiptsXDPOS2(nil, rawDecoder{payload}, peer); err == nil {
		t.Fatal("receipts for more blocks than maxReceiptsServe accepted")
	}
}
