package eth

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

func testVote(round types.Round) *types.Vote {
	return &types.Vote{ProposedBlockInfo: &types.BlockInfo{
		Hash:   common.Hash{byte(round)},
		Number: big.NewInt(1),
		Round:  round,
	}}
}

// When forwarded messages fill the main queue of a peer, this node's own
// message must still reach the peer, and before the forwarded ones (#2079).
func TestOwnBFTMessageSurvivesFullForwardQueue(t *testing.T) {
	stall, rw := makeBlockingPeer(enode.ID{21})
	defer rw.Close()
	defer stall.Close()
	defer stall.stopBFTSend()
	h := makeBFTTestHandler(stall)

	// The sender blocks on the first write; the next bftPeerSendQueueLen fill the
	// queue; the last ones are dropped.
	for i := 0; i < bftPeerSendQueueLen+4; i++ {
		h.BroadcastVote(testVote(1))
	}
	h.BroadcastOwn(testVote(99))

	// One forwarded message may already be in flight; the own vote must come
	// next, ahead of the queued forwarded ones.
	for i := 0; i < 2; i++ {
		done := make(chan types.Round, 1)
		go func() {
			msg, err := rw.ReadMsg()
			if err != nil {
				t.Error(err)
				done <- 0
				return
			}
			var v types.Vote
			if err := msg.Decode(&v); err != nil {
				t.Error(err)
			}
			done <- v.ProposedBlockInfo.Round
		}()
		select {
		case round := <-done:
			if round == 99 {
				return
			}
		case <-time.After(time.Second):
			t.Fatalf("message %d was not sent", i)
		}
	}
	t.Fatal("own vote was not among the first two messages sent")
}

// The sender must not start for a peer that was already removed (#2079).
func TestNoBFTSenderAfterPeerStopped(t *testing.T) {
	peer, msgCh := makeReadingPeer(enode.ID{22})
	defer peer.Close()
	peer.stopBFTSend() // handler.removePeer does this

	h := makeBFTTestHandler(peer)
	h.BroadcastVote(testVote(1))
	h.BroadcastOwn(testVote(2))

	select {
	case msg := <-msgCh:
		t.Fatalf("a stopped peer was sent message code %d", msg.Code)
	case <-time.After(100 * time.Millisecond):
	}
}
