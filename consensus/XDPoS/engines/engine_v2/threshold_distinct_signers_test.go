// Copyright (c) 2026 XDC Network
// Tests for F-2026-19525 (pool gates): the vote and timeout threshold gates must count distinct
// masternode signers, not pool entries. The pool keys entries by message hash,
// which includes the signature bytes, so one key can hold many entries.

package engine_v2

import (
	"crypto/ecdsa"
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// chainGate adds a current header and a GetHeaderByHash counter to
// mockChainForA894. The vote gate's first action is GetHeaderByHash, and the
// epoch info is pre-seeded, so a non-zero count means the gate fired.
type chainGate struct {
	mockChainForA894
	head    *types.Header
	gap     *types.Header
	lookups int
}

// GetHeaderByNumber serves the gap-0 header that VerifyVoteMessage resolves
// through getSnapshot before checking the signer.
func (c *chainGate) GetHeaderByNumber(n uint64) *types.Header {
	if n == 0 {
		return c.gap
	}
	return nil
}

func (c *chainGate) CurrentHeader() *types.Header { return c.head }
func (c *chainGate) GetHeaderByHash(h common.Hash) *types.Header {
	c.lookups++
	return c.mockChainForA894.GetHeaderByHash(h)
}

// signRandomK signs hash with a random nonce, so each call returns a
// different, valid, low-S signature by the same key.
func signRandomK(t *testing.T, key *ecdsa.PrivateKey, hash common.Hash) types.Signature {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if n := crypto.S256().Params().N; s.Cmp(new(big.Int).Rsh(n, 1)) > 0 {
		s.Sub(n, s)
	}
	sig := make([]byte, 65)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:64])
	for v := byte(0); v < 2; v++ {
		sig[64] = v
		if pub, err := crypto.SigToPub(hash[:], sig); err == nil && crypto.PubkeyToAddress(*pub) == crypto.PubkeyToAddress(key.PublicKey) {
			return sig
		}
	}
	t.Fatal("no recovery id matches")
	return nil
}

// setupGate returns an engine in round 5 with 3 masternodes (threshold 2),
// a proposed block for round 5 and a chain that serves it.
func setupGate(t *testing.T) (*XDPoS_v2, *chainGate, []*ecdsa.PrivateKey, []common.Address, *types.BlockInfo) {
	eng, _ := newA894Engine(t)
	eng.BroadcastCh = make(chan interface{}, 64)
	eng.ForensicsProcessor = nil
	eng.isInitialized = true
	eng.currentRound = 5

	keys, addrs := genNKeys(t, 3)

	extra, err := (&types.ExtraFields_v2{Round: 5, QuorumCert: &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: common.Hash{0xbe}, Round: 4, Number: big.NewInt(1)},
		Signatures:        []types.Signature{},
	}}).EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(2), ParentHash: common.Hash{0xbe}, Extra: extra, Difficulty: big.NewInt(1)}
	info := &types.BlockInfo{Round: 5, Number: big.NewInt(2), Hash: header.Hash()}
	eng.epochSwitches.Add(info.Hash, &types.EpochSwitchInfo{
		Masternodes:          addrs,
		MasternodesLen:       len(addrs),
		EpochSwitchBlockInfo: &types.BlockInfo{Round: 0, Number: big.NewInt(0)},
	})
	gap := &types.Header{Number: big.NewInt(0), Difficulty: big.NewInt(1)}
	eng.snapshots.Add(gap.Hash(), newSnapshot(0, gap.Hash(), addrs))
	chain := &chainGate{mockChainForA894: mockChainForA894{headers: map[common.Hash]*types.Header{info.Hash: header}}, head: header, gap: gap}
	return eng, chain, keys, addrs, info
}

// voteGate builds a vote and sets its signer the way VerifyVoteMessage does.
func voteGate(t *testing.T, eng *XDPoS_v2, key *ecdsa.PrivateKey, addrs []common.Address, info *types.BlockInfo) *types.Vote {
	h := types.VoteSigHash(&types.VoteForSign{ProposedBlockInfo: info, GapNumber: 0})
	v := &types.Vote{ProposedBlockInfo: info, Signature: signRandomK(t, key, h), GapNumber: 0}
	ok, signer, err := eng.verifyMsgSignature(h, v.Signature, addrs)
	if err != nil || !ok {
		t.Fatalf("vote does not verify: %v", err)
	}
	v.SetSigner(signer)
	return v
}

// timeoutGate builds a timeout and sets its signer the way VerifyTimeoutMessage does.
func timeoutGate(t *testing.T, eng *XDPoS_v2, key *ecdsa.PrivateKey, addrs []common.Address, round types.Round) *types.Timeout {
	h := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: 0})
	to := &types.Timeout{Round: round, Signature: signRandomK(t, key, h), GapNumber: 0}
	ok, signer, err := eng.verifyMsgSignature(h, to.Signature, addrs)
	if err != nil || !ok {
		t.Fatalf("timeout does not verify: %v", err)
	}
	to.SetSigner(signer)
	return to
}

// One signer sending 4 distinct valid votes (threshold is 2) must not open the gate.
func TestVoteGate_OneSignerManyEntries(t *testing.T) {
	eng, chain, keys, addrs, info := setupGate(t)
	for i := 0; i < 4; i++ {
		if err := eng.voteHandler(chain, voteGate(t, eng, keys[0], addrs, info)); err != nil {
			t.Fatalf("vote %d: %v", i, err)
		}
	}
	if chain.lookups != 0 {
		t.Fatalf("vote threshold gate fired (%d header lookups) for 1 distinct signer (4 pool entries, threshold 2)", chain.lookups)
	}
	if eng.currentRound != 5 {
		t.Fatalf("round advanced to %d on votes from one signer", eng.currentRound)
	}
}

// Two distinct signers reach the threshold: the gate fires exactly once, on
// the second signer, even after the first signer's extra entries.
func TestVoteGate_DistinctSignersReachThreshold(t *testing.T) {
	eng, chain, keys, addrs, info := setupGate(t)
	for i := 0; i < 2; i++ {
		if err := eng.voteHandler(chain, voteGate(t, eng, keys[0], addrs, info)); err != nil {
			t.Fatalf("vote A%d: %v", i, err)
		}
	}
	if chain.lookups != 0 || eng.currentRound != 5 {
		t.Fatalf("gate fired before a second signer: lookups=%d round=%d", chain.lookups, eng.currentRound)
	}
	if err := eng.voteHandler(chain, voteGate(t, eng, keys[1], addrs, info)); err != nil {
		t.Fatalf("vote B: %v", err)
	}
	if eng.currentRound != 6 {
		t.Fatalf("QC not formed with 2 distinct signers: round=%d", eng.currentRound)
	}
	if got := len(eng.highestQuorumCert.Signatures); got != 2 {
		t.Fatalf("QC has %d signatures, want 2", got)
	}
	// A late vote for the certified round does not re-enter the gate.
	if err := eng.voteHandler(chain, voteGate(t, eng, keys[2], addrs, info)); err == nil {
		t.Fatal("late vote for round 5 accepted after QC")
	}
}

// One signer sending 4 distinct valid timeouts (threshold is 2) must not build a TC.
func TestTimeoutGate_OneSignerManyEntries(t *testing.T) {
	eng, chain, keys, addrs, _ := setupGate(t)
	eng.epochSwitches.Add(chain.head.Hash(), &types.EpochSwitchInfo{
		Masternodes: addrs, MasternodesLen: len(addrs), EpochSwitchBlockInfo: &types.BlockInfo{Number: big.NewInt(0)},
	})
	for i := 0; i < 4; i++ {
		if err := eng.timeoutHandler(chain, timeoutGate(t, eng, keys[0], addrs, 5)); err != nil {
			t.Fatalf("timeout %d: %v (round now %d: a TC was built from one signer)", i, err, eng.currentRound)
		}
	}
	if eng.currentRound != 5 || eng.highestTimeoutCert.Round != 0 {
		t.Fatalf("TC built from 1 distinct signer: round=%d highestTC.round=%d sigs=%d",
			eng.currentRound, eng.highestTimeoutCert.Round, len(eng.highestTimeoutCert.Signatures))
	}
}

// Two distinct signers reach the threshold: exactly one TC, with 2 signatures.
func TestTimeoutGate_DistinctSignersReachThreshold(t *testing.T) {
	eng, chain, keys, addrs, _ := setupGate(t)
	eng.epochSwitches.Add(chain.head.Hash(), &types.EpochSwitchInfo{
		Masternodes: addrs, MasternodesLen: len(addrs), EpochSwitchBlockInfo: &types.BlockInfo{Number: big.NewInt(0), Hash: chain.head.Hash()},
	})
	for i := 0; i < 2; i++ {
		if err := eng.timeoutHandler(chain, timeoutGate(t, eng, keys[0], addrs, 5)); err != nil {
			t.Fatalf("timeout A%d: %v", i, err)
		}
	}
	if eng.currentRound != 5 {
		t.Fatalf("TC built before a second signer: round=%d", eng.currentRound)
	}
	if err := eng.timeoutHandler(chain, timeoutGate(t, eng, keys[1], addrs, 5)); err != nil {
		t.Fatalf("timeout B: %v", err)
	}
	if eng.currentRound != 6 || eng.highestTimeoutCert.Round != 5 {
		t.Fatalf("TC not formed with 2 distinct signers: round=%d highestTC.round=%d", eng.currentRound, eng.highestTimeoutCert.Round)
	}
	if got := len(eng.highestTimeoutCert.Signatures); got != 2 {
		t.Fatalf("TC has %d signatures, want 2", got)
	}
	// A late timeout for the timed-out round does not build a second TC.
	if err := eng.timeoutHandler(chain, timeoutGate(t, eng, keys[2], addrs, 5)); err == nil {
		t.Fatal("late timeout for round 5 accepted after TC")
	}
}
