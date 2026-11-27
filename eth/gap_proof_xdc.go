// Copyright (c) 2026 XDC Network
//
// Below-pivot gap snapshots proven from the 0x88 validator contract storage.
//
// A snap/fast-synced node holds no state below its pivot. Full verification of
// an epoch switch needs the candidate pool of the gap block (floor(S/epoch)*epoch
// - gap), which for the first switches after the pivot lies below it. The pool
// is fetched with eth_getProof from an untrusted RPC (--snap.gap-rpc) and
// accepted only if every trie node hashes to the gap header's state root, the
// same way a beacon light client proves the next sync committee against an
// authenticated header root. F-2026-20648.

package eth

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
)

const (
	gapProofKeysPerCall   = 256              // storage keys per eth_getProof call
	gapProofMaxCandidates = 8192             // sanity bound on the proven array length
	gapProofMaxNodes      = 256*16 + 64      // trie nodes accepted from one response
	gapProofCallTimeout   = 30 * time.Second // per RPC call
	gapProofRunTimeout    = 3 * time.Minute  // one proveGapPools run
	gapProofRetryEvery    = 30 * time.Second // fullSyncOnce re-attempt rate
)

// gapsBelowPivot lists the gap blocks below pivot P that a switch above P can
// need. The first switch S > P has gap floor(S/epoch)*epoch-gap, at least
// g0 = floor((P+1)/epoch)*epoch-gap; V2 switches drift a few blocks below a
// multiple of epoch, so g0+epoch is needed too. With tiered rewards the reward
// reads the pools of the two previous epochs as well.
func gapsBelowPivot(P, epoch, gap uint64, tiered bool) []uint64 {
	if epoch == 0 {
		return nil
	}
	g0 := int64((P+1)/epoch*epoch) - int64(gap) // negative before the first epoch boundary
	cands := []int64{g0, g0 + int64(epoch)}
	if tiered {
		cands = append(cands, g0-int64(epoch), g0-2*int64(epoch))
	}
	var out []uint64
	for _, g := range cands {
		if g > 0 && uint64(g) < P {
			out = append(out, uint64(g))
		}
	}
	return out
}

// fetchGapProof returns every trie node (account and storage proofs) the
// endpoint sent for the 0x88 account at block and keys. The nodes are
// unauthenticated; the caller keys them by their own hash.
func fetchGapProof(ctx context.Context, u string, block uint64, keys []common.Hash) ([][]byte, error) {
	strs := make([]string, len(keys))
	for i, k := range keys {
		strs[i] = k.Hex()
	}
	ctx, cancel := context.WithTimeout(ctx, gapProofCallTimeout)
	defer cancel()
	cli, err := rpc.DialContext(ctx, u)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	var res struct {
		AccountProof []hexutil.Bytes `json:"accountProof"`
		StorageProof []struct {
			Proof []hexutil.Bytes `json:"proof"`
		} `json:"storageProof"`
	}
	if err := cli.CallContext(ctx, &res, "eth_getProof", common.MasternodeVotingSMCBinary, strs, hexutil.EncodeUint64(block)); err != nil {
		return nil, err
	}
	nodes := make([][]byte, 0, len(res.AccountProof))
	for _, n := range res.AccountProof {
		nodes = append(nodes, n)
	}
	for _, sp := range res.StorageProof {
		for _, n := range sp.Proof {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) > gapProofMaxNodes {
		return nil, fmt.Errorf("response carries %d trie nodes", len(nodes))
	}
	return nodes, nil
}

// proveGapStateFrom runs proveGapState against each endpoint in order and
// returns the first state that verifies, with that endpoint's host. An endpoint
// that errors, lies or withholds nodes just yields the next one.
func proveGapStateFrom(ctx context.Context, urls []string, block uint64, root common.Hash) (*state.StateDB, string, error) {
	var errs []error
	for _, u := range urls {
		st, err := proveGapState(ctx, func(c context.Context, keys []common.Hash) ([][]byte, error) {
			return fetchGapProof(c, u, block, keys)
		}, root)
		if err == nil {
			host := ""
			if pu, perr := url.Parse(u); perr == nil {
				host = pu.Host
			}
			return st, host, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", redactURL(u), err))
	}
	return nil, "", errors.Join(errs...)
}

// redactURL keeps credentials in the URL out of logs and errors.
func redactURL(u string) string {
	if pu, err := url.Parse(u); err == nil {
		return pu.Redacted()
	}
	return "<invalid url>"
}

// proveGapState reads the 0x88 candidate pool of the state with the given root
// through the fetch func, in three rounds of at most gapProofKeysPerCall keys:
// the candidates array length, the array elements, then each candidate's cap.
// Proof nodes go into a memory database under their own keccak, so a forged
// node is never found by the trie and a withheld one is a read error, not a
// wrong value; the returned state is only valid for those reads.
func proveGapState(ctx context.Context, fetch func(context.Context, []common.Hash) ([][]byte, error), root common.Hash) (*state.StateDB, error) {
	memdb := rawdb.NewMemoryDatabase()
	sdb := state.NewDatabase(triedb.NewDatabase(memdb, triedb.HashDefaults), nil)
	round := func(keys []common.Hash) error {
		for len(keys) > 0 {
			n := min(len(keys), gapProofKeysPerCall)
			nodes, err := fetch(ctx, keys[:n])
			if err != nil {
				return err
			}
			for _, blob := range nodes {
				rawdb.WriteLegacyTrieNode(memdb, crypto.Keccak256Hash(blob), blob)
			}
			keys = keys[n:]
		}
		return nil
	}
	// A new StateDB per round: a failed read is sticky in it.
	open := func() (*state.StateDB, error) { return state.New(root, sdb) }

	if err := round([]common.Hash{state.CandidatesLenKey()}); err != nil {
		return nil, err
	}
	st, err := open()
	if err != nil {
		return nil, fmt.Errorf("account proof does not reach root %x: %w", root, err)
	}
	count := st.GetState(common.MasternodeVotingSMCBinary, state.CandidatesLenKey()).Big()
	if err := st.Error(); err != nil {
		return nil, err
	}
	if count.Sign() == 0 || count.Cmp(big.NewInt(gapProofMaxCandidates)) > 0 {
		return nil, fmt.Errorf("proven candidates length %v out of range", count)
	}
	keys := make([]common.Hash, count.Uint64())
	for i := range keys {
		keys[i] = state.CandidateElementKey(uint64(i))
	}
	if err := round(keys); err != nil {
		return nil, err
	}
	if st, err = open(); err != nil {
		return nil, err
	}
	candidates := state.GetCandidates(st)
	if err := st.Error(); err != nil {
		return nil, err
	}
	keys = keys[:0]
	for _, c := range candidates {
		if c != (common.Address{}) {
			keys = append(keys, state.CandidateCapKey(c))
		}
	}
	if err := round(keys); err != nil {
		return nil, err
	}
	return open()
}

// gapHeaderOK requires hdr to be the canonical header below the pivot.
func gapHeaderOK(canonical func(uint64) common.Hash, hdr *types.Header, pivot uint64) error {
	if n := hdr.Number.Uint64(); n >= pivot {
		return fmt.Errorf("gap header %d is not below the pivot %d", n, pivot)
	}
	if canonical(hdr.Number.Uint64()) != hdr.Hash() {
		return fmt.Errorf("gap header %d is not canonical", hdr.Number)
	}
	return nil
}

// errGapPoolChanged: a 0x88 log lies between the gap and the pivot, so the
// pivot state is not the gap's pool.
var errGapPoolChanged = errors.New("0x88 emitted a log after the gap")

// noVotingLogs checks blocks (g, P] for a log of the validator contract. Every
// 0x88 function that changes storage emits an event, so without one the
// contract storage at g equals the storage at P. The header bloom (committed
// by the hash chain that reaches the pivot) rules a block out; a bloom hit
// needs the block's receipts, checked against the header's receipt root. A
// missing header or receipt is an error, never "no log".
func noVotingLogs(g, P uint64, headerAt func(uint64) *types.Header, receiptsOf func(*types.Header) types.Receipts) (int, error) {
	for n := g + 1; n <= P; n++ {
		h := headerAt(n)
		if h == nil {
			return 0, fmt.Errorf("header %d unavailable", n)
		}
		if !types.BloomLookup(h.Bloom, common.MasternodeVotingSMCBinary) {
			continue
		}
		rs := receiptsOf(h)
		if rs == nil {
			return 0, fmt.Errorf("receipts of block %d unavailable", n)
		}
		if types.DeriveSha(rs, trie.NewStackTrie(nil)) != h.ReceiptHash {
			return 0, fmt.Errorf("receipts of block %d do not match the receipt root", n)
		}
		for _, r := range rs {
			for _, l := range r.Logs {
				if l.Address == common.MasternodeVotingSMCBinary {
					return 0, fmt.Errorf("%w (block %d)", errGapPoolChanged, n)
				}
			}
		}
	}
	return int(P - g), nil
}

// seedGapFromPivot seeds the gap snapshot from the snap-verified pivot state
// when noVotingLogs finds no 0x88 log in (g, P]; the gap header only supplies
// the number and hash. Returns the number of blocks scanned.
func seedGapFromPivot(v2 *engine_v2.XDPoS_v2, chain *core.BlockChain, gapHdr, pivotHdr *types.Header,
	headerAt func(uint64) *types.Header, receiptsOf func(*types.Header) types.Receipts) (int, error) {
	scanned, err := noVotingLogs(gapHdr.Number.Uint64(), pivotHdr.Number.Uint64(), headerAt, receiptsOf)
	if err != nil {
		return 0, err
	}
	st, err := chain.StateAt(pivotHdr.Root, pivotHdr.Number, pivotHdr.Time)
	if err != nil {
		return 0, fmt.Errorf("pivot state unavailable: %w", err)
	}
	return scanned, v2.SeedGapSnapshotFromState(chain, gapHdr, st)
}

// proveGap seeds one gap, first from the pivot state, then from proofs of
// --snap.gap-rpc. It returns which path succeeded.
func (s *xdcSyncer) proveGap(ctx context.Context, v2 *engine_v2.XDPoS_v2, hdr, pivotHdr *types.Header,
	headerAt func(uint64) *types.Header, receiptsOf func(*types.Header) types.Receipts) (string, error) {
	g, P := hdr.Number.Uint64(), pivotHdr.Number.Uint64()
	scanned, perr := seedGapFromPivot(v2, s.handler.chain, hdr, pivotHdr, headerAt, receiptsOf)
	if perr == nil {
		log.Info("XDC: gap pool from pivot state (no 0x88 change since gap)", "gap", g, "pivot", P, "blocks", scanned)
		return "pivot", nil
	}
	log.Info("XDC: gap pool not provable from the pivot state", "gap", g, "pivot", P, "reason", perr)
	if len(s.handler.snapGapRPC) == 0 {
		log.Error("XDC: gap pool proof needed but no RPC configured; set --snap.gap-rpc to an RPC with --history.trienode",
			"gap", g, "pivot", P)
		return "", errors.New("no --snap.gap-rpc configured")
	}
	st, host, err := proveGapStateFrom(ctx, s.handler.snapGapRPC, g, hdr.Root)
	if err == nil {
		err = v2.SeedGapSnapshotFromState(s.handler.chain, hdr, st)
	}
	if err != nil {
		return "", err
	}
	log.Info("XDC: gap pool proven", "gap", g, "root", hdr.Root,
		"candidates", len(state.GetCandidates(st)), "rpc", host)
	return "rpc", nil
}

// proveGapPools seeds a proven (version 7) snapshot for every gap below the
// pivot that a coming epoch switch can use and that has none yet. Failures are
// logged per gap; the node retries (see fullSyncOnce).
func (s *xdcSyncer) proveGapPools(ctx context.Context) {
	if !s.gapProofMu.TryLock() {
		return
	}
	defer s.gapProofMu.Unlock()
	chain := s.handler.chain
	cfg := chain.Config()
	if cfg == nil || cfg.XDPoS == nil || cfg.XDPoS.V2 == nil || cfg.XDPoS.V2.SwitchBlock == nil || cfg.XDPoS.Epoch == 0 {
		return
	}
	xdpos, ok := chain.Engine().(*XDPoS.XDPoS)
	if !ok {
		return
	}
	v2, ok := xdpos.EngineV2.(*engine_v2.XDPoS_v2)
	if !ok || v2 == nil {
		return
	}
	pivot := rawdb.ReadLastPivotNumber(s.handler.database)
	if pivot == nil {
		return // full sync: every gap was executed
	}
	epoch := cfg.XDPoS.Epoch
	tiered := cfg.IsTIPTieredReward(new(big.Int).SetUint64(*pivot + 2*epoch))
	var gaps []uint64
	for _, g := range gapsBelowPivot(*pivot, epoch, cfg.XDPoS.Gap, tiered) {
		if g >= cfg.XDPoS.V2.SwitchBlock.Uint64() {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return
	}
	pivotHeader := chain.GetHeaderByNumber(*pivot)
	if pivotHeader == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, gapProofRunTimeout)
	defer cancel()

	// Headers below the pivot may not be stored (the skeleton stops at it):
	// fetch [lowest gap, pivot) once, chained by parent hash to the local pivot.
	var (
		fetched   []*types.Header
		fetchedAt = slices.Min(gaps)
		tried     bool
	)
	headerAt := func(n uint64) *types.Header {
		if h := chain.GetHeaderByNumber(n); h != nil {
			return h
		}
		if !tried {
			tried = true
			fetched = s.fetchVerifiedHeaderRange(fetchedAt, *pivot, pivotHeader)
		}
		if i := int(n) - int(fetchedAt); n >= fetchedAt && i < len(fetched) {
			return fetched[i]
		}
		return nil
	}
	receiptsOf := func(h *types.Header) types.Receipts {
		return rawdb.ReadRawReceipts(s.handler.database, h.Hash(), h.Number.Uint64())
	}
	for _, g := range gaps {
		hdr := chain.GetHeaderByNumber(g)
		if hdr != nil {
			if err := gapHeaderOK(func(n uint64) common.Hash { return chain.GetCanonicalHash(n) }, hdr, *pivot); err != nil {
				log.Warn("XDC: gap pool proof skipped", "gap", g, "err", err)
				continue
			}
		} else if hdr = headerAt(g); hdr == nil {
			log.Warn("XDC: gap pool proof skipped, gap header unavailable", "gap", g, "pivot", *pivot)
			continue
		}
		if v2.HasProvenGapSnapshot(hdr.Hash()) {
			continue
		}
		if _, err := s.proveGap(ctx, v2, hdr, pivotHeader, headerAt, receiptsOf); err != nil {
			log.Warn("XDC: gap pool proof failed", "gap", g, "root", hdr.Root, "err", err)
		}
	}
}

// proveGapPoolsRetry is the fullSyncOnce hook for ErrV2SnapshotMissing: it
// retries proveGapPools at most once per gapProofRetryEvery, so a node
// recovers by itself once the RPC is back.
func (s *xdcSyncer) proveGapPoolsRetry() {
	now := time.Now().UnixNano()
	last := s.gapProofAt.Load()
	if now-last < int64(gapProofRetryEvery) || !s.gapProofAt.CompareAndSwap(last, now) {
		return
	}
	ctx, cancel := s.quitContext()
	defer cancel()
	s.proveGapPools(ctx)
}

// proveGapPoolsQuit runs proveGapPools until the syncer stops.
func (s *xdcSyncer) proveGapPoolsQuit() {
	ctx, cancel := s.quitContext()
	defer cancel()
	s.proveGapPools(ctx)
}

// quitContext returns a context cancelled when the syncer stops.
func (s *xdcSyncer) quitContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-s.quitCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
