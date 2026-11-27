// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
)

// syncSpyDB counts SyncKeyValue calls and can make them fail.
type syncSpyDB struct {
	ethdb.Database
	syncs int
	err   error
}

func (d *syncSpyDB) SyncKeyValue() error {
	d.syncs++
	return d.err
}

// TestXDCHunt30SafetyStateIsSyncedBeforeVoteAndSeal checks that the voted
// round, the self-mined round and the lock are flushed to disk, and that a
// failed flush stops the vote and the seal from leaving the node.
func TestXDCHunt30SafetyStateIsSyncedBeforeVoteAndSeal(t *testing.T) {
	eng, db := newA894Engine(t)
	spy := &syncSpyDB{Database: db}
	eng.db = spy

	// Self-mined round.
	if err := eng.markSelfMined(5); err != nil || spy.syncs != 1 {
		t.Fatalf("markSelfMined: err=%v syncs=%d, want nil and 1", err, spy.syncs)
	}
	spy.err = errors.New("disk")
	if err := eng.markSelfMined(6); err == nil {
		t.Fatal("markSelfMined hid a failed sync")
	}

	// Lock, through processQC.
	spy.err, spy.syncs = nil, 0
	b8 := buildV2Header(t, 8, 8, 7, common.Hash{0x07}, nil)
	b9 := buildV2Header(t, 9, 9, 8, b8.Hash(), nil)
	b10 := buildV2Header(t, 10, 10, 9, b9.Hash(), nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{8: b8, 9: b9, 10: b10}}
	lock := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Hash: b10.Hash(), Round: 10, Number: big.NewInt(10)}}
	eng.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Round: 1, Number: big.NewInt(1)}}
	if err := eng.processQC(chain, lock); err != nil || spy.syncs == 0 {
		t.Fatalf("processQC: err=%v syncs=%d, want nil and a sync after the lock write", err, spy.syncs)
	}

	// Vote: same shape as Test1457_SeatedNodeVotes.
	eng.config.V2.SwitchBlock = big.NewInt(2700)
	eng.BroadcastCh = make(chan interface{}, 1)
	selfKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	self := crypto.PubkeyToAddress(selfKey.PublicKey)
	eng.Authorize(self, func(_ accounts.Account, data []byte) ([]byte, error) { return crypto.Sign(data, selfKey) })
	seated := []common.Address{{0x01}, {0x02}, {0x03}, {0x04}, self}
	switchHeader := &types.Header{Number: big.NewInt(2700), Difficulty: big.NewInt(1),
		Extra: append(append(make([]byte, 32), seated[0].Bytes()...), make([]byte, 65)...)}
	gapHeader := &types.Header{Number: big.NewInt(3150), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	eng.snapshots.Add(gapHeader.Hash(), newSnapshot(3150, gapHeader.Hash(), seated))
	voteChain := &mockChain1330{byNumber: map[uint64]*types.Header{2700: switchHeader, 3150: gapHeader}}
	blockHash := common.HexToHash("0x1457")
	eng.epochSwitches.Add(blockHash, &types.EpochSwitchInfo{
		Masternodes:          seated,
		MasternodesLen:       len(seated),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: common.HexToHash("0x4499"), Round: 2100, Number: big.NewInt(4499)},
	})
	const round = types.Round(2406)
	blockInfo := &types.BlockInfo{Hash: blockHash, Round: round, Number: big.NewInt(5104)}
	eng.currentRound = round
	eng.isInitialized = true

	spy.err = errors.New("disk")
	if err := eng.sendVote(voteChain, blockInfo); err == nil {
		t.Fatal("sendVote broadcast although the safety state was not synced")
	}
	select {
	case <-eng.BroadcastCh:
		t.Fatal("vote left the node after a failed sync")
	default:
	}

	spy.err, spy.syncs = nil, 0
	eng.highestVotedRound = 0
	if err := eng.sendVote(voteChain, blockInfo); err != nil {
		t.Fatalf("sendVote: %v", err)
	}
	if spy.syncs == 0 {
		t.Fatal("sendVote broadcast without syncing highestVotedRound")
	}
	<-eng.BroadcastCh
}
