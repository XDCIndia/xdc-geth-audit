// Copyright (c) 2026 XDC Network
// An epoch-switch block whose number sits at a gap position
// (number%Epoch == Epoch-Gap) must resolve the snapshot of its own epoch, as
// canonical XDPoSChain calcMasternodes does (getSnapshot isGapNumber=false).

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Devnet 5551 block 59,850 is an epoch switch (round 58,500) and 59,850%900 ==
// 450. Its masternodes come from the gap-58,950 snapshot. Reading 59,850 as a
// gap number looks up the block itself, which is not in the chain yet, and
// rejects it with "no header at gap number 59850".
func TestCalcMasternodesEpochSwitchAtGapPosition(t *testing.T) {
	eng, db := newA894Engine(t)
	eng.config.V2.SwitchBlock = big.NewInt(2700)
	eng.config.V2.CurrentConfig.MaxMasternodes = 18

	a := common.HexToAddress("0x46f1b53474f89030a58804e21a040e1d33e68de6")
	b := common.HexToAddress("0xb835588009d20d0c5683fd92cea11bee88389374")
	gapHeader := &types.Header{Number: big.NewInt(58950), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	parent := &types.Header{Number: big.NewInt(59849), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{58950: gapHeader, 59849: parent}}

	snap := newSnapshot(58950, gapHeader.Hash(), []common.Address{b, a})
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatal(err)
	}
	eng.snapshots.Add(snap.Hash, snap)

	ms, _, _, err := eng.calcMasternodes(chain, big.NewInt(59850), parent.Hash(), 58500, nil)
	if err != nil {
		t.Fatalf("calcMasternodes(59850): %v", err)
	}
	if len(ms) != 2 || ms[0] != b || ms[1] != a {
		t.Fatalf("calcMasternodes(59850) = %v, want the gap-58950 snapshot [%s %s]", ms, b, a)
	}
}
