// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

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
