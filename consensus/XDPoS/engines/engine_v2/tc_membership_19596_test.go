// Copyright (c) 2026 XDC Network
// Tests for F-2026-19596 / F-2026-19612:
//   onTimeoutPoolThresholdReached must not include signatures from signers
//   that are not in the active masternode set of the round's epoch.

package engine_v2

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// epochInfoFor builds a minimal EpochSwitchInfo for testing; callers pass the
// active (non-penalized) masternode list.
func epochInfoFor(activeMNs []common.Address) *types.EpochSwitchInfo {
	return &types.EpochSwitchInfo{
		Masternodes:          activeMNs,
		MasternodesLen:       len(activeMNs),
		EpochSwitchBlockInfo: &types.BlockInfo{Round: 0, Number: big.NewInt(0)},
	}
}

// makeSignedTimeout signs a timeout message with the given key and stamps the
// recovered signer, as VerifyTimeoutMessage does before pool admission.
func makeSignedTimeout(t *testing.T, key *ecdsa.PrivateKey, round types.Round, gapNum uint64) *types.Timeout {
	t.Helper()
	h := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: gapNum})
	sig, err := crypto.Sign(h[:], key)
	if err != nil {
		t.Fatal(err)
	}
	to := &types.Timeout{Round: round, Signature: sig, GapNumber: gapNum}
	to.SetSigner(crypto.PubkeyToAddress(key.PublicKey))
	return to
}

// newEngineForTC returns a minimal engine for calling onTimeoutPoolThresholdReached.
func newEngineForTC(t *testing.T, round types.Round) (*XDPoS_v2, *chain1552) {
	t.Helper()
	eng, _ := newA894Engine(t)
	eng.BroadcastCh = make(chan interface{}, 64)
	eng.ForensicsProcessor = nil
	eng.isInitialized = true
	eng.currentRound = round
	// A minimal chain reader; onTimeoutPoolThresholdReached uses it only for
	// processTC → setNewRound, which just writes fields, not chain reads.
	chain := &chain1552{mockChainForA894: mockChainForA894{}, head: &types.Header{Number: big.NewInt(1)}}
	return eng, chain
}

// TestTCBuildExcludesPenalizedSigner_19596 verifies that when the timeout pool
// contains both active and non-active (penalized/non-member) signers,
// onTimeoutPoolThresholdReached builds a TC containing only active signers.
//
// Setup: 4 active masternodes (threshold 3), 1 penalized signer in pool.
// Pool: 3 active + 1 penalized.
// Expected: TC built (3 active >= threshold 3), TC.Signatures has exactly 3 entries.
func TestTCBuildExcludesPenalizedSigner_19596(t *testing.T) {
	const round = types.Round(5)
	const gapNum = uint64(0)

	activeKeys, activeAddrs := genNKeys(t, 4)
	penalizedKeys, _ := genNKeys(t, 1)
	penalizedAddr := crypto.PubkeyToAddress(penalizedKeys[0].PublicKey)

	eng, chain := newEngineForTC(t, round)
	epochInfo := epochInfoFor(activeAddrs)

	// Pool: 3 active signers (indices 0-2) + 1 penalized signer.
	pooled := make(map[common.Hash]utils.PoolObj)
	for i := 0; i < 3; i++ {
		to := makeSignedTimeout(t, activeKeys[i], round, gapNum)
		pooled[crypto.Keccak256Hash(to.Signature)] = to
	}
	penTo := makeSignedTimeout(t, penalizedKeys[0], round, gapNum)
	pooled[crypto.Keccak256Hash(penTo.Signature)] = penTo

	// currentTimeoutMsg is from the 3rd active signer (the one that pushed us over).
	currentMsg := makeSignedTimeout(t, activeKeys[2], round, gapNum)

	if err := eng.onTimeoutPoolThresholdReached(chain, pooled, currentMsg, gapNum, epochInfo); err != nil {
		t.Fatalf("onTimeoutPoolThresholdReached: %v", err)
	}

	tc := eng.highestTimeoutCert
	if tc == nil || tc.Round != round {
		t.Fatalf("expected TC for round %d, got %v", round, tc)
	}

	// TC must have exactly 3 signatures — the 3 active masternodes only.
	if got := len(tc.Signatures); got != 3 {
		t.Errorf("TC has %d signatures, want 3 (penalized signer must be excluded, F-2026-19596)", got)
	}

	// Confirm the penalized address signature is not present.
	penSigStr := string(penTo.Signature)
	for _, sig := range tc.Signatures {
		if string(sig) == penSigStr {
			t.Errorf("TC contains signature from penalized addr %v (F-2026-19596 regression)", penalizedAddr)
		}
	}
}

// TestTCBuildThresholdNotMetAfterFilter_19596 verifies that when the raw pool
// appears to satisfy the threshold by count but fewer than threshold entries
// belong to active masternodes, no TC is built.
//
// Setup: 4 active masternodes (threshold 3), pool has 2 active + 2 penalized.
// Expected: no TC built (2 active < threshold 3).
func TestTCBuildThresholdNotMetAfterFilter_19596(t *testing.T) {
	const round = types.Round(5)
	const gapNum = uint64(0)

	activeKeys, activeAddrs := genNKeys(t, 4)
	penalizedKeys, _ := genNKeys(t, 2)

	eng, chain := newEngineForTC(t, round)
	epochInfo := epochInfoFor(activeAddrs)

	// Pool: 2 active + 2 penalized = 4 raw entries, but only 2 active (< threshold 3).
	pooled := make(map[common.Hash]utils.PoolObj)
	for i := 0; i < 2; i++ {
		to := makeSignedTimeout(t, activeKeys[i], round, gapNum)
		pooled[crypto.Keccak256Hash(to.Signature)] = to
	}
	for i := 0; i < 2; i++ {
		to := makeSignedTimeout(t, penalizedKeys[i], round, gapNum)
		pooled[crypto.Keccak256Hash(to.Signature)] = to
	}

	currentMsg := makeSignedTimeout(t, activeKeys[1], round, gapNum)
	prevTC := eng.highestTimeoutCert

	if err := eng.onTimeoutPoolThresholdReached(chain, pooled, currentMsg, gapNum, epochInfo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// highestTimeoutCert must not have advanced — no TC should have been built.
	if eng.highestTimeoutCert != prevTC {
		t.Errorf("TC was built with only 2 active signers; threshold is 3 (F-2026-19596 regression)")
	}
}

// chain1552 adds a current header and a GetHeaderByHash counter to
// mockChainForA894.
type chain1552 struct {
	mockChainForA894
	head    *types.Header
	lookups int
}

func (c *chain1552) CurrentHeader() *types.Header { return c.head }
func (c *chain1552) GetHeaderByHash(h common.Hash) *types.Header {
	c.lookups++
	return c.mockChainForA894.GetHeaderByHash(h)
}
