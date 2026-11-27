// Copyright (c) 2026 XDC Network
// F-2026-19602: a V1 checkpoint must be checked against a signer set the header
// did not choose. Canonical (engine_v1/engine.go:255-268) compares the
// snapshot, then the contract state of the gap block, and never seeds a
// snapshot from the header under verification.

package XDPoS

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// gapSnapChain serves headers by hash so the checkpoint walk can reach the gap block.
type gapSnapChain struct {
	cfg    *params.ChainConfig
	byHash map[common.Hash]*types.Header
}

func (c gapSnapChain) Config() *params.ChainConfig  { return c.cfg }
func (c gapSnapChain) CurrentHeader() *types.Header { return nil }
func (c gapSnapChain) GetHeader(h common.Hash, _ uint64) *types.Header {
	return c.byHash[h]
}
func (c gapSnapChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (c gapSnapChain) GetHeaderByHash(h common.Hash) *types.Header { return c.byHash[h] }

// gapSnapCheckpoint builds a checkpoint at block 10 (gap block 7) that lists
// `listed`, sealed by key. The parent snapshot holds `parentSigners`; the gap
// snapshot in the DB holds `gapSigners`; the contract hook returns `contract`.
func gapSnapCheckpoint(t *testing.T, key *ecdsa.PrivateKey, parentSigners, gapSigners, contract, listed []common.Address) (*XDPoS, gapSnapChain, *types.Header, *types.Header, *types.Header, *int) {
	t.Helper()
	cfg := *testConfig
	cfg.Epoch, cfg.Gap = 10, 3
	db := rawdb.NewMemoryDatabase()
	engine := New(&cfg, db)
	chain := gapSnapChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}, map[common.Hash]*types.Header{}}

	var gap, parent *types.Header
	ph := common.Hash{}
	for n := uint64(7); n <= 9; n++ {
		h := &types.Header{
			Number:     new(big.Int).SetUint64(n),
			ParentHash: ph,
			Difficulty: big.NewInt(1),
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   420000000,
			Time:       1000 + n,
		}
		chain.byHash[h.Hash()] = h
		if n == 7 {
			gap = h
		}
		parent, ph = h, h.Hash()
	}
	if err := newSnapshot(&cfg, engine.signatures, 7, gap.Hash(), gapSigners).store(db); err != nil {
		t.Fatal(err)
	}
	engine.recents.Add(parent.Hash(), newSnapshot(&cfg, engine.signatures, 9, parent.Hash(), parentSigners))

	calls := new(int)
	engine.HookGetSignersFromContract = func(*types.Header) ([]common.Address, error) {
		*calls++
		return contract, nil
	}

	extra := make([]byte, extraVanity+len(listed)*common.AddressLength+extraSeal)
	for i, a := range listed {
		copy(extra[extraVanity+i*common.AddressLength:], a[:])
	}
	cp := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(10),
		Time:       parent.Time + cfg.Period,
		Extra:      extra,
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   parent.GasLimit,
		Difficulty: engine.calcDifficulty(chain, parent, crypto.PubkeyToAddress(key.PublicKey)),
	}
	sig, err := crypto.Sign(sigHash(cp).Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	copy(cp.Extra[len(cp.Extra)-extraSeal:], sig)
	return engine, chain, gap, parent, cp, calls
}

func verifyGapSnapCheckpoint(t *testing.T, engine *XDPoS, chain gapSnapChain, parent, cp *types.Header) error {
	t.Helper()
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(false)
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(saved) })
	_, results := engine.VerifyHeaders(chain, []*types.Header{parent, cp})
	<-results // parent: not under test
	return <-results
}

func sameSigners(got, want []common.Address) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[common.Address]bool{}
	for _, a := range got {
		seen[a] = true
	}
	for _, a := range want {
		if !seen[a] {
			return false
		}
	}
	return true
}

// TestF202619602OutsiderExtraRejectedNothingWritten: a checkpoint whose Extra
// adds an outsider to the set is rejected. The gap snapshot in recents and the
// DB keeps the real set. Before the fix evictPoisonedGapSnap rebuilt the gap
// snapshot from the Extra, so the header compared against itself and passed.
func TestF202619602OutsiderExtraRejectedNothingWritten(t *testing.T) {
	key, _ := crypto.GenerateKey()
	sealer := crypto.PubkeyToAddress(key.PublicKey)
	real := []common.Address{sealer, mn(0xbb)}
	outsider := mn(0xee)

	engine, chain, gap, parent, cp, calls := gapSnapCheckpoint(t, key, real, real, real, append(append([]common.Address{}, real...), outsider))
	if err := verifyGapSnapCheckpoint(t, engine, chain, parent, cp); !errors.Is(err, errInvalidCheckpointSigners) {
		t.Fatalf("checkpoint with an outsider in Extra: got %v, want %v", err, errInvalidCheckpointSigners)
	}
	if *calls == 0 {
		t.Fatal("the contract fallback was not tried")
	}
	if s, ok := engine.recents.Get(gap.Hash()); ok && !sameSigners(s.GetSigners(), real) {
		t.Fatalf("recents gap snapshot changed to %v", s.GetSigners())
	}
	stored, err := loadSnapshot(engine.config, engine.signatures, engine.db, gap.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if !sameSigners(stored.GetSigners(), real) {
		t.Fatalf("DB gap snapshot changed to %v, want %v", stored.GetSigners(), real)
	}
}
