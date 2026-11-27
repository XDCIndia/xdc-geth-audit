// Copyright 2026 XDC Network
// XDC-HUNT-19 (#2072): V1 header verification runs the base-fee rule of
// canonical engine_v1/engine.go:258 (eip1559.VerifyEip1559Header): before the
// EIP-1559 block the header carries no base fee, from it the exact tier price.
//
// Run: go test ./consensus/XDPoS/ -run TestV1HeaderBaseFee -count=1

package XDPoS

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestV1HeaderBaseFee(t *testing.T) {
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
	tier := params.XDCBaseFeeForBlock(&params.ChainConfig{}, big.NewInt(1))

	// verify checks block 901 (V1) with the given base fee, on full or light
	// verification, with EIP-1559 starting at forkBlock (nil: never).
	verify := func(full bool, forkBlock *big.Int, baseFee *big.Int) error {
		saved := core.XdcBulkSyncMode.Load()
		core.XdcBulkSyncMode.Store(!full)
		defer core.XdcBulkSyncMode.Store(saved)

		cfg := &params.ChainConfig{ChainID: big.NewInt(50), XDPoS: testConfig, Eip1559Block: forkBlock}
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
			Difficulty: engine.calcDifficulty(chain, cp, a),
			BaseFee:    baseFee,
		}
		sig, err := crypto.Sign(sigHash(h).Bytes(), keyA)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[len(h.Extra)-extraSeal:], sig)
		if h.Validator, err = crypto.Sign(sigHash(h).Bytes(), keyB); err != nil {
			t.Fatal(err)
		}
		return engine.VerifyHeader(chain, h)
	}

	for _, full := range []bool{true, false} {
		name := map[bool]string{true: "full", false: "light"}[full]
		// Before the EIP-1559 block a base fee is refused (BaseFee is in the seal hash).
		if err := verify(full, nil, big.NewInt(12345)); err == nil || !strings.Contains(err.Error(), "invalid baseFee") {
			t.Errorf("%s, pre-fork header with a base fee: got %v, want invalid baseFee", name, err)
		}
		if err := verify(full, nil, nil); err != nil {
			t.Errorf("%s, pre-fork header without a base fee: %v", name, err)
		}
		// From the EIP-1559 block the exact tier price is required.
		fork := big.NewInt(int64(testConfig.Epoch) + 1)
		if err := verify(full, fork, nil); err == nil || !strings.Contains(err.Error(), "missing baseFee") {
			t.Errorf("%s, post-fork header without a base fee: got %v, want missing baseFee", name, err)
		}
		if err := verify(full, fork, big.NewInt(12345)); err == nil || !strings.Contains(err.Error(), "invalid baseFee") {
			t.Errorf("%s, post-fork header with a wrong base fee: got %v, want invalid baseFee", name, err)
		}
		if err := verify(full, fork, tier); err != nil {
			t.Errorf("%s, post-fork header with the tier price: %v", name, err)
		}
	}
}
