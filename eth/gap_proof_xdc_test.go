// Copyright (c) 2026 XDC Network
// F-2026-20648: below-pivot gap pools proven from eth_getProof.

package eth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
)

// The two live wedges: both pivots need gap 109,697,850.
func TestGapsBelowPivotLiveScenarios(t *testing.T) {
	for _, P := range []uint64{109_697_859, 109_698_489} {
		if !slices.Contains(gapsBelowPivot(P, 900, 450, false), 109_697_850) {
			t.Errorf("pivot %d: gap 109697850 missing from %v", P, gapsBelowPivot(P, 900, 450, false))
		}
	}
	if got := gapsBelowPivot(109_698_489, 900, 450, true); !slices.Equal(got, []uint64{109_697_850, 109_696_950, 109_696_050}) {
		t.Errorf("tiered set %v", got)
	}
}

// Property: for every pivot and every switch S in (P, P+2E], the switch's gap is
// at or above the pivot (state exists) or in the set.
func TestGapsBelowPivotCoversEverySwitch(t *testing.T) {
	const E, G = 900, 450
	for P := uint64(1); P < 4*E+7; P++ {
		set := gapsBelowPivot(P, E, G, false)
		for S := P + 1; S <= P+2*E; S++ {
			if S/E*E < G {
				continue // no gap below the first epoch boundary
			}
			g := S/E*E - G
			if g >= P || g == 0 || slices.Contains(set, g) {
				continue
			}
			t.Fatalf("pivot %d switch %d: gap %d not covered by %v", P, S, g, set)
		}
		for _, g := range set {
			if g == 0 || g >= P {
				t.Fatalf("pivot %d: gap %d out of range", P, g)
			}
		}
	}
	if got := gapsBelowPivot(10, 900, 450, true); len(got) != 0 {
		t.Fatalf("pivot below the first gap: %v", got)
	}
}

// commitPool commits a 0x88 pool and returns its root and trie database.
func commitPool(t *testing.T, n int) (common.Hash, *triedb.Database, []common.Address) {
	t.Helper()
	tdb := triedb.NewDatabase(rawdb.NewMemoryDatabase(), triedb.HashDefaults)
	st, err := state.New(types.EmptyRootHash, state.NewDatabase(tdb, nil))
	if err != nil {
		t.Fatal(err)
	}
	c := common.MasternodeVotingSMCBinary
	st.SetNonce(c, 1, 0)
	st.SetState(c, state.CandidatesLenKey(), common.BigToHash(big.NewInt(int64(n))))
	addrs := make([]common.Address, n)
	for i := range addrs {
		addrs[i] = common.Address{byte((i + 1) >> 8), byte(i + 1), 0xaa}
		st.SetState(c, state.CandidateElementKey(uint64(i)), common.BytesToHash(addrs[i].Bytes()))
		st.SetState(c, state.CandidateCapKey(addrs[i]), common.BigToHash(big.NewInt(int64(n-i)*1_000_000)))
	}
	root, err := st.Commit(params.Rules{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return root, tdb, addrs
}

type proofResp struct {
	AccountProof []hexutil.Bytes `json:"accountProof"`
	StorageProof []struct {
		Proof []hexutil.Bytes `json:"proof"`
	} `json:"storageProof"`
}

// getProof answers eth_getProof for the 0x88 account at root, one proof per key.
func getProof(t *testing.T, tdb *triedb.Database, root common.Hash, keys []string) proofResp {
	t.Helper()
	var res proofResp
	nodes := func(f func(w *memorydb.Database)) []hexutil.Bytes {
		w := memorydb.New()
		f(w)
		var out []hexutil.Bytes
		it := w.NewIterator(nil, nil)
		defer it.Release()
		for it.Next() {
			out = append(out, common.CopyBytes(it.Value()))
		}
		return out
	}
	tr, err := trie.NewStateTrie(trie.StateTrieID(root), tdb)
	if err != nil {
		t.Fatal(err)
	}
	addrHash := crypto.Keccak256Hash(common.MasternodeVotingSMCBinary[:])
	res.AccountProof = nodes(func(w *memorydb.Database) { tr.Prove(addrHash[:], w) })
	acct, err := tr.GetAccount(common.MasternodeVotingSMCBinary)
	if err != nil || acct == nil {
		t.Fatal("account", err)
	}
	str, err := trie.NewStateTrie(trie.StorageTrieID(root, addrHash, acct.Root), tdb)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		key := common.HexToHash(k)
		res.StorageProof = append(res.StorageProof, struct {
			Proof []hexutil.Bytes `json:"proof"`
		}{nodes(func(w *memorydb.Database) { str.Prove(crypto.Keccak256(key[:]), w) })})
	}
	return res
}

// proofServer serves eth_getProof; mutate may alter the answer (nil = honest).
func proofServer(t *testing.T, tdb *triedb.Database, root common.Hash, mutate func(*proofResp)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Method != "eth_getProof" || len(req.Params) != 3 {
			http.Error(w, "bad request", 400)
			return
		}
		var keys []string
		json.Unmarshal(req.Params[1], &keys)
		if len(keys) > gapProofKeysPerCall {
			http.Error(w, "too many keys", 400)
			return
		}
		res := getProof(t, tdb, root, keys)
		if mutate != nil {
			mutate(&res)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func prove(t *testing.T, urls []string, root common.Hash) (*state.StateDB, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return proveGapStateFrom(ctx, urls, 450, root)
}

// Honest server: the pool reads back, across more than one round of keys.
func TestProveGapStateHonest(t *testing.T) {
	root, tdb, addrs := commitPool(t, 300) // 300 elements and 300 caps: two chunks each
	srv := proofServer(t, tdb, root, nil)
	st, host, err := prove(t, []string{srv.URL}, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.GetCandidates(st); !slices.Equal(got, addrs) || st.Error() != nil {
		t.Fatalf("pool mismatch: %d candidates, err %v", len(got), st.Error())
	}
	for i, a := range addrs {
		if want := big.NewInt(int64(len(addrs)-i) * 1_000_000); state.GetCandidateCap(st, a).Cmp(want) != 0 {
			t.Fatalf("cap of candidate %d wrong", i)
		}
	}
	if !strings.HasPrefix(host, "127.0.0.1") {
		t.Fatalf("host %q", host)
	}
}

// Dishonest or broken servers end in an error, never a wrong pool.
func TestProveGapStateRefusesBadProofs(t *testing.T) {
	root, tdb, _ := commitPool(t, 8)
	otherRoot, otherDB, _ := commitPool(t, 3)

	dropFirst := func(r *proofResp) {
		if len(r.StorageProof) > 0 && len(r.StorageProof[0].Proof) > 0 {
			r.StorageProof[0].Proof = r.StorageProof[0].Proof[:len(r.StorageProof[0].Proof)-1]
		}
	}
	for name, mutate := range map[string]func(*proofResp){
		"dropped node": dropFirst,
		"tampered node": func(r *proofResp) {
			for i := range r.StorageProof {
				for j := range r.StorageProof[i].Proof {
					r.StorageProof[i].Proof[j] = append(common.CopyBytes(r.StorageProof[i].Proof[j]), 0x01)
				}
			}
		},
		"no account proof": func(r *proofResp) { r.AccountProof = nil },
		"no storage proof": func(r *proofResp) { r.StorageProof = nil },
	} {
		srv := proofServer(t, tdb, root, mutate)
		if st, _, err := prove(t, []string{srv.URL}, root); err == nil {
			t.Errorf("%s: accepted (state err %v)", name, st.Error())
		}
	}
	// A proof for another state, asked against this root.
	wrong := proofServer(t, otherDB, otherRoot, nil)
	if _, _, err := prove(t, []string{wrong.URL}, root); err == nil {
		t.Error("proof of another root accepted")
	}
	// Right proof, wrong expected root (e.g. a gap header from another chain).
	srv := proofServer(t, tdb, root, nil)
	if _, _, err := prove(t, []string{srv.URL}, common.Hash{0x42}); err == nil {
		t.Error("honest proof accepted against a different root")
	}
	// Absent account: the empty root has no 0x88 account.
	if _, _, err := prove(t, []string{srv.URL}, types.EmptyRootHash); err == nil {
		t.Error("absent account accepted")
	}
	// Garbage and a dead endpoint.
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not json")) }))
	t.Cleanup(garbage.Close)
	if _, _, err := prove(t, []string{garbage.URL}, root); err == nil {
		t.Error("garbage response accepted")
	}
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	if _, _, err := prove(t, []string{deadURL}, root); err == nil {
		t.Error("dead endpoint: want error")
	}
}

// The endpoints are tried in order: a dead and a lying one before an honest one.
func TestProveGapStateFallsThroughToHonestEndpoint(t *testing.T) {
	root, tdb, addrs := commitPool(t, 5)
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	liar := proofServer(t, tdb, root, func(r *proofResp) { r.AccountProof = nil })
	honest := proofServer(t, tdb, root, nil)
	st, host, err := prove(t, []string{deadURL, liar.URL, honest.URL}, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.GetCandidates(st), addrs) {
		t.Fatal("pool mismatch")
	}
	if want := strings.TrimPrefix(honest.URL, "http://"); host != want {
		t.Fatalf("host %q, want %q", host, want)
	}
}

// A gap header must be canonical and below the pivot.
func TestGapHeaderOK(t *testing.T) {
	n := newXDCFakeNet(t, 10, 10)
	canon := func(num uint64) common.Hash { return n.chain.GetCanonicalHash(num) }
	h5 := n.chain.GetHeaderByNumber(5)
	if err := gapHeaderOK(canon, h5, 8); err != nil {
		t.Fatalf("canonical header below the pivot: %v", err)
	}
	if err := gapHeaderOK(canon, h5, 5); err == nil {
		t.Fatal("header at the pivot accepted")
	}
	side := types.CopyHeader(h5)
	side.Extra = []byte("sibling")
	if err := gapHeaderOK(canon, side, 8); err == nil {
		t.Fatal("non-canonical header accepted")
	}
}

// Without a recorded pivot (full sync) or an XDPoS engine nothing is fetched.
func TestProveGapPoolsNoopWithoutPivotOrXDPoS(t *testing.T) {
	n := newXDCFakeNet(t, 5, 5)
	s := n.newTestXDCSyncer(t)
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- struct{}{} }))
	t.Cleanup(srv.Close)
	s.handler.snapGapRPC = []string{srv.URL}
	s.proveGapPools(context.Background())
	select {
	case <-hit:
		t.Fatal("RPC contacted")
	default:
	}
}

// gapPivotNet is a fake chain whose genesis holds a 0x88 pool; blocks 1..10 change
// nothing, so the pool at the gap (block 3) equals the pool at the pivot (block 8).
func gapPivotNet(t *testing.T) (*xdcFakeNet, *engine_v2.XDPoS_v2, ethdb.Database) {
	t.Helper()
	storage := map[common.Hash]common.Hash{state.CandidatesLenKey(): common.BigToHash(big.NewInt(6))}
	for i := 0; i < 6; i++ {
		a := common.Address{byte(i + 1), 0xbb}
		storage[state.CandidateElementKey(uint64(i))] = common.BytesToHash(a.Bytes())
		storage[state.CandidateCapKey(a)] = common.BigToHash(big.NewInt(int64(10-i/2) * 1_000_000)) // ties in pairs
	}
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
		Alloc:      types.GenesisAlloc{common.MasternodeVotingSMCBinary: {Nonce: 1, Storage: storage}},
	}
	n := newXDCFakeNetGspec(t, 10, 10, ethash.NewFaker(), gspec)
	db := rawdb.NewMemoryDatabase()
	return n, newTestV2(db), db
}

func newTestV2(db ethdb.Database) *engine_v2.XDPoS_v2 {
	return engine_v2.NewIsolated(&params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: 900, Gap: 450,
			V2: &params.V2{
				SwitchBlock: new(big.Int),
				CurrentConfig: &params.V2Config{
					MinePeriod: 2, TimeoutPeriod: 10, CertThreshold: 2.0 / 3.0,
					ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 2.0, MaxExponent: 6},
				},
				AllConfigs: map[uint64]*params.V2Config{},
			},
		},
	}, db, nil, nil)
}

func snapBlob(t *testing.T, db ethdb.Database, h common.Hash) []byte {
	t.Helper()
	blob, err := db.Get(append([]byte("XDPoS-V2-"), h[:]...))
	if err != nil {
		t.Fatalf("no snapshot stored: %v", err)
	}
	return blob
}

func noReceipts(*types.Header) types.Receipts { return nil }

// A 0x88 log of block n, as a bloom-carrying header copy and its receipts.
func withVotingLog(h *types.Header) (*types.Header, types.Receipts) {
	rs := types.Receipts{{Status: 1, CumulativeGasUsed: 21000,
		Logs: []*types.Log{{Address: common.MasternodeVotingSMCBinary, Topics: []common.Hash{{1}}}}}}
	rs[0].Bloom = types.CreateBloom(rs[0])
	c := types.CopyHeader(h)
	c.Bloom = types.CreateBloom(rs[0])
	c.ReceiptHash = types.DeriveSha(rs, trie.NewStackTrie(nil))
	return c, rs
}

// No 0x88 log between the gap and the pivot: the pool comes from the pivot state
// and equals the one read from the gap's own state. No RPC is configured or used.
func TestGapPoolFromPivotState(t *testing.T) {
	n, v2, db := gapPivotNet(t)
	s := n.newTestXDCSyncer(t)
	gapHdr, pivotHdr := n.chain.GetHeaderByNumber(3), n.chain.GetHeaderByNumber(8)
	headerAt := func(num uint64) *types.Header { return n.chain.GetHeaderByNumber(num) }

	path, err := s.proveGap(context.Background(), v2, gapHdr, pivotHdr, headerAt, noReceipts)
	if err != nil || path != "pivot" {
		t.Fatalf("path %q, err %v", path, err)
	}
	if !v2.HasProvenGapSnapshot(gapHdr.Hash()) {
		t.Fatal("no version 7 snapshot")
	}

	// Reference: the pool read from the state at the gap itself.
	st, err := n.chain.StateAt(gapHdr.Root, gapHdr.Number, gapHdr.Time)
	if err != nil {
		t.Fatal(err)
	}
	_, _, refDB := gapPivotNet(t)
	refEng := newTestV2(refDB)
	if err := refEng.SeedGapSnapshotFromState(n.chain, gapHdr, st); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapBlob(t, db, gapHdr.Hash()), snapBlob(t, refDB, gapHdr.Hash())) {
		t.Fatal("pivot-state pool differs from the gap-state pool")
	}
}

// A 0x88 log in the window, a missing or wrong receipt, or a missing header
// refuses the pivot path; the RPC proofs are then used, and without an RPC
// nothing is stored.
func TestGapPoolPivotRefusedFallsBackToRPC(t *testing.T) {
	for _, name := range []string{"0x88 log in the window", "receipts missing", "receipts do not match", "header missing"} {
		t.Run(name, func(t *testing.T) {
			n, v2, _ := gapPivotNet(t)
			s := n.newTestXDCSyncer(t)
			gapHdr, pivotHdr := n.chain.GetHeaderByNumber(3), n.chain.GetHeaderByNumber(8)
			srv := proofServer(t, n.chain.TrieDB(), gapHdr.Root, nil)

			logHdr, logReceipts := withVotingLog(n.chain.GetHeaderByNumber(6))
			blk6, receipts := logHdr, noReceipts
			var want error
			switch name {
			case "0x88 log in the window":
				receipts, want = func(*types.Header) types.Receipts { return logReceipts }, errGapPoolChanged
			case "receipts do not match":
				blk6 = types.CopyHeader(logHdr)
				blk6.ReceiptHash = common.Hash{9}
				receipts = func(*types.Header) types.Receipts { return logReceipts }
			case "header missing":
				blk6 = nil
			}
			headerAt := func(num uint64) *types.Header {
				if num == 6 {
					return blk6
				}
				return n.chain.GetHeaderByNumber(num)
			}
			if _, err := noVotingLogs(3, 8, headerAt, receipts); err == nil || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("noVotingLogs err %v", err)
			}
			s.handler.snapGapRPC = nil
			if path, err := s.proveGap(context.Background(), v2, gapHdr, pivotHdr, headerAt, receipts); err == nil {
				t.Fatalf("seeded without an RPC via %q", path)
			}
			if v2.HasProvenGapSnapshot(gapHdr.Hash()) {
				t.Fatal("snapshot stored without a proof")
			}
			s.handler.snapGapRPC = []string{srv.URL}
			path, err := s.proveGap(context.Background(), v2, gapHdr, pivotHdr, headerAt, receipts)
			if err != nil || path != "rpc" || !v2.HasProvenGapSnapshot(gapHdr.Hash()) {
				t.Fatalf("path %q err %v", path, err)
			}
		})
	}
}
