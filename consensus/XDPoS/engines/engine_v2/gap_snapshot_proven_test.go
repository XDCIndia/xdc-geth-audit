// Copyright (c) 2026 XDC Network
// F-2026-20648: gap snapshots below the snap pivot proven by Merkle proof
// (version 7) are accepted by getSnapshot, survive a restart, are never replaced
// by header seeds, and a pool built from proofs equals the state-built one.
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestProven

package engine_v2

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
)

// commitCandidateState commits a 0x88 pool (slot 8 array, caps in validatorsState)
// and returns its root and trie database. Stakes tie in pairs, so the ordering
// depends on the frozen sort.
func commitCandidateState(t *testing.T, addrs []common.Address) (common.Hash, *triedb.Database) {
	t.Helper()
	tdb := triedb.NewDatabase(rawdb.NewMemoryDatabase(), triedb.HashDefaults)
	st, err := state.New(types.EmptyRootHash, state.NewDatabase(tdb, nil))
	if err != nil {
		t.Fatal(err)
	}
	c := common.MasternodeVotingSMCBinary
	st.SetNonce(c, 1, 0)
	st.SetState(c, state.CandidatesLenKey(), common.BigToHash(big.NewInt(int64(len(addrs)))))
	for i, a := range addrs {
		st.SetState(c, state.CandidateElementKey(uint64(i)), common.BytesToHash(a.Bytes()))
		stake := big.NewInt(int64(len(addrs)-i/2) * 10_000_000)
		st.SetState(c, state.CandidateCapKey(a), common.BigToHash(stake))
	}
	root, err := st.Commit(params.Rules{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return root, tdb
}

// collectProof returns the trie nodes eth_getProof would send for the 0x88
// account and keys.
func collectProof(t *testing.T, tdb *triedb.Database, root common.Hash, keys []common.Hash) [][]byte {
	t.Helper()
	out := memorydb.New()
	tr, err := trie.NewStateTrie(trie.StateTrieID(root), tdb)
	if err != nil {
		t.Fatal(err)
	}
	addrHash := crypto.Keccak256Hash(common.MasternodeVotingSMCBinary[:])
	if err := tr.Prove(addrHash[:], out); err != nil {
		t.Fatal(err)
	}
	acct, err := tr.GetAccount(common.MasternodeVotingSMCBinary)
	if err != nil || acct == nil {
		t.Fatalf("account: %v %v", acct, err)
	}
	str, err := trie.NewStateTrie(trie.StorageTrieID(root, addrHash, acct.Root), tdb)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := str.Prove(crypto.Keccak256(k[:]), out); err != nil {
			t.Fatal(err)
		}
	}
	var nodes [][]byte
	it := out.NewIterator(nil, nil)
	defer it.Release()
	for it.Next() {
		nodes = append(nodes, common.CopyBytes(it.Value()))
	}
	return nodes
}

func poolKeys(addrs []common.Address) []common.Hash {
	keys := []common.Hash{state.CandidatesLenKey()}
	for i, a := range addrs {
		keys = append(keys, state.CandidateElementKey(uint64(i)), state.CandidateCapKey(a))
	}
	return keys
}

// provenState opens root over only the given nodes, keyed by their own hash.
func provenState(t *testing.T, root common.Hash, nodes [][]byte) (*state.StateDB, error) {
	t.Helper()
	memdb := rawdb.NewMemoryDatabase()
	for _, n := range nodes {
		rawdb.WriteLegacyTrieNode(memdb, crypto.Keccak256Hash(n), n)
	}
	return state.New(root, state.NewDatabase(triedb.NewDatabase(memdb, triedb.HashDefaults), nil))
}

func provenFixture(t *testing.T, n int) (root common.Hash, addrs []common.Address, nodes [][]byte, tdb *triedb.Database) {
	t.Helper()
	_, addrs = genNKeys(t, n)
	root, tdb = commitCandidateState(t, addrs)
	return root, addrs, collectProof(t, tdb, root, poolKeys(addrs)), tdb
}

// Witness: the pool built from proofs equals the one built from the real state,
// in the same order, and is stored as version 7.
func TestProvenWitnessEqualsStateRebuild(t *testing.T) {
	root, addrs, nodes, tdb := provenFixture(t, 9)
	gapHeader := &types.Header{Number: big.NewInt(450), Root: root, Difficulty: big.NewInt(1)}
	chain := &mockChain1330WithState{mockChain1330{byNumber: map[uint64]*types.Header{450: gapHeader}}}

	real, err := state.New(root, state.NewDatabase(tdb, nil))
	if err != nil {
		t.Fatal(err)
	}
	chain.statedb = real
	eng, db := newA894Engine(t)
	want := eng.rebuildGapSnapshotFromState(chain, 450, gapHeader)
	if want == nil || want.Version != 6 || len(want.NextEpochCandidates) != len(addrs) {
		t.Fatalf("state rebuild: %+v", want)
	}

	st, err := provenState(t, root, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.SeedGapSnapshotFromState(chain, gapHeader, st); err != nil {
		t.Fatal(err)
	}
	got, err := loadSnapshot(db, gapHeader.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != snapshotVersionProven {
		t.Fatalf("version %d, want %d", got.Version, snapshotVersionProven)
	}
	if len(got.NextEpochCandidates) != len(want.NextEpochCandidates) {
		t.Fatalf("pool %d, want %d", len(got.NextEpochCandidates), len(want.NextEpochCandidates))
	}
	for i := range want.NextEpochCandidates {
		if got.NextEpochCandidates[i] != want.NextEpochCandidates[i] {
			t.Fatalf("pool[%d] = %x, want %x", i, got.NextEpochCandidates[i], want.NextEpochCandidates[i])
		}
	}
	if !eng.HasProvenGapSnapshot(gapHeader.Hash()) {
		t.Fatal("HasProvenGapSnapshot false after seeding")
	}
}

// Witness negatives: any dropped or tampered node, an absent account and a
// wrong root all end in an error and store nothing.
func TestProvenWitnessNegatives(t *testing.T) {
	root, addrs, nodes, _ := provenFixture(t, 6)
	gapHeader := &types.Header{Number: big.NewInt(450), Root: root, Difficulty: big.NewInt(1)}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{450: gapHeader}}
	eng, db := newA894Engine(t)

	seed := func(root common.Hash, nodes [][]byte) error {
		st, err := provenState(t, root, nodes)
		if err != nil {
			return err
		}
		return eng.SeedGapSnapshotFromState(chain, gapHeader, st)
	}
	for i := range nodes {
		dropped := append(append([][]byte{}, nodes[:i]...), nodes[i+1:]...)
		if err := seed(root, dropped); err == nil {
			t.Fatalf("dropped node %d: want error", i)
		}
		tampered := append([][]byte{}, nodes...)
		tampered[i] = append(common.CopyBytes(nodes[i]), 0x00)
		if err := seed(root, tampered); err == nil {
			t.Fatalf("tampered node %d: want error", i)
		}
	}
	if err := seed(common.Hash{1}, nodes); err == nil {
		t.Fatal("wrong root: want error")
	}
	// Absent account: a proof for an empty trie, then for another account only.
	if err := seed(types.EmptyRootHash, nil); err == nil {
		t.Fatal("absent account: want error")
	}
	otherRoot, tdb := commitCandidateState(t, addrs[:1])
	if err := seed(otherRoot, collectProof(t, tdb, otherRoot, nil)); err == nil {
		t.Fatal("account without candidates: want error")
	}
	if snap, _ := loadSnapshot(db, gapHeader.Hash()); snap != nil {
		t.Fatal("a refused proof stored a snapshot")
	}
}

// Guards: v7 below the pivot is returned to non-signing callers on the cache and
// DB paths; v4, v5 and v6 are still refused.
func TestProvenGuardBelowPivot(t *testing.T) {
	gapHeader := &types.Header{Number: big.NewInt(450)}
	switchHeader := &types.Header{Number: big.NewInt(1000), Validators: common.Address{0x01}.Bytes()}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{450: gapHeader, 1000: switchHeader}}
	for _, fromDB := range []bool{false, true} {
		for _, v := range []uint64{4, 5, 6, 7} {
			eng, db := newA894Engine(t)
			rawdb.WriteLastPivotNumber(db, 2000)
			snap := newSnapshot(450, gapHeader.Hash(), []common.Address{{0x01}})
			snap.Version = v
			if fromDB {
				if err := storeSnapshot(snap, db); err != nil {
					t.Fatal(err)
				}
			} else {
				eng.snapshots.Add(snap.Hash, snap)
			}
			got, err := eng.getSnapshot(chain, 1000, false, nil)
			if v == snapshotVersionProven {
				if err != nil || got == nil || got.Version != v {
					t.Fatalf("fromDB=%v v%d: want the snapshot, got %v, %v", fromDB, v, got, err)
				}
			} else if !errors.Is(err, utils.ErrV2SnapshotMissing) {
				t.Fatalf("fromDB=%v v%d: want ErrV2SnapshotMissing, got %v", fromDB, v, err)
			}
		}
	}
}

// Restart: with an empty cache, a signing call and then a non-signing call both
// succeed, and the stored snapshot stays version 7.
func TestProvenSurvivesRestartAndSigningCall(t *testing.T) {
	_, db := newA894Engine(t)
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
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
	_, masternodes := genNKeys(t, 5)
	_, standby := genNKeys(t, 2)
	const gapNumber = uint64(4050)
	gapHeader := &types.Header{Number: big.NewInt(int64(gapNumber)), Difficulty: big.NewInt(1), Extra: []byte{0x01}}
	switchHeader := makeRoundEpochSwitchHeader(t, 4500, 4500, 4499, masternodes)
	chain := &mockChainForGapNumber1327{
		byHash:   map[common.Hash]*types.Header{gapHeader.Hash(): gapHeader, switchHeader.Hash(): switchHeader},
		byNumber: map[uint64]*types.Header{gapNumber: gapHeader, 4500: switchHeader},
	}
	pool := append(append([]common.Address{}, masternodes...), standby...)
	snap := newSnapshot(gapNumber, gapHeader.Hash(), pool)
	snap.Version = snapshotVersionProven
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatal(err)
	}
	rawdb.WriteLastPivotNumber(db, 6000)
	// Fresh engine over the same DB: empty cache, as after a restart.
	eng := createEngine(cfg, db, nil, nil)

	got, err := eng.getSnapshot(chain, gapNumber, true, nil)
	if err != nil || got == nil || len(got.NextEpochCandidates) != len(pool) {
		t.Fatalf("forSigning after restart: %v, %v", got, err)
	}
	if got.Version < snapshotVersionProven {
		t.Fatalf("forSigning returned version %d, want >= 7", got.Version)
	}
	if got, err := eng.getSnapshot(chain, 4500, false, nil); err != nil || got == nil || got.Version != snapshotVersionProven {
		t.Fatalf("non-signing after the signing call: %v, %v", got, err)
	}
	if stored, err := loadSnapshot(db, gapHeader.Hash()); err != nil || stored.Version != snapshotVersionProven {
		t.Fatalf("stored snapshot after the signing call: %v, %v", stored, err)
	}
}

// Overwrite: header-seed writers leave a version 7 snapshot alone.
func TestProvenNotOverwrittenByHeaderSeed(t *testing.T) {
	eng, db := newA894Engine(t)
	gapHeader := &types.Header{Number: big.NewInt(450), Difficulty: big.NewInt(1)}
	pool := []common.Address{{0x01}, {0x02}, {0x03}}
	snap := newSnapshot(450, gapHeader.Hash(), pool)
	snap.Version = snapshotVersionProven
	if err := storeSnapshot(snap, db); err != nil {
		t.Fatal(err)
	}
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{450: gapHeader}}
	for _, cached := range []bool{false, true} {
		if cached {
			eng.snapshots.Add(snap.Hash, snap)
			if err := db.Delete(append([]byte("XDPoS-V2-"), snap.Hash[:]...)); err != nil {
				t.Fatal(err)
			}
		}
		if err := eng.UpdateMasternodesWithPenalties(chain, gapHeader, []common.Address{{0x09}}, []common.Address{{0x08}}); err != nil {
			t.Fatal(err)
		}
		cur, ok := eng.snapshots.Get(snap.Hash)
		if !cached {
			cur, _ = loadSnapshot(db, snap.Hash)
			ok = cur != nil
		}
		if !ok || cur.Version != snapshotVersionProven || len(cur.NextEpochCandidates) != len(pool) {
			t.Fatalf("cached=%v: header seed replaced the proven snapshot: %+v", cached, cur)
		}
	}
	// Control: a version 6 snapshot is still replaced.
	other := &types.Header{Number: big.NewInt(1350), Difficulty: big.NewInt(1)}
	s6 := newSnapshot(1350, other.Hash(), pool)
	s6.Version = 6
	_ = storeSnapshot(s6, db)
	if err := eng.UpdateMasternodesWithPenalties(chain, other, []common.Address{{0x09}}, nil); err != nil {
		t.Fatal(err)
	}
	if cur, _ := loadSnapshot(db, other.Hash()); cur == nil || len(cur.NextEpochCandidates) != 1 {
		t.Fatalf("control: version 6 snapshot should be replaced, got %+v", cur)
	}
}

// End to end: an epoch switch whose gap lies below the pivot cannot be verified
// before the gap pool is proven, passes after, and a forged list still fails.
func TestProvenEpochSwitchEndToEnd(t *testing.T) {
	eng, chain, keys, mns, p, h := esFixture(t, true)
	rawdb.WriteLastPivotNumber(eng.db, 500) // gap 450 is below the pivot
	h.Validators = packValidators(mns)
	esSeal(t, h, keys[0])
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); !errors.Is(err, utils.ErrV2SnapshotMissing) {
		t.Fatalf("before the gap pool is proven: want ErrV2SnapshotMissing, got %v", err)
	}

	root, tdb := commitCandidateState(t, mns)
	st, err := provenState(t, root, collectProof(t, tdb, root, poolKeys(mns)))
	if err != nil {
		t.Fatal(err)
	}
	gap := chain.GetHeaderByNumber(450)
	if err := eng.SeedGapSnapshotFromState(chain, gap, st); err != nil {
		t.Fatal(err)
	}
	// The leader (index 1800 % 900 % N = 0) is the first of the stake-sorted pool.
	snap, err := loadSnapshot(eng.db, gap.Hash())
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range mns {
		if a == snap.NextEpochCandidates[0] {
			h.Validators = packValidators(snap.NextEpochCandidates)
			esSeal(t, h, keys[i])
		}
	}
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); err != nil {
		t.Fatalf("after the gap pool is proven: %v", err)
	}

	forgedKeys, forged := genNKeys(t, 2)
	f := headerClone(h)
	f.Validators = packValidators(forged)
	esSeal(t, f, forgedKeys[0])
	if err := eng.verifyHeader(chain, f, []*types.Header{p}, true); !errors.Is(err, utils.ErrValidatorsNotLegit) {
		t.Fatalf("forged switch: want ErrValidatorsNotLegit, got %v", err)
	}
}
