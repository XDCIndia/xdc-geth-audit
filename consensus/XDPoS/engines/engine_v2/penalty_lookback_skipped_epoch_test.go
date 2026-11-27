// Copyright (c) 2026 XDC Network
// XDC-HUNT-13 (#2066): the penalty lookback must go by epoch number, as
// canonical does (engine_v2/engine.go:1240-1261 in XDPoSChain
// v3.0.0-testnet-2), not by a count of epoch-switch blocks. When a whole
// epoch has no block, the list of that epoch is empty. Counting switch
// blocks reads an older epoch instead, and the two clients then compute
// different penalties for the next epoch-switch block.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestPenaltyLookbackSkippedEpoch -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestPenaltyLookbackSkippedEpoch(t *testing.T) {
	x := newTestXDPoS_v2(100, 900)
	x.epochSwitches = lru.NewCache[common.Hash, *types.EpochSwitchInfo](64)
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}

	mn := []common.Address{{0x01}}
	pen1 := common.Address{0xe1}
	pen3 := common.Address{0xe3}

	// Epoch numbers (SwitchEpoch 0): E0 = block 100 (round 0), E1 = block 1000
	// (round 900), E3 = block 1900 (round 2700). Epoch 2 has no block.
	e0 := &types.Header{Number: big.NewInt(100)}
	h999 := &types.Header{Number: big.NewInt(999), ParentHash: common.Hash{0x99}}
	e1 := &types.Header{Number: big.NewInt(1000), ParentHash: h999.Hash(), Penalties: pen1.Bytes()}
	h1899 := &types.Header{Number: big.NewInt(1899), ParentHash: e1.Hash()}
	e3 := &types.Header{Number: big.NewInt(1900), ParentHash: h1899.Hash(), Penalties: pen3.Bytes()}
	h1950 := &types.Header{Number: big.NewInt(1950), ParentHash: e3.Hash()}
	for _, h := range []*types.Header{e0, h999, e1, h1899, e3, h1950} {
		chain.add(h)
	}
	info := func(h *types.Header, round types.Round, pens []common.Address) *types.EpochSwitchInfo {
		return &types.EpochSwitchInfo{
			Masternodes: mn, MasternodesLen: len(mn), Penalties: pens,
			EpochSwitchBlockInfo: &types.BlockInfo{Hash: h.Hash(), Number: h.Number, Round: round},
		}
	}
	i0, i1, i3 := info(e0, 0, nil), info(e1, 900, []common.Address{pen1}), info(e3, 2700, []common.Address{pen3})
	x.epochSwitches.Add(e0.Hash(), i0)
	x.epochSwitches.Add(h999.Hash(), i0)
	x.epochSwitches.Add(e1.Hash(), i1)
	x.epochSwitches.Add(h1899.Hash(), i1)
	x.epochSwitches.Add(e3.Hash(), i3)
	x.epochSwitches.Add(h1950.Hash(), i3)

	for _, tc := range []struct {
		limit int
		want  []common.Address
	}{
		{0, []common.Address{pen3}}, // epoch 3
		{1, nil},                    // epoch 2: skipped, empty (was E1's list)
		{2, []common.Address{pen1}}, // epoch 1
		{3, nil},                    // epoch 0 holds no penalties
		{4, nil},                    // current epoch 3 < limit 4: empty
	} {
		got, err := x.GetPreviousPenaltyByHash(chain, h1950.Hash(), tc.limit)
		if err != nil {
			t.Fatalf("limit %d: %v", tc.limit, err)
		}
		if len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
			t.Errorf("limit %d: got %v, want %v", tc.limit, got, tc.want)
		}
	}

	// A header that is missing from the database stays a retryable error.
	chain2 := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
	for _, h := range []*types.Header{e0, h999, e1, h1899, h1950} { // e3 absent
		chain2.add(h)
	}
	if _, err := x.GetPreviousPenaltyByHash(chain2, h1950.Hash(), 2); err == nil {
		t.Error("missing epoch-switch header: want a retryable error, got nil")
	}
}
