// Copyright (c) 2026 XDC Network
//
// Benchmarks and safety tests for #1636: O(N²) → O(N) fix for
// GetMasternodesWithParents and getEpochSwitchInfoWithParents.
//
// Root cause: PR #1635 added an O(N) parents-slice scan to both functions for
// correctness during in-batch epoch-switch verification.  Called O(N) times per
// batch → O(N²) total — observed as −45% full-verify sync throughput.
//
// Fix: getEpochSwitchInfoInner now checks x.epochSwitches keyed by parent hash
// when chain.GetHeaderByHash returns nil for an in-batch parent (parent not yet
// committed to the DB).  Because each header's epoch-switch info is cached by its
// own hash when first resolved, the parent-hash lookup hits for every subsequent
// header in the same batch in O(1).  The O(N) parents scan is retained as a
// fallback for the first header in a new epoch (whose epoch-switch block is also
// in-batch and therefore not yet cached) and for checkpoint-sync edge cases.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ \
//          -run=^$ -bench=BenchmarkBatch1636 -benchtime=3s -count=3
//      go test ./consensus/XDPoS/engines/engine_v2/ \
//          -run=TestBranchSafetyEpochSwitchInfo -v

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// bench1636Config returns a V2-from-genesis config with epoch=900.
func bench1636Config() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID: big.NewInt(999),
		XDPoS: &params.XDPoSConfig{
			Epoch: 900,
			Gap:   450,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
				SwitchEpoch: 0,
				CurrentConfig: &params.V2Config{
					MaxMasternodes: 108,
					MinePeriod:     2,
					TimeoutPeriod:  10,
					ExpTimeoutConfig: params.ExpTimeoutConfig{
						Base:        2.0,
						MaxExponent: 6,
					},
					CertThreshold: 2.0 / 3.0,
				},
				AllConfigs: map[uint64]*params.V2Config{},
			},
		},
	}
}

// buildBatch1636 builds a correctly-linked epoch-switch block and a batch of N
// ordinary headers that follow it.  Returns:
//   - epochSwitch: V2 epoch-switch block at number=900, round=900
//   - batch[i]: block at number=901+i, round=901+i, ParentHash linked to predecessor
//
// The linked ParentHash fields ensure that the real VerifyHeaders scenario —
// where getEpochSwitchInfoInner walks the parent-hash chain to find the epoch
// switch info cached by the previous batch step — is exercised correctly.
func buildBatch1636(n int) (epochSwitch *types.Header, batch []*types.Header) {
	masternodes := []common.Address{
		common.BytesToAddress([]byte{0x01}),
		common.BytesToAddress([]byte{0x02}),
		common.BytesToAddress([]byte{0x03}),
	}

	// Epoch-switch block (round==0 in V2 means new epoch; round=900 here means
	// the block's QC points to round-0 parent, making IsEpochSwitch return true).
	esExtra, _ := (&types.ExtraFields_v2{
		Round: 900,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.Hash{0xaa},
				Round:  0,
				Number: big.NewInt(0),
			},
			GapNumber: 450,
		},
	}).EncodeToBytes()
	epochSwitch = &types.Header{
		Number:     big.NewInt(900),
		Extra:      esExtra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		// Validators carry the canonical masternode list for this epoch.
		Validators: packValidators(masternodes),
		UncleHash:  utils.UncleHash,
	}

	esHash := epochSwitch.Hash()
	esInfo := &types.BlockInfo{
		Hash:   esHash,
		Round:  900,
		Number: big.NewInt(900),
	}

	batch = make([]*types.Header, n)
	prevHash := esHash
	for i := 0; i < n; i++ {
		round := types.Round(901 + uint64(i))
		extra, _ := (&types.ExtraFields_v2{
			Round: round,
			QuorumCert: &types.QuorumCert{
				ProposedBlockInfo: esInfo,
				GapNumber:         450,
			},
		}).EncodeToBytes()
		h := &types.Header{
			Number:     new(big.Int).SetUint64(900 + uint64(i) + 1),
			ParentHash: prevHash, // linked chain — critical for parent-hash propagation
			Extra:      extra,
			Difficulty: big.NewInt(1),
			GasLimit:   10_000_000,
			UncleHash:  utils.UncleHash,
		}
		batch[i] = h
		prevHash = h.Hash()
	}
	return
}

// BenchmarkBatch1636_GetMasternodesWithParents measures the O(N²)→O(N)
// improvement from the parent-hash cache propagation in GetMasternodesWithParents.
//
// Setup: epoch switch at number=900 in parents[0]; batch headers at 901..1028
// with proper ParentHash links.  As in VerifyHeaders, each call receives the
// headers verified so far as parents (growing from 0 to N-1 entries).
//
// With the fix: first call for batch[0] does one O(N) scan; subsequent calls
// resolve via parent-hash cache in O(1).  Total: O(N).
func BenchmarkBatch1636_GetMasternodesWithParents(b *testing.B) {
	const batchSize = 128
	cfg := bench1636Config()
	db := rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	epochSwitch, batch := buildBatch1636(batchSize)
	chain := newIBChain(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Cold-start caches each iteration.
		eng.epochSwitches.Purge()
		// Pre-populate epochSwitches with the epoch-switch block itself
		// (simulates it having been processed as a target header earlier in the
		// batch, which is what VerifyHeaders does before reaching the ordinary headers).
		esInfo := &types.EpochSwitchInfo{
			Masternodes: []common.Address{
				common.BytesToAddress([]byte{0x01}),
				common.BytesToAddress([]byte{0x02}),
				common.BytesToAddress([]byte{0x03}),
			},
			MasternodesLen: 3,
			EpochSwitchBlockInfo: &types.BlockInfo{
				Hash:   epochSwitch.Hash(),
				Round:  900,
				Number: big.NewInt(900),
			},
		}
		eng.epochSwitches.Add(epochSwitch.Hash(), esInfo)
		// Process batch sequentially with growing parents (mirrors VerifyHeaders).
		parents := make([]*types.Header, 0, batchSize+1)
		parents = append(parents, epochSwitch)
		for _, h := range batch {
			_ = eng.GetMasternodesWithParents(chain, h, parents)
			parents = append(parents, h)
		}
	}
}

// BenchmarkBatch1636_NoCacheGetMasternodesWithParents is the "before" baseline:
// purges the epoch caches before every individual call, simulating the pre-fix
// O(N²) behaviour where the parents scan runs for every header.
func BenchmarkBatch1636_NoCacheGetMasternodesWithParents(b *testing.B) {
	const batchSize = 128
	cfg := bench1636Config()
	db := rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	epochSwitch, batch := buildBatch1636(batchSize)
	chain := newIBChain(cfg)

	parents := make([]*types.Header, batchSize)
	parents[0] = epochSwitch
	copy(parents[1:], batch[:batchSize-1])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Purge before every call = O(N) scan every time = O(N²) total.
		for _, h := range batch {
			eng.epochSwitches.Purge()
			_ = eng.GetMasternodesWithParents(chain, h, parents)
		}
	}
}

// BenchmarkBatch1636_getEpochSwitchInfoWithParents measures the same hot path
// exercised by verifyQC inside every verifyHeader call.
func BenchmarkBatch1636_getEpochSwitchInfoWithParents(b *testing.B) {
	const batchSize = 128
	cfg := bench1636Config()
	db := rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	epochSwitch, batch := buildBatch1636(batchSize)
	chain := newIBChain(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng.epochSwitches.Purge()
		// Seed epoch switch into cache (as verifyHeader would when it processes
		// the epoch switch block as a target).
		esInfo := &types.EpochSwitchInfo{
			Masternodes: []common.Address{
				common.BytesToAddress([]byte{0x01}),
				common.BytesToAddress([]byte{0x02}),
				common.BytesToAddress([]byte{0x03}),
			},
			MasternodesLen: 3,
			EpochSwitchBlockInfo: &types.BlockInfo{
				Hash:   epochSwitch.Hash(),
				Round:  900,
				Number: big.NewInt(900),
			},
		}
		eng.epochSwitches.Add(epochSwitch.Hash(), esInfo)
		parents := []*types.Header{epochSwitch}
		for _, h := range batch {
			_, _ = eng.getEpochSwitchInfoWithParents(chain, h, h.Hash(), parents)
			parents = append(parents, h)
		}
	}
}

// TestBranchSafetyEpochSwitchInfo verifies that two sibling epoch-switch blocks
// at the same round but with different validator sets each resolve to their own
// masternode list — proving that the hash-keyed cache cannot cross-contaminate
// sibling branches (which the rejected round-keyed cache would have allowed).
func TestBranchSafetyEpochSwitchInfo(t *testing.T) {
	t.Parallel()
	cfg := bench1636Config()
	db := rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true
	chain := newIBChain(cfg)

	// Two distinct validator sets.
	mnsA := []common.Address{
		common.BytesToAddress([]byte{0xA1}),
		common.BytesToAddress([]byte{0xA2}),
		common.BytesToAddress([]byte{0xA3}),
	}
	mnsB := []common.Address{
		common.BytesToAddress([]byte{0xB1}),
		common.BytesToAddress([]byte{0xB2}),
		common.BytesToAddress([]byte{0xB3}),
	}

	// Two sibling epoch-switch blocks at round=900 (same round, different Validators).
	makeES := func(mns []common.Address, salt byte) *types.Header {
		esExtra, _ := (&types.ExtraFields_v2{
			Round: 900,
			QuorumCert: &types.QuorumCert{
				ProposedBlockInfo: &types.BlockInfo{
					Hash:   common.Hash{salt}, // different parent → different hash
					Round:  0,
					Number: big.NewInt(0),
				},
				GapNumber: 450,
			},
		}).EncodeToBytes()
		return &types.Header{
			Number:     big.NewInt(900),
			Extra:      esExtra,
			Difficulty: big.NewInt(1),
			GasLimit:   10_000_000,
			Validators: packValidators(mns),
			UncleHash:  utils.UncleHash,
		}
	}
	esA := makeES(mnsA, 0xAA) // branch A epoch switch
	esB := makeES(mnsB, 0xBB) // branch B epoch switch — same round, different hash

	// Ordinary headers on each branch, linked to their own epoch switch.
	makeChild := func(parent *types.Header, esHeader *types.Header) *types.Header {
		esInfo := &types.BlockInfo{
			Hash:   esHeader.Hash(),
			Round:  900,
			Number: big.NewInt(900),
		}
		extra, _ := (&types.ExtraFields_v2{
			Round: 901,
			QuorumCert: &types.QuorumCert{
				ProposedBlockInfo: esInfo,
				GapNumber:         450,
			},
		}).EncodeToBytes()
		return &types.Header{
			Number:     big.NewInt(901),
			ParentHash: parent.Hash(),
			Extra:      extra,
			Difficulty: big.NewInt(1),
			GasLimit:   10_000_000,
			UncleHash:  utils.UncleHash,
		}
	}
	childA := makeChild(esA, esA)
	childB := makeChild(esB, esB)

	// Seed branch A's epoch switch into cache — simulates it having been
	// processed as a target header earlier in branch A's batch.
	infoA := &types.EpochSwitchInfo{
		Masternodes:    mnsA,
		MasternodesLen: len(mnsA),
		EpochSwitchBlockInfo: &types.BlockInfo{
			Hash: esA.Hash(), Round: 900, Number: big.NewInt(900),
		},
	}
	eng.epochSwitches.Add(esA.Hash(), infoA)

	// Branch A: childA should resolve to mnsA.
	parentsA := []*types.Header{esA}
	gotA, err := eng.getEpochSwitchInfoWithParents(chain, childA, childA.Hash(), parentsA)
	if err != nil {
		t.Fatalf("branch A: unexpected error: %v", err)
	}
	if len(gotA.Masternodes) != len(mnsA) || gotA.Masternodes[0] != mnsA[0] {
		t.Errorf("branch A: got masternodes %v, want %v", gotA.Masternodes, mnsA)
	}

	// Branch B: childB should resolve to mnsB — NOT mnsA from branch A.
	// The round-keyed cache (rejected design) would have returned mnsA here;
	// the hash-keyed parent propagation correctly isolates each branch.
	parentsB := []*types.Header{esB}
	// esB has NOT been cached yet — this exercises the parents-scan fallback.
	gotB, err := eng.getEpochSwitchInfoWithParents(chain, childB, childB.Hash(), parentsB)
	if err != nil {
		t.Fatalf("branch B: unexpected error: %v", err)
	}
	if len(gotB.Masternodes) != len(mnsB) || gotB.Masternodes[0] != mnsB[0] {
		t.Errorf("branch B: got masternodes %v, want %v (branch isolation failed)", gotB.Masternodes, mnsB)
	}

	// Extra check: the two results must use different epoch-switch hashes.
	if gotA.EpochSwitchBlockInfo.Hash == gotB.EpochSwitchBlockInfo.Hash {
		t.Error("branch A and B resolved to the same epoch-switch block — cache cross-contamination")
	}
}

// TestFastPath2SkipsEpochSwitchChild proves that fast path 2
// (propagate parent's cached epoch info to current header) does NOT fire when
// the current header is itself an epoch switch — it would return the PREVIOUS
// epoch's masternodes instead of the new epoch's.
//
// Guard: fast path 2 checks round < cached.EpochSwitchBlockInfo.Round + Epoch.
// For an epoch-switch child: round = epochSwitchRound + Epoch → guard is false → skipped.
//
// Setup:
//   - Epoch 1 switch: block 900, round 900 → mnsA seeded in cache
//   - Epoch 2 switch: block 901, round 1800 (QC.ProposedBlockInfo.Round=900 < epochStartRound=1800)
//   - getEpochSwitchInfoWithParents(es2) must NOT return mnsA.
func TestFastPath2SkipsEpochSwitchChild(t *testing.T) {
	cfg := bench1636Config()
	db := rawdb.NewMemoryDatabase()
	chain := newIBChain(cfg)
	eng := createEngine(cfg, db, nil, nil)

	mnsA := []common.Address{{0x0a}, {0x0b}}

	// Epoch 1 switch block at number=900, round=900.
	es1Extra, _ := (&types.ExtraFields_v2{
		Round: 900,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.Hash{0xff},
				Round:  0,
				Number: big.NewInt(0),
			},
			GapNumber: 450,
		},
	}).EncodeToBytes()
	es1 := &types.Header{
		Number:     big.NewInt(900),
		Extra:      es1Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		UncleHash:  utils.UncleHash,
	}
	// Seed epoch 1 info into cache.
	infoA := &types.EpochSwitchInfo{
		Masternodes:    mnsA,
		MasternodesLen: len(mnsA),
		EpochSwitchBlockInfo: &types.BlockInfo{
			Hash: es1.Hash(), Round: 900, Number: big.NewInt(900),
		},
	}
	eng.epochSwitches.Add(es1.Hash(), infoA)

	// Epoch 2 switch block at number=901, round=1800.
	// QC.ProposedBlockInfo.Round=900 < epochStartRound=1800 → IsEpochSwitch=true.
	es2Extra, _ := (&types.ExtraFields_v2{
		Round: 1800,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   es1.Hash(),
				Round:  900,
				Number: big.NewInt(900),
			},
			GapNumber: 450,
		},
	}).EncodeToBytes()
	es2 := &types.Header{
		Number:     big.NewInt(901),
		ParentHash: es1.Hash(),
		Extra:      es2Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		UncleHash:  utils.UncleHash,
	}

	// Fast path 2 guard check:
	//   es2.Round (1800) < infoA.EpochSwitchBlockInfo.Round (900) + Epoch (900) = 1800 → FALSE
	// So fast path 2 must be skipped; the parents scan runs.
	// The parents scan will find es2 is an epoch switch and attempt calcMasternodes
	// (which needs a gap block snapshot in DB — not present here, so may return an error
	// or return es2's own masternodes if found in Validators).
	// The ONLY wrong result is returning mnsA — that would prove fast path 2 fired.
	parents := []*types.Header{es2}
	got, err := eng.getEpochSwitchInfoWithParents(chain, es2, es2.Hash(), parents)

	if err == nil && got != nil && len(got.Masternodes) > 0 {
		// If we got a result, verify it's NOT the stale epoch 1 masternodes.
		if len(got.Masternodes) == len(mnsA) && got.Masternodes[0] == mnsA[0] {
			t.Error("fast path 2 guard failed: epoch-switch child returned parent epoch's masternodes (mnsA)")
		}
		// Also verify the resolved epoch switch round is 1800, not 900.
		if got.EpochSwitchBlockInfo != nil && got.EpochSwitchBlockInfo.Round == 900 {
			t.Error("fast path 2 guard failed: returned epoch 1 epoch-switch round (900) for epoch 2 child")
		}
	}
	// An error from the parents scan (missing gap block) is also acceptable —
	// it proves fast path 2 did not return mnsA without querying the DB.
	t.Logf("result: info=%v err=%v (error acceptable if gap block missing)", got, err)
}

// TestFastPath2SkippedRoundSwitchParent covers a parent epoch switch whose round
// is past its epoch start (a TC skipped rounds): epoch 1 opened at round 905, so
// a header at round 1801 is already in epoch 2 although 1801 < 905+900. The
// parent's cached info must not be propagated to it (#1652).
func TestFastPath2SkippedRoundSwitchParent(t *testing.T) {
	cfg := bench1636Config()
	chain := newIBChain(cfg)
	eng := createEngine(cfg, rawdb.NewMemoryDatabase(), nil, nil)

	parentHash := common.Hash{0xaa}
	stale := []common.Address{{0x0a}, {0x0b}}
	eng.epochSwitches.Add(parentHash, &types.EpochSwitchInfo{
		Masternodes:          stale,
		MasternodesLen:       len(stale),
		EpochSwitchBlockInfo: &types.BlockInfo{Hash: parentHash, Round: 905, Number: big.NewInt(900)},
	})
	extra, _ := (&types.ExtraFields_v2{
		Round: 1801,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{Hash: parentHash, Round: 1799, Number: big.NewInt(1799)},
			GapNumber:         450,
		},
	}).EncodeToBytes()
	header := &types.Header{
		Number:     big.NewInt(1801),
		ParentHash: parentHash,
		Extra:      extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		UncleHash:  utils.UncleHash,
	}
	got := eng.GetMasternodesWithParents(chain, header, nil)
	if len(got) == len(stale) && got[0] == stale[0] {
		t.Fatal("round 1801 got the masternodes of the epoch opened at round 905 (stale fast path)")
	}
}
