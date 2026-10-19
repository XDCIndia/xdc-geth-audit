// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/params"
)

// capReader serves headers plus a chain config with tipIncreaseMasternodesBlock.
type capReader struct {
	mapHeaderReader
	cfg *params.ChainConfig
}

func (r capReader) Config() *params.ChainConfig { return r.cfg }

// TestUpdateMasternodesV1GenesisCap: the V1 cap follows the genesis
// tipIncreaseMasternodesBlock and XDPoS.maxMasternodesV2, as canonical
// engine_v1 UpdateMasternodes does (#1569).
func TestUpdateMasternodesV1GenesisCap(t *testing.T) {
	cands := addrs(7)
	stakes := []int64{100, 106, 101, 105, 102, 104, 103}
	for _, tc := range []struct {
		tip  int64
		want int
	}{{3, 5}, {9, 7}} {
		cfg := &params.XDPoSConfig{Period: 2, Epoch: 10, Gap: 5, MaxMasternodesV2: 5}
		engine := New(cfg, rawdb.NewMemoryDatabase())
		headers := v1Segment(t, engine, 9, addrs(3))
		chain := capReader{mapHeaderReader{}, &params.ChainConfig{XDPoS: cfg, TIPIncreaseMasternodesBlock: big.NewInt(tc.tip)}}
		for _, h := range headers[:5] {
			chain.mapHeaderReader[h.Hash()] = h
		}
		if err := engine.UpdateMasternodesV1(chain, headers[5], candidateState(t, cands, stakes)); err != nil {
			t.Fatalf("UpdateMasternodesV1: %v", err)
		}
		snap, err := engine.GetSnapshot(chain, headers[5])
		if err != nil {
			t.Fatal(err)
		}
		sameSet(t, snap.Signers, canonicalSorted(cands, stakes)[:tc.want])
	}
}
