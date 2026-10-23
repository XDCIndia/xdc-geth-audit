// Copyright (c) 2026 XDC Network
// CertiK XDC-10 / #1719: epoch-switch headers accepted in bulk-sync (!fullVerify)
// without local masternode verification must be re-checked once the canonical DB
// is committed.
//
// Three properties verified here:
//   1. A forged epoch-switch header (wrong Validators) accepted under !fullVerify
//      is recorded; RecheckDeferredSwitches returns it as bad once calcMasternodes
//      can run against the committed DB.
//   2. A genuine epoch-switch header (correct Validators) accepted under !fullVerify
//      is recorded; RecheckDeferredSwitches clears it and returns nil.
//   3. A recorded header is kept when RecheckDeferredSwitches cannot run
//      (CheckpointSyncNoStateActive=true — state is not yet available below the pivot).
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestCertikXDC10

package engine_v2

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

// headerClone shallow-copies src so tests can mutate Validators/Validator/Coinbase
// without aliasing the original.
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

// TestCertikXDC10_ForgedHeaderDetected: a forged epoch-switch header (Validators
// lists an attacker set) is accepted under !fullVerify (calcMasternodes fails because
// the gap snapshot is absent during bulk sync). Once the gap snapshot is committed to
// the DB, RecheckDeferredSwitches detects the mismatch and returns the forged header.
//
// This test FAILS on origin/main (pendingSwitchRechecks field does not exist) and
// passes after this PR.
func TestCertikXDC10_ForgedHeaderDetected(t *testing.T) {
	// esFixture(skipSnapshot=true): gap snapshot absent — calcMasternodes will fail
	// with ErrV2SnapshotMissing when called with parents != nil (bulk-sync path).
	eng, chain, keys, mns, p, hTemplate := esFixture(t, true)

	// Build forged epoch-switch header: attacker-controlled validator set.
	attKeys, attMns := genNKeys(t, 2)
	h := headerClone(hTemplate)
	h.Validators = packValidators(attMns)
	// Leader index for round 1800, epoch 900, 2 forged masternodes: 1800%900%2 = 0.
	esSeal(t, h, attKeys[0])

	// Accept under !fullVerify with parents=[p] (the bulk-sync signature).
	// calcMasternodes fails (no snapshot, parents != nil → ErrV2SnapshotMissing),
	// falls back to header.Validators, and RECORDS the header.
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("forged header must be accepted under !fullVerify, got %v", err)
	}
	if !eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("forged header must be in pendingSwitchRechecks after !fullVerify acceptance")
	}

	// Seed the gap snapshot with the REAL masternodes into eng.db (accessible
	// within the same package) and register h as canonical in the chain mock.
	gapHash := chain.byNumber[450].Hash()
	if err := storeSnapshot(newSnapshot(450, gapHash, mns), eng.db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}
	chain.add(h)

	// RecheckDeferredSwitches must detect the Validators mismatch.
	bad, recheckErr := eng.RecheckDeferredSwitches(chain)
	if bad == nil {
		t.Fatal("RecheckDeferredSwitches must return the forged header as bad")
	}
	if bad.Hash() != h.Hash() {
		t.Fatalf("bad header hash mismatch: want %s, got %s", h.Hash(), bad.Hash())
	}
	if !errors.Is(recheckErr, utils.ErrValidatorsNotLegit) {
		t.Fatalf("want ErrValidatorsNotLegit, got %v", recheckErr)
	}
	_ = keys // used only via mns
}

// TestCertikXDC10_GenuineHeaderCleared: a genuine epoch-switch header (correct
// Validators, correct proposer) accepted under !fullVerify is recorded, then
// cleared by RecheckDeferredSwitches once the local DB has the gap snapshot.
func TestCertikXDC10_GenuineHeaderCleared(t *testing.T) {
	eng, chain, keys, mns, p, hTemplate := esFixture(t, true)

	// Genuine header: Validators = real masternodes, signed by in-turn proposer.
	// Leader index: 1800%900%4 = 0 → keys[0] is the in-turn proposer.
	h := headerClone(hTemplate)
	h.Validators = packValidators(mns)
	esSeal(t, h, keys[0])

	// Accept under !fullVerify — calcMasternodes fails, header is recorded.
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("genuine header must be accepted under !fullVerify, got %v", err)
	}
	if !eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("genuine header must be recorded in pendingSwitchRechecks")
	}

	// Seed the gap snapshot and register h as canonical.
	gapHash := chain.byNumber[450].Hash()
	if err := storeSnapshot(newSnapshot(450, gapHash, mns), eng.db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}
	chain.add(h)

	// RecheckDeferredSwitches must clear the entry cleanly.
	bad, recheckErr := eng.RecheckDeferredSwitches(chain)
	if bad != nil || recheckErr != nil {
		t.Fatalf("genuine header must recheck clean, got bad=%v err=%v", bad, recheckErr)
	}
	if eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("genuine header must be removed from pendingSwitchRechecks after clean recheck")
	}
}

// TestCertikXDC10_NoStateKeptNotRejected: when the node is in checkpoint-sync-
// without-state mode (CheckpointSyncNoStateActive=true, e.g. immediately after a
// fast-sync pivot before state healing completes), RecheckDeferredSwitches short-
// circuits and keeps all entries rather than rejecting on the absence of state.
func TestCertikXDC10_NoStateKeptNotRejected(t *testing.T) {
	eng, chain, _, _, p, hTemplate := esFixture(t, true)

	// Build a forged header and accept it under !fullVerify to seed the pending set.
	attKeys, attMns := genNKeys(t, 2)
	h := headerClone(hTemplate)
	h.Validators = packValidators(attMns)
	esSeal(t, h, attKeys[0])

	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("header must be accepted under !fullVerify, got %v", err)
	}
	if !eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("header must be recorded after !fullVerify acceptance")
	}

	// Register h as canonical.
	chain.add(h)

	// Simulate fast-sync post-pivot: state below the pivot is unavailable.
	saved := core.CheckpointSyncNoStateActive.Load()
	core.CheckpointSyncNoStateActive.Store(true)
	defer core.CheckpointSyncNoStateActive.Store(saved)

	// RecheckDeferredSwitches must defer all rechecks (returns nil, nil) and
	// keep the entry for a later attempt once state is built.
	bad, recheckErr := eng.RecheckDeferredSwitches(chain)
	if bad != nil || recheckErr != nil {
		t.Fatalf("no-state recheck must not reject, got bad=%v err=%v", bad, recheckErr)
	}
	if !eng.pendingSwitchRechecks.Contains(h.Hash()) {
		t.Fatal("entry must be kept when state is unavailable (CheckpointSyncNoStateActive)")
	}
}
