// Copyright (c) 2026 XDC Network
// F-2026-20645: the fast-sync catch-up window skipped verifyQC for every block up
// to anchor + 2 epochs. Above the anchor nothing authenticates a header but
// consensus, so one masternode signature was enough to extend the chain.
//
// A header skips the certificate checks only when it is also on the anchor's
// parent-hash chain (second fix), and the 3-chain commit is skipped only while
// the parent or grandparent is missing.
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestF202620645

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// headerClone is copied from certik_1719_deferred_switch_recheck_test.go, which is
// not in the audit tree. It shallow-copies src so a test can mutate the copy.
func headerClone(src *types.Header) *types.Header {
	c := *src
	c.Number = new(big.Int).Set(src.Number)
	if src.Validators != nil {
		c.Validators = append([]byte(nil), src.Validators...)
	}
	if src.Penalties != nil {
		c.Penalties = append([]byte(nil), src.Penalties...)
	}
	if src.Extra != nil {
		c.Extra = append([]byte(nil), src.Extra...)
	}
	c.Validator = append([]byte(nil), src.Validator...)
	return &c
}

// esAnchorChain is an esChain with a fast-sync anchor installed.
type esAnchorChain struct {
	*esChain
	anchor     uint64
	anchorHash common.Hash
}

func (c *esAnchorChain) GetTrustedCheckpointAnchor() (uint64, common.Hash, bool) {
	return c.anchor, c.anchorHash, true
}

// anchorChainAbove stores a parent-linked chain of headers from.Number+1 up to
// anchorNum on top of from and returns an anchor chain reader whose anchor is
// the last of them.
func anchorChainAbove(base *esChain, from *types.Header, anchorNum uint64) *esAnchorChain {
	top := from
	for n := from.Number.Uint64() + 1; n <= anchorNum; n++ {
		next := &types.Header{Number: new(big.Int).SetUint64(n), ParentHash: top.Hash(), Difficulty: big.NewInt(1)}
		base.add(next)
		top = next
	}
	return &esAnchorChain{esChain: base, anchor: anchorNum, anchorHash: top.Hash()}
}

// A single-signature branch above the anchor, inside anchor + 2 epochs, is rejected.
func TestF202620645OneSignatureBranchAboveAnchorRejected(t *testing.T) {
	eng, base, keys, mns, p, h := esFixture(t, true)
	chain := &esAnchorChain{esChain: base, anchor: 500, anchorHash: common.Hash{0x01}} // h = 903 is inside 500 + 1800
	h.Validators = packValidators(mns)
	h.Extra = withoutQCSignatures(t, h)
	esSeal(t, h, keys[0]) // leaderIndex = 1800 % 900 % 4 = 0
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err == nil {
		t.Fatal("header without a QC quorum accepted above the fast-sync anchor")
	}
}

// A side-branch header numbered below the anchor, not on its parent-hash chain,
// gets the full certificate check. The number-only test skipped it.
func TestF202620645SideBranchBelowAnchorRejected(t *testing.T) {
	eng, base, keys, mns, p, h := esFixture(t, true)
	h.Validators = packValidators(mns)
	h.Extra = withoutQCSignatures(t, h)
	esSeal(t, h, keys[0])

	// The anchor chain runs through a different header at the same height.
	canon := headerClone(h)
	canon.Time++
	esSeal(t, canon, keys[0])
	if canon.Hash() == h.Hash() {
		t.Fatal("test setup: canonical twin has the same hash")
	}
	base.add(canon)
	chain := anchorChainAbove(base, canon, 1000)

	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err == nil {
		t.Fatal("side-branch header below the anchor accepted without a QC quorum")
	}
	if !eng.inAnchorBackfill(chain, canon) {
		t.Fatal("canonical header below the anchor is not recognised as backfill")
	}
	if eng.inAnchorBackfill(chain, h) {
		t.Fatal("side-branch header recognised as backfill")
	}
}

// withoutQCSignatures returns h.Extra re-encoded with an empty QC signature list.
func withoutQCSignatures(t *testing.T, h *types.Header) []byte {
	t.Helper()
	var extra types.ExtraFields_v2
	if err := DecodeExtraFields(h.Extra, &extra); err != nil {
		t.Fatalf("decode extra: %v", err)
	}
	qc := *extra.QuorumCert
	qc.Signatures = nil
	out, err := (&types.ExtraFields_v2{Round: extra.Round, QuorumCert: &qc}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode extra: %v", err)
	}
	return out
}
