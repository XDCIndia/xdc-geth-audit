// Copyright (c) 2026 XDC Network
// Tests for pool signer deduplication in onVotePoolThresholdReached and
// onTimeoutPoolThresholdReached (F-2026-19525).
//
// The pool keys entries by message hash, which includes the signature bytes.
// A single signer key can produce multiple pool entries with byte-distinct
// signatures (e.g. via ECDSA malleation). Before this fix the threshold
// callbacks counted all entries for the same signer, letting one key satisfy
// the BFT quorum alone.
//
// Canonical XDPoSChain v2.9.3-testnet deduplicates by recovered signer
// address in verifyAllSignatures (vote.go:200, timeout.go:82).

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestPoolSignerDedup_Vote verifies that onVotePoolThresholdReached deduplicates
// pool entries by recovered signer address (F-2026-19525).
//
// Setup: 3 masternodes, certThreshold=2/3 → need ≥2 distinct signers.
// Pool: 2 entries from 1 signer (original + malleated signature, different hashes).
//
// Before fix (origin/main): both entries counted as valid → 2 ≥ 3*(2/3)≈2.0
// → processQC called → highestQuorumCert.Round updated from 0 to 10 → FAIL.
// After fix: dedup by signer → 1 distinct signer < 2 → threshold not met
// → processQC not called → highestQuorumCert.Round stays 0 → PASS.
func TestPoolSignerDedup_Vote(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.isInitialized = true

	keys, addrs := genNKeys(t, 3)

	proposedBlockInfo := &types.BlockInfo{
		Hash:   common.HexToHash("0xabc123"),
		Round:  types.Round(10),
		Number: big.NewInt(1001),
	}

	// Inject epoch info: 3 masternodes → certThreshold check needs ≥2 distinct signers.
	epochInfo := &types.EpochSwitchInfo{
		Masternodes:          addrs,
		MasternodesLen:       3,
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: proposedBlockInfo.Hash, Round: 1, Number: big.NewInt(1)},
	}
	eng.epochSwitches.Add(proposedBlockInfo.Hash, epochInfo)

	// Sign the vote with keys[0], then malleate to get a byte-distinct signature
	// that recovers to the same signer address. Both sigs have the same VoteForSign
	// content, so both would pass VerifyVoteMessage and be added to the pool.
	voteForSign := &types.VoteForSign{ProposedBlockInfo: proposedBlockInfo, GapNumber: 450}
	h := types.VoteSigHash(voteForSign)
	sig1, err := crypto.Sign(h[:], keys[0])
	if err != nil {
		t.Fatal(err)
	}
	// malleateSig is defined in a98_dedup_test.go: same signer, different bytes.
	sig2 := malleateSig(t, types.Signature(sig1))

	vote1 := &types.Vote{ProposedBlockInfo: proposedBlockInfo, Signature: types.Signature(sig1), GapNumber: 450}
	vote1.SetSigner(addrs[0])
	vote2 := &types.Vote{ProposedBlockInfo: proposedBlockInfo, Signature: sig2, GapNumber: 450}
	vote2.SetSigner(addrs[0])

	// Pool keys by Vote.Hash() = rlpHash(Signature, ProposedBlockInfo, GapNumber),
	// so different Signature bytes → different hash → two distinct pool entries.
	pooledVotes := map[common.Hash]utils.PoolObj{
		vote1.Hash(): vote1,
		vote2.Hash(): vote2,
	}

	// processQC (called when threshold is met) updates highestQuorumCert.Round
	// when incomingQC.Round (10) > current (0). Use this as the observable signal.
	initialRound := eng.highestQuorumCert.ProposedBlockInfo.Round // 0

	header := &types.Header{Number: big.NewInt(1001)}
	if err := eng.onVotePoolThresholdReached(&mockChainForA894{}, pooledVotes, vote1, header); err != nil {
		t.Fatalf("unexpected error from onVotePoolThresholdReached: %v", err)
	}

	if eng.highestQuorumCert.ProposedBlockInfo.Round != initialRound {
		t.Errorf("FAIL (missing signer dedup, F-2026-19525): processQC was called with only 1 distinct "+
			"signer; highestQC.Round changed from %d to %d (expected to stay %d)",
			initialRound, eng.highestQuorumCert.ProposedBlockInfo.Round, initialRound)
	}
}

// TestPoolSignerDedup_Timeout verifies that onTimeoutPoolThresholdReached
// deduplicates pool entries by recovered signer address (F-2026-19525).
//
// Setup: pool with 2 entries from 1 signer (original + malleated signature).
// onTimeoutPoolThresholdReached always calls processTC (threshold check is in
// the caller, timeoutHandler). The dedup prevents a TC carrying multiple
// signatures from the same signer: such a TC is cryptographically invalid
// (verifyTC via RecoverUniqueSigners would reject it) and must not be built.
//
// Before fix (origin/main): both signatures appended → highestTimeoutCert
// has len(Signatures)=2 → FAIL.
// After fix: dedup by signer → 1 signature in TC → len(Signatures)=1 → PASS.
func TestPoolSignerDedup_Timeout(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.isInitialized = true

	keys, addrs := genNKeys(t, 3)

	const round = types.Round(10)
	const gapNumber = uint64(450)

	// Sign the timeout with keys[0], then malleate for a byte-distinct signature
	// recovering to the same signer.
	h := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: gapNumber})
	sig1, err := crypto.Sign(h[:], keys[0])
	if err != nil {
		t.Fatal(err)
	}
	sig2 := malleateSig(t, types.Signature(sig1)) // different bytes, same recovered signer
	_ = addrs                                     // used to set signer fields below

	t1 := &types.Timeout{Round: round, Signature: types.Signature(sig1), GapNumber: gapNumber}
	t1.SetSigner(addrs[0])
	t2 := &types.Timeout{Round: round, Signature: sig2, GapNumber: gapNumber}
	t2.SetSigner(addrs[0])

	// Timeout.Hash() = rlpHash(Round, Signature, GapNumber); different Signature
	// bytes → different hash → two distinct pool entries despite being same signer.
	pooledTimeouts := map[common.Hash]utils.PoolObj{
		t1.Hash(): t1,
		t2.Hash(): t2,
	}

	if err := eng.onTimeoutPoolThresholdReached(&mockChainForA894{}, pooledTimeouts, t1, gapNumber); err != nil {
		t.Fatalf("unexpected error from onTimeoutPoolThresholdReached: %v", err)
	}

	// processTC stores the TC in highestTimeoutCert (when TC.Round=10 > 0).
	// The number of signatures in that TC is the observable signal.
	gotSigs := len(eng.highestTimeoutCert.Signatures)
	if gotSigs != 1 {
		t.Errorf("FAIL (missing signer dedup, F-2026-19525): TC built with %d signature(s), "+
			"want 1 (1 distinct signer); pool had 2 entries from the same signer", gotSigs)
	}
}
