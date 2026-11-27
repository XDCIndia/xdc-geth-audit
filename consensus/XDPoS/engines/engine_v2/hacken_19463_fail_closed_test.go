// Copyright (c) 2026 XDC Network
// F-2026-19463: at the tip (not bulk sync) a node without state, whose
// calcMasternodes fails, must not take the leader list from the header being
// verified. A reordered Validators would otherwise pick the slot leader.
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestF202619463

package engine_v2

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

func reversed(in []common.Address) []common.Address {
	out := make([]common.Address, len(in))
	for i, a := range in {
		out[len(in)-1-i] = a
	}
	return out
}

// Same set as the verified one, reordered so another key is the leader of the
// slot: accepted on bulk sync (overlap gate), refused at the tip.
func TestF202619463ReorderedListRefusedAtTip(t *testing.T) {
	noStateNode(t)
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(false)
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(saved) })

	eng, chain, keys, mns, p, h := esFixture(t, true)
	h.Validators = packValidators(reversed(mns))
	esSeal(t, h, keys[len(keys)-1]) // leaderIndex 0 of the reordered list
	err := eng.verifyHeader(chain, h, []*types.Header{p}, false)
	if !errors.Is(err, utils.ErrV2SnapshotMissing) {
		t.Fatalf("reordered list at the tip: want the calcMasternodes error, got %v", err)
	}
	if eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("refused header was recorded for a later recheck")
	}

	// Bulk sync above the anchor keeps the overlap gate: same header passes.
	core.XdcBulkSyncMode.Store(true)
	eng2, chain2, keys2, mns2, p2, h2 := esFixture(t, true)
	h2.Validators = packValidators(reversed(mns2))
	esSeal(t, h2, keys2[len(keys2)-1])
	if err := eng2.verifyHeader(chain2, h2, []*types.Header{p2}, false); err != nil {
		t.Fatalf("reordered verified set refused on bulk sync: %v", err)
	}
}
