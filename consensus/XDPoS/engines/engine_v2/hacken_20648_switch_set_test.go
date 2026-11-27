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
