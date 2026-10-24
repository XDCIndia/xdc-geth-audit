// Copyright (c) 2026 XDC Network
// SYNC-05: backfill QC verification fails with wrong-epoch masternodes (#1847).
//
// During BeaconSync backfill on a fresh fast-sync node, getEpochSwitchInfoInner's
// DB walk finds a prior epoch's switch block and overwrites the correct epoch-N
// masternodes (loaded from the gap snapshot via Source B0) with epoch-(N-1)
// masternodes.  verifyQC then calls recoverUniqueSigners against the wrong set and
// returns ErrInvalidQCSignatures ("signature from a non-masternode").
//
// Scenario (epoch=10, gap=5, SwitchBlock=0):
//
//	block 10  epoch-1 switch (Validators=addrsA, in chain DB)
//	block 15  gap block for epoch-2 (in chain DB; snapshot → addrsB)
//	block 20  epoch-2 switch  (NOT in DB — backfill hasn't reached it)
//	block 24  target header   (NOT in DB, round=24, parent not in DB)
//	qc24      QC for block 24 signed by keysB[0], keysB[1]
//
// Buggy code:   DB walk finds block 10 → overwrites addrsB with addrsA
//               → recoverUniqueSigners returns errSignerNotMasternode
//               → verifyQC returns ErrInvalidQCSignatures  (test FAILS)
// Fixed code:   DB walk finds block 10, checks epoch (10 ≠ 20), breaks early
//               → addrsB preserved → recoverUniqueSigners passes → test PASSES
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/... -run TestSync05 -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
)

func TestSync05_BackfillQCWrongEpoch(t *testing.T) {
	// --- engine with real DB (needed for snapshot persistence) ---
	dir := t.TempDir()
	lvl, err := leveldb.New(dir, 256, 0, "", false)
	if err != nil {
		t.Fatalf("leveldb.New: %v", err)
	}
	db := rawdb.NewDatabase(lvl)

	cfg := &params.ChainConfig{
		ChainID: big.NewInt(1337),
		XDPoS: &params.XDPoSConfig{
			Epoch: 10,
			Gap:   5,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
				CurrentConfig: &params.V2Config{
					MinePeriod:    2,
					TimeoutPeriod: 10,
					ExpTimeoutConfig: params.ExpTimeoutConfig{
						Base:        2.0,
						MaxExponent: 6,
					},
					CertThreshold:  2.0 / 3.0,
					MaxMasternodes: 18,
				},
				AllConfigs: map[uint64]*params.V2Config{},
			},
		},
	}

	engine := createEngine(cfg, db, nil, nil)

	// --- two disjoint masternode sets ---
	keysA, addrsA := genNKeys(t, 3) // epoch-1 masternodes
	keysB, addrsB := genNKeys(t, 3) // epoch-2 masternodes
	_ = keysA

	// --- block 0: genesis / V1–V2 switch block (V1 format extra) ---
	b0 := &types.Header{
		Number:     big.NewInt(0),
		Extra:      make([]byte, 97), // 32 vanity + 0 addrs + 65 seal
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		UncleHash:  utils.UncleHash,
	}

	// --- block 10: first V2 epoch switch, round=10, Validators=addrsA ---
	// QC inside block 10 certifies block 0 (the SwitchBlock).
	b10QC := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   b0.Hash(),
			Number: big.NewInt(0),
			Round:  types.Round(0),
		},
		GapNumber: 0,
	}
	b10 := esV2Header(t, 10, b0, types.Round(10), b10QC, 10)
	b10.Validators = packValidators(addrsA)
	esSeal(t, b10, keysA[0])

	// --- block 15: gap block for epoch-2 (round=15, in chain DB) ---
	// QC inside block 15 certifies block 14 (mid-epoch-1; ProposedBlockInfo.Number≠0
	// so IsEpochSwitch returns false for block 15).
	b15QC := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   common.HexToHash("0xeeee"),
			Number: big.NewInt(14),
			Round:  types.Round(14),
		},
		GapNumber: 15,
	}
	b15 := esV2Header(t, 15, b10, types.Round(15), b15QC, 15)
	esSeal(t, b15, keysB[0])

	// --- chain DB: only blocks 0, 10, 15 are committed ---
	// Block 20 (epoch-2 switch) is absent — BeaconSync backfill hasn't reached it.
	chain := &esChain{
		cfg:      cfg,
		byHash:   make(map[common.Hash]*types.Header),
		byNumber: make(map[uint64]*types.Header),
	}
	chain.add(b0)
	chain.add(b10)
	chain.add(b15)

	// --- seed gap snapshot at b15.Hash() with addrsB (epoch-2 masternodes) ---
	// getEpochSwitchInfoInner Source B0 calls getSnapshot(chain, 15, true, nil),
	// which resolves b15.Hash() and loads this snapshot from DB.
	snap := newSnapshot(15, b15.Hash(), addrsB)
	if serr := storeSnapshot(snap, db); serr != nil {
		t.Fatalf("storeSnapshot: %v", serr)
	}

	// --- block 23 (phantom parent): NOT in chain ---
	// b24's parent must be absent from chain so getEpochSwitchInfoInner enters
	// the synthetic-fallback path (parent not in DB).
	b23phantom := &types.Header{
		Number:     big.NewInt(23),
		Difficulty: big.NewInt(1),
		UncleHash:  utils.UncleHash,
		Extra:      make([]byte, 97),
	}

	// --- block 24: mid-epoch-2 target header (NOT in chain) ---
	// Its embedded QC carries GapNumber=15 so Source B0 finds the seeded snapshot.
	b24QC := &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   b23phantom.Hash(),
			Number: big.NewInt(23),
			Round:  types.Round(23),
		},
		GapNumber: 15,
	}
	b24 := esV2Header(t, 24, b23phantom, types.Round(24), b24QC, 24)
	esSeal(t, b24, keysB[1])
	// b24 is deliberately NOT added to chain.

	// --- QC for block 24: signed by keysB[0] and keysB[1] (2-of-3 epoch-2 keys) ---
	blockInfo24 := &types.BlockInfo{
		Hash:   b24.Hash(),
		Number: big.NewInt(24),
		Round:  types.Round(24),
	}
	qc24 := &types.QuorumCert{
		ProposedBlockInfo: blockInfo24,
		GapNumber:         15,
		Signatures:        makeQCSignatures(t, blockInfo24, 15, keysB[:2]),
	}

	// verifyQC with b24 as parentHeader and no pre-fetched parents.
	//
	// Code path: getEpochSwitchInfoWithParents(parents=nil) →
	//            getEpochSwitchInfo → getEpochSwitchInfoInner →
	//            synthetic fallback (b23phantom not in DB) →
	//            Source B0 loads addrsB from gap snapshot at b15 →
	//            DB walk finds block 10 (epoch-1) …
	//
	// Buggy:  DB walk overwrites addrsB with addrsA → ErrInvalidQCSignatures.
	// Fixed:  DB walk checks epoch (esEpochStart=10 ≠ targetEpochStart=20) →
	//         breaks early → addrsB preserved → verifyQC returns nil.
	if verr := engine.verifyQC(chain, qc24, b24, nil); verr != nil {
		t.Errorf("SYNC-05: verifyQC returned error (want nil after fix): %v", verr)
	}
}
