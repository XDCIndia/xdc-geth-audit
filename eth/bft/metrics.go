// Copyright 2026 XDC Network

package bft

import "github.com/ethereum/go-ethereum/metrics"

var (
	// forwardDropped counts peer-relayed BFT messages (vote/timeout/syncInfo)
	// that were discarded because fwdQueue was full. Own-originated messages
	// from the engine's broadcastCh are never counted here: they use a blocking
	// enqueue that only gives up on quit.
	forwardDropped = metrics.NewRegisteredCounter("eth/bft/forward_dropped", nil)
)
