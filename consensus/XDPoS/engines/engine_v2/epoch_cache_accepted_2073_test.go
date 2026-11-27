package engine_v2

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// A header that fails verifyHeader after the epoch-switch check must not replace
// the round-to-block cache entry of the accepted block for its round (#2073).
func TestRejectedHeaderDoesNotReplaceEpochCacheEntry(t *testing.T) {
	eng, chain, keys, mns, p, h := esFixture(t, false)
	h.Validators = packValidators(mns)
	esSeal(t, h, keys[0]) // leader of round 1800
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); err != nil {
		t.Fatalf("accepted header: %v", err)
	}
	if got, ok := eng.round2epochBlockInfo.Get(1800); !ok || got.Hash != h.Hash() {
		t.Fatalf("accepted epoch switch is not cached for round 1800: %v", got)
	}

	// Same round, other content, sealed by a key that is not the leader.
	bad := types.CopyHeader(h)
	bad.Time++
	esSeal(t, bad, keys[1])
	if bad.Hash() == h.Hash() {
		t.Fatal("test header has the same hash")
	}
	if err := eng.verifyHeader(chain, bad, []*types.Header{p}, true); err == nil {
		t.Fatal("header from a non-leader was accepted")
	}
	if got, _ := eng.round2epochBlockInfo.Get(1800); got == nil || got.Hash != h.Hash() {
		t.Fatalf("rejected header replaced the cache entry for round 1800: %v", got)
	}
}

// A single cache hit that is not a block of the main chain is not returned (#2073).
func TestEpochCacheSingleHitMustBeOnChain(t *testing.T) {
	eng, chain, _, _, _, h := esFixture(t, false)
	eng.round2epochBlockInfo.Add(1800, &types.BlockInfo{Hash: h.Hash(), Round: 1800, Number: h.Number})

	if got := eng.getBlockByEpochNumberInCache(chain, 1800); got != nil {
		t.Fatalf("returned %v, which is not in the chain", got)
	}
	chain.add(h)
	if got := eng.getBlockByEpochNumberInCache(chain, 1800); got == nil || got.Hash != h.Hash() {
		t.Fatalf("block on the chain was not returned: %v", got)
	}
}
