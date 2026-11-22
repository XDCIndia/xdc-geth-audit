// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestF202619469TimeoutEquivocationSendsAProof(t *testing.T) {
	f := NewForensics()
	ch := make(chan types.ForensicsEvent, 1)
	sub := f.SubscribeForensicsEvent(ch)
	defer sub.Unsubscribe()

	pool := utils.NewPool()
	first := &types.Timeout{Round: 4, GapNumber: 1}
	second := &types.Timeout{Round: 4, GapNumber: 9}
	signer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	first.SetSigner(signer)
	second.SetSigner(signer)
	pool.Add(first)
	f.DetectEquivocationInTimeoutPool(second, pool)
	select {
	case ev := <-ch:
		if ev.ForensicsProof == nil || ev.ForensicsProof.ForensicsType != "Timeout" {
			t.Fatalf("proof = %+v", ev.ForensicsProof)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout equivocation did not send a proof")
	}
}

func TestF202619469SubscriberReceivesProof(t *testing.T) {
	x := &XDPoS_v2{
		ForensicsProcessor: NewForensics(),
		broadcastQuit:      make(chan struct{}),
	}
	go x.watchForensics()
	t.Cleanup(func() { close(x.broadcastQuit) })

	ch := make(chan types.ForensicsEvent, 1)
	sub := x.ForensicsProcessor.SubscribeForensicsEvent(ch)
	defer sub.Unsubscribe()
	proof := &types.ForensicProof{Id: "qc-1", ForensicsType: "QC"}
	x.ForensicsProcessor.forensicsFeed.Send(types.ForensicsEvent{ForensicsProof: proof})
	select {
	case ev := <-ch:
		if ev.ForensicsProof == nil || ev.ForensicsProof.Id != "qc-1" {
			t.Fatalf("proof = %+v", ev.ForensicsProof)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the proof")
	}
}
