// Copyright 2026 The XDC Network Authors
// Tests for #1861: XDPOS2RID (v101) with request-ID for header/body/receipt fetches (SYNC-10).

package eth

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	ethproto "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
)

// noopEthBackend satisfies ethproto.Backend for tests that only exercise the
// dispatcher path (ETH69/70). None of the backend methods are called during
// handleBlockBodies or handleReceipts69 for non-XDPOS2 peers.
type noopEthBackend struct{ chain *core.BlockChain }

func (b *noopEthBackend) Chain() *core.BlockChain                        { return b.chain }
func (b *noopEthBackend) TxPool() ethproto.TxPool                        { return nil }
func (b *noopEthBackend) BlobPool() ethproto.BlobPool                    { return nil }
func (b *noopEthBackend) AcceptTxs() bool                                { return false }
func (b *noopEthBackend) RunPeer(*ethproto.Peer, ethproto.Handler) error { return nil }
func (b *noopEthBackend) PeerInfo(enode.ID) interface{}                  { return nil }
func (b *noopEthBackend) Handle(*ethproto.Peer, ethproto.Packet) error   { return nil }

// TestPeerHasRequestID_SYNC10 verifies that peerHasRequestID returns true only
// for eth/69+ peers, never for XDPOS2 (100) or eth/63.
func TestPeerHasRequestID_SYNC10(t *testing.T) {
	n := newXDCFakeNet(t, 0, 1)
	cfg := n.chain.Config()

	cases := []struct {
		ver  uint
		want bool
	}{
		{ethproto.ETH69, true},
		{ethproto.ETH70, true},
		{ethproto.XDPOS2RID, true}, // XDPOS2RID=101 uses request-ID dispatcher
		{ethproto.XDPOS2, false},
		{ethproto.ETH63, false},
	}
	for _, tc := range cases {
		app, other := p2p.MsgPipe()
		t.Cleanup(func() { app.Close(); other.Close() })
		p := ethproto.NewPeer(tc.ver, p2p.NewPeer(enode.ID{}, "ver-test", nil), app, nil, nil, cfg)
		t.Cleanup(p.Close)
		if got := peerHasRequestID(p); got != tc.want {
			t.Errorf("version %d: peerHasRequestID=%v want %v", tc.ver, got, tc.want)
		}
	}
}

// TestFetchBodiesRequestID_SYNC10 verifies that fetchBodiesRequestID uses the
// request-ID dispatcher path (eth/69 wire) and returns the correct bodies.
func TestFetchBodiesRequestID_SYNC10(t *testing.T) {
	// Chain with non-empty tx bodies needed for hash verification.
	n := newXDCFakeNetWithTxs(t, 0, 3)
	s := n.newTestXDCSyncer(t)

	blockMap := make(map[common.Hash]*types.Block, len(n.remote))
	for _, b := range n.remote {
		blockMap[b.Hash()] = b
	}

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.ETH69, p2p.NewPeer(enode.ID{2}, "modern-bodies", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	// Dispatch loop on the local side: reads BlockBodiesMsg from app and
	// routes the response to the per-request sink channel via dispatchResponse.
	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	// Fake remote: reads GetBlockBodiesPacket (request-ID form) and replies
	// with BlockBodiesRLPPacket carrying the same RequestId.
	go func() {
		for {
			msg, err := remote.ReadMsg()
			if err != nil {
				return
			}
			if msg.Code != ethproto.GetBlockBodiesMsg {
				msg.Discard()
				continue
			}
			var req ethproto.GetBlockBodiesPacket
			if err := msg.Decode(&req); err != nil {
				return
			}
			var rawBodies ethproto.BlockBodiesRLPResponse
			for _, h := range req.GetBlockBodiesRequest {
				b := blockMap[h]
				if b == nil {
					break
				}
				txs, _ := rlp.EncodeToRawList(b.Transactions())
				uncles, _ := rlp.EncodeToRawList(b.Uncles())
				body := ethproto.BlockBody{Transactions: txs, Uncles: uncles}
				enc, _ := rlp.EncodeToBytes(&body)
				rawBodies = append(rawBodies, enc)
			}
			p2p.Send(remote, ethproto.BlockBodiesMsg, &ethproto.BlockBodiesRLPPacket{ //nolint:errcheck
				RequestId:              req.RequestId,
				BlockBodiesRLPResponse: rawBodies,
			})
		}
	}()

	hashes := []common.Hash{n.remote[0].Hash(), n.remote[1].Hash()}
	bodies := s.fetchBodiesRequestID(p, hashes)
	if len(bodies) != 2 {
		t.Fatalf("fetchBodiesRequestID: want 2 bodies, got %d", len(bodies))
	}
	// Each block has exactly one transaction; verify the tx list is non-empty.
	for i, bd := range bodies {
		txs, err := bd.Transactions.Items()
		if err != nil {
			t.Errorf("body %d: decode transactions: %v", i, err)
		}
		if len(txs) == 0 {
			t.Errorf("body %d: expected non-empty tx list", i)
		}
	}
}

// serveModern69 is a reusable fake-remote goroutine for ETH69 body+receipt
// tests. rcptCap limits how many receipt lists the remote returns per request
// (0 = all). rcptReqs counts GetReceiptsMsg deliveries.
func serveModern69(
	remote p2p.MsgReadWriter,
	blockMap map[common.Hash]*types.Block,
	receiptMap map[common.Hash]types.Receipts,
	rcptCap int,
	rcptReqs *atomic.Int32,
) {
	for {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		switch msg.Code {
		case ethproto.GetBlockBodiesMsg:
			var req ethproto.GetBlockBodiesPacket
			if err := msg.Decode(&req); err != nil {
				return
			}
			var rawBodies ethproto.BlockBodiesRLPResponse
			for _, h := range req.GetBlockBodiesRequest {
				b := blockMap[h]
				if b == nil {
					break
				}
				txs, _ := rlp.EncodeToRawList(b.Transactions())
				uncles, _ := rlp.EncodeToRawList(b.Uncles())
				body := ethproto.BlockBody{Transactions: txs, Uncles: uncles}
				enc, _ := rlp.EncodeToBytes(&body)
				rawBodies = append(rawBodies, enc)
			}
			p2p.Send(remote, ethproto.BlockBodiesMsg, &ethproto.BlockBodiesRLPPacket{ //nolint:errcheck
				RequestId:              req.RequestId,
				BlockBodiesRLPResponse: rawBodies,
			})
		case ethproto.GetReceiptsMsg:
			if rcptReqs != nil {
				rcptReqs.Add(1)
			}
			var req ethproto.GetReceiptsPacket69
			if err := msg.Decode(&req); err != nil {
				return
			}
			var list rlp.RawList[*ethproto.ReceiptList]
			cap := len(req.GetReceiptsRequest)
			if rcptCap > 0 && rcptCap < cap {
				cap = rcptCap
			}
			for _, h := range req.GetReceiptsRequest[:cap] {
				rcpts := receiptMap[h]
				if rcpts == nil {
					break
				}
				_ = list.Append(ethproto.NewReceiptList(rcpts))
			}
			p2p.Send(remote, ethproto.ReceiptsMsg, &ethproto.ReceiptsPacket69{ //nolint:errcheck
				RequestId: req.RequestId,
				List:      list,
			})
		default:
			msg.Discard()
		}
	}
}

// TestBackfillChunkModernPeer_SYNC10 is an end-to-end test of
// backfillBodyReceiptChunkModern: the ETH69 fake peer serves bodies and all
// receipts in one request, and both land in the DB.
func TestBackfillChunkModernPeer_SYNC10(t *testing.T) {
	const nBlocks = 3
	n := newXDCFakeNetWithTxs(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	blockMap := make(map[common.Hash]*types.Block, len(n.remote))
	receiptMap := make(map[common.Hash]types.Receipts, len(n.remote))
	for i, b := range n.remote {
		blockMap[b.Hash()] = b
		if i < len(n.remoteReceipts) {
			receiptMap[b.Hash()] = n.remoteReceipts[i]
		}
	}

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.ETH69, p2p.NewPeer(enode.ID{3}, "modern-full", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	var rcptReqs atomic.Int32
	go serveModern69(remote, blockMap, receiptMap, 0, &rcptReqs)

	// Backfill all remote blocks in one chunk.
	headers := make([]*types.Header, len(n.remote))
	for i, b := range n.remote {
		headers[i] = b.Header()
	}
	if tainted := s.backfillBodyReceiptChunkModern(p, headers, n.db); tainted {
		t.Fatal("backfillBodyReceiptChunkModern: peer unexpectedly tainted")
	}

	// Verify bodies and receipts landed in the DB.
	for _, b := range n.remote {
		h, num := b.Hash(), b.NumberU64()
		if !rawdb.HasBody(n.db, h, num) {
			t.Errorf("block %d: body missing from DB", num)
		}
		if !rawdb.HasReceipts(n.db, h, num) {
			t.Errorf("block %d: receipts missing from DB", num)
		}
	}

	// Exactly one GetReceiptsMsg must have been sent for the whole chunk.
	if got := rcptReqs.Load(); got != 1 {
		t.Errorf("receipt requests: want 1, got %d (was a per-block loop used?)", got)
	}
}

// TestBackfillChunkShortReceiptReply_SYNC10 verifies that when the peer returns
// fewer receipt lists than requested, the function writes verified entries and
// returns not-tainted; blocks missing receipts stay pending for the next attempt.
func TestBackfillChunkShortReceiptReply_SYNC10(t *testing.T) {
	const nBlocks = 3
	n := newXDCFakeNetWithTxs(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	blockMap := make(map[common.Hash]*types.Block, len(n.remote))
	receiptMap := make(map[common.Hash]types.Receipts, len(n.remote))
	for i, b := range n.remote {
		blockMap[b.Hash()] = b
		if i < len(n.remoteReceipts) {
			receiptMap[b.Hash()] = n.remoteReceipts[i]
		}
	}

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.ETH69, p2p.NewPeer(enode.ID{4}, "modern-short", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	// Remote returns only 1 receipt list even when asked for nBlocks — short reply.
	go serveModern69(remote, blockMap, receiptMap, 1, nil)

	headers := make([]*types.Header, len(n.remote))
	for i, b := range n.remote {
		headers[i] = b.Header()
	}
	if tainted := s.backfillBodyReceiptChunkModern(p, headers, n.db); tainted {
		t.Fatal("short receipt reply must not taint peer")
	}

	// Only the first block's receipt should be in the DB; the rest are pending.
	gotRcpt := 0
	for _, b := range n.remote {
		if rawdb.HasReceipts(n.db, b.Hash(), b.NumberU64()) {
			gotRcpt++
		}
	}
	if gotRcpt != 1 {
		t.Errorf("expected 1 receipt written after short reply, got %d", gotRcpt)
	}
	// All bodies must have been written (body fetch returned all nBlocks).
	for _, b := range n.remote {
		if !rawdb.HasBody(n.db, b.Hash(), b.NumberU64()) {
			t.Errorf("block %d: body missing from DB", b.NumberU64())
		}
	}
}

// serveModernV101 is a fake-remote goroutine for XDPOS2RID (v101) body+receipt
// tests. v101's RequestReceipts sends GetReceiptsPacket70 (p.version > ETH69),
// so this helper decodes that and replies with ReceiptsPacket70. Body requests
// use the same GetBlockBodiesPacket as ETH69. rcptReqs counts GetReceiptsMsg.
func serveModernV101(
	remote p2p.MsgReadWriter,
	blockMap map[common.Hash]*types.Block,
	receiptMap map[common.Hash]types.Receipts,
	rcptReqs *atomic.Int32,
) {
	for {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		switch msg.Code {
		case ethproto.GetBlockBodiesMsg:
			var req ethproto.GetBlockBodiesPacket
			if err := msg.Decode(&req); err != nil {
				return
			}
			var rawBodies ethproto.BlockBodiesRLPResponse
			for _, h := range req.GetBlockBodiesRequest {
				b := blockMap[h]
				if b == nil {
					break
				}
				txs, _ := rlp.EncodeToRawList(b.Transactions())
				uncles, _ := rlp.EncodeToRawList(b.Uncles())
				body := ethproto.BlockBody{Transactions: txs, Uncles: uncles}
				enc, _ := rlp.EncodeToBytes(&body)
				rawBodies = append(rawBodies, enc)
			}
			p2p.Send(remote, ethproto.BlockBodiesMsg, &ethproto.BlockBodiesRLPPacket{ //nolint:errcheck
				RequestId:              req.RequestId,
				BlockBodiesRLPResponse: rawBodies,
			})
		case ethproto.GetReceiptsMsg:
			if rcptReqs != nil {
				rcptReqs.Add(1)
			}
			// v101 sends GetReceiptsPacket70 (FirstBlockReceiptIndex added).
			var req ethproto.GetReceiptsPacket70
			if err := msg.Decode(&req); err != nil {
				return
			}
			var list rlp.RawList[*ethproto.ReceiptList]
			for _, h := range req.GetReceiptsRequest {
				rcpts := receiptMap[h]
				if rcpts == nil {
					break
				}
				_ = list.Append(ethproto.NewReceiptList(rcpts))
			}
			p2p.Send(remote, ethproto.ReceiptsMsg, &ethproto.ReceiptsPacket70{ //nolint:errcheck
				RequestId:           req.RequestId,
				LastBlockIncomplete: false,
				List:                list,
			})
		default:
			msg.Discard()
		}
	}
}

// TestBackfillChunkV101Peer_SYNC10 verifies that backfillBodyReceiptChunkModern
// works when the peer negotiates XDPOS2RID (v101). v101's RequestReceipts sends
// GetReceiptsPacket70; the fake remote decodes that and replies with
// ReceiptsPacket70. peerHasRequestID must return true for XDPOS2RID.
func TestBackfillChunkV101Peer_SYNC10(t *testing.T) {
	const nBlocks = 3
	n := newXDCFakeNetWithTxs(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	blockMap := make(map[common.Hash]*types.Block, len(n.remote))
	receiptMap := make(map[common.Hash]types.Receipts, len(n.remote))
	for i, b := range n.remote {
		blockMap[b.Hash()] = b
		if i < len(n.remoteReceipts) {
			receiptMap[b.Hash()] = n.remoteReceipts[i]
		}
	}

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2RID, p2p.NewPeer(enode.ID{5}, "v101-peer", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	if !peerHasRequestID(p) {
		t.Fatal("XDPOS2RID peer must have peerHasRequestID=true")
	}

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	var rcptReqs atomic.Int32
	go serveModernV101(remote, blockMap, receiptMap, &rcptReqs)

	headers := make([]*types.Header, len(n.remote))
	for i, b := range n.remote {
		headers[i] = b.Header()
	}
	if tainted := s.backfillBodyReceiptChunkModern(p, headers, n.db); tainted {
		t.Fatal("backfillBodyReceiptChunkModern: XDPOS2RID peer unexpectedly tainted")
	}

	for _, b := range n.remote {
		h, num := b.Hash(), b.NumberU64()
		if !rawdb.HasBody(n.db, h, num) {
			t.Errorf("block %d: body missing from DB", num)
		}
		if !rawdb.HasReceipts(n.db, h, num) {
			t.Errorf("block %d: receipts missing from DB", num)
		}
	}
	if got := rcptReqs.Load(); got != 1 {
		t.Errorf("receipt requests: want 1, got %d", got)
	}
}

// TestIsXDPOS2Family_SYNC10 verifies IsXDPOS2Family covers both v100 and v101
// and excludes all standard eth versions.
func TestIsXDPOS2Family_SYNC10(t *testing.T) {
	for _, tc := range []struct {
		ver  uint
		want bool
	}{
		{ethproto.XDPOS2, true},
		{ethproto.XDPOS2RID, true},
		{ethproto.ETH63, false},
		{ethproto.ETH69, false},
		{ethproto.ETH70, false},
	} {
		if got := ethproto.IsXDPOS2Family(tc.ver); got != tc.want {
			t.Errorf("IsXDPOS2Family(%d) = %v, want %v", tc.ver, got, tc.want)
		}
	}
}

// TestFetchHeaderBatchV101_SYNC10 verifies that fetchHeaderBatch works when the
// peer negotiates XDPOS2RID (v101). v101 uses the request-ID dispatcher for
// header responses; handleBlockHeaders must bridge the response to XDCHeaderResp
// so that the nil-sink fetchHeaderBatch caller is unblocked. Refs #1861 BLOCKER-1.
func TestFetchHeaderBatchV101_SYNC10(t *testing.T) {
	const nBlocks = 5
	n := newXDCFakeNet(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2RID, p2p.NewPeer(enode.ID{10}, "v101-headers", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	// Run the local handler so handleBlockHeaders fires for incoming responses.
	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	// Fake remote: reads GetBlockHeadersPacket (request-ID form), replies with
	// BlockHeadersRLPPacket carrying the same RequestId.
	go func() {
		for {
			msg, err := remote.ReadMsg()
			if err != nil {
				return
			}
			if msg.Code != ethproto.GetBlockHeadersMsg {
				msg.Discard()
				continue
			}
			var req ethproto.GetBlockHeadersPacket
			if err := msg.Decode(&req); err != nil {
				return
			}
			from := req.Origin.Number
			want := int(req.Amount)
			var hdrsRLP ethproto.BlockHeadersRLPResponse
			for _, b := range n.remote {
				if b.NumberU64() >= from && len(hdrsRLP) < want {
					enc, _ := rlp.EncodeToBytes(b.Header())
					hdrsRLP = append(hdrsRLP, enc)
				}
			}
			p2p.Send(remote, ethproto.BlockHeadersMsg, &ethproto.BlockHeadersRLPPacket{ //nolint:errcheck
				RequestId:               req.RequestId,
				BlockHeadersRLPResponse: hdrsRLP,
			})
		}
	}()

	from := n.remote[0].NumberU64()
	got := s.fetchHeaderBatch(p, from, 3)
	if len(got) == 0 {
		t.Fatal("fetchHeaderBatch: expected headers from XDPOS2RID peer, got none")
	}
	if got[0].Number.Uint64() != from {
		t.Errorf("fetchHeaderBatch: want first header %d, got %d", from, got[0].Number.Uint64())
	}
}

// TestFetchReceiptsBatchV101_SYNC10 verifies that fetchReceiptsBatch routes
// XDPOS2RID (v101) through fetchReceiptsRequestID and decodes the RLP result
// to types.Receipts. Refs #1861 BLOCKER-1.
func TestFetchReceiptsBatchV101_SYNC10(t *testing.T) {
	const nBlocks = 2
	n := newXDCFakeNetWithTxs(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	receiptMap := make(map[common.Hash]types.Receipts, len(n.remote))
	for i, b := range n.remote {
		if i < len(n.remoteReceipts) {
			receiptMap[b.Hash()] = n.remoteReceipts[i]
		}
	}

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2RID, p2p.NewPeer(enode.ID{11}, "v101-receipts", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	go serveModernV101(remote, nil, receiptMap, nil)

	headers := make([]*types.Header, len(n.remote))
	for i, b := range n.remote {
		headers[i] = b.Header()
	}
	got := s.fetchReceiptsBatch(p, headers)
	if len(got) != nBlocks {
		t.Fatalf("fetchReceiptsBatch v101: want %d receipt lists, got %d", nBlocks, len(got))
	}
	for i, rcpts := range got {
		if len(rcpts) == 0 {
			t.Errorf("block %d: empty receipt list", i)
		}
	}
}

// serveHeadersV101 is a fake-remote helper that reads GetBlockHeadersPacket
// (request-ID form used by v101) and replies with headers starting at
// req.Origin.Number.  Exported only within the test package.
func serveHeadersV101(remote p2p.MsgReadWriter, blocks []*types.Block) {
	for {
		msg, err := remote.ReadMsg()
		if err != nil {
			return
		}
		if msg.Code != ethproto.GetBlockHeadersMsg {
			msg.Discard()
			continue
		}
		var req ethproto.GetBlockHeadersPacket
		if err := msg.Decode(&req); err != nil {
			return
		}
		from := req.Origin.Number
		want := int(req.Amount)
		var hdrsRLP ethproto.BlockHeadersRLPResponse
		for _, b := range blocks {
			if b.NumberU64() >= from && len(hdrsRLP) < want {
				enc, _ := rlp.EncodeToBytes(b.Header())
				hdrsRLP = append(hdrsRLP, enc)
			}
		}
		p2p.Send(remote, ethproto.BlockHeadersMsg, &ethproto.BlockHeadersRLPPacket{ //nolint:errcheck
			RequestId:               req.RequestId,
			BlockHeadersRLPResponse: hdrsRLP,
		})
	}
}

// TestNilSinkNoCrossover_SYNC10 verifies that a nil-sink fetchHeaderBatch and a
// concurrent real-sink RequestHeadersByNumber each receive their own headers,
// and that the real-sink response does not leak into XDCHeaderResp.
// Routing must be by request (nil-sink check in dispatchResponse), not by
// version. Refs #1861 review of a482efad5.
func TestNilSinkNoCrossover_SYNC10(t *testing.T) {
	const nBlocks = 10
	n := newXDCFakeNet(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2RID, p2p.NewPeer(enode.ID{20}, "v101-nocross", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)
	go serveHeadersV101(remote, n.remote)

	// (a) Nil-sink fetch for range A: reads from XDCHeaderResp.
	from1 := n.remote[0].NumberU64()
	got1 := s.fetchHeaderBatch(p, from1, 3)
	if len(got1) == 0 || got1[0].Number.Uint64() != from1 {
		t.Fatalf("nil-sink: want first=%d, got %v", from1, got1)
	}

	// XDCHeaderResp must be empty after fetchHeaderBatch consumed it.
	select {
	case stray := <-p.XDCHeaderResp:
		t.Fatalf("XDCHeaderResp not empty after nil-sink fetch; stray first=%d", stray[0].Number.Uint64())
	default:
	}

	// (b) Real-sink fetch for range B (non-overlapping): must go via sink only.
	from5 := n.remote[5].NumberU64()
	sink := make(chan *ethproto.Response, 1)
	req2, err := p.RequestHeadersByNumber(from5, 3, 0, false, sink)
	if err != nil {
		t.Fatal("real-sink request failed:", err)
	}
	defer req2.Close()

	select {
	case res := <-sink:
		res.Done <- nil
		got2 := []*types.Header(*res.Res.(*ethproto.BlockHeadersRequest))
		if len(got2) == 0 || got2[0].Number.Uint64() != from5 {
			t.Errorf("real-sink: want first=%d, got %v", from5, got2)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("real-sink: timed out")
	}

	// (c) XDCHeaderResp must still be empty: real-sink response did not leak there.
	select {
	case stray := <-p.XDCHeaderResp:
		t.Fatalf("crossover: real-sink response leaked into XDCHeaderResp; first=%d", stray[0].Number.Uint64())
	default:
	}
}

// TestNilSinkNonBlocking_SYNC10 verifies that the peer message loop is not
// blocked while a nil-sink request is outstanding and XDCHeaderResp is full.
// dispatchResponse must return immediately (non-blocking push) so subsequent
// messages can be handled. Refs #1861 review of a482efad5.
func TestNilSinkNonBlocking_SYNC10(t *testing.T) {
	const nBlocks = 10
	n := newXDCFakeNet(t, 0, nBlocks)

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2RID, p2p.NewPeer(enode.ID{21}, "v101-nonblock", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)
	go serveHeadersV101(remote, n.remote)

	// Pre-fill XDCHeaderResp (buffer 1) so the nil-sink push will drop.
	p.XDCHeaderResp <- []*types.Header{n.remote[0].Header()}

	// Issue nil-sink request. The response arrives, dispatchResponse does a
	// non-blocking push (drops because channel is full) and returns immediately.
	from1 := n.remote[0].NumberU64()
	req1, err := p.RequestHeadersByNumber(from1, 1, 0, false, nil)
	if err != nil {
		t.Fatal("nil-sink request failed:", err)
	}
	defer req1.Close()

	// Now issue a real-sink request. If the nil-sink path blocked the message
	// loop, this will time out.
	from5 := n.remote[5].NumberU64()
	sink := make(chan *ethproto.Response, 1)
	req2, err := p.RequestHeadersByNumber(from5, 1, 0, false, sink)
	if err != nil {
		t.Fatal("real-sink request failed:", err)
	}
	defer req2.Close()

	select {
	case res := <-sink:
		res.Done <- nil
		got := []*types.Header(*res.Res.(*ethproto.BlockHeadersRequest))
		if len(got) == 0 || got[0].Number.Uint64() != from5 {
			t.Errorf("non-blocking: real-sink got wrong headers, want from=%d", from5)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("non-blocking: peer message loop appears blocked (real-sink timed out)")
	}

	// Drain the pre-filled dummy so the channel is clean for t.Cleanup.
	select {
	case <-p.XDCHeaderResp:
	default:
	}
}

// TestFetchHeaderBatchXDPOS2Unchanged_SYNC10 verifies that v100 (XDPOS2)
// fetchHeaderBatch still works after the nil-sink routing change in
// dispatchResponse. The XDPOS2 handler pushes directly to XDCHeaderResp
// before calling dispatchResponse; the extra nil-sink push from
// xdcNilSinkDelivery is non-blocking and harmless. Refs #1861.
func TestFetchHeaderBatchXDPOS2Unchanged_SYNC10(t *testing.T) {
	const nBlocks = 5
	n := newXDCFakeNet(t, 0, nBlocks)
	s := n.newTestXDCSyncer(t)

	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { app.Close() })
	p := ethproto.NewPeer(ethproto.XDPOS2, p2p.NewPeer(enode.ID{22}, "v100-legacy", nil), app, nil, nil, n.chain.Config())
	t.Cleanup(p.Close)

	go ethproto.Handle(&noopEthBackend{chain: n.chain}, p)

	// Fake remote for XDPOS2: reads bare GetBlockHeadersRequest and replies
	// with a bare list of RLP-encoded headers.
	go func() {
		for {
			msg, err := remote.ReadMsg()
			if err != nil {
				return
			}
			if msg.Code != ethproto.GetBlockHeadersMsg {
				msg.Discard()
				continue
			}
			var req ethproto.GetBlockHeadersRequest
			if err := msg.Decode(&req); err != nil {
				return
			}
			from := req.Origin.Number
			want := int(req.Amount)
			var hdrsRLP []rlp.RawValue
			for _, b := range n.remote {
				if b.NumberU64() >= from && len(hdrsRLP) < want {
					enc, _ := rlp.EncodeToBytes(b.Header())
					hdrsRLP = append(hdrsRLP, enc)
				}
			}
			p2p.Send(remote, ethproto.BlockHeadersMsg, hdrsRLP) //nolint:errcheck
		}
	}()

	from := n.remote[0].NumberU64()
	got := s.fetchHeaderBatch(p, from, 3)
	if len(got) == 0 {
		t.Fatal("v100 fetchHeaderBatch: got no headers")
	}
	if got[0].Number.Uint64() != from {
		t.Errorf("v100 fetchHeaderBatch: want first=%d, got %d", from, got[0].Number.Uint64())
	}
	// The v100 handler already delivered the reply; the dispatcher must not
	// deliver it a second time, or the next fetch would read a stale batch.
	select {
	case dup := <-p.XDCHeaderResp:
		t.Fatalf("v100 reply delivered twice: stale batch of %d headers left in XDCHeaderResp", len(dup))
	case <-time.After(300 * time.Millisecond):
	}
}
