package XDPoS

import (
	"math/big"
	"sort"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	xdc_sort "github.com/ethereum/go-ethereum/common/sort"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// mapHeaderReader is a consensus.ChainHeaderReader over an in-memory header set.
type mapHeaderReader map[common.Hash]*types.Header

func (mapHeaderReader) Config() *params.ChainConfig  { return &params.ChainConfig{} }
func (mapHeaderReader) CurrentHeader() *types.Header { return nil }
func (r mapHeaderReader) GetHeader(h common.Hash, _ uint64) *types.Header {
	return r[h]
}
func (r mapHeaderReader) GetHeaderByHash(h common.Hash) *types.Header { return r[h] }
func (r mapHeaderReader) GetHeaderByNumber(n uint64) *types.Header {
	for _, h := range r {
		if h.Number.Uint64() == n {
			return h
		}
	}
	return nil
}

// candidateState builds a state whose 0x88 voting contract lists addrs with
// the given stakes, laid out like the XDCValidator contract storage.
func candidateState(t *testing.T, addrs []common.Address, stakes []int64) *state.StateDB {
	t.Helper()
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	slot8 := common.BigToHash(big.NewInt(8))
	statedb.SetState(common.MasternodeVotingSMCBinary, slot8, common.BigToHash(big.NewInt(int64(len(addrs)))))
	for i, a := range addrs {
		statedb.SetState(common.MasternodeVotingSMCBinary, state.GetLocDynamicArrAtElement(slot8, uint64(i), 1), common.BytesToHash(a.Bytes()))
		capLoc := new(big.Int).Add(state.GetLocMappingAtKey(common.BytesToHash(a.Bytes()), 1), big.NewInt(1))
		statedb.SetState(common.MasternodeVotingSMCBinary, common.BigToHash(capLoc), common.BigToHash(big.NewInt(stakes[i])))
	}
	return statedb
}

// v1Segment builds signed V1 headers 1..n on top of a genesis whose snapshot
// (the stale set) is seeded into the engine's recents.
func v1Segment(t *testing.T, engine *XDPoS, n int, stale []common.Address) []*types.Header {
	t.Helper()
	key, _ := crypto.GenerateKey()
	genesis := &types.Header{Number: big.NewInt(0), Difficulty: big.NewInt(1)}
	engine.recents.Add(genesis.Hash(), newSnapshot(engine.config, engine.signatures, 0, genesis.Hash(), stale))
	headers := []*types.Header{genesis}
	for i := 1; i <= n; i++ {
		h := &types.Header{
			ParentHash: headers[i-1].Hash(),
			Number:     big.NewInt(int64(i)),
			Difficulty: big.NewInt(1),
			Extra:      make([]byte, extraVanity+extraSeal),
		}
		sig, err := crypto.Sign(sigHash(h).Bytes(), key)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[extraVanity:], sig)
		headers = append(headers, h)
	}
	return headers
}

func addrs(n int) []common.Address {
	out := make([]common.Address, n)
	for i := range out {
		out[i] = common.BigToAddress(big.NewInt(int64(0x1000 + i)))
	}
	return out
}

// canonicalSorted mirrors XDPoSChain utils.SortMasternodesByStakeDesc verbatim.
func canonicalSorted(as []common.Address, stakes []int64) []common.Address {
	ms := make([]Masternode, len(as))
	for i := range as {
		ms[i] = Masternode{Address: as[i], Stake: big.NewInt(stakes[i])}
	}
	xdc_sort.Slice(ms, func(i, j int) bool { return ms[i].Stake.Cmp(ms[j].Stake) >= 0 })
	out := make([]common.Address, len(ms))
	for i, m := range ms {
		out[i] = m.Address
	}
	return out
}

func sameSet(t *testing.T, got map[common.Address]struct{}, want []common.Address) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("signer count: got %d, want %d", len(got), len(want))
	}
	for _, a := range want {
		if _, ok := got[a]; !ok {
			t.Fatalf("signer %x missing from snapshot", a)
		}
	}
}

// TestUpdateMasternodesV1GapRefresh: writing a V1 gap block replaces the stale
// signer set with the candidates of that block's state, and the snapshot at the
// next checkpoint's parent (the one checkpoint verification uses) carries it.
// Mirrors mainnet checkpoint 516,600, where the stale V1 snapshot had 3 signers
// and the header listed 17.
func TestUpdateMasternodesV1GapRefresh(t *testing.T) {
	cfg := &params.XDPoSConfig{Period: 2, Epoch: 10, Gap: 5}
	db := rawdb.NewMemoryDatabase()
	engine := New(cfg, db)
	stale := addrs(3)
	headers := v1Segment(t, engine, 9, stale)

	cands := addrs(17) // A..Q
	stakes := make([]int64, len(cands))
	for i := range stakes {
		stakes[i] = int64(100 + i)
	}
	gap := headers[5]
	// The gap header is not readable from the chain yet, as during the write.
	chain := mapHeaderReader{}
	for _, h := range headers[:5] {
		chain[h.Hash()] = h
	}
	if err := engine.UpdateMasternodesV1(chain, gap, candidateState(t, cands, stakes)); err != nil {
		t.Fatalf("UpdateMasternodesV1: %v", err)
	}
	for _, h := range headers[5:] {
		chain[h.Hash()] = h
	}
	snap, err := engine.snapshot(chain, 9, headers[9].Hash(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sameSet(t, snap.Signers, cands)

	// The refreshed set survives a restart (fresh engine over the same db).
	reloaded, err := loadSnapshot(engine.config, engine.signatures, db, gap.Hash())
	if err != nil {
		t.Fatal(err)
	}
	sameSet(t, reloaded.Signers, cands)
}

// TestUpdateMasternodesV1CapAndOrder: the set is the stake-sorted candidates
// capped at MaxMasternodes (18) before TIPIncreaseMasternodes and at
// MaxMasternodesV2 from it on; ties straddling the cap resolve like
// canonical's frozen sort.
func TestUpdateMasternodesV1CapAndOrder(t *testing.T) {
	cands := addrs(24)
	stakes := make([]int64, len(cands))
	for i := range stakes {
		stakes[i] = 500 // all tied: which 18 survive is decided by the sort alone
	}
	stakes[0], stakes[23] = 900, 800
	want := canonicalSorted(cands, stakes)

	// Guard: the input must actually separate the frozen sort from a stable one.
	stableMs := make([]Masternode, len(cands))
	for i := range cands {
		stableMs[i] = Masternode{Address: cands[i], Stake: big.NewInt(stakes[i])}
	}
	sort.SliceStable(stableMs, func(i, j int) bool { return stableMs[i].Stake.Cmp(stableMs[j].Stake) > 0 })
	differs := false
	inWant := map[common.Address]bool{}
	for _, a := range want[:MaxMasternodes] {
		inWant[a] = true
	}
	for _, m := range stableMs[:MaxMasternodes] {
		if !inWant[m.Address] {
			differs = true
		}
	}
	if !differs {
		t.Fatal("test input does not distinguish the frozen sort from a stable sort")
	}

	defer func(old *big.Int) { TIPIncreaseMasternodes = old }(TIPIncreaseMasternodes)
	for _, tc := range []struct {
		name string
		tip  *big.Int
		want []common.Address
	}{
		{"before TIPIncreaseMasternodes", big.NewInt(1000), want[:MaxMasternodes]},
		{"after TIPIncreaseMasternodes", big.NewInt(0), want[:min(len(want), MaxMasternodesV2)]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			TIPIncreaseMasternodes = tc.tip
			engine := New(&params.XDPoSConfig{Period: 2, Epoch: 10, Gap: 5}, rawdb.NewMemoryDatabase())
			headers := v1Segment(t, engine, 5, addrs(3))
			chain := mapHeaderReader{}
			for _, h := range headers[:5] {
				chain[h.Hash()] = h
			}
			if err := engine.UpdateMasternodesV1(chain, headers[5], candidateState(t, cands, stakes)); err != nil {
				t.Fatal(err)
			}
			snap, err := engine.snapshot(chain, 5, headers[5].Hash(), nil)
			if err != nil {
				t.Fatal(err)
			}
			sameSet(t, snap.Signers, tc.want)
		})
	}
}

// TestUpdateMasternodesV1NoCandidates: like canonical, an empty candidate set
// is an error rather than an empty signer set.
func TestUpdateMasternodesV1NoCandidates(t *testing.T) {
	engine := New(&params.XDPoSConfig{Period: 2, Epoch: 10, Gap: 5}, rawdb.NewMemoryDatabase())
	headers := v1Segment(t, engine, 5, addrs(3))
	if err := engine.UpdateMasternodesV1(mapHeaderReader{}, headers[5], candidateState(t, nil, nil)); err == nil {
		t.Fatal("expected an error for a gap block without candidates")
	}
}

// TestUpdateMasternodesV1DropsStaleDescendants: header verification runs ahead
// of block writes, so a block after the gap block can be verified (and its
// snapshot cached from the pre-refresh set) before the gap block is written and
// refreshed. The walk back from the checkpoint must not stop at that stale
// snapshot (mainnet replay: checkpoint 516,600 saw the previous epoch's set).
func TestUpdateMasternodesV1DropsStaleDescendants(t *testing.T) {
	cfg := &params.XDPoSConfig{Period: 2, Epoch: 10, Gap: 5}
	engine := New(cfg, rawdb.NewMemoryDatabase())
	stale := addrs(3)
	headers := v1Segment(t, engine, 9, stale)

	all := mapHeaderReader{}
	for _, h := range headers {
		all[h.Hash()] = h
	}
	// Verification of block 8 ran first: snapshot at 7 cached with the old set.
	early, err := engine.snapshot(all, 7, headers[7].Hash(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sameSet(t, early.Signers, stale)

	cands := addrs(17)
	stakes := make([]int64, len(cands))
	for i := range stakes {
		stakes[i] = int64(100 + i)
	}
	if err := engine.UpdateMasternodesV1(all, headers[5], candidateState(t, cands, stakes)); err != nil {
		t.Fatalf("UpdateMasternodesV1: %v", err)
	}
	snap, err := engine.snapshot(all, 9, headers[9].Hash(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sameSet(t, snap.Signers, cands)
}
