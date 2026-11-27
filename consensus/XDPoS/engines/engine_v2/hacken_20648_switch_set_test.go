// Copyright (c) 2026 XDC Network
// F-2026-20648: on a node without state, an epoch-switch header whose list is
// not derived from verified data must not choose the set that checks its own
// seal. GetMasternodesWithParents on the header itself returns header.Validators,
// so the old fallback compared the header with itself.
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestF202620648

package engine_v2

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

func noStateNode(t *testing.T) {
	t.Helper()
	saved := core.CheckpointSyncNoStateActive.Load()
	core.CheckpointSyncNoStateActive.Store(true)
	t.Cleanup(func() { core.CheckpointSyncNoStateActive.Store(saved) })
}

// bulkSyncNode models the bulk sync above the anchor, where the F-2026-20648
// overlap gate applies. Outside bulk sync a state-less node fails closed
// (F-2026-19463), see hacken_19463_fail_closed_test.go.
func bulkSyncNode(t *testing.T) {
	t.Helper()
	noStateNode(t)
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(true)
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(saved) })
}

// A forged list on a fast-synced node (no gap snapshot) is refused.
func TestF202620648ForgedValidatorListRefusedWithoutState(t *testing.T) {
	bulkSyncNode(t)
	eng, chain, _, _, p, h := esFixture(t, true)
	attKeys, attMns := genNKeys(t, 2)
	h.Validators = packValidators(attMns)
	esSeal(t, h, attKeys[0]) // leaderIndex = 1800 % 900 % 2 = 0
	err := eng.verifyHeader(chain, h, []*types.Header{p}, false)
	if !errors.Is(err, utils.ErrValidatorsNotLegit) {
		t.Fatalf("forged epoch-switch list on a state-less node: want ErrValidatorsNotLegit, got %v", err)
	}
	if eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("refused header was recorded for a later recheck")
	}
}

// The same forged list with a gap snapshot present: the derived set wins.
func TestF202620648ForgedValidatorListRefusedAgainstDerivedSet(t *testing.T) {
	noStateNode(t)
	savedBulk := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(true) // the leader check then also takes the header's list
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(savedBulk) })
	eng, chain, _, _, p, h := esFixture(t, false)
	attKeys, attMns := genNKeys(t, 2)
	h.Validators = packValidators(attMns)
	esSeal(t, h, attKeys[0])
	err := eng.verifyHeader(chain, h, []*types.Header{p}, false)
	if !errors.Is(err, utils.ErrValidatorsNotLegit) {
		t.Fatalf("forged list against a derived set: want ErrValidatorsNotLegit, got %v", err)
	}
}

// Controls: the verified set, and a one-key rotation of it, still pass, so a
// fast-synced node keeps syncing across an epoch switch.
func TestF202620648VerifiedSetAndRotationStillAccepted(t *testing.T) {
	bulkSyncNode(t)
	eng, chain, keys, mns, p, h := esFixture(t, true)
	same := headerClone(h)
	same.Validators = packValidators(mns)
	esSeal(t, same, keys[0])
	if err := eng.verifyHeader(chain, same, []*types.Header{p}, false); err != nil {
		t.Fatalf("verified set refused: %v", err)
	}

	eng2, chain2, keys2, mns2, p2, h2 := esFixture(t, true)
	_, extra := genNKeys(t, 1)
	h2.Validators = packValidators(append([]common.Address{mns2[0], mns2[1], mns2[2]}, extra...))
	esSeal(t, h2, keys2[0]) // leaderIndex = 1800 % 900 % 4 = 0
	if err := eng2.verifyHeader(chain2, h2, []*types.Header{p2}, false); err != nil {
		t.Fatalf("one-key rotation refused: %v", err)
	}
}

// With no verified epoch behind the header, the set cannot be derived: refuse.
func TestF202620648RefusedWhenSetCannotBeDerived(t *testing.T) {
	noStateNode(t)
	eng, chain, _, mns, p, h := esFixture(t, true)
	delete(chain.byHash, chain.byNumber[901].Hash()) // epoch-switch header E1 is gone
	delete(chain.byNumber, 901)
	h.Validators = packValidators(mns)
	err := eng.requireDerivedSwitchSet(chain, h, p, []*types.Header{p})
	if !errors.Is(err, utils.ErrEpochSwitchSetNotDerivable) {
		t.Fatalf("want ErrEpochSwitchSetNotDerivable, got %v", err)
	}
}

func TestSignersOverlap(t *testing.T) {
	_, a := genNKeys(t, 6)
	for _, c := range []struct {
		name     string
		ref, got []common.Address
		want     bool
	}{
		{"same", a, a, true},
		{"one swapped of six", a, append(append([]common.Address{}, a[:5]...), common.Address{9}), true},
		{"two swapped of six", a, append(append([]common.Address{}, a[:4]...), common.Address{8}, common.Address{9}), false},
		{"disjoint", a, []common.Address{{7}}, false},
		{"empty reference", nil, a, false},
		{"duplicates count once", a, []common.Address{a[0], a[0], a[0], a[0], a[0], a[0]}, false},
	} {
		if got := utils.SignersOverlap(c.ref, c.got); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
