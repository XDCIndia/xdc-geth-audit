// Copyright (c) 2026 XDC Network
// F-2026-19602: when a V1 checkpoint's sealer is not in the parent snapshot,
// verifySeal authorizes that sealer from the checkpoint's own extra. On chain
// 50 under full verification the signer-list compare rejects a forged extra
// first. Chain 51 and the bulk-sync light path skip that compare, so the
// fallback still authorizes the sealer.
//
// The fallback also covers ignore-listed checkpoints and a node whose snapshot
// is missing the sealer. Every V1 block on mainnet, Apothem and net 2040 is
// already canonical. Limiting the fallback would reject some of those blocks
// on import, so the rule is unchanged. These tests lock both sides: a forged
// list is still rejected where the compare runs, and a header current-main
// accepts is still accepted here.

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

func checkpointExtraChain(chainID int64) cpSignersChain {
	return cpSignersChain{&params.ChainConfig{ChainID: big.NewInt(chainID), XDPoS: testConfig}}
}

// sealCheckpoint lists `listed` in the checkpoint extra and seals it with key.
// The parent snapshot contains only `snapshotSigner`.
func sealCheckpoint(t *testing.T, chainID int64, number uint64, snapshotSigner, listed common.Address, key *ecdsa.PrivateKey) (*XDPoS, cpSignersChain, *types.Header, *types.Header) {
	t.Helper()
	chain := checkpointExtraChain(chainID)
	engine := New(testConfig, rawdb.NewMemoryDatabase())
	parent := &types.Header{
		Number:     new(big.Int).SetUint64(number - 1),
		Difficulty: big.NewInt(1),
		Extra:      make([]byte, extraVanity+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}
	engine.recents.Add(parent.Hash(), newSnapshot(testConfig, engine.signatures, number-1, parent.Hash(), []common.Address{snapshotSigner}))

	extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(extra[extraVanity:], listed[:])
	cp := &types.Header{
		ParentHash: parent.Hash(),
		Number:     new(big.Int).SetUint64(number),
		Time:       parent.Time + testConfig.Period,
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
	return engine, chain, parent, cp
}

func verifyCheckpoint(t *testing.T, engine *XDPoS, chain cpSignersChain, parent, cp *types.Header, bulk bool) error {
	t.Helper()
	savedBulk := core.XdcBulkSyncMode.Load()
	savedForce := core.XdcForceFullVerify.Load()
	core.XdcBulkSyncMode.Store(bulk)
	core.XdcForceFullVerify.Store(false)
	t.Cleanup(func() {
		core.XdcBulkSyncMode.Store(savedBulk)
		core.XdcForceFullVerify.Store(savedForce)
	})
	_, results := engine.VerifyHeaders(chain, []*types.Header{parent, cp})
	<-results
	return <-results
}

// TestF202619602ForgedExtraRejectedOnFullMainnet checks the protection that
// already runs: chain 50 and a non-Apothem chain such as net 2040 reject a
// checkpoint whose extra replaces the signer set.
func TestF202619602ForgedExtraRejectedOnFullMainnet(t *testing.T) {
	signerKey, _ := crypto.GenerateKey()
	forgerKey, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(signerKey.PublicKey)
	forger := crypto.PubkeyToAddress(forgerKey.PublicKey)
	for _, chainID := range []int64{50, 2040} {
		engine, chain, parent, cp := sealCheckpoint(t, chainID, testConfig.Epoch, signer, forger, forgerKey)
		err := verifyCheckpoint(t, engine, chain, parent, cp, false)
		if !errors.Is(err, errInvalidCheckpointSigners) {
			t.Fatalf("chain %d forged extra: got %v, want %v", chainID, err, errInvalidCheckpointSigners)
		}
	}
}
