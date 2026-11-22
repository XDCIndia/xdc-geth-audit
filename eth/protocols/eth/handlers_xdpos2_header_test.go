// Copyright 2026 The XDC Network Authors
// Regression tests for an XDPOS2 BlockHeaders reply that used to drop the
// in-flight request before the batch was checked.

package eth

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/tracker"
	"github.com/ethereum/go-ethereum/rlp"
)

// rawDecoder is a test Decoder whose Decode is rlp.DecodeBytes.
type rawDecoder struct {
	payload []byte
}

func (d rawDecoder) Decode(val interface{}) error {
	return rlp.DecodeBytes(d.payload, val)
}

func newHeaderTestPeer() *Peer {
	peer := &Peer{
		id:            "0123456789abcdef",
		version:       XDPOS2,
		XDCHeaderResp: make(chan []*types.Header, 1),
		resDispatch:   make(chan *response, 4),
	}
	peer.tracker = tracker.New(p2p.Cap{Name: "eth", Version: uint(XDPOS2)}, peer.id, time.Minute)
	go func() {
		for op := range peer.resDispatch {
			op.fail <- errDanglingResponse
		}
	}()
	return peer
}

func TestXDPOS2HeaderReplyConsumesUnmatchedRequest(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	peer.pushLegacyHeaderReqNumber(42, 2, 100, true)
	peer.pushLegacyHeaderReqNumber(43, 2, 200, true)

	empty, err := rlp.EncodeToBytes([]*types.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{empty}, peer); err != nil {
		t.Fatal(err)
	}
	first, ok := peer.peekLegacyHeaderReq()
	if !ok || first.id != 42 {
		t.Fatalf("oldest remaining request id = %d, want 42", first.id)
	}

	peer.pushLegacyHeaderReqNumber(44, 1, 100, true)
	batch, err := rlp.EncodeToBytes([]*types.Header{{Number: big.NewInt(999)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{batch}, peer); err != nil {
		t.Fatal(err)
	}
	var ids []uint64
	for {
		req := peer.popLegacyHeaderReq()
		if req.id == 0 {
			break
		}
		ids = append(ids, req.id)
	}
	if len(ids) != 3 || ids[0] != 42 || ids[1] != 43 || ids[2] != 44 {
		t.Fatalf("queued ids = %v, want [42 43 44]", ids)
	}
	select {
	case got := <-peer.XDCHeaderResp:
		t.Fatalf("unmatched reply delivered %d headers", len(got))
	default:
	}
}

// TestXDPOS2HeaderReplyKeepsUnsinkedRequest checks a caller with no private
// sink: a bad batch must not be written to XDCHeaderResp and must not drop
// the queued request.
func TestXDPOS2HeaderReplyKeepsUnsinkedRequest(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	peer.pushLegacyHeaderReqNumber(44, 1, 100, false)

	empty, err := rlp.EncodeToBytes([]*types.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{empty}, peer); err != nil {
		t.Fatal(err)
	}
	batch, err := rlp.EncodeToBytes([]*types.Header{{Number: big.NewInt(999)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{batch}, peer); err != nil {
		t.Fatal(err)
	}
	got, ok := peer.peekLegacyHeaderReq()
	if !ok || got.id != 44 {
		t.Fatalf("request id = %d, want 44 still queued", got.id)
	}
	select {
	case headers := <-peer.XDCHeaderResp:
		t.Fatalf("bad reply written to XDCHeaderResp (%d headers)", len(headers))
	default:
	}
}

func TestXDPOS2HeaderReplyFulfilsMatchingRequest(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	for _, id := range []uint64{42, 43} {
		if err := peer.tracker.Track(tracker.Request{
			ID:       id,
			Size:     2,
			ReqCode:  GetBlockHeadersMsg,
			RespCode: BlockHeadersMsg,
		}); err != nil {
			t.Fatal(err)
		}
	}
	peer.pushLegacyHeaderReqNumber(42, 2, 100, true)
	peer.pushLegacyHeaderReqNumber(43, 2, 200, true)

	// The anchor is not the first header. Only request 42 is answered.
	batch, err := rlp.EncodeToBytes([]*types.Header{
		{Number: big.NewInt(99)},
		{Number: big.NewInt(100)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{batch}, peer); err != nil {
		t.Fatal(err)
	}
	if got := peer.popLegacyHeaderReq(); got.id != 43 {
		t.Fatalf("oldest remaining request id = %d, want 43", got.id)
	}
	if extra := peer.popLegacyHeaderReq(); extra.id != 0 {
		t.Fatalf("request %d still queued after its anchor was delivered", extra.id)
	}
	if err := peer.tracker.Fulfil(tracker.Response{ID: 42, MsgCode: BlockHeadersMsg, Size: 1}); err != tracker.ErrNoMatchingRequest {
		t.Fatalf("request 42 still tracked or unexpected err: %v", err)
	}
	if err := peer.tracker.Fulfil(tracker.Response{ID: 43, MsgCode: BlockHeadersMsg, Size: 1}); err != nil {
		t.Fatalf("request 43 was fulfilled with request 42: %v", err)
	}
	select {
	case <-peer.XDCHeaderResp:
		t.Fatal("request with its own sink was copied onto XDCHeaderResp")
	default:
	}

	// A hash-anchored batch that does not contain the hash stays queued.
	peer.pushLegacyHeaderReqHash(45, 1, common.HexToHash("0xabc"), false)
	off, err := rlp.EncodeToBytes([]*types.Header{{Number: big.NewInt(100), Time: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{off}, peer); err != nil {
		t.Fatal(err)
	}
	if got, ok := peer.peekLegacyHeaderReq(); !ok || got.id != 45 {
		t.Fatalf("hash request id = %d, want 45 still queued", got.id)
	}
	select {
	case <-peer.XDCHeaderResp:
		t.Fatal("off-anchor hash reply written to XDCHeaderResp")
	default:
	}
}

func TestXDPOS2HeaderReplyDropsPeerWithNothingInFlight(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	batch, err := rlp.EncodeToBytes([]*types.Header{{Number: big.NewInt(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{batch}, peer); err == nil {
		t.Fatal("BlockHeaders with nothing in flight returned nil")
	}
	select {
	case <-peer.XDCHeaderResp:
		t.Fatal("unsolicited reply was delivered")
	default:
	}
}

func TestXDPOS2HeaderReplyDeliversMatchingBatchToSyncer(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	if err := peer.tracker.Track(tracker.Request{
		ID:       7,
		Size:     1,
		ReqCode:  GetBlockHeadersMsg,
		RespCode: BlockHeadersMsg,
	}); err != nil {
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(100), Time: 5}
	peer.pushLegacyHeaderReqHash(7, 1, header.Hash(), false)
	batch, err := rlp.EncodeToBytes([]*types.Header{header})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, rawDecoder{batch}, peer); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-peer.XDCHeaderResp:
		if len(got) != 1 || got[0].Number.Uint64() != 100 {
			t.Fatalf("delivered header number = %v, want 100", got)
		}
	default:
		t.Fatal("matching reply was not written to XDCHeaderResp")
	}
	if got := peer.popLegacyHeaderReq(); got.id != 0 {
		t.Fatalf("request %d still queued after a matching hash reply", got.id)
	}
	if err := peer.tracker.Fulfil(tracker.Response{ID: 7, MsgCode: BlockHeadersMsg, Size: 1}); err != tracker.ErrNoMatchingRequest {
		t.Fatalf("request 7 still tracked: %v", err)
	}
}

// r2Decoder is a test Decoder whose Decode is rlp.DecodeBytes.
type r2Decoder struct {
	payload []byte
}

func (d r2Decoder) Decode(val interface{}) error {
	return rlp.DecodeBytes(d.payload, val)
}

func TestXDPOS2GenesisHeaderAnchorNeverMatches(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	peer.pushLegacyHeaderReqNumber(7, 2, 0, false)

	h0 := &types.Header{Number: big.NewInt(0), GasLimit: 8_000_000, Time: 1}
	h1 := &types.Header{Number: big.NewInt(1), ParentHash: h0.Hash(), GasLimit: 8_000_000, Time: 2}
	batch, err := rlp.EncodeToBytes([]*types.Header{h0, h1})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, r2Decoder{batch}, peer); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if _, ok := peer.peekLegacyHeaderReq(); ok {
		t.Fatal("request for block 0 still queued")
	}
	select {
	case hdrs := <-peer.XDCHeaderResp:
		if hdrs[0].Number.Uint64() != 0 {
			t.Fatalf("delivered %d, want 0", hdrs[0].Number)
		}
	default:
		t.Fatal("genesis batch was not delivered")
	}
}

func TestStaleHeaderRequestBlocksLaterMatch(t *testing.T) {
	peer := newHeaderTestPeer()
	defer peer.tracker.Stop()
	peer.pushLegacyHeaderReqNumber(1, 1, 100, false)
	peer.pushLegacyHeaderReqNumber(2, 1, 200, false)
	peer.removeLegacyHeaderReq(1)

	h := &types.Header{Number: big.NewInt(200), GasLimit: 8_000_000, Time: 200}
	batch, err := rlp.EncodeToBytes([]*types.Header{h})
	if err != nil {
		t.Fatal(err)
	}
	if err := handleBlockHeaders(nil, r2Decoder{batch}, peer); err != nil {
		t.Fatalf("handler: %v", err)
	}
	select {
	case hdrs := <-peer.XDCHeaderResp:
		if hdrs[0].Number.Uint64() != 200 {
			t.Fatalf("delivered %d, want 200", hdrs[0].Number)
		}
	default:
		t.Fatal("batch for the later request was not delivered")
	}
}
