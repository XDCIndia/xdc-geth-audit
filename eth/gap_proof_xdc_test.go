// Copyright (c) 2026 XDC Network
// F-2026-20648: below-pivot gap pools proven from eth_getProof.

package eth

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
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
