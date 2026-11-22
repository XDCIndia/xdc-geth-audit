// Copyright (c) 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

type initialRecordingV2 struct {
	fakeEngineV2
	initialWith *types.Header
}

func (f *initialRecordingV2) Initial(_ consensus.ChainReader, h *types.Header) error {
	f.initialWith = h
	return nil
}

// TestXDCHunt06InitialSeedsV2EngineFromLocalHead checks that XDPoS.Initial,
// which eth.New calls with the local head at startup, initialises the V2
// engine for a V2 head and leaves it alone for a V1 head.
func TestXDCHunt06InitialSeedsV2EngineFromLocalHead(t *testing.T) {
	fake := &initialRecordingV2{}
	x := &XDPoS{}
	x.EngineV2 = fake
	x.config = &params.XDPoSConfig{V2: &params.V2{SwitchBlock: big.NewInt(10)}}

	v1 := &types.Header{Number: big.NewInt(5)}
	if err := x.Initial(headerOnlyChainReader{}, v1); err != nil || fake.initialWith != nil {
		t.Fatalf("V1 head: err=%v initialWith=%v, want no V2 init", err, fake.initialWith)
	}
	v2 := &types.Header{Number: big.NewInt(11)}
	if err := x.Initial(headerOnlyChainReader{}, v2); err != nil {
		t.Fatalf("V2 head: %v", err)
	}
	if fake.initialWith != v2 {
		t.Fatalf("V2 head: engine initialised with %v, want the local head", fake.initialWith)
	}
}
