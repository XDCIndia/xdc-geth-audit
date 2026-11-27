// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestF202619609LockFollowsTheCertifiedBlock delivers a certificate for block
// B and checks the vote that follows. The lock must name B. A same-height
// sibling of B is refused. The next block a current-main proposer builds, the
// child of B carrying B's certificate, is still voted for.
func TestF202619609LockFollowsTheCertifiedBlock(t *testing.T) {
	// Build with no forensics processor. Setting the field after New races
	// with watchForensics, which reads it from another goroutine.
	eng, _ := newA894Engine(t)
	eng.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Round: 1, Number: big.NewInt(1)}}
	eng.currentRound = 12
	eng.highestVotedRound = 0
	eng.lockQuorumCert = nil

	b10 := buildV2Header(t, 10, 10, 9, common.Hash{0x09}, nil)
	b11 := buildV2Header(t, 11, 11, 10, b10.Hash(), nil)
	b12 := buildV2Header(t, 12, 12, 11, b11.Hash(), nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{
		10: b10,
		11: b11,
		12: b12,
	}}
	certified := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{
		Hash: b12.Hash(), Round: 12, Number: big.NewInt(12),
	}}
	if err := eng.processQC(chain, certified); err != nil {
		t.Fatalf("processQC: %v", err)
	}
	if eng.lockQuorumCert == nil || eng.lockQuorumCert.ProposedBlockInfo == nil ||
		eng.lockQuorumCert.ProposedBlockInfo.Hash != b12.Hash() ||
		eng.lockQuorumCert.ProposedBlockInfo.Round != 12 {
		t.Fatalf("lock = %+v, want certificate for block 12", eng.lockQuorumCert)
	}

	// Sibling of B at the next round. Its certificate names B's parent, which
	// is what the old lock stored, so the old rule voted for this sibling.
	sibling := buildV2Header(t, 12, 13, 11, b11.Hash(), nil)
	siblingQC := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{
		Hash: b11.Hash(), Round: 11, Number: big.NewInt(11),
	}}
	ok, err := eng.verifyVotingRule(chain, &types.BlockInfo{
		Hash: sibling.Hash(), Round: 13, Number: big.NewInt(12),
	}, siblingQC)
	if err != nil || ok {
		t.Fatalf("sibling of the certified block: vote=%v err=%v, want false", ok, err)
	}

	// The block a current-main node proposes next: it extends B and carries
	// the certificate for B. Both lock rules vote for it.
	child := buildV2Header(t, 13, 13, 12, b12.Hash(), nil)
	chain.byNumber[13] = child
	ok, err = eng.verifyVotingRule(chain, &types.BlockInfo{
		Hash: child.Hash(), Round: 13, Number: big.NewInt(13),
	}, certified)
	if err != nil || !ok {
		t.Fatalf("child of the certified block: vote=%v err=%v, want true", ok, err)
	}

	// An older certificate must not lower the lock.
	older := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{
		Hash: b11.Hash(), Round: 11, Number: big.NewInt(11),
	}}
	if err := eng.processQC(chain, older); err != nil && eng.lockQuorumCert.ProposedBlockInfo.Round != 12 {
		t.Fatalf("older certificate: err=%v lock round=%d", err, eng.lockQuorumCert.ProposedBlockInfo.Round)
	}
	if eng.lockQuorumCert.ProposedBlockInfo.Round != 12 || eng.lockQuorumCert.ProposedBlockInfo.Hash != b12.Hash() {
		t.Fatalf("lock moved off block 12: %+v", eng.lockQuorumCert.ProposedBlockInfo)
	}
}

// lockRestartFixture returns a chain 10..12 whose head B (12) carries only the
// certificate for its parent, plus the signed certificate for B that the node
// stored as its lock before the restart. The epoch cache knows B's committee.
func lockRestartFixture(t *testing.T, eng *XDPoS_v2, signers int) (*mockChain1330, *types.Header, *types.QuorumCert) {
	t.Helper()
	keys, addrs := genNKeys(t, 4)
	b8 := buildV2Header(t, 8, 8, 7, common.Hash{0x07}, nil)
	b9 := buildV2Header(t, 9, 9, 8, b8.Hash(), nil)
	b10 := buildV2Header(t, 10, 10, 9, b9.Hash(), nil)
	b11 := buildV2Header(t, 11, 11, 10, b10.Hash(), nil)
	b12 := buildV2Header(t, 12, 12, 11, b11.Hash(), nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{8: b8, 9: b9, 10: b10, 11: b11, 12: b12}}
	eng.epochSwitches.Add(b12.Hash(), &types.EpochSwitchInfo{
		Masternodes:          addrs,
		MasternodesLen:       len(addrs),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: b10.Hash(), Round: 0, Number: big.NewInt(0)},
	})
	info := &types.BlockInfo{Hash: b12.Hash(), Round: 12, Number: big.NewInt(12)}
	lock := &types.QuorumCert{
		ProposedBlockInfo: info,
		Signatures:        makeQCSignatures(t, info, 0, keys[:signers]),
	}
	return chain, b12, lock
}

func restartEngine(eng *XDPoS_v2) {
	eng.isInitialized = false
	eng.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{}}
	eng.currentRound = 0
	eng.highestVotedRound = 0
	eng.lockQuorumCert = nil
}

// TestF202619609RestartRaisesHighestQCToStoredLock restarts a node that held
// the certificate for its head B. The head header carries only the
// certificate for B's parent. The stored lock must raise the highest
// certificate to B, so the node builds on B and votes for a child of B.
func TestF202619609RestartRaisesHighestQCToStoredLock(t *testing.T) {
	eng, db := newA894Engine(t)
	chain, head, lock := lockRestartFixture(t, eng, 4)
	rawdb.WriteBFTLockQC(db, lock)
	restartEngine(eng)

	eng.lock.Lock()
	err := eng.initial(chain, head)
	eng.lock.Unlock()
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	if got := eng.highestQuorumCert.ProposedBlockInfo; got.Hash != head.Hash() || got.Round != 12 {
		t.Fatalf("highest QC = %+v, want the certificate for block 12", got)
	}
	if eng.currentRound != 13 {
		t.Fatalf("currentRound = %d, want 13", eng.currentRound)
	}

	child := buildV2Header(t, 13, 13, 12, head.Hash(), nil)
	chain.byNumber[13] = child
	ok, err := eng.verifyVotingRule(chain, &types.BlockInfo{
		Hash: child.Hash(), Round: 13, Number: big.NewInt(13),
	}, lock)
	if err != nil || !ok {
		t.Fatalf("child of the head: vote=%v err=%v, want true", ok, err)
	}
}

// TestXDCHunt54RestartIgnoresUnverifiedLock keeps the highest certificate at
// the header certificate when the stored lock does not carry a quorum.
func TestXDCHunt54RestartIgnoresUnverifiedLock(t *testing.T) {
	eng, db := newA894Engine(t)
	chain, head, lock := lockRestartFixture(t, eng, 1)
	rawdb.WriteBFTLockQC(db, lock)
	restartEngine(eng)

	eng.lock.Lock()
	err := eng.initial(chain, head)
	eng.lock.Unlock()
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	if got := eng.highestQuorumCert.ProposedBlockInfo.Round; got != 11 {
		t.Fatalf("highest QC round = %d, want 11 (lock has 1 of 4 signatures)", got)
	}
}

// TestXDCHunt37ReinitBFTKeepsHigherQC runs ReinitBFT on a node that holds the
// certificate for its head. The highest certificate must not fall back to the
// parent certificate in the head header.
func TestXDCHunt37ReinitBFTKeepsHigherQC(t *testing.T) {
	eng, _ := newA894Engine(t)
	chain, head, held := lockRestartFixture(t, eng, 4)
	restartEngine(eng)
	eng.highestQuorumCert = held

	if err := eng.ReinitBFT(chain, head); err != nil {
		t.Fatalf("ReinitBFT: %v", err)
	}
	if got := eng.highestQuorumCert.ProposedBlockInfo.Round; got != 12 {
		t.Fatalf("highest QC round = %d, want 12", got)
	}
}
