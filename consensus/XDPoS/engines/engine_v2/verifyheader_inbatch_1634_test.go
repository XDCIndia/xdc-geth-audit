// Copyright (c) 2026 XDC Network
// #1634: two in-batch verification bugs at the V1→V2 switch boundary.
//
// Bug 1 — "no checkpoint header at <SwitchBlock>":
//   verifyHeader(firstV2EpochSwitch, parents=[switchBlock], fullVerify=true) fails
//   because calcMasternodes→getSnapshot looks up the switch block by number and gets
//   nil (it is in the parents batch, not yet committed to the chain DB).
//   Fix: getSnapshot scans parents when GetHeaderByNumber returns nil.
//
// Bug 2 — "Invalid blocker proposer, not its turn":
//   verifyHeader(midEpochBlock, parents=[epoch2Switch], fullVerify=true) returns the
//   wrong masternode set because getEpochSwitchInfo's synthetic fallback (Source B0
//   snapshot + DB walk) finds the epoch-1 switch block in the DB and overwrites the
//   correct epoch-2 masternodes with epoch-1's, since the epoch-2 switch block is in
//   the parents batch and is invisible to GetHeaderByNumber.
//   Fix: GetMasternodesWithParents scans parents before calling getEpochSwitchInfo.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestInBatch -count=1

package engine_v2

import (
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

// newIB1Config returns a ChainConfig for Bug 1: epoch=10, gap=5, SwitchBlock=10.
func newIB1Config() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: 10,
			Gap:   5,
			V2: &params.V2{
				SwitchBlock: big.NewInt(10),
				SwitchEpoch: 1,
				CurrentConfig: &params.V2Config{
					MaxMasternodes: 18,
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

// newIB2Config returns a ChainConfig for Bug 2: epoch=10, gap=5, SwitchBlock=0.
func newIB2Config() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: 10,
			Gap:   5,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
				SwitchEpoch: 0,
				CurrentConfig: &params.V2Config{
					MaxMasternodes: 18,
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

// ibChain extends esChain with the chain config already attached.
type ibChain struct{ *esChain }

func newIBChain(cfg *params.ChainConfig) *ibChain {
	return &ibChain{&esChain{
		cfg:      cfg,
		byHash:   map[common.Hash]*types.Header{},
		byNumber: map[uint64]*types.Header{},
	}}
}

// v1SwitchHeader builds a minimal V1-format header at `num` that encodes
// `addrs` in Extra (vanity-32 + N*20 + seal-65).  It is NOT a V2 header and
// carries no V2 extra fields.
func v1SwitchHeader(num uint64, addrs []common.Address, gasLimit uint64, ts uint64) *types.Header {
	const vanity, seal = 32, 65
	extra := make([]byte, vanity+len(addrs)*common.AddressLength+seal)
	for i, a := range addrs {
		copy(extra[vanity+i*common.AddressLength:], a[:])
	}
	return &types.Header{
		Number:     new(big.Int).SetUint64(num),
		Extra:      extra,
		Difficulty: big.NewInt(1),
		GasLimit:   gasLimit,
		Time:       ts,
		UncleHash:  utils.UncleHash,
	}
}

// TestInBatch_Bug1_SwitchBlockNotYetInDB verifies that the first V2 epoch-switch
// block (number = SwitchBlock+1 = 11) can be verified under fullVerify=true when
// the V1 switch block (number = 10) lives only in the parents batch and has not
// yet been committed to the chain DB.
//
// Without the fix, getSnapshot returns "no checkpoint header at 10".
// With the fix, getSnapshot finds block 10 in parents and bootstraps the snapshot.
func TestInBatch_Bug1_SwitchBlockNotYetInDB(t *testing.T) {
	t.Parallel()
	cfg := newIB1Config()
	var db ethdb.Database = rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	keys, addrs := genNKeys(t, 3) // epoch-1 masternodes: addrs[0..2]
	chain := newIBChain(cfg)
	now := uint64(time.Now().Unix())

	// Gap block at 5 — must be in the chain DB so getSnapshot can find it.
	gap5 := &types.Header{
		Number:     big.NewInt(5),
		Extra:      []byte{0x01},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 200,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap5)
	// Store a snapshot at the gap block.  The V1→V2 switch path in getSnapshot
	// evicts this cached entry and rebuilds from the switch header, but the gap
	// block itself still needs to be present in the DB.
	if err := storeSnapshot(newSnapshot(5, gap5.Hash(), addrs), db); err != nil {
		t.Fatalf("storeSnapshot gap5: %v", err)
	}

	// Switch block 10 — V1 format, NOT in chain DB (lives only in parents).
	// Extra = vanity(32) + addrs[0..2] (60 bytes) + seal(65) = 157 bytes.
	switchBlock := v1SwitchHeader(10, addrs, 10_000_000, now-100)

	// Block 11 — first V2 epoch-switch block.
	//   • QC proposes the switch block (round 0) — qcRound=0 skips signature threshold.
	//   • round = 10, so leaderIndex = 10 % 10 % 3 = 0 → addrs[0] is the leader.
	//   • Validators = packed addrs (must match calcMasternodes output).
	//   • Nonce = NonceDropVote (required for epoch-switch blocks).
	pInfoSwitch := &types.BlockInfo{
		Hash:   switchBlock.Hash(),
		Round:  0,
		Number: big.NewInt(10),
	}
	block11Extra, err := (&types.ExtraFields_v2{
		Round: 10,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: pInfoSwitch,
			GapNumber:         5,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block11 extra: %v", err)
	}
	block11 := &types.Header{
		Number:     big.NewInt(11),
		ParentHash: switchBlock.Hash(),
		Extra:      block11Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 50,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs),
	}
	copy(block11.Nonce[:], utils.NonceDropVote)

	// Seal block 11 as addrs[0] (leaderIndex=0).
	block11.Coinbase = addrs[0]
	sig, err := crypto.Sign(sigHash(block11).Bytes(), keys[0])
	if err != nil {
		t.Fatalf("seal block11: %v", err)
	}
	block11.Validator = sig

	// Bug 1: without the fix, this returns
	//   "calcMasternodes failed under fullVerify … no checkpoint header at 10"
	// With the fix, getSnapshot finds switchBlock in parents → success.
	if err := eng.verifyHeader(chain.esChain, block11, []*types.Header{switchBlock}, true); err != nil {
		t.Fatalf("Bug 1: first V2 epoch-switch with switch block in-batch must succeed, got: %v", err)
	}
}

// TestInBatch_Bug2_EpochSwitchNotYetInDB verifies that a non-epoch-switch block
// in epoch 2 can be verified under fullVerify=true when the epoch-2 switch block
// lives only in the parents batch and has not yet been committed to the chain DB.
//
// Without the fix:
//
//	getEpochSwitchInfo's synthetic path loads the epoch-2 gap snapshot (Source B0),
//	then the DB walk overwrites masternodes with epoch-1's set (because the epoch-2
//	switch block is nil in the DB). GetMasternodesWithParents returns the wrong list,
//	leaderIndex points to epoch-1's address[1] instead of epoch-2's address[1], and
//	verifyHeader returns ErrNotItsTurn.
//
// With the fix:
//
//	GetMasternodesWithParents finds the epoch-2 switch block in parents before calling
//	getEpochSwitchInfo and returns the correct epoch-2 masternode set.
func TestInBatch_Bug2_EpochSwitchNotYetInDB(t *testing.T) {
	t.Parallel()
	cfg := newIB2Config()
	var db ethdb.Database = rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	// Epoch-1 masternodes (stable order): [A, B, C]
	keys, addrs := genNKeys(t, 3)
	// Epoch-2 masternodes (deliberately different order): [C, A, B]
	// leaderIndex for round=21 = 21%10%3 = 1
	//   epoch-1: masterNodes[1] = B  ← wrong (what the bug produces)
	//   epoch-2: masterNodes[1] = A  ← correct (what the fix produces)
	addrs2 := []common.Address{addrs[2], addrs[0], addrs[1]}
	keys2 := []*ecdsa.PrivateKey{keys[2], keys[0], keys[1]}

	chain := newIBChain(cfg)
	now := uint64(time.Now().Unix())

	// --- Chain DB -------------------------------------------------------

	// Epoch-1 gap block at 5.
	gap5 := &types.Header{
		Number:     big.NewInt(5),
		Extra:      []byte{0x05},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 500,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap5)
	if err := storeSnapshot(newSnapshot(5, gap5.Hash(), addrs), db); err != nil {
		t.Fatalf("storeSnapshot gap5: %v", err)
	}

	// Epoch-1 switch block at round=10 (block 10).
	// parentRound=9 < epochStartRound=10 → IsEpochSwitch=true.
	// Validators = epoch-1 masternodes, so the DB walk finds the right epoch-1 set
	// when the epoch-2 switch is missing from the DB.
	e1Extra, err := (&types.ExtraFields_v2{
		Round: 10,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xaa}),
				Round:  9,
				Number: big.NewInt(9),
			},
			GapNumber: 5,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch1 switch extra: %v", err)
	}
	epoch1Switch := &types.Header{
		Number:     big.NewInt(10),
		Extra:      e1Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 400,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs),
	}
	chain.add(epoch1Switch)

	// Epoch-2 gap block at 15.
	// NOT an epoch switch; the DB walk treats it as an ordinary non-switch block.
	gap15 := &types.Header{
		Number:     big.NewInt(15),
		Extra:      []byte{0x0f},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 300,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap15)
	// This snapshot is picked up by getEpochSwitchInfo Source B0 (getSnapshot
	// forSigning=true at GapNumber=15).  The DB walk then overwrites it with
	// epoch-1's masternodes — that overwrite is exactly Bug 2.
	if err := storeSnapshot(newSnapshot(15, gap15.Hash(), addrs2), db); err != nil {
		t.Fatalf("storeSnapshot gap15: %v", err)
	}

	// --- Parents batch (not yet in DB) ----------------------------------

	// Epoch-2 switch block at round=20 (block 20).
	// parentRound=19 < epochStartRound=20 → IsEpochSwitch=true.
	// NOT added to chain.byNumber / byHash.
	e2Extra, err := (&types.ExtraFields_v2{
		Round: 20,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xbb}),
				Round:  19,
				Number: big.NewInt(19),
			},
			GapNumber: 15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch2 switch extra: %v", err)
	}
	epoch2Switch := &types.Header{
		Number:     big.NewInt(20),
		Extra:      e2Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 200,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs2), // epoch-2 set
	}

	// Block 21 — the block being verified (non-epoch-switch, round=21).
	// Leader at round=21: leaderIndex = 21%10%3 = 1 → epoch-2[1] = addrs[0] = A.
	// Block is sealed by addrs[0] (A), which is correct for epoch-2 but wrong for epoch-1.
	pInfo20 := &types.BlockInfo{
		Hash:   epoch2Switch.Hash(),
		Round:  20,
		Number: big.NewInt(20),
	}
	sigs20 := makeQCSignatures(t, pInfo20, 15, keys2[:]) // all 3 epoch-2 signers
	b21Extra, err := (&types.ExtraFields_v2{
		Round: 21,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: pInfo20,
			Signatures:        sigs20,
			GapNumber:         15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block21 extra: %v", err)
	}
	block21 := &types.Header{
		Number:     big.NewInt(21),
		ParentHash: epoch2Switch.Hash(),
		Extra:      b21Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 100,
		UncleHash:  utils.UncleHash,
		// Validators and Penalties must be empty for non-epoch-switch blocks.
	}
	copy(block21.Nonce[:], utils.NonceAuthVote)

	// Seal block21 as addrs[0] (A): correct epoch-2 leader, wrong epoch-1 leader.
	block21.Coinbase = addrs[0]
	sig, err := crypto.Sign(sigHash(block21).Bytes(), keys[0])
	if err != nil {
		t.Fatalf("seal block21: %v", err)
	}
	block21.Validator = sig

	// Bug 2: without the fix, GetMasternodesWithParents returns epoch-1 masternodes
	// (DB-walk overwrites epoch-2 masternodes from the gap snapshot), causing the
	// leader check to fail with ErrNotItsTurn.
	// With the fix, parents are scanned first, finding epoch2Switch → correct set.
	if err := eng.verifyHeader(chain.esChain, block21, []*types.Header{epoch2Switch}, true); err != nil {
		t.Fatalf("Bug 2: mid-epoch block with epoch-switch in-batch must succeed, got: %v", err)
	}
}

// TestInBatch_Bug2_TCSkippedRounds reproduces the case where TCs skipped rounds
// so the epoch-2 switch block's round is > epochStartRound.
//
// Setup: epoch=10, epochStartRound=20.  Leaders are down for rounds 20, 21, 22,
// so TCs advance the round counter and the first block the active leader proposes
// is at round 23 — but it is still the epoch-2 switch block (parentRound=19 <
// epochStartRound=20 ≤ round=23).
//
// Failure mode with original Fix 2 (pRound == epochStartRound):
//
//	The inner loop checks pRound==20, sees pRound=23≠20, keeps scanning, finds no
//	match, and falls through to the buggy DB walk that returns epoch-1 masternodes.
//	verifyHeader then rejects the block with ErrNotItsTurn.
//
// Correct fix (IsEpochSwitch + pEpochStart == epochStartRound):
//
//	pEpochStart = 23-23%10 = 20 == epochStartRound=20, and IsEpochSwitch returns
//	true (parentRound=19 < 20).  The epoch-2 masternodes are used and the block
//	is accepted.
func TestInBatch_Bug2_TCSkippedRounds(t *testing.T) {
	t.Parallel()
	cfg := newIB2Config()
	var db ethdb.Database = rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	// Epoch-1 masternodes: [A, B, C]
	keys, addrs := genNKeys(t, 3)
	// Epoch-2 masternodes (different order): [C, A, B]
	// leaderIndex for round=24: 24%10%3 = 4%3 = 1 → epoch2[1] = addrs[0] = A
	addrs2 := []common.Address{addrs[2], addrs[0], addrs[1]}
	keys2 := []*ecdsa.PrivateKey{keys[2], keys[0], keys[1]}

	chain := newIBChain(cfg)
	now := uint64(time.Now().Unix())

	// --- Chain DB -------------------------------------------------------

	gap5 := &types.Header{
		Number:     big.NewInt(5),
		Extra:      []byte{0x05},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 600,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap5)
	if err := storeSnapshot(newSnapshot(5, gap5.Hash(), addrs), db); err != nil {
		t.Fatalf("storeSnapshot gap5: %v", err)
	}

	// Epoch-1 switch block at round=10 (block 10), in DB.
	e1Extra, err := (&types.ExtraFields_v2{
		Round: 10,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xaa}),
				Round:  9,
				Number: big.NewInt(9),
			},
			GapNumber: 5,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch1 switch extra: %v", err)
	}
	epoch1Switch := &types.Header{
		Number:     big.NewInt(10),
		Extra:      e1Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 500,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs),
	}
	chain.add(epoch1Switch)

	// Epoch-2 gap block at 15, in DB with epoch-2 snapshot.
	gap15 := &types.Header{
		Number:     big.NewInt(15),
		Extra:      []byte{0x0f},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 400,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap15)
	if err := storeSnapshot(newSnapshot(15, gap15.Hash(), addrs2), db); err != nil {
		t.Fatalf("storeSnapshot gap15: %v", err)
	}

	// --- Parents batch (not yet in DB) ----------------------------------

	// Epoch-2 switch block at round=23 (TC skipped rounds 20, 21, 22).
	// parentRound=19 < epochStartRound=20 → IsEpochSwitch=true.
	// pRound=23 > epochStartRound=20 — this is the case the old fix missed.
	e2Extra, err := (&types.ExtraFields_v2{
		Round: 23,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xbb}),
				Round:  19,
				Number: big.NewInt(19),
			},
			GapNumber: 15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch2 TC-skip switch extra: %v", err)
	}
	epoch2SwitchTC := &types.Header{
		Number:     big.NewInt(20),
		Extra:      e2Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 300,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs2),
	}
	// NOT added to chain.byNumber / byHash — lives only in parents.

	// Block 24 — the block being verified (non-epoch-switch, round=24).
	// epochStartRound = 24 - 24%10 = 20.
	// leaderIndex = 24%10%3 = 4%3 = 1 → epoch2[1] = addrs[0] = A.
	pInfo23 := &types.BlockInfo{
		Hash:   epoch2SwitchTC.Hash(),
		Round:  23,
		Number: big.NewInt(20),
	}
	sigs23 := makeQCSignatures(t, pInfo23, 15, keys2[:])
	b24Extra, err := (&types.ExtraFields_v2{
		Round: 24,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: pInfo23,
			Signatures:        sigs23,
			GapNumber:         15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block24 extra: %v", err)
	}
	block24 := &types.Header{
		Number:     big.NewInt(21),
		ParentHash: epoch2SwitchTC.Hash(),
		Extra:      b24Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 200,
		UncleHash:  utils.UncleHash,
	}
	copy(block24.Nonce[:], utils.NonceAuthVote)

	// Seal block24 as addrs[0] (A): correct epoch-2 leader, wrong epoch-1 leader.
	block24.Coinbase = addrs[0]
	sig, err := crypto.Sign(sigHash(block24).Bytes(), keys[0])
	if err != nil {
		t.Fatalf("seal block24: %v", err)
	}
	block24.Validator = sig

	// TC-skip bug: old Fix 2 checks pRound==epochStartRound (23==20 → false),
	// finds no match in parents, falls through to DB walk, gets epoch-1 masternodes,
	// and rejects block24 with ErrNotItsTurn.
	// Corrected fix: pEpochStart=20==epochStartRound=20 and IsEpochSwitch=true
	// (parentRound=19 < 20), so epoch-2 masternodes are used → block24 accepted.
	if err := eng.verifyHeader(chain.esChain, block24, []*types.Header{epoch2SwitchTC}, true); err != nil {
		t.Fatalf("Bug 2 TC-skip: block after TC-skipped epoch switch in-batch must succeed, got: %v", err)
	}
}

// TestInBatch_Bug34_QCVerifyInBatchEpochSwitch exercises Bugs 3 & 4: verifyQC for
// a block AFTER an in-batch epoch switch fails because getEpochSwitchInfoWithParents
// returns epoch-1 masternode info (from the DB) instead of epoch-2 info (from the
// in-batch switch block).  This causes two symptoms:
//
//   - QC signature verification failed  — the QC is signed by epoch-2 masternodes
//     but is checked against epoch-1's list
//   - gap number mismatch               — the epoch-switch number used to derive
//     the gap comes from epoch-1's switch, not epoch-2's
//
// Both symptoms are triggered by the same root: getEpochSwitchInfoWithParents calls
// getEpochSwitchInfo (cache/DB) before scanning parents, and the DB returns the
// epoch-1 switch while the epoch-2 switch is only in the parents batch.
//
// Fix: getEpochSwitchInfoWithParents scans parents FIRST (same IsEpochSwitch +
// epoch-range filter as GetMasternodesWithParents) before falling back to DB/cache.
func TestInBatch_Bug34_QCVerifyInBatchEpochSwitch(t *testing.T) {
	t.Parallel()
	cfg := newIB2Config()
	var db ethdb.Database = rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	// Epoch-1 masternodes: [A, B, C]
	keys, addrs := genNKeys(t, 3)
	// Epoch-2 masternodes (different order): [C, A, B]
	// leaderIndex for round=22: 22%10%3 = 2%3 = 2 → epoch2[2] = addrs[1] = B
	addrs2 := []common.Address{addrs[2], addrs[0], addrs[1]}
	keys2 := []*ecdsa.PrivateKey{keys[2], keys[0], keys[1]}

	chain := newIBChain(cfg)
	now := uint64(time.Now().Unix())

	// --- Chain DB -------------------------------------------------------

	gap5 := &types.Header{
		Number:     big.NewInt(5),
		Extra:      []byte{0x05},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 600,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap5)
	if err := storeSnapshot(newSnapshot(5, gap5.Hash(), addrs), db); err != nil {
		t.Fatalf("storeSnapshot gap5: %v", err)
	}

	// Epoch-1 switch block at round=10 (block 10), IN DB.
	e1Extra, err := (&types.ExtraFields_v2{
		Round: 10,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xaa}),
				Round:  9,
				Number: big.NewInt(9),
			},
			GapNumber: 5,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch1 switch extra: %v", err)
	}
	epoch1Switch := &types.Header{
		Number:     big.NewInt(10),
		Extra:      e1Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 500,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs),
	}
	chain.add(epoch1Switch)

	// Epoch-2 gap block at 15, IN DB with epoch-2 snapshot.
	gap15 := &types.Header{
		Number:     big.NewInt(15),
		Extra:      []byte{0x0f},
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 400,
		UncleHash:  utils.UncleHash,
	}
	chain.add(gap15)
	if err := storeSnapshot(newSnapshot(15, gap15.Hash(), addrs2), db); err != nil {
		t.Fatalf("storeSnapshot gap15: %v", err)
	}

	// --- Parents batch (not yet in DB) ----------------------------------

	// Epoch-2 switch block at round=20 (block 20), NOT in DB.
	// parentRound=19 < epochStartRound=20 → IsEpochSwitch=true.
	e2Extra, err := (&types.ExtraFields_v2{
		Round: 20,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   common.BytesToHash([]byte{0xbb}),
				Round:  19,
				Number: big.NewInt(19),
			},
			GapNumber: 15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode epoch2 switch extra: %v", err)
	}
	epoch2Switch := &types.Header{
		Number:     big.NewInt(20),
		Extra:      e2Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 300,
		UncleHash:  utils.UncleHash,
		Validators: packValidators(addrs2),
	}

	// Block 21 — between epoch switch and the verified block (NOT in DB).
	e21Extra, err := (&types.ExtraFields_v2{
		Round: 21,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Hash:   epoch2Switch.Hash(),
				Round:  20,
				Number: big.NewInt(20),
			},
			GapNumber: 15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block21 extra: %v", err)
	}
	block21 := &types.Header{
		Number:     big.NewInt(21),
		ParentHash: epoch2Switch.Hash(),
		Extra:      e21Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 200,
		UncleHash:  utils.UncleHash,
	}
	copy(block21.Nonce[:], utils.NonceAuthVote)
	// leaderIndex for round=21: 21%10%3 = 1 → epoch2[1] = addrs[0] = A
	block21.Coinbase = addrs[0]
	sig21, err := crypto.Sign(sigHash(block21).Bytes(), keys[0])
	if err != nil {
		t.Fatalf("seal block21: %v", err)
	}
	block21.Validator = sig21

	// Block 22 — the block being verified.
	// leaderIndex for round=22: 22%10%3 = 2 → epoch2[2] = addrs[1] = B
	// Its QC proposes block21 (round=21), signed by all 3 epoch-2 signers.
	pInfo21 := &types.BlockInfo{
		Hash:   block21.Hash(),
		Round:  21,
		Number: big.NewInt(21),
	}
	// QC signed by epoch-2 keys (keys2) — correct for epoch 2, wrong for epoch 1.
	sigs21 := makeQCSignatures(t, pInfo21, 15, keys2[:])
	b22Extra, err := (&types.ExtraFields_v2{
		Round: 22,
		QuorumCert: &types.QuorumCert{
			ProposedBlockInfo: pInfo21,
			Signatures:        sigs21,
			GapNumber:         15,
		},
	}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode block22 extra: %v", err)
	}
	block22 := &types.Header{
		Number:     big.NewInt(22),
		ParentHash: block21.Hash(),
		Extra:      b22Extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       now - 100,
		UncleHash:  utils.UncleHash,
	}
	copy(block22.Nonce[:], utils.NonceAuthVote)
	block22.Coinbase = addrs[1]
	sig22, err := crypto.Sign(sigHash(block22).Bytes(), keys[1])
	if err != nil {
		t.Fatalf("seal block22: %v", err)
	}
	block22.Validator = sig22

	// Parents batch: epoch2Switch and block21 are in-batch (not in DB).
	// block22 is the block being verified.
	//
	// Without the fix: getEpochSwitchInfoWithParents for block21 (QC's proposed
	// block) goes to getEpochSwitchInfo → DB walk → finds epoch1Switch (in DB) →
	// returns epoch-1 masternodes and epoch-1 epochSwitchNumber.
	//   QC signature check: epoch-2 sigs vs epoch-1 masternodes → fails (Bug 4)
	//   OR gap check: epoch1SwitchNum→gap≠15 → fails (Bug 3 variant)
	//
	// With the fix: getEpochSwitchInfoWithParents scans parents first → finds
	// epoch2Switch → epoch-2 masternodes, correct epochSwitchNumber → passes.
	parents := []*types.Header{epoch2Switch, block21}
	if err := eng.verifyHeader(chain.esChain, block22, parents, true); err != nil {
		t.Fatalf("Bug 3/4: block after in-batch epoch switch with epoch-2 QC must succeed, got: %v", err)
	}
}
