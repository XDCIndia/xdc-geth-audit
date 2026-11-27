// Copyright 2026 XDC Network
//
// F-2026-19637 regression tests for the post-TIPUpgradePenalty comeback rule
// (a penalised signer is released only after LimitPenaltyEpoch consecutive
// penalty epochs and MinimumSigningTx signing txs).
//
//   - TestApothemPenaltyReplay re-verifies stored Apothem epoch-switch headers
//     after block 83,604,500 with the real V2 engine. It is opt-in: set
//     XDC_APOTHEM_CHAINDATA to a COPY of an Apothem chaindata directory that
//     holds headers and bodies past 83,604,500. Never point it at a live node.
//   - TestPenaltyComebackDevnetRound50000 is a synthetic check with the devnet
//     V2 config that is active from round 50,000 (LimitPenaltyEpoch 5,
//     MinimumSigningTx 30).

package hooks

import (
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/pebble"
	"github.com/ethereum/go-ethereum/params"
)

// replayStart is the first block whose epoch switches are replayed. A node
// that was snap-synced has no gap snapshot or block bodies below its pivot, so
// XDC_APOTHEM_REPLAY_FROM can raise it to a block above the pivot.
const replayStart = 83_604_500

// storedChain is a read-only view of a copied chaindata directory, enough for
// header verification and the penalty hook (headers and bodies, no state).
type storedChain struct {
	db  ethdb.Database
	cfg *params.ChainConfig
}

var _ consensus.ChainReader = (*storedChain)(nil)

func (c *storedChain) Config() *params.ChainConfig { return c.cfg }
func (c *storedChain) CurrentHeader() *types.Header {
	return rawdb.ReadHeader(c.db, rawdb.ReadHeadHeaderHash(c.db), c.headNumber())
}
func (c *storedChain) headNumber() uint64 {
	n, ok := rawdb.ReadHeaderNumber(c.db, rawdb.ReadHeadHeaderHash(c.db))
	if !ok {
		return 0
	}
	return n
}
func (c *storedChain) GetHeader(h common.Hash, n uint64) *types.Header {
	return rawdb.ReadHeader(c.db, h, n)
}
func (c *storedChain) GetHeaderByNumber(n uint64) *types.Header {
	h := rawdb.ReadCanonicalHash(c.db, n)
	if h == (common.Hash{}) {
		return nil
	}
	return rawdb.ReadHeader(c.db, h, n)
}
func (c *storedChain) GetHeaderByHash(h common.Hash) *types.Header {
	n, ok := rawdb.ReadHeaderNumber(c.db, h)
	if !ok {
		return nil
	}
	return rawdb.ReadHeader(c.db, h, n)
}
func (c *storedChain) GetBlock(h common.Hash, n uint64) *types.Block {
	return rawdb.ReadBlock(c.db, h, n)
}

// newReplayEngine wires the V2 engine and the penalty hook the way eth.New does.
func newReplayEngine(db ethdb.Database, cfg *params.ChainConfig) (*XDPoS.XDPoS, *engine_v2.XDPoS_v2) {
	adaptor := XDPoS.New(cfg.XDPoS, db)
	v2 := engine_v2.NewIsolated(cfg, db, make(chan int, 1), make(chan types.Round, 1))
	adaptor.SetEngineV2(v2)
	AttachConsensusV2Hooks(adaptor, nil, cfg) // bc is only used by HookReward
	return adaptor, v2
}

// hookPenalties recomputes the Penalties list of epoch-switch header h with
// the penalty hook, the same call calcMasternodes makes.
func hookPenalties(t *testing.T, v2 *engine_v2.XDPoS_v2, chain consensus.ChainReader, h *types.Header) []common.Address {
	t.Helper()
	pens, err := v2.HookPenalty(chain, h.Number, h.ParentHash, nil)
	if err != nil {
		t.Fatalf("block %d: penalty hook: %v", h.Number, err)
	}
	return pens
}

// withLimitPenaltyEpoch returns a copy of cfg whose V2 configs use limit.
func withLimitPenaltyEpoch(cfg *params.ChainConfig, limit int) *params.ChainConfig {
	out := *cfg
	x := *cfg.XDPoS
	v := &params.V2{
		SwitchBlock:      cfg.XDPoS.V2.SwitchBlock,
		SwitchEpoch:      cfg.XDPoS.V2.SwitchEpoch,
		BALSealBlock:     cfg.XDPoS.V2.BALSealBlock,
		RoundSkipTCBlock: cfg.XDPoS.V2.RoundSkipTCBlock,
		AllConfigs:       map[uint64]*params.V2Config{},
	}
	for k, c := range cfg.XDPoS.V2.AllConfigs {
		cc := *c
		cc.LimitPenaltyEpoch = limit
		v.AllConfigs[k] = &cc
	}
	cur := *cfg.XDPoS.V2.CurrentConfig
	cur.LimitPenaltyEpoch = limit
	v.CurrentConfig = &cur
	v.BuildConfigIndex()
	x.V2 = v
	out.XDPoS = &x
	return &out
}

type replayCase struct {
	number uint64
	kind   string // "streak" or "release"
	detail string
}

func TestApothemPenaltyReplay(t *testing.T) {
	dir := os.Getenv("XDC_APOTHEM_CHAINDATA")
	if dir == "" {
		t.Skip("XDC_APOTHEM_CHAINDATA not set (path to a COPY of an Apothem chaindata directory)")
	}
	prev := core.XdcForceFullVerify.Load()
	core.XdcForceFullVerify.Store(true)
	defer core.XdcForceFullVerify.Store(prev)

	kv, err := pebble.New(dir, 512, 512, "", false)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	db, err := rawdb.Open(kv, rawdb.OpenOptions{Ancient: filepath.Join(dir, "ancient")})
	if err != nil {
		t.Fatalf("open freezer: %v", err)
	}
	defer db.Close()

	cfg := params.XDCApothemChainConfig
	chain := &storedChain{db: db, cfg: cfg}
	head := chain.headNumber()
	from := uint64(replayStart)
	if v, err := strconv.ParseUint(os.Getenv("XDC_APOTHEM_REPLAY_FROM"), 10, 64); err == nil && v > from {
		from = v
	}
	if head <= from {
		t.Skipf("datadir head %d is not past %d", head, from)
	}

	// Walk the epoch switches from the fork (83,600,000) so the consecutive
	// penalty count is right at replayStart, and pick the cases to replay.
	maxCases := 5
	if v, err := strconv.Atoi(os.Getenv("XDC_APOTHEM_MAX_CASES")); err == nil && v > 0 {
		maxCases = v
	}
	streak := map[common.Address]int{}
	var cases []replayCase
	var releaseCase *replayCase
	releaseFull := false
	for n := uint64(params.XDCApothemChainConfig.TIPUpgradePenaltyBlock.Int64()); n <= head; n++ {
		h := chain.GetHeaderByNumber(n)
		if h == nil || len(h.Validators) == 0 {
			continue // not an epoch switch (or a hole in the copy)
		}
		cur := map[common.Address]bool{}
		for _, a := range contracts.ExtractAddressFromBytes(h.Penalties) {
			cur[a] = true
		}
		next := map[common.Address]int{}
		for a := range cur {
			next[a] = streak[a] + 1
		}
		if n >= from {
			for a, s := range next {
				if s >= 2 && s <= 4 && len(cases) < maxCases {
					cases = append(cases, replayCase{n, "streak", a.Hex() + " in " + strconv.Itoa(s) + " consecutive Penalties lists"})
					break
				}
			}
			for a, s := range streak {
				if cur[a] {
					continue
				}
				// released: prefer one that served the full 5 epochs
				if releaseCase == nil || (s >= 5 && !releaseFull) {
					releaseCase = &replayCase{n, "release", a.Hex() + " released after " + strconv.Itoa(s) + " epochs"}
					releaseFull = s >= 5
				}
			}
		}
		streak = next
		if len(cases) >= maxCases && releaseFull {
			break
		}
	}
	if releaseCase != nil {
		cases = append(cases, *releaseCase)
	}
	if len(cases) == 0 {
		t.Skipf("no epoch switch with a penalised signer found in %d..%d", from, head)
	}

	engine, v2 := newReplayEngine(db, cfg)
	for _, c := range cases {
		h := chain.GetHeaderByNumber(c.number)
		// Exact comparison of Penalties, then the engine's full verification,
		// which compares Validators against calcMasternodes under fullVerify.
		if got := hookPenalties(t, v2, chain, h); !utils.CompareSignersLists(got, contracts.ExtractAddressFromBytes(h.Penalties)) {
			t.Errorf("block %d (%s: %s): recomputed Penalties %v != stored %x", c.number, c.kind, c.detail, got, h.Penalties)
			continue
		}
		if err := engine.VerifyHeader(chain, h); err != nil {
			t.Errorf("block %d (%s: %s): stored Validators/Penalties rejected: %v", c.number, c.kind, c.detail, err)
			continue
		}
		t.Logf("block %d (%s: %s): Validators=%d Penalties=%d match", c.number, c.kind, c.detail,
			len(h.Validators)/common.AddressLength, len(h.Penalties)/common.AddressLength)
	}

	// Negative control: with LimitPenaltyEpoch=1 a signer with enough signing
	// txs is released a few epochs early. The recomputed Penalties then differ
	// from the stored ones and the header is rejected. verifyHeader compares
	// Validators before Penalties, and a released signer usually re-enters the
	// validator set, so the error is ErrValidatorsNotLegit or ErrPenaltiesNotLegit.
	badCfg := withLimitPenaltyEpoch(cfg, 1)
	badChain := &storedChain{db: db, cfg: badCfg}
	badEngine, badV2 := newReplayEngine(db, badCfg)
	diverged, rejected := 0, 0
	for _, c := range cases {
		if c.kind != "streak" {
			continue
		}
		h := chain.GetHeaderByNumber(c.number)
		if !utils.CompareSignersLists(hookPenalties(t, badV2, badChain, h), contracts.ExtractAddressFromBytes(h.Penalties)) {
			diverged++
		}
		err := badEngine.VerifyHeader(badChain, h)
		if errors.Is(err, utils.ErrPenaltiesNotLegit) || errors.Is(err, utils.ErrValidatorsNotLegit) {
			rejected++
		}
		t.Logf("negative control: block %d: %v", c.number, err)
	}
	if diverged == 0 || rejected == 0 {
		t.Fatalf("negative control: LimitPenaltyEpoch=1 diverged on %d and rejected %d of the streak headers, want at least one of each", diverged, rejected)
	}
}

// devnetRound50000Fake reports a round at which the devnet V2 config with
// LimitPenaltyEpoch 5 and MinimumSigningTx 30 is active.
type devnetRound50000Fake struct{ comebackFakeV2 }

func (f *devnetRound50000Fake) GetRoundNumber(*types.Header) (types.Round, error) {
	return 50_000, nil
}

func TestPenaltyComebackDevnetRound50000(t *testing.T) {
	cfg := params.XDCDevnetChainConfig
	cfg.XDPoS.V2.BuildConfigIndex() // the engine does this at start-up
	v2cfg := cfg.XDPoS.V2.Config(50_000)
	if v2cfg.LimitPenaltyEpoch != 5 || v2cfg.MinimumSigningTx != 30 {
		t.Fatalf("devnet config at round 50000: limit=%d minSign=%d, want 5/30", v2cfg.LimitPenaltyEpoch, v2cfg.MinimumSigningTx)
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := crypto.PubkeyToAddress(key.PublicKey)

	// number-1 is a multiple of MergeSignRange so the signing txs of the
	// parent block count. It is past 50,000 and past the 5-epoch comeback height.
	number := big.NewInt(63_001)
	hash := common.HexToHash("0xabcdef02")

	adaptor := XDPoS.New(cfg.XDPoS, rawdb.NewMemoryDatabase())
	fake := &devnetRound50000Fake{}
	adaptor.SetEngineV2(fake)
	AttachConsensusV2Hooks(adaptor, nil, cfg)
	fn := fake.capturedFn
	if fn == nil {
		t.Fatal("penalty hook not wired")
	}
	chain := &comebackTestChain{
		parent: &types.Header{Number: big.NewInt(int64(number.Uint64() - 1))},
		cfg:    cfg,
	}

	signingTxs := func(n int) []*types.Transaction {
		data := common.Hex2Bytes(common.HexSignMethod)
		data = append(data, common.LeftPadBytes(big.NewInt(63_000).Bytes(), 32)...)
		data = append(data, hash.Bytes()...)
		txs := make([]*types.Transaction, n)
		for i := range txs {
			raw := types.NewTransaction(uint64(i), common.BlockSignersBinary, big.NewInt(0), 200_000, big.NewInt(0), data)
			txs[i], err = types.SignTx(raw, types.MakeSigner(cfg, number, 0), key)
			if err != nil {
				t.Fatal(err)
			}
		}
		return txs
	}
	// penalisedIn makes the signer appear in the first n previous epochs.
	penalisedIn := func(n int) {
		fake.penaltiesAtEpoch = func(limit int) []common.Address {
			if limit < n {
				return []common.Address{signer}
			}
			return nil
		}
	}
	run := func(name string, epochs, txs int, wantPenalised bool) {
		t.Helper()
		penalisedIn(epochs)
		adaptor.BlockSigners.Add(hash, signingTxs(txs))
		pens, err := fn(chain, number, hash, nil)
		if err != nil {
			t.Fatalf("%s: hook error: %v", name, err)
		}
		if got := addrIn(pens, signer); got != wantPenalised {
			t.Fatalf("%s: signer penalised=%v, want %v (penalties %v)", name, got, wantPenalised, pens)
		}
	}
	// The signer is in lastPenalty (offset 0) in every case.
	run("5 of 5 epochs, 30 sign txs: released", 5, 30, false)
	run("5 of 5 epochs, 29 sign txs: stays", 5, 29, true)
	run("4 of 5 epochs, 30 sign txs: stays", 4, 30, true)
	run("2 of 5 epochs, 30 sign txs: stays", 2, 30, true)
}
