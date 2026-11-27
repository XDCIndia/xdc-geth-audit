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
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core"
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

// Controls: a header with a full QC is accepted above the anchor, and a header
// on the anchor's hash chain below it skips verifyQC.
func TestF202620645BackfillAndCertifiedHeadersStillAccepted(t *testing.T) {
	eng, base, keys, mns, p, h := esFixture(t, true)
	certified := headerClone(h)
	certified.Validators = packValidators(mns)
	esSeal(t, certified, keys[0])
	if err := eng.verifyHeader(&esAnchorChain{esChain: base, anchor: 500, anchorHash: common.Hash{0x01}}, certified, []*types.Header{p}, false); err != nil {
		t.Fatalf("header with a QC quorum above the anchor: %v", err)
	}

	eng2, base2, keys2, mns2, p2, h2 := esFixture(t, true)
	h2.Validators = packValidators(mns2)
	h2.Extra = withoutQCSignatures(t, h2)
	esSeal(t, h2, keys2[0])
	base2.add(h2)
	chain2 := anchorChainAbove(base2, h2, 1000)
	if err := eng2.verifyHeader(chain2, h2, []*types.Header{p2}, false); err != nil {
		t.Fatalf("backfill header on the anchor chain must skip verifyQC: %v", err)
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

// A round skip carries a timeout certificate, which is checked above the anchor
// like the quorum certificate; only the anchor's hash chain skips it.
func TestF202620645ForgedTimeoutCertAboveAnchorRejected(t *testing.T) {
	eng, base, keys, mns, p, h := esFixture(t, true)
	eng.config.V2.RoundSkipTCBlock = big.NewInt(0)
	var extra types.ExtraFields_v2
	if err := DecodeExtraFields(h.Extra, &extra); err != nil {
		t.Fatal(err)
	}
	extra.TimeoutCert = &types.TimeoutCert{Round: 1799, Signatures: []types.Signature{}, GapNumber: 450}
	enc, err := extra.EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	h.Extra = enc
	h.Validators = packValidators(mns)
	esSeal(t, h, keys[0])

	above := &esAnchorChain{esChain: base, anchor: 500, anchorHash: common.Hash{0x01}}
	if err := eng.verifyRoundSkip(above, h, p, nil, extra.Round, extra.QuorumCert); err == nil {
		t.Fatal("forged timeout certificate accepted above the anchor")
	}
	base.add(h)
	if err := eng.verifyRoundSkip(anchorChainAbove(base, h, 1000), h, p, nil, extra.Round, extra.QuorumCert); err != nil {
		t.Fatalf("timeout certificate on the anchor chain must be skipped: %v", err)
	}
}

// The leader-check tolerance of bulk sync does not extend above the anchor.
func TestF202620645LeaderToleranceEndsAtAnchor(t *testing.T) {
	core.XdcBulkSyncMode.Store(true)
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(false) })

	// keys[1] is a masternode but not the leader (index 0) of round 1800.
	build := func() (*XDPoS_v2, *esChain, *types.Header, *types.Header) {
		eng, base, keys, mns, p, h := esFixture(t, true)
		h.Validators = packValidators(mns)
		esSeal(t, h, keys[1])
		return eng, base, p, h
	}
	// No anchor: unchanged, the mismatch is tolerated.
	eng, base, p, h := build()
	if err := eng.verifyHeader(base, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("no anchor: %v", err)
	}
	// Above the anchor: a valid QC is not enough, the leader is enforced.
	eng, base, p, h = build()
	err := eng.verifyHeader(&esAnchorChain{esChain: base, anchor: 500, anchorHash: common.Hash{0x01}}, h, []*types.Header{p}, false)
	if !errors.Is(err, utils.ErrNotItsTurn) {
		t.Fatalf("non-leader above the anchor: want ErrNotItsTurn, got %v", err)
	}
	// At or below the anchor: still tolerated.
	eng, base, p, h = build()
	base.add(h)
	if err := eng.verifyHeader(anchorChainAbove(base, h, 1000), h, []*types.Header{p}, false); err != nil {
		t.Fatalf("below the anchor: %v", err)
	}
}

// commitBlocks no longer skips a whole window after the anchor: it commits when
// the parent and grandparent are stored and skips, without error, when either
// is missing.
func TestF202620645CommitBlocksSkipsOnlyMissingAncestors(t *testing.T) {
	build := func(withGrandparent, withParent bool) (*XDPoS_v2, *esAnchorChain, *types.Header, types.Round) {
		eng, base, _, _, _, _ := esFixture(t, true)
		qc := func(parent *types.Header, round types.Round) *types.QuorumCert {
			return &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Hash: parent.Hash(), Round: round, Number: new(big.Int).Set(parent.Number)}}
		}
		root := &types.Header{Number: big.NewInt(1000), Difficulty: big.NewInt(1), Extra: []byte{0x03}}
		gp := esV2Header(t, 1001, root, 2000, qc(root, 1999), 1)
		par := esV2Header(t, 1002, gp, 2001, qc(gp, 2000), 2)
		prop := esV2Header(t, 1003, par, 2002, qc(par, 2001), 3)
		if withGrandparent {
			base.add(gp)
		}
		if withParent {
			base.add(par)
		}
		// Anchor above the block: the old window skip would have applied.
		return eng, &esAnchorChain{esChain: base, anchor: 1000, anchorHash: root.Hash()}, prop, 2002
	}

	eng, chain, prop, round := build(true, true)
	committed, err := eng.commitBlocks(chain, prop, &round, &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Number: big.NewInt(0)}})
	if err != nil || eng.highestCommitBlock == nil || eng.highestCommitBlock.Number.Uint64() != 1001 {
		t.Fatalf("parent and grandparent stored: committed=%v err=%v highest=%v", committed, err, eng.highestCommitBlock)
	}

	for name, c := range map[string][2]bool{"grandparent missing": {false, true}, "parent missing": {false, false}} {
		eng, chain, prop, round := build(c[0], c[1])
		if _, err := eng.commitBlocks(chain, prop, &round, &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Number: big.NewInt(0)}}); err != nil {
			t.Fatalf("%s: want a silent skip, got %v", name, err)
		}
		if eng.highestCommitBlock != nil {
			t.Fatalf("%s: committed %v", name, eng.highestCommitBlock)
		}
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
