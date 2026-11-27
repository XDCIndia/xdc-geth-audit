// Copyright (c) 2026 XDC Network
// F-2026-20648: gap snapshots below the snap pivot proven by Merkle proof
// (version 7) are accepted by getSnapshot, survive a restart, are never replaced
// by header seeds, and a pool built from proofs equals the state-built one.
//
// Run: go test -count=1 ./consensus/XDPoS/engines/engine_v2/ -run TestProven

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
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
