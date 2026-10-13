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

// cpSignersChain serves only the chain config; parents come from the batch.
type cpSignersChain struct{ cfg *params.ChainConfig }

func (c cpSignersChain) Config() *params.ChainConfig                 { return c.cfg }
func (c cpSignersChain) CurrentHeader() *types.Header                { return nil }
func (c cpSignersChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c cpSignersChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (c cpSignersChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// cpSignersSetup builds an engine whose snapshot at number-1 holds only
// signer, and a checkpoint at number that lists only listed, sealed by key.
func cpSignersSetup(t *testing.T, number uint64, signer common.Address, listed common.Address, key *ecdsa.PrivateKey) (*XDPoS, cpSignersChain, *types.Header, *types.Header) {
	t.Helper()
	chain := cpSignersChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: testConfig}}
	engine := New(testConfig, rawdb.NewMemoryDatabase())
	parent := &types.Header{
		Number:     new(big.Int).SetUint64(number - 1),
		Difficulty: big.NewInt(1),
		Extra:      make([]byte, extraVanity+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}
	engine.recents.Add(parent.Hash(), newSnapshot(testConfig, engine.signatures, number-1, parent.Hash(), []common.Address{signer}))

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

// TestForgedCheckpointSignersRejected: a V1 checkpoint that lists only its own
// sealer, who is not in the parent snapshot, must be rejected with
// errInvalidCheckpointSigners and must not change the snapshot (#1418).
func TestForgedCheckpointSignersRejected(t *testing.T) {
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(false)
	defer core.XdcBulkSyncMode.Store(saved)

	signerKey, _ := crypto.GenerateKey()
	forgerKey, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(signerKey.PublicKey)
	forger := crypto.PubkeyToAddress(forgerKey.PublicKey)

	engine, chain, parent, cp := cpSignersSetup(t, testConfig.Epoch, signer, forger, forgerKey)
	_, results := engine.VerifyHeaders(chain, []*types.Header{parent, cp})
	<-results // parent: not under test
	if err := <-results; !errors.Is(err, errInvalidCheckpointSigners) {
		t.Fatalf("forged checkpoint: got %v, want %v", err, errInvalidCheckpointSigners)
	}
	snap, ok := engine.recents.Get(parent.Hash())
	if !ok {
		t.Fatal("parent snapshot gone")
	}
	if got := snap.GetSigners(); len(got) != 1 || got[0] != signer {
		t.Fatalf("parent snapshot signers changed to %v, want [%v]", got, signer)
	}
}

// TestIgnoreListedCheckpointSkipsSignerCheck: canonical skips the signer check
// on the ignore-listed checkpoints, so a strict reject must too (#1418).
func TestIgnoreListedCheckpointSkipsSignerCheck(t *testing.T) {
	signerKey, _ := crypto.GenerateKey()
	other := mn(0xbb)
	signer := crypto.PubkeyToAddress(signerKey.PublicKey)

	const ignored = 1032300 // mainnet IgnoreSignerCheckBlocksMainnet
	engine, chain, parent, cp := cpSignersSetup(t, ignored, signer, other, signerKey)
	snap, _ := engine.recents.Get(parent.Hash())
	if err := engine.checkSignersOnCheckpoint(chain, cp, snap.GetSigners(), nil); err != nil {
		t.Fatalf("ignore-listed checkpoint %d: got %v, want nil", ignored, err)
	}
	// The same mismatch one epoch earlier is not ignore-listed.
	engine, chain, parent, cp = cpSignersSetup(t, ignored-testConfig.Epoch, signer, other, signerKey)
	snap, _ = engine.recents.Get(parent.Hash())
	if err := engine.checkSignersOnCheckpoint(chain, cp, snap.GetSigners(), nil); !errors.Is(err, errInvalidCheckpointSigners) {
		t.Fatalf("checkpoint %d: got %v, want %v", ignored-testConfig.Epoch, err, errInvalidCheckpointSigners)
	}
}
