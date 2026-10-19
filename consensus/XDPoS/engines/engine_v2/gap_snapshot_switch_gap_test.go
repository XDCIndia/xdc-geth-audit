// Copyright (c) 2026 XDC Network
// Fix #1464: getSnapshot must not round the gap number SwitchBlock+Gap down to
// the V2 switch block and replace the gap snapshot with the switch block's V1
// Extra.

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Net 2004 layout: epoch 900, gap 450, V2 switch 2700. Import stored the gap-3150
// snapshot in stake order; the switch block's V1 Extra lists the signers in address
// order. Legacy XDPoSChain picks the leader of epoch round 1800 (block 4495) from the
// stake order, so a rebuilt snapshot rejects block 4495 with "not its turn".
func TestGetSnapshotSwitchPlusGapKeepsStoredSnapshot(t *testing.T) {
	eng, db := newA894Engine(t)
	eng.config.V2.SwitchBlock = big.NewInt(2700)

	a := common.HexToAddress("0x46f1b53474f89030a58804e21a040e1d33e68de6")
	b := common.HexToAddress("0xb835588009d20d0c5683fd92cea11bee88389374")
	extra := make([]byte, 32+2*common.AddressLength+65)
	copy(extra[32:], a[:])
	copy(extra[32+common.AddressLength:], b[:])
	switchHeader := &types.Header{Number: big.NewInt(2700), Difficulty: big.NewInt(1), Extra: extra}
	gapHeader := &types.Header{Number: big.NewInt(3150), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{2700: switchHeader, 3150: gapHeader}}

	stored := newSnapshot(3150, gapHeader.Hash(), []common.Address{b, a})
	if err := storeSnapshot(stored, db); err != nil {
		t.Fatal(err)
	}
	for _, forSigning := range []bool{true, false} {
		eng.snapshots.Add(stored.Hash, stored)
		snap, err := eng.getSnapshot(chain, 3150, forSigning, nil)
		if err != nil {
			t.Fatalf("forSigning=%t: %v", forSigning, err)
		}
		if got := snap.NextEpochCandidates; len(got) != 2 || got[0] != b || got[1] != a {
			t.Fatalf("forSigning=%t: got %v, want the stored stake order [%s %s]", forSigning, got, b, a)
		}
		eng.snapshots.Remove(stored.Hash)
		if disk, err := loadSnapshot(db, gapHeader.Hash()); err != nil || disk.NextEpochCandidates[0] != b {
			t.Fatalf("forSigning=%t: stored gap snapshot overwritten: %v %v", forSigning, disk, err)
		}
	}
}
