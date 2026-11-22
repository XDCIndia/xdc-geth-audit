// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestF202620608HighSVoteDoesNotAdvanceRound checks that a high-S vote is
// refused before it is pooled and that a certificate built from one does not
// move the local round. Peers reject the same signature in recoverUniqueSigners.
func TestF202620608HighSVoteDoesNotAdvanceRound(t *testing.T) {
	eng, _ := newA894Engine(t)
	keys, addrs := genNKeys(t, 1)
	header := buildV2Header(t, 1, 5, 0, common.Hash{0x01}, nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{1: header}}

	info := &types.BlockInfo{Hash: header.Hash(), Round: 5, Number: big.NewInt(1)}
	sig := makeQCSignatures(t, info, 0, keys)[0]
	high := malleate(sig)

	ok, _, err := eng.verifyMsgSignature(types.VoteSigHash(&types.VoteForSign{
		ProposedBlockInfo: info,
		GapNumber:         0,
	}), high, addrs)
	if err == nil || ok {
		t.Fatalf("high-S vote signature accepted: ok=%v err=%v", ok, err)
	}

	eng.epochSwitches.Add(header.Hash(), &types.EpochSwitchInfo{
		Masternodes:    addrs,
		MasternodesLen: 1,
		EpochSwitchBlockInfo: &types.BlockInfo{
			Hash:   header.Hash(),
			Round:  0,
			Number: big.NewInt(1),
		},
	})
	eng.currentRound = 5
	vote := &types.Vote{
		ProposedBlockInfo: info,
		Signature:         high,
		GapNumber:         0,
	}
	vote.SetSigner(addrs[0])

	if err := eng.onVotePoolThresholdReached(chain, map[common.Hash]utils.PoolObj{
		vote.Hash(): vote,
	}, vote, header); err != nil {
		t.Fatalf("onVotePoolThresholdReached: %v", err)
	}
	if eng.currentRound != 5 {
		t.Fatalf("high-S vote advanced round from 5 to %d", eng.currentRound)
	}
	if _, _, err := recoverUniqueSigners(types.VoteSigHash(&types.VoteForSign{
		ProposedBlockInfo: info,
		GapNumber:         0,
	}), []types.Signature{high}, addrs); err == nil {
		t.Fatal("recoverUniqueSigners accepted the high-S signature peers must reject")
	}
}

// malleate is copied from tc_signer_set_test.go, which is not part of the audit tree.
func malleate(sig types.Signature) types.Signature {
	out := append(types.Signature{}, sig...)
	s := new(big.Int).SetBytes(sig[32:64])
	new(big.Int).Sub(crypto.S256().Params().N, s).FillBytes(out[32:64])
	out[64] ^= 1
	return out
}
