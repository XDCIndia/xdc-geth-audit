package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// gapLookupChain counts the header lookups by number. getSnapshot starts with
// one for the gap number, so the count shows how often a snapshot was looked up
// or built.
type gapLookupChain struct {
	voteCapChain
	byNumberCalls int
}

func (c *gapLookupChain) GetHeaderByNumber(n uint64) *types.Header {
	c.byNumberCalls++
	return nil
}

func newGapIntakeFixture(t *testing.T) (*XDPoS_v2, *gapLookupChain) {
	t.Helper()
	eng, _ := newA894Engine(t)
	head := buildV2Header(t, 100, 1, 0, common.Hash{0x01}, nil)
	eng.epochSwitches.Add(head.Hash(), &types.EpochSwitchInfo{
		Masternodes:    []common.Address{{0xaa}},
		MasternodesLen: 1,
		EpochSwitchBlockInfo: &types.BlockInfo{
			Hash:   head.Hash(),
			Round:  0,
			Number: big.NewInt(901), // its gap is 450, the next epoch's is 1350
		},
	})
	eng.currentRound = 10
	eng.isInitialized = true
	return eng, &gapLookupChain{voteCapChain: voteCapChain{head: head}}
}

// A vote or timeout with a gap number that is not the gap of the head epoch or
// the next one is rejected before any snapshot lookup, so a peer cannot make the
// node build and store a snapshot per number (#2062).
func TestIntakeRejectsBadGapBeforeSnapshotLookup(t *testing.T) {
	eng, chain := newGapIntakeFixture(t)

	for gap := uint64(1); gap <= 300; gap++ {
		vote := &types.Vote{
			ProposedBlockInfo: &types.BlockInfo{Hash: common.Hash{0xee}, Round: 10, Number: big.NewInt(100)},
			GapNumber:         gap,
			Signature:         make([]byte, 65),
		}
		if ok, err := eng.VerifyVoteMessage(chain, vote); ok || err == nil {
			t.Fatalf("vote with gap %d: verified=%v err=%v, want a rejection", gap, ok, err)
		}
		tm := &types.Timeout{Round: 10, GapNumber: gap, Signature: make([]byte, 65)}
		if ok, err := eng.VerifyTimeoutMessage(chain, tm); ok || err == nil {
			t.Fatalf("timeout with gap %d: verified=%v err=%v, want a rejection", gap, ok, err)
		}
	}
	if chain.byNumberCalls != 0 {
		t.Fatalf("%d header lookups by number: a snapshot was looked up for a bad gap", chain.byNumberCalls)
	}
	if eng.snapshots.Len() != 0 {
		t.Fatalf("%d snapshots cached for rejected messages", eng.snapshots.Len())
	}

	// The gap of the head epoch is still looked up, so the check is not too wide.
	vote := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{Hash: common.Hash{0xee}, Round: 10, Number: big.NewInt(100)},
		GapNumber:         450,
		Signature:         make([]byte, 65),
	}
	_, _ = eng.VerifyVoteMessage(chain, vote)
	tm := &types.Timeout{Round: 10, GapNumber: 450, Signature: make([]byte, 65)}
	_, _ = eng.VerifyTimeoutMessage(chain, tm)
	if chain.byNumberCalls < 2 {
		t.Fatalf("gap 450 did not reach the snapshot lookup (%d lookups)", chain.byNumberCalls)
	}
}
