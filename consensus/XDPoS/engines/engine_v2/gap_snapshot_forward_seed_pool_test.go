// Copyright (c) 2026 XDC Network
package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
)

// TestLiveMnSet_ForwardSeed_KeepsCandidatePool reproduces the net2012 block
// 6291 state-root divergence. The gap block's snapshot, written at import from
// contract state, holds 5 masternodes plus 2 standby candidates. A vote or
// timeout check (forSigning) in the epoch it seats cannot reopen the gap
// block's state, so the forward seed rewrote the snapshot with the 5 seated
// masternodes alone. GetStandbynodes then came back empty, and the first
// TIPUpgradeReward epoch switch paid no protector/observer reward where
// XDPoSChain v2.9.3 did.
func TestLiveMnSet_ForwardSeed_KeepsCandidatePool(t *testing.T) {
	lvl, err := leveldb.New(t.TempDir(), 256, 0, "", false)
	if err != nil {
		t.Fatalf("leveldb: %v", err)
	}
	db := rawdb.NewDatabase(lvl)
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(2012),
		XDPoS: &params.XDPoSConfig{
			Epoch: 900,
			Gap:   450,
			V2: &params.V2{
				SwitchBlock: new(big.Int),
				CurrentConfig: &params.V2Config{
					MinePeriod: 2, TimeoutPeriod: 10, CertThreshold: 2.0 / 3.0,
					ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 2.0, MaxExponent: 6},
				},
				AllConfigs: map[uint64]*params.V2Config{},
			},
		},
	}
	eng := createEngine(cfg, db, nil, nil)

	_, masternodes := genNKeys(t, 5)
	_, standby := genNKeys(t, 2)
	const gapNumber = uint64(4050)
	gapHeader := &types.Header{Number: big.NewInt(int64(gapNumber)), Difficulty: big.NewInt(1), Extra: []byte{0x01}}
	switchHeader := makeRoundEpochSwitchHeader(t, 4500, 4500, 4499, masternodes)
	chain := &mockChainForGapNumber1327{
		byHash:   map[common.Hash]*types.Header{gapHeader.Hash(): gapHeader, switchHeader.Hash(): switchHeader},
		byNumber: map[uint64]*types.Header{gapNumber: gapHeader, 4500: switchHeader},
	}
	// As UpdateMasternodesFromHeader stores it at gap import: the stake-sorted pool.
	pool := append(append([]common.Address{}, masternodes...), standby...)
	if err := storeSnapshot(newSnapshot(gapNumber, gapHeader.Hash(), pool), db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	// A vote/timeout membership check for the epoch seated at 4500.
	snap, err := eng.getSnapshot(chain, gapNumber, true, nil)
	if err != nil {
		t.Fatalf("getSnapshot(forSigning): %v", err)
	}
	if len(snap.NextEpochCandidates) != len(pool) {
		t.Fatalf("forSigning snapshot has %d candidates, want the %d of the gap pool", len(snap.NextEpochCandidates), len(pool))
	}
	got := eng.GetStandbynodes(chain, switchHeader)
	if len(got) != 2 || got[0] != standby[0] || got[1] != standby[1] {
		t.Fatalf("GetStandbynodes = %v, want %v (protector, observer)", got, standby)
	}
	stored, err := loadSnapshot(db, gapHeader.Hash())
	if err != nil || len(stored.NextEpochCandidates) != len(pool) {
		t.Fatalf("stored gap snapshot lost the standby candidates: %v, err %v", stored, err)
	}
}
