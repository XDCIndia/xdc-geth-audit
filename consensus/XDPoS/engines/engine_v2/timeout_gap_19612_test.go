// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"strings"
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
