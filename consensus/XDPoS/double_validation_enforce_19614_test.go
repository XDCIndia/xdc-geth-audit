package XDPoS

import (
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

// Helpers and tests in this file were copied from double_validation_enforce_test.go because that file is not in the audit tree.

// m2Chain serves one checkpoint header by number and by hash.
type m2Chain struct {
	cfg *params.ChainConfig
	cp  *types.Header
}

func (c m2Chain) Config() *params.ChainConfig { return c.cfg }

func (c m2Chain) CurrentHeader() *types.Header { return c.cp }

func (c m2Chain) GetHeader(h common.Hash, n uint64) *types.Header {
	if n == c.cp.Number.Uint64() && h == c.cp.Hash() {
		return c.cp
	}
	return nil
}

func (c m2Chain) GetHeaderByNumber(n uint64) *types.Header {
	if n == c.cp.Number.Uint64() {
		return c.cp
	}
	return nil
}

func (c m2Chain) GetHeaderByHash(h common.Hash) *types.Header {
	if h == c.cp.Hash() {
		return c.cp
	}
	return nil
}

// TestV1LightPathUnassignedM2AfterRandomize: on the bulk-sync path a creator
// who is not in the checkpoint M2 map is rejected once TIPRandomize is active,
// and still accepted before that fork (F-2026-19614).
func TestV1LightPathUnassignedM2AfterRandomize(t *testing.T) {
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(true)
	defer core.XdcBulkSyncMode.Store(saved)

	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	a, b := crypto.PubkeyToAddress(keyA.PublicKey), crypto.PubkeyToAddress(keyB.PublicKey)

	// Checkpoint lists only b, so creator a has no M2 assignment.
	extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(extra[extraVanity:], b[:])
	cp := &types.Header{
		Number:     big.NewInt(int64(testConfig.Epoch)),
		Difficulty: big.NewInt(1),
		Extra:      extra,
		Validators: []byte("\x00\x00\x000"),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}
	verify := func(randomizeAt int64) error {
		cfg := &params.ChainConfig{
			ChainID:           big.NewInt(50),
			XDPoS:             testConfig,
			TIPRandomizeBlock: big.NewInt(randomizeAt),
		}
		chain := m2Chain{cfg, cp}
		engine := New(testConfig, rawdb.NewMemoryDatabase())
		engine.recents.Add(cp.Hash(), newSnapshot(testConfig, engine.signatures, cp.Number.Uint64(), cp.Hash(), []common.Address{a, b}))
		h := &types.Header{
			ParentHash: cp.Hash(),
			Number:     big.NewInt(int64(testConfig.Epoch) + 1),
			Time:       cp.Time + testConfig.Period,
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   cp.GasLimit,
		}
		h.Difficulty = engine.calcDifficulty(chain, cp, a)
		sig, err := crypto.Sign(sigHash(h).Bytes(), keyA)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[len(h.Extra)-extraSeal:], sig)
		if h.Validator, err = crypto.Sign(sigHash(h).Bytes(), keyA); err != nil {
			t.Fatal(err)
		}
		return engine.VerifyHeader(chain, h)
	}
	if err := verify(0); !errors.Is(err, errFailedDoubleValidation) {
		t.Fatalf("after TIPRandomize: got %v, want %v", err, errFailedDoubleValidation)
	}
	if err := verify(1_000_000_000); err != nil {
		t.Fatalf("before TIPRandomize: got %v, want nil", err)
	}
}
