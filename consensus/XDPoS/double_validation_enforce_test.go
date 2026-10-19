// Copyright 2026 XDC Network

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

// m2Chain serves one checkpoint header by number and by hash.
type m2Chain struct {
	cfg *params.ChainConfig
	cp  *types.Header
}

func (c m2Chain) Config() *params.ChainConfig  { return c.cfg }
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

// TestV1FullVerifyEnforcesDoubleValidation: under full verification a V1 block
// past the first epoch must carry the M2 co-signature of the creator's assigned
// validator (#1421). Canonical skips this on chain 51.
func TestV1FullVerifyEnforcesDoubleValidation(t *testing.T) {
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(false)
	defer core.XdcBulkSyncMode.Store(saved)

	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	a, b := crypto.PubkeyToAddress(keyA.PublicKey), crypto.PubkeyToAddress(keyB.PublicKey)

	// Checkpoint 900 lists [a, b] with M2 indices [1, 0]: a's M2 is b.
	extra := make([]byte, extraVanity+2*common.AddressLength+extraSeal)
	copy(extra[extraVanity:], a[:])
	copy(extra[extraVanity+common.AddressLength:], b[:])
	cp := &types.Header{
		Number:     big.NewInt(int64(testConfig.Epoch)),
		Difficulty: big.NewInt(1),
		Extra:      extra,
		Validators: []byte("\x00\x00\x001\x00\x00\x000"),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}

	verify := func(chainID int64, m2Key *ecdsa.PrivateKey) error {
		chain := m2Chain{&params.ChainConfig{ChainID: big.NewInt(chainID), XDPoS: testConfig}, cp}
		engine := New(testConfig, rawdb.NewMemoryDatabase())
		engine.recents.Add(cp.Hash(), newSnapshot(testConfig, engine.signatures, cp.Number.Uint64(), cp.Hash(), []common.Address{a, b}))
		h := &types.Header{
			ParentHash: cp.Hash(),
			Number:     big.NewInt(int64(testConfig.Epoch) + 1),
			Time:       cp.Time + testConfig.Period,
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   cp.GasLimit,
			Difficulty: engine.calcDifficulty(chain, cp, a),
		}
		sig, err := crypto.Sign(sigHash(h).Bytes(), keyA)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[len(h.Extra)-extraSeal:], sig)
		if m2Key != nil {
			if h.Validator, err = crypto.Sign(sigHash(h).Bytes(), m2Key); err != nil {
				t.Fatal(err)
			}
		}
		return engine.VerifyHeader(chain, h)
	}

	if err := verify(50, keyA); !errors.Is(err, errFailedDoubleValidation) {
		t.Errorf("wrong M2 co-signer: got %v, want %v", err, errFailedDoubleValidation)
	}
	if err := verify(50, nil); !errors.Is(err, errNoValidatorSignature) {
		t.Errorf("empty Validator: got %v, want %v", err, errNoValidatorSignature)
	}
	if err := verify(50, keyB); err != nil {
		t.Errorf("assigned M2 co-signer rejected: %v", err)
	}
	if err := verify(51, keyA); err != nil {
		t.Errorf("chain 51 (fullVerify off in canonical): got %v, want nil", err)
	}
	// Custom chain (2003): canonical v2.9.3 enforces double validation on every
	// chain but 51, so an empty or wrong co-signature is rejected there too.
	if err := verify(2003, nil); !errors.Is(err, errNoValidatorSignature) {
		t.Errorf("chain 2003, empty Validator: got %v, want %v", err, errNoValidatorSignature)
	}
	if err := verify(2003, keyA); !errors.Is(err, errFailedDoubleValidation) {
		t.Errorf("chain 2003, wrong M2 co-signer: got %v, want %v", err, errFailedDoubleValidation)
	}
	if err := verify(2003, keyB); err != nil {
		t.Errorf("chain 2003, assigned M2 co-signer rejected: %v", err)
	}
	if err := verify(51, nil); err != nil {
		t.Errorf("chain 51, empty Validator: got %v, want nil", err)
	}
}

// batchChain is m2Chain before the checkpoint is written: the checkpoint is only
// known by hash (as a parent in the batch), not by canonical number.
type batchChain struct{ m2Chain }

func (c batchChain) GetHeaderByNumber(n uint64) *types.Header { return nil }

// TestV1DoubleValidationCheckpointInBatch: a batch that crosses a checkpoint
// verifies the first block after it before the checkpoint is written. The M2
// lookup must find the checkpoint among the batch's parents, as canonical does
// through its batch chain reader (verify_chain_reader.go); otherwise every
// epoch boundary fails "couldn't find checkpoint header" once.
func TestV1DoubleValidationCheckpointInBatch(t *testing.T) {
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(false)
	defer core.XdcBulkSyncMode.Store(saved)

	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	a, b := crypto.PubkeyToAddress(keyA.PublicKey), crypto.PubkeyToAddress(keyB.PublicKey)

	extra := make([]byte, extraVanity+2*common.AddressLength+extraSeal)
	copy(extra[extraVanity:], a[:])
	copy(extra[extraVanity+common.AddressLength:], b[:])
	cp := &types.Header{
		Number:     big.NewInt(int64(testConfig.Epoch)),
		Difficulty: big.NewInt(1),
		Extra:      extra,
		Validators: []byte("\x00\x00\x001\x00\x00\x000"),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}
	chain := batchChain{m2Chain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: testConfig}, cp}}
	engine := New(testConfig, rawdb.NewMemoryDatabase())
	engine.recents.Add(cp.Hash(), newSnapshot(testConfig, engine.signatures, cp.Number.Uint64(), cp.Hash(), []common.Address{a, b}))
	h := &types.Header{
		ParentHash: cp.Hash(),
		Number:     big.NewInt(int64(testConfig.Epoch) + 1),
		Time:       cp.Time + testConfig.Period,
		Extra:      make([]byte, extraVanity+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   cp.GasLimit,
		Difficulty: engine.calcDifficulty(chain, cp, a),
	}
	sig, err := crypto.Sign(sigHash(h).Bytes(), keyA)
	if err != nil {
		t.Fatal(err)
	}
	copy(h.Extra[len(h.Extra)-extraSeal:], sig)
	if h.Validator, err = crypto.Sign(sigHash(h).Bytes(), keyB); err != nil {
		t.Fatal(err)
	}
	if err := engine.verifySeal(chain, h, []*types.Header{cp}); err != nil {
		t.Fatalf("checkpoint in the batch's parents: got %v, want nil", err)
	}
	// Sidechain re-import after a state rewind: the checkpoint is stored (found
	// by hash) but not canonical (not found by number) and not in the batch.
	if err := engine.verifySeal(chain, h, nil); err != nil {
		t.Fatalf("checkpoint stored but not canonical: got %v, want nil", err)
	}
}

// TestV1VerifyHeaderBulkSyncRefusesEmptyValidator: in bulk sync VerifyHeader
// still refuses a V1 block past the first epoch with an empty header.Validator,
// as the VerifyHeaders light path does. The NewBlock handler relies on it to
// wait for the M2 co-signed block instead of importing (and reporting as a BAD
// BLOCK) the un-co-signed one (#1624). The co-signer identity check stays
// full-verify only.
func TestV1VerifyHeaderBulkSyncRefusesEmptyValidator(t *testing.T) {
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(true)
	defer core.XdcBulkSyncMode.Store(saved)

	keyA, _ := crypto.GenerateKey()
	keyB, _ := crypto.GenerateKey()
	a, b := crypto.PubkeyToAddress(keyA.PublicKey), crypto.PubkeyToAddress(keyB.PublicKey)
	extra := make([]byte, extraVanity+2*common.AddressLength+extraSeal)
	copy(extra[extraVanity:], a[:])
	copy(extra[extraVanity+common.AddressLength:], b[:])
	cp := &types.Header{
		Number:     big.NewInt(int64(testConfig.Epoch)),
		Difficulty: big.NewInt(1),
		Extra:      extra,
		Validators: []byte("\x00\x00\x001\x00\x00\x000"),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}
	verify := func(chainID int64, m2Key *ecdsa.PrivateKey) error {
		chain := m2Chain{&params.ChainConfig{ChainID: big.NewInt(chainID), XDPoS: testConfig}, cp}
		engine := New(testConfig, rawdb.NewMemoryDatabase())
		engine.recents.Add(cp.Hash(), newSnapshot(testConfig, engine.signatures, cp.Number.Uint64(), cp.Hash(), []common.Address{a, b}))
		h := &types.Header{
			ParentHash: cp.Hash(),
			Number:     big.NewInt(int64(testConfig.Epoch) + 1),
			Time:       cp.Time + testConfig.Period,
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   cp.GasLimit,
			Difficulty: engine.calcDifficulty(chain, cp, a),
		}
		sig, err := crypto.Sign(sigHash(h).Bytes(), keyA)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[len(h.Extra)-extraSeal:], sig)
		if m2Key != nil {
			if h.Validator, err = crypto.Sign(sigHash(h).Bytes(), m2Key); err != nil {
				t.Fatal(err)
			}
		}
		return engine.VerifyHeader(chain, h)
	}
	if err := verify(2002, nil); !errors.Is(err, errNoValidatorSignature) {
		t.Errorf("bulk sync, empty Validator: got %v, want %v", err, errNoValidatorSignature)
	}
	if err := verify(2002, keyB); err != nil {
		t.Errorf("bulk sync, assigned M2 co-signer rejected: %v", err)
	}
	if err := verify(2002, keyA); err != nil {
		t.Errorf("bulk sync, co-signer identity is full-verify only: got %v, want nil", err)
	}
	if err := verify(51, nil); err != nil {
		t.Errorf("chain 51, empty Validator: got %v, want nil", err)
	}
}
