// Copyright 2026 XDC Network

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

// unsealedChain is a minimal in-memory ChainHeaderReader for #1417.
type unsealedChain struct {
	cfg     *params.ChainConfig
	headers map[uint64]*types.Header
}

func (c *unsealedChain) Config() *params.ChainConfig  { return c.cfg }
func (c *unsealedChain) CurrentHeader() *types.Header { return c.headers[0] }
func (c *unsealedChain) GetHeader(h common.Hash, n uint64) *types.Header {
	if hd := c.headers[n]; hd != nil && hd.Hash() == h {
		return hd
	}
	return nil
}
func (c *unsealedChain) GetHeaderByNumber(n uint64) *types.Header  { return c.headers[n] }
func (c *unsealedChain) GetHeaderByHash(common.Hash) *types.Header { return nil }

// unsealedFieldSetters sets each post-London optional header field.
var unsealedFieldSetters = map[string]func(h *types.Header){
	"WithdrawalsHash":     func(h *types.Header) { h.WithdrawalsHash = &types.EmptyWithdrawalsHash },
	"BlobGasUsed":         func(h *types.Header) { h.BlobGasUsed = new(uint64) },
	"ExcessBlobGas":       func(h *types.Header) { h.ExcessBlobGas = new(uint64) },
	"ParentBeaconRoot":    func(h *types.Header) { h.ParentBeaconRoot = new(common.Hash) },
	"RequestsHash":        func(h *types.Header) { h.RequestsHash = &types.EmptyRequestsHash },
	"BlockAccessListHash": func(h *types.Header) { h.BlockAccessListHash = &types.EmptyBlockAccessListHash },
	"SlotNumber":          func(h *types.Header) { h.SlotNumber = new(uint64) },
}

// TestRejectUnsealedHeaderFields: a correctly sealed V1 header verifies; the
// same header with any post-London optional field set is rejected, on both
// the full and the bulk-sync path (#1417).
func TestRejectUnsealedHeaderFields(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(key.PublicKey)

	extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(extra[extraVanity:], signer[:])
	genesis := &types.Header{
		Number: big.NewInt(0), Difficulty: big.NewInt(1), Extra: extra,
		UncleHash: types.EmptyUncleHash, GasLimit: 420000000,
	}
	chain := &unsealedChain{
		cfg:     &params.ChainConfig{ChainID: big.NewInt(551), XDPoS: testConfig},
		headers: map[uint64]*types.Header{0: genesis},
	}
	engine := New(testConfig, rawdb.NewMemoryDatabase())
	sealed := &types.Header{
		ParentHash: genesis.Hash(),
		Number:     big.NewInt(1),
		Time:       genesis.Time + testConfig.Period,
		Extra:      make([]byte, extraVanity+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   genesis.GasLimit,
		Difficulty: engine.calcDifficulty(chain, genesis, signer),
	}
	sig, err := crypto.Sign(sigHash(sealed).Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	copy(sealed.Extra[len(sealed.Extra)-extraSeal:], sig)

	if err := engine.VerifyHeader(chain, sealed); err != nil {
		t.Fatalf("normal sealed header rejected: %v", err)
	}
	for _, bulk := range []bool{false, true} {
		saved := core.XdcBulkSyncMode.Load()
		core.XdcBulkSyncMode.Store(bulk)
		for name, set := range unsealedFieldSetters {
			twin := types.CopyHeader(sealed)
			set(twin)
			_, results := engine.VerifyHeaders(chain, []*types.Header{twin})
			if err := <-results; !errors.Is(err, errUnsealedHeaderFields) {
				t.Errorf("bulk=%v %s set: got %v, want %v", bulk, name, err, errUnsealedHeaderFields)
			}
		}
		core.XdcBulkSyncMode.Store(saved)
	}
}

// TestRejectUnsealedHeaderFieldsV2: the check runs before V1/V2 dispatch, so a
// V2 header with WithdrawalsHash set is rejected before reaching engine_v2.
func TestRejectUnsealedHeaderFieldsV2(t *testing.T) {
	cfg := *testConfig
	cfg.V2 = &params.V2{
		SwitchBlock:   big.NewInt(900),
		CurrentConfig: &params.V2Config{MaxMasternodes: 108, MinePeriod: 2},
		AllConfigs:    map[uint64]*params.V2Config{0: {MaxMasternodes: 108, MinePeriod: 2}},
	}
	// AmsterdamBlock is nil (not set) so the unseal check applies in full.
	chainCfg := &params.ChainConfig{ChainID: big.NewInt(551), XDPoS: &cfg}
	chain := &unsealedChain{cfg: chainCfg}
	engine := New(&cfg, rawdb.NewMemoryDatabase())
	h := &types.Header{Number: big.NewInt(901), WithdrawalsHash: &types.EmptyWithdrawalsHash}
	if err := engine.verifyHeader(chain, h, nil); !errors.Is(err, errUnsealedHeaderFields) {
		t.Fatalf("V2 header with WithdrawalsHash: got %v, want %v", err, errUnsealedHeaderFields)
	}
}

// canonicalAmsterdamHeader returns a header with all mandatory Amsterdam XDC
// optional fields set to their canonical empty values plus a non-nil BAL hash.
func canonicalAmsterdamHeader() *types.Header {
	zeroHash := common.Hash{}
	blobZero := uint64(0)
	balHash := types.EmptyBlockAccessListHash
	return &types.Header{
		Number:              big.NewInt(31500),
		WithdrawalsHash:     &types.EmptyWithdrawalsHash,
		BlobGasUsed:         &blobZero,
		ExcessBlobGas:       &blobZero,
		ParentBeaconRoot:    &zeroHash,
		RequestsHash:        &types.EmptyRequestsHash,
		BlockAccessListHash: &balHash,
	}
}

// TestAmsterdamXDCCanonicalHeaderAccepted: a header carrying all canonical
// empty placeholder values passes verifyNoUnsealedFields on Amsterdam blocks.
func TestAmsterdamXDCCanonicalHeaderAccepted(t *testing.T) {
	amsterdamBlock := big.NewInt(31500)
	chainCfg := &params.ChainConfig{
		ChainID:        big.NewInt(2030),
		XDPoS:          testConfig,
		AmsterdamBlock: amsterdamBlock,
	}
	if err := verifyNoUnsealedFields(chainCfg, canonicalAmsterdamHeader()); err != nil {
		t.Fatalf("canonical Amsterdam header rejected: %v", err)
	}
}

// TestAmsterdamXDCHeaderRejectsArbitraryValues: a header with an arbitrary
// ParentBeaconRoot (non-zero) is rejected even when Amsterdam is active.
func TestAmsterdamXDCHeaderRejectsArbitraryValues(t *testing.T) {
	chainCfg := &params.ChainConfig{
		ChainID:        big.NewInt(2030),
		XDPoS:          testConfig,
		AmsterdamBlock: big.NewInt(31500),
	}
	cases := []struct {
		name   string
		mutate func(h *types.Header)
	}{
		{"nil WithdrawalsHash", func(h *types.Header) { h.WithdrawalsHash = nil }},
		{"nil BlobGasUsed", func(h *types.Header) { h.BlobGasUsed = nil }},
		{"nil ExcessBlobGas", func(h *types.Header) { h.ExcessBlobGas = nil }},
		{"nil ParentBeaconRoot", func(h *types.Header) { h.ParentBeaconRoot = nil }},
		{"nil RequestsHash", func(h *types.Header) { h.RequestsHash = nil }},
		{"nil BlockAccessListHash", func(h *types.Header) { h.BlockAccessListHash = nil }},
		{"non-zero ParentBeaconRoot", func(h *types.Header) {
			h.ParentBeaconRoot = &common.Hash{1}
		}},
		{"non-empty WithdrawalsHash", func(h *types.Header) {
			wh := common.Hash{2}
			h.WithdrawalsHash = &wh
		}},
		{"SlotNumber set", func(h *types.Header) { h.SlotNumber = new(uint64) }},
	}
	for _, tc := range cases {
		h := canonicalAmsterdamHeader()
		tc.mutate(h)
		if err := verifyNoUnsealedFields(chainCfg, h); !errors.Is(err, errUnsealedHeaderFields) {
			t.Errorf("%s: got %v, want %v", tc.name, err, errUnsealedHeaderFields)
		}
	}
}
