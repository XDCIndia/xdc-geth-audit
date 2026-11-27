// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestF202619612TimeoutGapFollowsRoundEpoch rejects a timeout certificate
// whose gap is an older snapshot than the gap of the round's epoch.
func TestF202619612TimeoutGapFollowsRoundEpoch(t *testing.T) {
	f := newTCFixture(t, 1800)
	cached, ok := f.x.epochSwitches.Get(f.chain.head.Hash())
	if !ok {
		t.Fatal("head epoch missing")
	}
	info := cached
	info.EpochSwitchBlockInfo.Number = big.NewInt(1801)
	info.EpochSwitchBlockInfo.Round = 1800
	f.chain.head.Number = big.NewInt(1801)
	f.x.epochSwitches.Add(f.chain.head.Hash(), info)

	currentGap := &types.Header{Number: big.NewInt(1350)}
	cachedSnap, ok := f.x.snapshots.Get(f.chain.gap.Hash())
	if !ok {
		t.Fatal("gap snapshot missing")
	}
	oldSnap := cachedSnap
	current := *oldSnap
	current.Number = 1350
	current.Hash = currentGap.Hash()
	f.x.snapshots.Add(currentGap.Hash(), &current)

	round := types.Round(1800)
	hash := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: 450})
	sig, err := crypto.Sign(hash[:], f.active[0])
	if err != nil {
		t.Fatal(err)
	}
	msg := &types.Timeout{Round: round, GapNumber: 450, Signature: sig}
	if ok, err := f.x.VerifyTimeoutMessage(f.chain, msg); ok || err == nil {
		t.Fatalf("timeout from the old gap snapshot was accepted: ok=%v err=%v", ok, err)
	}

	sigs := signTimeout(t, round, f.active[:2]...)
	// signTimeout hashes gap 450, which is the certificate under test.
	if err := f.x.verifyTC(f.chain, &types.TimeoutCert{Round: round, GapNumber: 450, Signatures: sigs}); err == nil {
		t.Fatal("certificate naming gap 450 was accepted for the epoch whose gap is 1350")
	}

	goodHash := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: 1350})
	good := make([]types.Signature, 2)
	for i := range good {
		good[i], err = crypto.Sign(goodHash[:], f.active[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	f.chain.gap = currentGap
	if err := f.x.verifyTC(f.chain, &types.TimeoutCert{Round: round, GapNumber: 1350, Signatures: good}); err != nil {
		t.Fatalf("certificate for gap 1350: %v", err)
	}
}

// TestF202619612EpochSwitchRoundTimeout accepts the gap that sendTimeout signs
// for a round that opens an epoch whose switch block is not in the chain yet.
// Rejecting it halted net 2040 at round 999000.
func TestF202619612EpochSwitchRoundTimeout(t *testing.T) {
	f := newTCFixture(t, 1800)
	info := &types.EpochSwitchInfo{EpochSwitchBlockInfo: &types.BlockInfo{Number: big.NewInt(1801), Round: 1800}}
	f.chain.head.Number = big.NewInt(2699)
	next := f.x.gapFromSwitch(2700)
	if next == f.x.gapFromSwitch(1801) {
		t.Fatalf("fixture epoch/gap do not separate the two gaps: %d", next)
	}
	if err := f.x.timeoutGapMatchesEpoch(f.chain, info, types.Round(2700), next); err != nil {
		t.Fatalf("epoch-switch round timeout rejected: %v", err)
	}
	if err := f.x.timeoutGapMatchesEpoch(f.chain, info, types.Round(2700), 0); err == nil {
		t.Fatal("older gap accepted at an epoch-switch round")
	}
	if err := f.x.timeoutGapMatchesEpoch(f.chain, info, types.Round(2000), next); err == nil {
		t.Fatal("next-epoch gap accepted for a round inside the known epoch")
	}
}

// F-2026-19612: the certificate gap is checked against the round epoch before
// the gap snapshot is loaded. A wrong gap that has no snapshot fails with the
// gap error, not with a snapshot error.
func TestF202619612VerifyTCGapCheckedBeforeSnapshot(t *testing.T) {
	const round = types.Round(5)
	f := newTCFixture(t, 0)
	sigs := signTimeout(t, round, f.active[:2]...)
	// 12345 is not the gap of the epoch (450) and no snapshot exists for it.
	err := f.x.verifyTC(f.chain, &types.TimeoutCert{Round: round, GapNumber: 12345, Signatures: sigs})
	if err == nil {
		t.Fatal("wrong-gap TC accepted")
	}
	if strings.Contains(err.Error(), "snapshot") || !strings.Contains(err.Error(), "is not gap") {
		t.Fatalf("want the gap error, got: %v", err)
	}
}

// F-2026-19612: a nil epoch info, or one without a switch block, is an error
// for verifyTCAt. timeoutGapMatchesEpochAt alone would pass it.
func TestF202619612NilTCEpochInfoRejected(t *testing.T) {
	f := newTCFixture(t, 0)
	for name, info := range map[string]*types.EpochSwitchInfo{
		"nil info":         nil,
		"nil switch block": {Masternodes: []common.Address{{1}}},
		"nil number":       {EpochSwitchBlockInfo: &types.BlockInfo{}},
	} {
		if !tcEpochUnresolved(info) {
			t.Errorf("%s: not reported as unresolved", name)
		}
		if err := f.x.timeoutGapMatchesEpochAt(f.chain, f.chain.head, info, 5, 12345); err != nil {
			t.Fatalf("%s: gap check no longer passes an unresolved epoch; revisit verifyTCAt guard: %v", name, err)
		}
	}
	good := &types.EpochSwitchInfo{EpochSwitchBlockInfo: &types.BlockInfo{Number: big.NewInt(901)}}
	if tcEpochUnresolved(good) {
		t.Error("resolved epoch reported as unresolved")
	}
}
