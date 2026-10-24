// Copyright (c) 2026 XDC Network
// Tests for GetEpochSwitchInfoBetween when begin == end (CertiK XDC-30).
//
// The loop condition `iteratorNum.Cmp(begin.Number) > 0` skips the begin
// header entirely when begin == end.  If that header is an epoch-switch block,
// the caller receives an empty slice instead of [epochSwitchInfo].
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestGetEpochSwitchInfoBetween -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestGetEpochSwitchInfoBetween_BeginEqEndIsSwitch verifies that when
// begin == end and that block is an epoch-switch block, the function returns
// a one-element slice containing that block's epoch switch info.
//
// Before fix: returns [] (loop condition > 0 is false when begin==end) → FAIL
// After fix:  returns [s]                                                → PASS
func TestGetEpochSwitchInfoBetween_BeginEqEndIsSwitch(t *testing.T) {
	x := newTestXDPoS_v2(100, 900)
	x.epochSwitches = lru.NewCache[common.Hash, *types.EpochSwitchInfo](64)
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}

	mn := []common.Address{{0x01}}

	// Epoch switch block s at number 1000.
	s := &types.Header{Number: big.NewInt(1000)}
	chain.add(s)

	epochInfo := &types.EpochSwitchInfo{
		Masternodes:          mn,
		MasternodesLen:       len(mn),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: s.Hash(), Number: s.Number, Round: 900},
	}
	x.epochSwitches.Add(s.Hash(), epochInfo)

	infos, err := x.GetEpochSwitchInfoBetween(chain, s, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("(s, s) on epoch-switch block: got %d infos, want 1 (XDC-30)", len(infos))
	}
	if infos[0].EpochSwitchBlockInfo.Number.Uint64() != 1000 {
		t.Errorf("wrong epoch switch number: got %d, want 1000", infos[0].EpochSwitchBlockInfo.Number.Uint64())
	}
}

// TestGetEpochSwitchInfoBetween_BeginEqEndNotSwitch verifies that when
// begin == end and that block is NOT an epoch-switch block, the function
// returns an empty slice (no duplicates introduced by the fix).
func TestGetEpochSwitchInfoBetween_BeginEqEndNotSwitch(t *testing.T) {
	x := newTestXDPoS_v2(100, 900)
	x.epochSwitches = lru.NewCache[common.Hash, *types.EpochSwitchInfo](64)
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}

	mn := []common.Address{{0x01}}

	// Epoch switch at 1000; non-switch block at 1005.
	s := &types.Header{Number: big.NewInt(1000)}
	b := &types.Header{Number: big.NewInt(1005), ParentHash: s.Hash()}
	chain.add(s)
	chain.add(b)

	epochInfo := &types.EpochSwitchInfo{
		Masternodes:          mn,
		MasternodesLen:       len(mn),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: s.Hash(), Number: s.Number, Round: 900},
	}
	// b is in the same epoch as s; getEpochSwitchInfo for b returns s's info.
	x.epochSwitches.Add(s.Hash(), epochInfo)
	x.epochSwitches.Add(b.Hash(), epochInfo)

	infos, err := x.GetEpochSwitchInfoBetween(chain, b, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("(b, b) on non-switch block: got %d infos, want 0", len(infos))
	}
}

// TestGetEpochSwitchInfoBetween_NormalRange verifies that the existing
// behaviour for begin != end is unaffected (no regression).
func TestGetEpochSwitchInfoBetween_NormalRange(t *testing.T) {
	x := newTestXDPoS_v2(100, 900)
	x.epochSwitches = lru.NewCache[common.Hash, *types.EpochSwitchInfo](64)
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}

	mn := []common.Address{{0x01}}

	// Epochs at 100 (V2 switch), 1000, 1900.
	e0 := &types.Header{Number: big.NewInt(100)}
	e1 := &types.Header{Number: big.NewInt(1000), ParentHash: e0.Hash()}
	e2 := &types.Header{Number: big.NewInt(1900), ParentHash: e1.Hash()}
	tip := &types.Header{Number: big.NewInt(1950), ParentHash: e2.Hash()}
	for _, h := range []*types.Header{e0, e1, e2, tip} {
		chain.add(h)
	}

	mkInfo := func(h *types.Header, round types.Round, parentBlockInfo *types.BlockInfo) *types.EpochSwitchInfo {
		return &types.EpochSwitchInfo{
			Masternodes: mn, MasternodesLen: len(mn),
			EpochSwitchBlockInfo:       &types.BlockInfo{Hash: h.Hash(), Number: h.Number, Round: round},
			EpochSwitchParentBlockInfo: parentBlockInfo,
		}
	}
	i0 := mkInfo(e0, 0, nil)
	i1 := mkInfo(e1, 900, &types.BlockInfo{Hash: e0.Hash(), Number: e0.Number, Round: 0})
	i2 := mkInfo(e2, 1800, &types.BlockInfo{Hash: e1.Hash(), Number: e1.Number, Round: 900})
	for _, h := range []*types.Header{e0, e1} {
		x.epochSwitches.Add(h.Hash(), i0)
	}
	// e1 and any block between e1 and e2 belong to epoch i1.
	x.epochSwitches.Add(e1.Hash(), i1)
	x.epochSwitches.Add(e2.Hash(), i2)
	x.epochSwitches.Add(tip.Hash(), i2)

	infos, err := x.GetEpochSwitchInfoBetween(chain, e0, tip)
	if err != nil {
		t.Fatalf("normal range: %v", err)
	}
	// Should include e1 (1000) and e2 (1900) but NOT e0 (begin excluded).
	if len(infos) != 2 {
		t.Fatalf("normal range: got %d infos, want 2", len(infos))
	}
	if infos[0].EpochSwitchBlockInfo.Number.Uint64() != 1000 {
		t.Errorf("normal range: infos[0].Number = %d, want 1000", infos[0].EpochSwitchBlockInfo.Number.Uint64())
	}
	if infos[1].EpochSwitchBlockInfo.Number.Uint64() != 1900 {
		t.Errorf("normal range: infos[1].Number = %d, want 1900", infos[1].EpochSwitchBlockInfo.Number.Uint64())
	}
}
