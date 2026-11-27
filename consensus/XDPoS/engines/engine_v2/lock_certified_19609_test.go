// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
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
