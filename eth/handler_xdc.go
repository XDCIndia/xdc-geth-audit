// Copyright 2024 XDC Network
// XDC P2P broadcast functions for consensus messages.
// Ports BroadcastVote/BroadcastTimeout/BroadcastSyncInfo from v2.6.8
// (XDPoSChain/eth/handler.go BroadcastVote/Timeout/SyncInfo +
// peer.go SendVote/Timeout/SyncInfo).
// A.97-M.A Task 3: BroadcastBlock — NewBlockMsg gossip for self-minted blocks.
//
// Wire format: send types.Vote/Timeout/SyncInfo directly via p.Peer.Send()
// (which calls p2p.Send → RLP-encodes the object).  XDPoSChain's receive
// handler decodes as rlp.RawValue (raw RLP bytes of the struct) so the
// wire must carry RLP(types.Vote), NOT RLP(VotePacket{Vote: []byte}).

package eth

import (
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/log"
)

// bftPeerSendQueueLen is the number of outbound BFT or block messages queued
// for one peer. A further message is dropped. One goroutine drains the queue.
const bftPeerSendQueueLen = 32

// bftPeerOwnQueueLen is the length of the second queue, for this node's own
// votes, timeouts, syncInfo and blocks. The sender goroutine drains it first,
// so forwarded traffic that fills the main queue cannot drop them (#2079).
const bftPeerOwnQueueLen = 8

// bftPeerSendFullTimeout is how long a peer's send queue may stay full before
// the peer is disconnected. A peer that stops reading would otherwise hold
// the sender goroutine and drop every later message.
var bftPeerSendFullTimeout = 5 * time.Second

// bftOutbound is one queued send.
type bftOutbound struct {
	code uint64
	msg  interface{}
}

// bftPeerSender is one peer's outbound queue and the goroutine that serves it.
type bftPeerSender struct {
	ch        chan bftOutbound
	own       chan bftOutbound // served before ch
	quit      chan struct{}
	stopOnce  sync.Once
	mu        sync.Mutex
	fullSince time.Time
}

func (p *ethPeer) ensureBFTSender() *bftPeerSender {
	p.bftOnce.Do(func() {
		p.bftSend = newBFTPeerSender()
		go p.bftSendLoop(p.bftSend)
	})
	return p.bftSend
}

func newBFTPeerSender() *bftPeerSender {
	return &bftPeerSender{
		ch:   make(chan bftOutbound, bftPeerSendQueueLen),
		own:  make(chan bftOutbound, bftPeerOwnQueueLen),
		quit: make(chan struct{}),
	}
}

func (p *ethPeer) bftSendLoop(s *bftPeerSender) {
	send := func(item bftOutbound) {
		if err := p.Peer.Send(item.code, item.msg); err != nil {
			log.Trace("[XDC-P2P] peer send failed", "peer", p.ID(), "code", item.code, "err", err)
		}
	}
	for {
		// Own messages first.
		select {
		case item := <-s.own:
			send(item)
			continue
		default:
		}
		select {
		case <-s.quit:
			return
		case item := <-s.own:
			send(item)
		case item := <-s.ch:
			send(item)
		}
	}
}

// stopBFTSend stops the peer's sender goroutine. It is safe to call more than
// once. If the sender was never started, it is made already stopped, so a
// later enqueue cannot start a goroutine for a peer that is gone (#2079).
func (p *ethPeer) stopBFTSend() {
	if p == nil {
		return
	}
	p.bftOnce.Do(func() { p.bftSend = newBFTPeerSender() }) // no goroutine
	p.bftSend.stopOnce.Do(func() { close(p.bftSend.quit) })
}

// enqueuePeerSend pushes msg onto the peer's send queue. The call does not
// block. A message is dropped when the queue is full. If the queue stays full
// for bftPeerSendFullTimeout the peer is disconnected. own selects the queue
// for this node's own messages, which the sender serves first.
func (h *handler) enqueuePeerSend(p *ethPeer, code uint64, msg interface{}, own bool) {
	s := p.ensureBFTSender()
	select {
	case <-s.quit:
		return // peer is closed
	default:
	}
	q := s.ch
	if own {
		q = s.own
	}
	s.mu.Lock()
	select {
	case q <- bftOutbound{code: code, msg: msg}:
		s.fullSince = time.Time{}
		s.mu.Unlock()
	default:
		disconnect := false
		if s.fullSince.IsZero() {
			s.fullSince = time.Now()
		} else if time.Since(s.fullSince) >= bftPeerSendFullTimeout {
			disconnect = true
		}
		s.mu.Unlock()
		if disconnect {
			h.removePeer(p.ID())
		}
	}
}

// BroadcastVote propagates a V2 consensus vote to all connected peers.
// Sends types.Vote directly so wire carries RLP(types.Vote), matching
// XDPoSChain/eth/peer.go SendVote: p2p.Send(rw, VoteMsg, vote).
// Our handleVoteMsg receive path decodes as rlp.RawValue and then
// rlp.DecodeBytes back to types.Vote, which handles both directions.
func (h *handler) BroadcastVote(vote *types.Vote) {
	if vote == nil {
		return
	}
	h.peers.lock.RLock()
	peers := make([]*ethPeer, 0, len(h.peers.peers))
	for _, p := range h.peers.peers {
		peers = append(peers, p)
	}
	h.peers.lock.RUnlock()

	for _, p := range peers {
		h.enqueuePeerSend(p, eth.VoteMsg, vote, false)
	}
	log.Debug("[XDC-P2P] Vote broadcast complete", "peers", len(peers), "round", vote.ProposedBlockInfo.Round)
}

// BroadcastOwn sends a vote, timeout or syncInfo that this node made itself.
// It uses the priority queue of each peer, so forwarded messages do not drop it.
func (h *handler) BroadcastOwn(msg interface{}) {
	var code uint64
	switch msg.(type) {
	case *types.Vote:
		code = eth.VoteMsg
	case *types.Timeout:
		code = eth.TimeoutMsg
	case *types.SyncInfo:
		code = eth.SyncInfoMsg
	default:
		return
	}
	h.peers.lock.RLock()
	peers := make([]*ethPeer, 0, len(h.peers.peers))
	for _, p := range h.peers.peers {
		peers = append(peers, p)
	}
	h.peers.lock.RUnlock()

	for _, p := range peers {
		h.enqueuePeerSend(p, code, msg, true)
	}
}

// BroadcastTimeout propagates a V2 consensus timeout to all connected peers.
// Wire format: p2p.Send(TimeoutMsg, timeout) — matches XDPoSChain/eth/peer.go
// SendTimeout which calls p2p.Send(rw, TimeoutMsg, timeout) directly.
// Sending the struct directly ensures XDPoSChain peers decode with
// msg.Decode(&timeout) correctly (refs #903 wire-compat fix).
func (h *handler) BroadcastTimeout(timeout *types.Timeout) {
	if timeout == nil {
		return
	}
	h.peers.lock.RLock()
	peers := make([]*ethPeer, 0, len(h.peers.peers))
	for _, p := range h.peers.peers {
		peers = append(peers, p)
	}
	h.peers.lock.RUnlock()

	for _, p := range peers {
		h.enqueuePeerSend(p, eth.TimeoutMsg, timeout, false)
	}
	log.Debug("[XDC-P2P] Timeout broadcast complete", "peers", len(peers), "round", timeout.Round)
}

// BroadcastSyncInfo propagates V2 sync info to all connected peers.
// Wire format: p2p.Send(SyncInfoMsg, syncInfo) — matches XDPoSChain/eth/peer.go
// SendSyncInfo which calls p2p.Send(rw, SyncInfoMsg, syncInfo) directly.
func (h *handler) BroadcastSyncInfo(syncInfo *types.SyncInfo) {
	if syncInfo == nil {
		return
	}
	h.peers.lock.RLock()
	peers := make([]*ethPeer, 0, len(h.peers.peers))
	for _, p := range h.peers.peers {
		peers = append(peers, p)
	}
	h.peers.lock.RUnlock()

	for _, p := range peers {
		h.enqueuePeerSend(p, eth.SyncInfoMsg, syncInfo, false)
	}
	log.Debug("[XDC-P2P] SyncInfo broadcast complete", "peers", len(peers))
}

// BroadcastBlock propagates a newly self-minted block to all connected peers
// via eth.NewBlockMsg (0x07). This is the M4c gap from A.97-M.A Task 3.
//
// Mirrors XDPoSChain/eth/handler.go BroadcastBlock (propagate=true path):
// compute TD from block.Difficulty (PoA: no cumulative TD tracking; peers
// ignore TD on XDPoS V2 blocks; block.Difficulty() is safe for wire use),
// wrap in NewBlockPacket, and Send to all eth peers.
//
// Called by the PR-B xdcWorker after InsertChain succeeds so that peers see
// the new tip and can vote on it via their own BFT dispatch path.
// Not called by anything in PR-A — exported here so PR-B can call it.
func (h *handler) BroadcastBlock(block *types.Block) {
	if block == nil {
		return
	}
	h.peers.lock.RLock()
	peers := make([]*ethPeer, 0, len(h.peers.peers))
	for _, p := range h.peers.peers {
		peers = append(peers, p)
	}
	h.peers.lock.RUnlock()

	if len(peers) == 0 {
		return
	}

	// XDC #1645: TD for NewBlock must be parent.TD + block.Difficulty(), matching
	// canonical XDPoSChain (eth/handler.go:1074). Legacy peers compute
	// trueTD = announced TD - block.Difficulty() and start a sync only when
	// trueTD > ownTD. Sending only block.Difficulty() (= 1 for V2) meant
	// trueTD = 0, so legacy nodes behind the tip never triggered a sync.
	var td *big.Int
	if parentTD := h.chain.GetTd(block.ParentHash(), block.NumberU64()-1); parentTD != nil {
		td = new(big.Int).Add(parentTD, block.Difficulty())
	} else {
		// ponytail: fallback if parent TD not yet stored (rare race on fresh node).
		td = new(big.Int).Set(block.Difficulty())
	}

	pkt := &eth.NewBlockPacket{
		Block: block,
		TD:    td,
	}
	for _, p := range peers {
		h.enqueuePeerSend(p, eth.NewBlockMsg, pkt, true)
	}
	log.Debug("[XDC-P2P] Block broadcast started",
		"number", block.NumberU64(), "hash", block.Hash(), "peers", len(peers))
}
