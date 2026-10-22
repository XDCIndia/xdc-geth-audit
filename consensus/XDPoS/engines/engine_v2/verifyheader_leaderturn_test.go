// Copyright (c) 2026 XDC Network
// #1413: a V2 header whose proposer is not the round leader must never be
// remembered as verified unless the leader check actually ran. Canonical
// XDPoSChain enforces the leader check unconditionally (verifyHeader.go:199-204)
// and only caches fully verified headers (verifyHeader.go:206-212).
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestVerifyHeaderLeaderTurn -count=1

package engine_v2

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

// ltChain is a ChainReader backed by explicit header maps.
type ltChain struct {
	cfg      *params.ChainConfig
	byHash   map[common.Hash]*types.Header
	byNumber map[uint64]*types.Header
}

func (c *ltChain) add(h *types.Header) {
	c.byHash[h.Hash()] = h
	c.byNumber[h.Number.Uint64()] = h
}
func (c *ltChain) Config() *params.ChainConfig                     { return c.cfg }
func (c *ltChain) CurrentHeader() *types.Header                    { return nil }
func (c *ltChain) GetBlock(common.Hash, uint64) *types.Block       { return nil }
func (c *ltChain) GetTd(common.Hash, uint64) *big.Int              { return nil }
func (c *ltChain) GetHeader(h common.Hash, _ uint64) *types.Header { return c.byHash[h] }
func (c *ltChain) GetHeaderByHash(h common.Hash) *types.Header     { return c.byHash[h] }
func (c *ltChain) GetHeaderByNumber(n uint64) *types.Header        { return c.byNumber[n] }

func ltPack(addrs []common.Address) []byte {
	out := make([]byte, 0, len(addrs)*common.AddressLength)
	for _, a := range addrs {
		out = append(out, a.Bytes()...)
	}
	return out
}

func ltV2Header(t *testing.T, num uint64, parent *types.Header, round types.Round, qc *types.QuorumCert, ts uint64) *types.Header {
	t.Helper()
	extra, err := (&types.ExtraFields_v2{Round: round, QuorumCert: qc}).EncodeToBytes()
	if err != nil {
		t.Fatalf("encode extra: %v", err)
	}
	h := &types.Header{
		Number:     new(big.Int).SetUint64(num),
		Extra:      extra,
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Time:       ts,
		UncleHash:  utils.UncleHash,
	}
	if parent != nil {
		h.ParentHash = parent.Hash()
	}
	return h
}

func ltSeal(t *testing.T, h *types.Header, key *ecdsa.PrivateKey) {
	t.Helper()
	h.Coinbase = crypto.PubkeyToAddress(key.PublicKey)
	sig, err := crypto.Sign(sigHash(h).Bytes(), key)
	if err != nil {
		t.Fatalf("sign header: %v", err)
	}
	h.Validator = sig
}

// ltFixture builds (epoch=900, gap=450, V2 from genesis):
//
//	450  gap block (snapshot holds the N masternodes, unless skipSnapshot)
//	901  epoch switch E1, round 900, Validators = the N masternodes
//	902  P, round 1798 (same epoch as E1)
//	903  H, QC over P signed by all N masternodes; round 1800 makes H an
//	     epoch switch, round 1799 a regular block of E1's epoch
//
// H is returned unsealed with Validators unset.
func ltFixture(t *testing.T, hRound types.Round) (*XDPoS_v2, *ltChain, []*ecdsa.PrivateKey, []common.Address, *types.Header, *types.Header) {
	t.Helper()
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{
			Epoch: 900,
			Gap:   450,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
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
	var db ethdb.Database = rawdb.NewMemoryDatabase()
	eng := createEngine(cfg, db, nil, nil)
	eng.isInitialized = true

	keys, mns := genNKeys(t, 4)
	chain := &ltChain{cfg: cfg, byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
	now := uint64(time.Now().Unix())

	gap := &types.Header{Number: big.NewInt(450), Difficulty: big.NewInt(1), Extra: []byte{0x01}}
	chain.add(gap)
	if err := storeSnapshot(newSnapshot(450, gap.Hash(), mns), db); err != nil {
		t.Fatalf("storeSnapshot: %v", err)
	}

	pre := &types.Header{Number: big.NewInt(900), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	e1 := ltV2Header(t, 901, pre, 900, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: pre.Hash(), Round: 899, Number: big.NewInt(900)},
	}, now-200)
	e1.Validators = ltPack(mns)
	chain.add(e1)

	p := ltV2Header(t, 902, e1, 1798, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: e1.Hash(), Round: 900, Number: big.NewInt(901)},
		GapNumber:         450,
	}, now-100)
	chain.add(p)

	pInfo := &types.BlockInfo{Hash: p.Hash(), Round: 1798, Number: big.NewInt(902)}
	h := ltV2Header(t, 903, p, hRound, &types.QuorumCert{
		ProposedBlockInfo: pInfo,
		Signatures:        makeQCSignatures(t, pInfo, 450, keys),
		GapNumber:         450,
	}, now-50)
	return eng, chain, keys, mns, p, h
}

// Regular block at round R=1799: leader is mns[R % 900 % 4] = mns[3]; the
// header is signed by the next masternode, mns[(3+1) % 4] = mns[0].
func TestVerifyHeaderLeaderTurn_WrongTurnRejectedUnderFullVerify(t *testing.T) {
	eng, chain, keys, _, p, h := ltFixture(t, 1799)
	ltSeal(t, h, keys[0])
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); !errors.Is(err, utils.ErrNotItsTurn) {
		t.Fatalf("wrong-turn leader under fullVerify: want ErrNotItsTurn, got %v", err)
	}
}

// A wrong-turn header first seen during bulk sync (XdcBulkSyncMode=true) must
// not be cached as verified: a later full verification of the same header must
// still reject it as out-of-turn rather than short-circuiting via the cache.
func TestVerifyHeaderLeaderTurn_SkippedCheckNotCached(t *testing.T) {
	savedBulk := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(true)
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(savedBulk) })

	eng, chain, keys, _, p, h := ltFixture(t, 1799)
	ltSeal(t, h, keys[0])
	// First call: bulk sync, A.87 tolerates the wrong-turn proposer.
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("XdcBulkSyncMode keeps the A.87 tolerance for header-derived lists, got %v", err)
	}
	// Header must not have been added to verifiedHeaders; later fullVerify must
	// run the check and catch the wrong-turn proposer.
	core.XdcBulkSyncMode.Store(false)
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, true); !errors.Is(err, utils.ErrNotItsTurn) {
		t.Fatalf("later fullVerify of a wrong-turn header: want ErrNotItsTurn, got %v (served from verifiedHeaders cache)", err)
	}
}

// Epoch switch at round 1800 whose Validators exactly match the locally
// computed list: the list does not come from the header, so the leader check
// is enforced even without full verification. Leader is mns[0]; mns[1] signs.
func TestVerifyHeaderLeaderTurn_LocalListEnforcedWithoutFullVerify(t *testing.T) {
	eng, chain, keys, mns, p, h := ltFixture(t, 1800)
	h.Validators = ltPack(mns)
	ltSeal(t, h, keys[1])
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); !errors.Is(err, utils.ErrNotItsTurn) {
		t.Fatalf("wrong-turn leader with a locally computed list: want ErrNotItsTurn, got %v", err)
	}
}

// Positive control: the in-turn leader passes both modes.
func TestVerifyHeaderLeaderTurn_InTurnAccepted(t *testing.T) {
	eng, chain, keys, _, p, h := ltFixture(t, 1799)
	ltSeal(t, h, keys[3])
	for _, full := range []bool{false, true} {
		if err := eng.verifyHeader(chain, h, []*types.Header{p}, full); err != nil {
			t.Fatalf("in-turn leader (fullVerify=%v): %v", full, err)
		}
	}
}

// F-2026-19463 residual: CheckpointSyncNoStateActive=true after fast-sync /
// snapshot-restore keeps xdcFullVerify() false even at the live tip. A new
// tip header from a non-leader must be rejected; only XdcBulkSyncMode earns
// the A.87 tolerance.
//
// Before the fix the A.87 branch fired on `!fullVerify`, which is true for
// both CheckpointSyncNoStateActive and XdcBulkSyncMode; after the fix it
// requires XdcBulkSyncMode specifically.
func TestVerifyHeaderLeaderTurn_CheckpointSyncEnforcesLeader(t *testing.T) {
	savedBulk := core.XdcBulkSyncMode.Load()
	savedCkpt := core.CheckpointSyncNoStateActive.Load()
	core.XdcBulkSyncMode.Store(false)
	core.CheckpointSyncNoStateActive.Store(true)
	t.Cleanup(func() {
		core.XdcBulkSyncMode.Store(savedBulk)
		core.CheckpointSyncNoStateActive.Store(savedCkpt)
	})

	eng, chain, keys, _, p, h := ltFixture(t, 1799)
	// Round 1799 leader is mns[3]; sign with mns[0] (wrong turn).
	ltSeal(t, h, keys[0])

	// fullVerify=false (CheckpointSyncNoStateActive makes xdcFullVerify false),
	// but XdcBulkSyncMode=false → must enforce ErrNotItsTurn.
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); !errors.Is(err, utils.ErrNotItsTurn) {
		t.Fatalf("wrong-turn tip header during checkpoint-sync-no-state: want ErrNotItsTurn, got %v", err)
	}
}

// F-2026-19463 control: XdcBulkSyncMode=true still earns the A.87 tolerance.
func TestVerifyHeaderLeaderTurn_BulkSyncStillTolerated(t *testing.T) {
	savedBulk := core.XdcBulkSyncMode.Load()
	savedCkpt := core.CheckpointSyncNoStateActive.Load()
	core.XdcBulkSyncMode.Store(true)
	core.CheckpointSyncNoStateActive.Store(false)
	t.Cleanup(func() {
		core.XdcBulkSyncMode.Store(savedBulk)
		core.CheckpointSyncNoStateActive.Store(savedCkpt)
	})

	eng, chain, keys, _, p, h := ltFixture(t, 1799)
	// Wrong-turn: mns[0] instead of mns[3].
	ltSeal(t, h, keys[0])

	// XdcBulkSyncMode=true → A.87 tolerance still applies, no error.
	if err := eng.verifyHeader(chain, h, []*types.Header{p}, false); err != nil {
		t.Fatalf("bulk-sync wrong-turn tolerance removed unexpectedly: %v", err)
	}
}
