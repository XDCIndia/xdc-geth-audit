// Copyright 2024 XDC Network
// Patient (geth-1.17) port of XDPoSChain/eth/bft/bft_handler.go.
//
// The Bfter is the BFT message dispatcher: it accepts inbound vote/timeout/
// sync-info packets from the eth wire protocol, hands them to the V2 engine
// for verification + handling, and (separately) forwards engine-emitted
// outbound BFT messages to the broadcast layer.
//
// This file deliberately defines a NARROW EngineV2 interface local to the
// bft package, instead of importing consensus/XDPoS, to avoid an import
// cycle (consensus/XDPoS already imports core/types; eth imports
// consensus/XDPoS; eth/bft must not pull XDPoS back in via the engine).
//
// Initialization is two-step (mirrors v2.6.8): construct via New(), then
// call SetConsensusFns once the engine is wired so we can capture
// VoteHandler / TimeoutHandler / SyncInfoHandler closures.

package bft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// maxBlockDist is the maximum allowed backward distance from the chain head.
// Matches XDPoSChain/eth/bft/bft_handler.go:12.
const maxBlockDist = 7

// Known-message cache sizes mirror XDPoSChain/eth/peer.go:maxKnown*.
// 128 K slots each; one common.Hash = 32 bytes → ~4 MB per cache at capacity.
const (
	maxKnownVotes     = 131072
	maxKnownTimeouts  = 131072
	maxKnownSyncInfos = 131072
)

// bcastWorkers is the number of concurrent peer-broadcast goroutines in the
// worker pool. A small fixed pool prevents goroutine exhaustion under flood
// (Hacken F-2026-19460 residual).
// ponytail: fixed pool; scale if per-peer send latency under load warrants it.
const bcastWorkers = 4

// bcastQueueSize caps the bounded forward queue for peer-relayed messages.
//
// Sizing: the XDC V2 committee holds at most MaxMasternodesV2 = 108 nodes.
// In a single round each masternode may send one vote, one timeout, and one
// syncInfo, giving 108 × 3 = 324 messages per round burst. We round up to
// the next power-of-two and add a 3× burst buffer to absorb overlap between
// consecutive rounds: 324 × 4 ≈ 1296 → 1024 is the minimum; use 1024.
//
// Own-originated messages from the engine's broadcastCh never enter this
// drop path: the loop uses a blocking enqueue for them.
const bcastQueueSize = 1024

// EngineV2 is the narrow surface of the V2 engine that the BFT dispatcher
// requires. Defined here (rather than imported from consensus/XDPoS) to
// keep the dependency one-way: eth -> eth/bft -> {core/types, consensus},
// not eth/bft -> consensus/XDPoS.
//
// Any concrete *engine_v2.XDPoS_v2 already satisfies this interface.
type EngineV2 interface {
	VerifyVoteMessage(chain consensus.ChainReader, vote *types.Vote) (bool, error)
	VoteHandler(chain consensus.ChainReader, vote *types.Vote) error

	VerifyTimeoutMessage(chain consensus.ChainReader, timeout *types.Timeout) (bool, error)
	TimeoutHandler(chain consensus.ChainReader, timeout *types.Timeout) error

	VerifySyncInfoMessage(chain consensus.ChainReader, syncInfo *types.SyncInfo) (bool, error)
	SyncInfoHandler(chain consensus.ChainReader, syncInfo *types.SyncInfo) error
}

// ChainHeightFn returns the current canonical chain head height.
type ChainHeightFn func() uint64

// Broadcast callback signatures, matching v2.6.8.
type (
	BroadcastVoteFn     func(*types.Vote)
	BroadcastTimeoutFn  func(*types.Timeout)
	BroadcastSyncInfoFn func(*types.SyncInfo)
)

// BroadcastFns bundles the three outbound broadcast callbacks the engine
// needs to reach peers via the eth handler.
type BroadcastFns struct {
	Vote     BroadcastVoteFn
	Timeout  BroadcastTimeoutFn
	SyncInfo BroadcastSyncInfoFn
}

// Bfter dispatches inbound BFT messages into the V2 engine and forwards
// engine-emitted outbound messages to the broadcast layer. Mirrors
// XDPoSChain/eth/bft/bft_handler.go:Bfter.
type Bfter struct {
	epoch uint64

	chain       consensus.ChainReader
	engine      EngineV2
	chainHeight ChainHeightFn

	broadcast   BroadcastFns
	broadcastCh chan interface{}
	quit        chan struct{}

	// fwdQueue is the bounded forward queue shared by the inbound-forward path
	// (Vote/Timeout/SyncInfo) and the engine-broadcast loop.
	//
	// Peer-relayed messages: try-send; dropped and counted when full.
	// Own-originated messages (from broadcastCh): blocking enqueue with quit
	// select so this node's own vote/timeout/syncInfo is never silently lost.
	//
	// bcastWorkers goroutines drain the queue and call the broadcast callbacks.
	// (Hacken F-2026-19460 residual)
	fwdQueue chan interface{}

	// Known-message caches deduplicate inbound BFT messages before verification.
	// Mirrors XDPoSChain/eth/handler.go knownVotes/knownTimeouts/knownSyncInfos
	// (F-2026-19460). The cache is keyed by message hash (which includes all
	// fields, including the signature bytes), so identical messages are dropped
	// and distinct messages from the same round are forwarded once each.
	knownVotes     *lru.Cache[common.Hash, struct{}]
	knownTimeouts  *lru.Cache[common.Hash, struct{}]
	knownSyncInfos *lru.Cache[common.Hash, struct{}]
}

// New creates a Bfter. Call SetConsensusFns afterwards to wire the engine
// reference (matches the two-step initialization in v2.6.8).
func New(broadcasts BroadcastFns, chain consensus.ChainReader, chainHeight ChainHeightFn) *Bfter {
	return &Bfter{
		broadcast:      broadcasts,
		chain:          chain,
		chainHeight:    chainHeight,
		quit:           make(chan struct{}),
		broadcastCh:    make(chan interface{}),
		fwdQueue:       make(chan interface{}, bcastQueueSize),
		knownVotes:     lru.NewCache[common.Hash, struct{}](maxKnownVotes),
		knownTimeouts:  lru.NewCache[common.Hash, struct{}](maxKnownTimeouts),
		knownSyncInfos: lru.NewCache[common.Hash, struct{}](maxKnownSyncInfos),
	}
}

// SetConsensusFns wires the engine reference. Must be called before the
// dispatcher accepts traffic. The engine's broadcast channel (if exposed)
// can be wired by the caller via SetBroadcastCh.
func (b *Bfter) SetConsensusFns(engine EngineV2) {
	b.engine = engine
}

// SetBroadcastCh wires the engine's outbound broadcast channel so the
// Bfter loop can forward engine-emitted messages to peers.
func (b *Bfter) SetBroadcastCh(ch chan interface{}) {
	if ch != nil {
		b.broadcastCh = ch
	}
}

// SetEpoch records the chain's epoch length, used in the timeout-distance
// guard (matches v2.6.8 Bfter.InitEpochNumber).
func (b *Bfter) SetEpoch(epoch uint64) {
	b.epoch = epoch
}

// Engine returns the wired V2 engine, or nil if not yet wired. Callers
// (e.g. eth/handler.go) use this to gate inbound dispatch.
func (b *Bfter) Engine() EngineV2 {
	return b.engine
}

// tryForward enqueues msg on fwdQueue without blocking. Used for peer-relayed
// messages only: if the queue is full the message is dropped and counted
// (Hacken F-2026-19460 residual). Own-originated messages from the engine's
// broadcastCh must NOT use this path; the loop uses a blocking enqueue for
// them so this node's own vote is never silently discarded.
func (b *Bfter) tryForward(msg interface{}, kind string) {
	select {
	case b.fwdQueue <- msg:
	default:
		log.Debug("BFT forward queue full, dropping peer-relayed message", "type", kind)
		forwardDropped.Inc(1)
	}
}

// Vote handles an inbound vote message from the given peer. Mirrors
// XDPoSChain/eth/bft/bft_handler.go:Vote.
func (b *Bfter) Vote(peer string, vote *types.Vote) error {
	if b == nil || b.engine == nil || vote == nil || vote.ProposedBlockInfo == nil {
		return nil
	}
	log.Trace("Receive Vote", "hash", vote.Hash().Hex(),
		"voted block hash", vote.ProposedBlockInfo.Hash.Hex(),
		"number", vote.ProposedBlockInfo.Number,
		"round", vote.ProposedBlockInfo.Round)

	voteBlockNum := vote.ProposedBlockInfo.Number.Int64()
	if dist := voteBlockNum - int64(b.chainHeight()); dist < -maxBlockDist || dist > maxBlockDist {
		log.Debug("Discarded propagated vote, too far away",
			"peer", peer, "number", voteBlockNum,
			"hash", vote.ProposedBlockInfo.Hash, "distance", dist)
		return nil
	}

	// Dedup by message hash before verification. Mirrors canonical
	// XDPoSChain/eth/handler.go:handleVote (F-2026-19460).
	h := vote.Hash()
	if b.knownVotes.Contains(h) {
		log.Trace("Discarded vote, already known", "hash", h, "peer", peer)
		return nil
	}
	b.knownVotes.Add(h, struct{}{})

	verified, err := b.engine.VerifyVoteMessage(b.chain, vote)
	if err != nil {
		log.Error("Verify BFT Vote", "error", err)
		return err
	}
	if !verified {
		return nil
	}

	// Peer-relayed: try-send, drop if fwdQueue is full (F-2026-19460 residual).
	b.tryForward(vote, "vote")
	if err := b.engine.VoteHandler(b.chain, vote); err != nil {
		log.Debug("handle BFT Vote", "error", err)
		return err
	}
	return nil
}

// Timeout handles an inbound timeout message from the given peer. Mirrors
// XDPoSChain/eth/bft/bft_handler.go:Timeout.
func (b *Bfter) Timeout(peer string, timeout *types.Timeout) error {
	if b == nil || b.engine == nil || timeout == nil {
		return nil
	}
	log.Debug("Receive Timeout", "timeout", timeout)

	gapNum := timeout.GapNumber
	if b.epoch > 0 {
		if dist := int64(gapNum) - int64(b.chainHeight()); dist < -int64(b.epoch)*3 || dist > int64(b.epoch)*3 {
			log.Debug("Discarded propagated timeout, too far away",
				"peer", peer, "gapNumber", gapNum, "distance", dist)
			return nil
		}
	}

	// Dedup by message hash before verification (F-2026-19460).
	h := timeout.Hash()
	if b.knownTimeouts.Contains(h) {
		log.Trace("Discarded timeout, already known", "hash", h, "peer", peer)
		return nil
	}
	b.knownTimeouts.Add(h, struct{}{})

	verified, err := b.engine.VerifyTimeoutMessage(b.chain, timeout)
	if err != nil {
		log.Error("Verify BFT Timeout", "timeoutRound", timeout.Round, "timeoutGapNum", gapNum, "error", err)
		return err
	}
	if !verified {
		return nil
	}

	// Peer-relayed: try-send, drop if full (F-2026-19460 residual).
	b.tryForward(timeout, "timeout")
	if err := b.engine.TimeoutHandler(b.chain, timeout); err != nil {
		log.Debug("handle BFT Timeout", "error", err)
		return err
	}
	return nil
}

// SyncInfo handles an inbound sync-info message from the given peer.
// Mirrors XDPoSChain/eth/bft/bft_handler.go:SyncInfo.
func (b *Bfter) SyncInfo(peer string, syncInfo *types.SyncInfo) error {
	if b == nil || b.engine == nil || syncInfo == nil ||
		syncInfo.HighestQuorumCert == nil || syncInfo.HighestQuorumCert.ProposedBlockInfo == nil {
		return nil
	}
	log.Debug("Receive SyncInfo", "syncInfo", syncInfo)

	qcBlockNum := syncInfo.HighestQuorumCert.ProposedBlockInfo.Number.Int64()
	if dist := qcBlockNum - int64(b.chainHeight()); dist < -maxBlockDist || dist > maxBlockDist {
		log.Debug("Discarded propagated syncInfo, too far away",
			"peer", peer, "blockNum", qcBlockNum, "distance", dist)
		return nil
	}

	// Dedup by message hash before verification (F-2026-19460).
	h := syncInfo.Hash()
	if b.knownSyncInfos.Contains(h) {
		log.Trace("Discarded syncInfo, already known", "hash", h, "peer", peer)
		return nil
	}
	b.knownSyncInfos.Add(h, struct{}{})

	verified, err := b.engine.VerifySyncInfoMessage(b.chain, syncInfo)
	if err != nil {
		log.Error("Verify BFT SyncInfo", "error", err)
		return err
	}
	if !verified {
		return nil
	}

	// Peer-relayed: try-send, drop if full (F-2026-19460 residual).
	b.tryForward(syncInfo, "syncinfo")
	if err := b.engine.SyncInfoHandler(b.chain, syncInfo); err != nil {
		log.Debug("handle BFT SyncInfo", "error", err)
		return err
	}
	return nil
}

// Start launches the worker pool and the engine-broadcast forwarding loop.
// Safe to skip if the engine never emits BFT messages (read-only/sync-only).
func (b *Bfter) Start() {
	for i := 0; i < bcastWorkers; i++ {
		go b.broadcastWorker()
	}
	go b.loop()
}

// Stop terminates the loop and all workers.
func (b *Bfter) Stop() {
	close(b.quit)
}

// broadcastWorker drains fwdQueue and delivers each message to the broadcast
// layer. bcastWorkers of these run concurrently; the pool size is the hard
// upper bound on concurrent peer-send goroutines (Hacken F-2026-19460 residual).
func (b *Bfter) broadcastWorker() {
	for {
		select {
		case <-b.quit:
			return
		case msg := <-b.fwdQueue:
			switch v := msg.(type) {
			case *types.Vote:
				b.broadcast.Vote(v)
			case *types.Timeout:
				b.broadcast.Timeout(v)
			case *types.SyncInfo:
				b.broadcast.SyncInfo(v)
			default:
				log.Error("Unknown BFT message type in forward queue", "value", v)
			}
		}
	}
}

func (b *Bfter) loop() {
	log.Info("BFT Loop Start")
	for {
		select {
		case <-b.quit:
			log.Warn("BFT Loop Close")
			// Drain any messages that engine goroutines are currently blocked
			// trying to send on broadcastCh. Without this drain, those goroutines
			// (spawned by broadcastToBftChannel) block indefinitely after Stop
			// (Hacken F-2026-19460 residual). Non-blocking: only picks up senders
			// that are already waiting; later sends may still block briefly until
			// the engine itself shuts down.
			for {
				select {
				case <-b.broadcastCh:
				default:
					return
				}
			}
		case obj := <-b.broadcastCh:
			// Own-originated message (engine's own vote/timeout/syncInfo).
			// Block until fwdQueue accepts or quit fires: dropping this node's
			// own vote costs the network a QC and a 60 s V2 timeout round.
			select {
			case b.fwdQueue <- obj:
			case <-b.quit:
			}
		}
	}
}
