// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"sort"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func sortedAddrs(a []common.Address) []common.Address {
	out := append([]common.Address(nil), a...)
	sort.Slice(out, func(i, j int) bool { return out[i].Cmp(out[j]) < 0 })
	return out
}

// TestCheckpointSignersUseGapSnapshot: the checkpoint is checked against the
// snapshot of its epoch's gap block (refreshed from 0x88 when the gap block is
// written), not a snapshot cached between the gap block and the checkpoint
// before that refresh. Mainnet replay: checkpoint 616,500 saw a pre-refresh set.
func TestCheckpointSignersUseGapSnapshot(t *testing.T) {
	cfg := *testConfig
	cfg.Epoch, cfg.Gap = 10, 3
	engine := New(&cfg, rawdb.NewMemoryDatabase())

	// Headers 7 (gap) .. 9 linked by ParentHash; checkpoint 10 on top.
	var parents []*types.Header
	parent := common.Hash{}
	for n := uint64(7); n <= 9; n++ {
		h := &types.Header{Number: new(big.Int).SetUint64(n), ParentHash: parent, Difficulty: big.NewInt(1)}
		parents = append(parents, h)
		parent = h.Hash()
	}
	cp := &types.Header{Number: big.NewInt(10), ParentHash: parent, Difficulty: big.NewInt(1)}
	gap := parents[0]

	refreshed := []common.Address{common.HexToAddress("0x0a"), common.HexToAddress("0x0b")}
	stale := []common.Address{common.HexToAddress("0x0a"), common.HexToAddress("0x0c")}
	engine.recents.Add(gap.Hash(), newSnapshot(engine.config, engine.signatures, 7, gap.Hash(), refreshed))
	parentSnap := newSnapshot(engine.config, engine.signatures, 9, parents[2].Hash(), stale)

	chain := cpSignersChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}}
	got := sortedAddrs(engine.checkpointSigners(chain, cp, parents, parentSnap))
	if want := sortedAddrs(refreshed); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("checkpoint signers = %v, want the gap snapshot %v", got, want)
	}
	// Gap block not resolvable (no parents, nothing stored): the parent snapshot is used.
	got = sortedAddrs(engine.checkpointSigners(chain, cp, nil, parentSnap))
	if want := sortedAddrs(stale); got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("fallback signers = %v, want the parent snapshot %v", got, want)
	}
}
