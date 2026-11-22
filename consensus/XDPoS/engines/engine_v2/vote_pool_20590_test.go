// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// voteCapChain serves one head and no other headers, so a vote hash is unknown
// unless it is the head hash.
type voteCapChain struct {
	mockChain1330
	head *types.Header
}

func (c *voteCapChain) CurrentHeader() *types.Header { return c.head }
func (c *voteCapChain) GetHeaderByHash(h common.Hash) *types.Header {
	if c.head != nil && c.head.Hash() == h {
		return c.head
	}
	return nil
}

// TestF202620590VoteGapAndUnstoredCap checks that a vote whose gap is neither
// the head epoch nor the next epoch is not stored, and that one signer cannot
// keep more unknown-block votes than the cap.
func TestF202620590VoteGapAndUnstoredCap(t *testing.T) {
	eng, _ := newA894Engine(t)
	head := buildV2Header(t, 100, 1, 0, common.Hash{0x01}, nil)
	eng.epochSwitches.Add(head.Hash(), &types.EpochSwitchInfo{
		Masternodes:    []common.Address{{0xaa}},
		MasternodesLen: 1,
		EpochSwitchBlockInfo: &types.BlockInfo{
			Hash:   head.Hash(),
			Round:  0,
			Number: big.NewInt(901),
		},
	})
	chain := &voteCapChain{head: head}
	eng.currentRound = 10
	eng.isInitialized = true

	signer := common.Address{0x11}
	bad := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{Hash: common.Hash{0xee}, Round: 10, Number: big.NewInt(100)},
		GapNumber:         9999,
	}
	bad.SetSigner(signer)
	if err := eng.voteHandler(chain, bad); err == nil {
		t.Fatal("vote with a gap outside the head epoch was accepted")
	}
	if eng.votePool.Size() != 0 {
		t.Fatalf("rejected gap vote was stored: size %d", eng.votePool.Size())
	}

	for i := 0; i < maxUnstoredVotesPerSigner; i++ {
		var h common.Hash
		h[0] = byte(i + 1)
		v := &types.Vote{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   h,
				Round:  types.Round(10 + i),
				Number: big.NewInt(int64(100 + i)),
			},
			GapNumber: 450,
		}
		v.SetSigner(signer)
		eng.votePool.Add(v)
	}
	if got := eng.votePool.Size(); got != maxUnstoredVotesPerSigner {
		t.Fatalf("preloaded unknown votes = %d, want %d", got, maxUnstoredVotesPerSigner)
	}
	extra := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{Hash: common.Hash{0xff}, Round: 10, Number: big.NewInt(200)},
		GapNumber:         450,
	}
	extra.SetSigner(signer)
	if err := eng.voteHandler(chain, extra); err == nil {
		t.Fatal("vote beyond the unknown-hash cap was accepted")
	}
}
