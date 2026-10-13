// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package engine_v2

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// tcChain serves a head header and the gap header at block 450.
type tcChain struct {
	mockChainForA894
	head, gap *types.Header
}

func (c *tcChain) CurrentHeader() *types.Header { return c.head }
func (c *tcChain) GetHeaderByNumber(n uint64) *types.Header {
	if n == c.gap.Number.Uint64() {
		return c.gap
	}
	return nil
}

// tcFixture: the head epoch (switch round epochRound) has masternodes active.
// The gap snapshot also lists stale (in NextEpochCandidates, not an epoch
// masternode) and penalized (only in Penalties).
type tcFixture struct {
	x                       *XDPoS_v2
	chain                   *tcChain
	active                  []*ecdsa.PrivateKey
	stale, penalized, other *ecdsa.PrivateKey
}

func newTCFixture(t *testing.T, epochRound types.Round) *tcFixture {
	t.Helper()
	x, _ := newA894Engine(t)
	x.isInitialized = true
	keys, addrs := genNKeys(t, 6)
	f := &tcFixture{x: x, active: keys[:3], stale: keys[3], penalized: keys[4], other: keys[5]}

	gap := &types.Header{Number: big.NewInt(450)}
	head := &types.Header{Number: big.NewInt(1000), Extra: []byte{1}}
	f.chain = &tcChain{head: head, gap: gap}

	switchInfo := &types.BlockInfo{Hash: common.Hash{0xee}, Round: epochRound, Number: big.NewInt(901)}
	info := &types.EpochSwitchInfo{
		Masternodes:          addrs[:3],
		MasternodesLen:       3,
		EpochSwitchBlockInfo: switchInfo,
	}
	x.epochSwitches.Add(head.Hash(), info)
	x.epochSwitches.Add(switchInfo.Hash, info)

	snap := &SnapshotV2{
		Number:              450,
		Hash:                gap.Hash(),
		NextEpochCandidates: []common.Address{addrs[0], addrs[1], addrs[2], addrs[3]},
		Penalties:           []common.Address{addrs[4]},
	}
	x.snapshots.Add(gap.Hash(), snap)
	return f
}

func signTimeout(t *testing.T, round types.Round, keys ...*ecdsa.PrivateKey) []types.Signature {
	t.Helper()
	h := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: 450})
	sigs := make([]types.Signature, len(keys))
	for i, k := range keys {
		sig, err := crypto.Sign(h[:], k)
		if err != nil {
			t.Fatal(err)
		}
		sigs[i] = sig
	}
	return sigs
}

// malleate returns the (r, N-s) twin of sig: different bytes, same signer.
func malleate(sig types.Signature) types.Signature {
	out := append(types.Signature{}, sig...)
	s := new(big.Int).SetBytes(sig[32:64])
	new(big.Int).Sub(crypto.S256().Params().N, s).FillBytes(out[32:64])
	out[64] ^= 1
	return out
}

// TestVerifyTCSignerSet pins canonical XDPoSChain verifyTC: signers are checked
// against the TC round's epoch masternodes, not the gap snapshot's candidate
// pool, and an unresolvable TC epoch is an error (no head-epoch fallback).
func TestVerifyTCSignerSet(t *testing.T) {
	const round = types.Round(5)
	f := newTCFixture(t, 0)
	for _, tc := range []struct {
		name string
		keys []*ecdsa.PrivateKey
		ok   bool
	}{
		{"active masternodes", f.active[:2], true},
		{"penalized-only signer", []*ecdsa.PrivateKey{f.active[0], f.penalized}, false},
		{"stale candidate not in the epoch", []*ecdsa.PrivateKey{f.active[0], f.stale}, false},
		// Byte-distinct (malleated) signatures from one key count once.
		{"same signer twice", []*ecdsa.PrivateKey{f.active[0], f.active[0]}, false},
	} {
		sigs := signTimeout(t, round, tc.keys...)
		if tc.name == "same signer twice" {
			sigs[1] = malleate(sigs[1])
		}
		err := f.x.verifyTC(f.chain, &types.TimeoutCert{Round: round, GapNumber: 450, Signatures: sigs})
		if (err == nil) != tc.ok {
			t.Errorf("%s: verifyTC err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}

	// Head epoch starts at round 900; a round-5 TC belongs to an epoch this
	// node cannot resolve. Canonical rejects; it must not fall back to the
	// head epoch.
	old := newTCFixture(t, 900)
	sigs := signTimeout(t, round, old.active[:2]...)
	if err := old.x.verifyTC(old.chain, &types.TimeoutCert{Round: round, GapNumber: 450, Signatures: sigs}); err == nil {
		t.Errorf("old-epoch TC: verifyTC accepted it via the head-epoch fallback")
	}
}

// TestVerifyTimeoutMessagePenalizedSigner: a single timeout message is checked
// against NextEpochCandidates only (canonical engine.go VerifyTimeoutMessage).
func TestVerifyTimeoutMessagePenalizedSigner(t *testing.T) {
	const round = types.Round(5)
	f := newTCFixture(t, 0)
	for _, tc := range []struct {
		name string
		key  *ecdsa.PrivateKey
		want bool
	}{
		{"candidate", f.active[0], true},
		{"penalized-only signer", f.penalized, false},
		{"unknown signer", f.other, false},
	} {
		msg := &types.Timeout{Round: round, GapNumber: 450, Signature: signTimeout(t, round, tc.key)[0]}
		got, _ := f.x.VerifyTimeoutMessage(f.chain, msg)
		if got != tc.want {
			t.Errorf("%s: VerifyTimeoutMessage = %v, want %v", tc.name, got, tc.want)
		}
	}
}
