// Copyright 2026 XDC Network

package miner

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestXDCMinerOmitsUnsealedHeaderFields: on an XDC chain config whose genesis
// sets Shanghai/Cancun times, generateWork must not stamp any post-London
// optional header field, since XDPoS rejects them (#1417).
func TestXDCMinerOmitsUnsealedHeaderFields(t *testing.T) {
	miner := createMiner(t)
	cfg := miner.chainConfig // shared with the test chain, so both see the forks
	zero := uint64(0)
	cfg.ShanghaiTime, cfg.CancunTime = &zero, &zero
	cfg.BlobScheduleConfig = params.DefaultBlobSchedule
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900}

	res := miner.generateWork(context.Background(), &generateParams{
		timestamp: miner.chain.CurrentBlock().Time + 2,
		coinbase:  miner.config.PendingFeeRecipient,
	}, false)
	if res.err != nil {
		t.Fatalf("generateWork: %v", res.err)
	}
	h := res.block.Header()
	if h.WithdrawalsHash != nil || h.BlobGasUsed != nil || h.ExcessBlobGas != nil ||
		h.ParentBeaconRoot != nil || h.RequestsHash != nil ||
		h.BlockAccessListHash != nil || h.SlotNumber != nil {
		t.Fatalf("XDC miner set post-London header fields: withdrawals=%v blobGasUsed=%v excessBlobGas=%v beaconRoot=%v requests=%v bal=%v slot=%v",
			h.WithdrawalsHash, h.BlobGasUsed, h.ExcessBlobGas, h.ParentBeaconRoot, h.RequestsHash, h.BlockAccessListHash, h.SlotNumber)
	}
}
