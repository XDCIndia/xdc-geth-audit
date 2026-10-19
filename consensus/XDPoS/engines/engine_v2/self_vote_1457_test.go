// Copyright (c) 2026 XDC Network
// Fix #1457: a seated masternode must not drop its own V2 vote.
//
// net2002 (Epoch 900, Gap 450, V2 switch 2700): geth was seated in the 5-node
// epoch that switched at block 4499 (gap 3150). getSnapshot(3150, forSigning)
// floors the GAP number to a checkpoint (3150 -> 2700 == V2 switch), so it
// evicts the import-time gap-3150 snapshot (5 candidates) and rebuilds it from
// block 2700's V1 Extra: the 4 legacy signers. sendVote then checked its own
// vote against that list and dropped it ("Signer not in masternode list
// masternodes=4"), while the threshold used the epoch's 5 masternodes.

package engine_v2

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func Test1457_SeatedNodeVotes(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.config.V2.SwitchBlock = big.NewInt(2700)
	eng.BroadcastCh = make(chan interface{}, 1)

	newKey := func() (*ecdsa.PrivateKey, common.Address) {
		k, err := crypto.GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		return k, crypto.PubkeyToAddress(k.PublicKey)
	}
	selfKey, self := newKey()
	eng.Authorize(self, func(_ accounts.Account, data []byte) ([]byte, error) {
		return crypto.Sign(data, selfKey)
	})
	legacy := []common.Address{{0x01}, {0x02}, {0x03}, {0x04}}
	seated := append(append([]common.Address{}, legacy...), self)

	// Block 2700: V2 switch block, V1 Extra = vanity + 4 legacy signers + seal.
	extra := make([]byte, 32, 32+len(legacy)*common.AddressLength+65)
	for _, a := range legacy {
		extra = append(extra, a.Bytes()...)
	}
	extra = append(extra, make([]byte, 65)...)
	switchHeader := &types.Header{Number: big.NewInt(2700), Difficulty: big.NewInt(1), Extra: extra}

	// Gap 3150: import-time snapshot holds the contract pool, incl. this node.
	gapHeader := &types.Header{Number: big.NewInt(3150), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	eng.snapshots.Add(gapHeader.Hash(), newSnapshot(3150, gapHeader.Hash(), seated))
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{2700: switchHeader, 3150: gapHeader}}

	// Voted block 5104 lives in the epoch switched at 4499 (gap 3150), 5 seated.
	const round = types.Round(2406)
	blockHash := common.HexToHash("0x1457")
	eng.epochSwitches.Add(blockHash, &types.EpochSwitchInfo{
		Masternodes:          seated,
		MasternodesLen:       len(seated),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: common.HexToHash("0x4499"), Round: 2100, Number: big.NewInt(4499)},
	})
	blockInfo := &types.BlockInfo{Hash: blockHash, Round: round, Number: big.NewInt(5104)}
	eng.currentRound = round
	eng.isInitialized = true

	if err := eng.sendVote(chain, blockInfo); err != nil {
		t.Fatalf("FAIL #1457: seated node dropped its own vote: %v", err)
	}
	vote := (<-eng.BroadcastCh).(*types.Vote)
	if vote.GapNumber != 3150 || vote.GetSigner() != self {
		t.Fatalf("broadcast vote: gap %d signer %s, want 3150 %s", vote.GapNumber, vote.GetSigner().Hex(), self.Hex())
	}
	if n := len(eng.votePool.GetByPoolKey(vote.PoolKey())); n != 1 {
		t.Fatalf("own vote not in pool: %d votes", n)
	}
	if eng.highestVotedRound != round {
		t.Fatalf("highestVotedRound %d, want %d", eng.highestVotedRound, round)
	}

	// A vote from a node outside the masternode set is still rejected.
	outKey, _ := newKey()
	sig, err := crypto.Sign(types.VoteSigHash(&types.VoteForSign{ProposedBlockInfo: blockInfo, GapNumber: 3150}).Bytes(), outKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := eng.VoteHandler(chain, &types.Vote{ProposedBlockInfo: blockInfo, Signature: sig, GapNumber: 3150}); err == nil {
		t.Fatal("vote from a non-masternode was accepted")
	}
	if n := len(eng.votePool.GetByPoolKey(vote.PoolKey())); n != 1 {
		t.Fatalf("non-member vote reached the pool: %d votes", n)
	}
}
