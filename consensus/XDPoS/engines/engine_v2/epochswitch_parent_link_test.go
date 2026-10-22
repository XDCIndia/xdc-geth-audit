// Copyright (c) 2026 XDC Network
// Epoch-switch cache entries built by getEpochSwitchInfoWithParents carry no
// EpochSwitchParentBlockInfo. The walk to earlier epochs must still reach them,
// or HookPenalty counts the last penalty list LimitPenaltyEpoch times and
// releases penalised signers early (canonical Apothem 83,738,378 rejected with
// "validators not legit").
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestPreviousEpochWithoutParentLink -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestPreviousEpochWithoutParentLink(t *testing.T) {
	x := newTestXDPoS_v2(100, 900)
	x.epochSwitches = lru.NewCache[common.Hash, *types.EpochSwitchInfo](64)
	chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}

	mn := []common.Address{{0x01}}
	penE1 := common.Address{0xe1}
	penE2 := common.Address{0xe2}

	// Epochs: E0 = V2 switch block 100, E1 = 1000, E2 = 1900.
	e0 := &types.Header{Number: big.NewInt(100)}
	h999 := &types.Header{Number: big.NewInt(999), ParentHash: common.Hash{0x99}}
	e1 := &types.Header{Number: big.NewInt(1000), ParentHash: h999.Hash(), Penalties: penE1.Bytes()}
	h1899 := &types.Header{Number: big.NewInt(1899), ParentHash: e1.Hash()}
	e2 := &types.Header{Number: big.NewInt(1900), ParentHash: h1899.Hash(), Penalties: penE2.Bytes()}
	h1950 := &types.Header{Number: big.NewInt(1950), ParentHash: e2.Hash()}
	for _, h := range []*types.Header{e0, h999, e1, h1899, e2, h1950} {
		chain.add(h)
	}

	// Cache entries as the in-batch path builds them: no parent link.
	info := func(h *types.Header, round types.Round, pens []common.Address) *types.EpochSwitchInfo {
		return &types.EpochSwitchInfo{
			Masternodes: mn, MasternodesLen: len(mn), Penalties: pens,
			EpochSwitchBlockInfo: &types.BlockInfo{Hash: h.Hash(), Number: h.Number, Round: round},
		}
	}
	i0, i1, i2 := info(e0, 0, nil), info(e1, 900, []common.Address{penE1}), info(e2, 1800, []common.Address{penE2})
	x.epochSwitches.Add(e0.Hash(), i0)
	x.epochSwitches.Add(h999.Hash(), i0)
	x.epochSwitches.Add(e1.Hash(), i1)
	x.epochSwitches.Add(h1899.Hash(), i1)
	x.epochSwitches.Add(e2.Hash(), i2)
	x.epochSwitches.Add(h1950.Hash(), i2)

	for limit, want := range map[int]uint64{0: 1900, 1: 1000, 2: 100, 5: 100} {
		got, err := x.getPreviousEpochSwitchInfoByHash(chain, h1950.Hash(), limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if n := got.EpochSwitchBlockInfo.Number.Uint64(); n != want {
			t.Errorf("limit %d: epoch switch %d, want %d", limit, n, want)
		}
	}

	// HookPenalty's input: limit 1 must be E1's penalties, not E2's again.
	if pens := x.GetPreviousPenaltyByHash(chain, h1950.Hash(), 1); len(pens) != 1 || pens[0] != penE1 {
		t.Errorf("GetPreviousPenaltyByHash(limit 1) = %v, want [%v]", pens, penE1)
	}

	// GetEpochSwitchInfoBetween must also walk past link-less entries.
	infos, err := x.GetEpochSwitchInfoBetween(chain, e0, h1950)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].EpochSwitchBlockInfo.Number.Uint64() != 1000 || infos[1].EpochSwitchBlockInfo.Number.Uint64() != 1900 {
		t.Errorf("GetEpochSwitchInfoBetween: got %d infos", len(infos))
	}
}
