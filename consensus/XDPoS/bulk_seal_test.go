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

// bulkSealChain is a minimal in-memory ChainHeaderReader for #1405.
type bulkSealChain struct {
	cfg     *params.ChainConfig
	headers map[uint64]*types.Header
}

func (c *bulkSealChain) Config() *params.ChainConfig  { return c.cfg }
func (c *bulkSealChain) CurrentHeader() *types.Header { return c.headers[0] }
func (c *bulkSealChain) GetHeader(h common.Hash, n uint64) *types.Header {
	if hd := c.headers[n]; hd != nil && hd.Hash() == h {
		return hd
	}
	return nil
}
func (c *bulkSealChain) GetHeaderByNumber(n uint64) *types.Header { return c.headers[n] }
func (c *bulkSealChain) GetHeaderByHash(h common.Hash) *types.Header {
	for _, hd := range c.headers {
		if hd.Hash() == h {
			return hd
		}
	}
	return nil
}

// TestBulkSyncV1VerifiesSeal: in bulk-sync mode a V1 header sealed by a key
// that is not a masternode must be rejected; one sealed by the masternode
// must be accepted (#1405).
func TestBulkSyncV1VerifiesSeal(t *testing.T) {
	saved := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(true)
	defer core.XdcBulkSyncMode.Store(saved)

	signerKey, _ := crypto.GenerateKey()
	forgerKey, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(signerKey.PublicKey)

	extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(extra[extraVanity:], signer[:])
	genesis := &types.Header{
		Number:     big.NewInt(0),
		Difficulty: big.NewInt(1),
		Extra:      extra,
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
	}
	chain := &bulkSealChain{
		cfg:     &params.ChainConfig{ChainID: big.NewInt(551), XDPoS: testConfig},
		headers: map[uint64]*types.Header{0: genesis},
	}

	seal := func(engine *XDPoS, key *ecdsa.PrivateKey) *types.Header {
		h := &types.Header{
			ParentHash: genesis.Hash(),
			Number:     big.NewInt(1),
			Time:       genesis.Time + testConfig.Period,
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   genesis.GasLimit,
			Difficulty: engine.calcDifficulty(chain, genesis, crypto.PubkeyToAddress(key.PublicKey)),
		}
		sig, err := crypto.Sign(sigHash(h).Bytes(), key)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[len(h.Extra)-extraSeal:], sig)
		return h
	}
	verify := func(engine *XDPoS, h *types.Header) error {
		_, results := engine.VerifyHeaders(chain, []*types.Header{h})
		return <-results
	}

	engine := New(testConfig, rawdb.NewMemoryDatabase())
	if err := verify(engine, seal(engine, forgerKey)); !errors.Is(err, errUnauthorized) {
		t.Fatalf("forged V1 header in bulk mode: got %v, want %v", err, errUnauthorized)
	}
	engine = New(testConfig, rawdb.NewMemoryDatabase())
	if err := verify(engine, seal(engine, signerKey)); err != nil {
		t.Fatalf("correctly sealed V1 header in bulk mode rejected: %v", err)
	}
}
