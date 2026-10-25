// Copyright 2026 XDC Network

package hooks

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// checkpointHeader builds a minimal checkpoint header with the given number and
// optional Validators bytes for use in hook tests.
func makeCheckpointHeader(number uint64, validators []byte) *types.Header {
	h := &types.Header{
		Number:     new(big.Int).SetUint64(number),
		ParentHash: common.Hash{0x01},
		Validators: validators,
	}
	return h
}

// TestV1HookVerifyMNsRecomputesValidators: the checkpoint Validators field is
// checked against the M2 list derived from the parent's 0x90 randomize values;
// wrong bytes are rejected and the header is never rewritten (#1412). A minted
// checkpoint gets the same list.
func TestV1HookVerifyMNsRecomputesValidators(t *testing.T) {
	var masternodes []common.Address
	for i := byte(1); i <= 5; i++ {
		masternodes = append(masternodes, common.BytesToAddress([]byte{0xa0, i}))
	}
	// masternodes[0] committed randomize value 42; the others committed none (0).
	var opening common.Hash
	copy(opening[:], "0123456789abcdef0123456789abcdef")
	var secret common.Hash
	copy(secret[common.HashLength-24:], contracts.Encrypt(opening[:], "42"))
	secretArr := common.BigToHash(state.GetLocMappingAtKey(common.BytesToHash(masternodes[0].Bytes()), 0))
	storage := map[common.Hash]common.Hash{
		secretArr: common.BigToHash(big.NewInt(1)),
		state.GetLocDynamicArrAtElement(secretArr, 0, 1):                                          secret,
		common.BigToHash(state.GetLocMappingAtKey(common.BytesToHash(masternodes[0].Bytes()), 1)): opening,
	}
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
		Alloc:      types.GenesisAlloc{common.RandomizeSMCBinary: {Balance: common.Big1, Storage: storage}},
	}
	db := rawdb.NewMemoryDatabase()
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	xdposConfig := &params.XDPoSConfig{Epoch: 900, Gap: 450, Period: 2}
	chainConfig := *params.AllEthashProtocolChanges
	chainConfig.XDPoS = xdposConfig
	adaptor := XDPoS.New(xdposConfig, db)
	AttachConsensusV1Hooks(adaptor, bc, &chainConfig)

	// Canonical GenM2FromRandomize([42 0 0 0 0], 5) = [4 3 0 2 1]; with no
	// randomize values at all it would be [4 3 1 0 2].
	correct := []byte("\x00\x00\x004\x00\x00\x003\x00\x00\x000\x00\x00\x002\x00\x00\x001")
	wrong := []byte("\x00\x00\x004\x00\x00\x003\x00\x00\x001\x00\x00\x000\x00\x00\x002")

	checkpoint := func(validators []byte) *types.Header {
		return &types.Header{
			Number:     big.NewInt(900),
			ParentHash: bc.Genesis().Hash(),
			Validators: bytes.Clone(validators),
		}
	}
	h := checkpoint(wrong)
	if err := adaptor.HookVerifyMNs(h, masternodes); !errors.Is(err, XDPoS.ErrInvalidCheckpointValidators) {
		t.Fatalf("wrong Validators: got %v, want %v", err, XDPoS.ErrInvalidCheckpointValidators)
	}
	if !bytes.Equal(h.Validators, wrong) {
		t.Fatalf("header.Validators rewritten to %x", h.Validators)
	}
	if err := adaptor.HookVerifyMNs(checkpoint(correct), masternodes); err != nil {
		t.Fatalf("canonical Validators rejected: %v", err)
	}
	// Mint side: a checkpoint this node seals carries the same list (canonical
	// HookValidator), so the other nodes accept it.
	h = checkpoint(nil)
	got, err := adaptor.HookValidator(h, masternodes)
	if err != nil {
		t.Fatalf("HookValidator: %v", err)
	}
	if !bytes.Equal(got, correct) || !bytes.Equal(h.Validators, correct) {
		t.Fatalf("minted Validators %x (header %x), want %x", got, h.Validators, correct)
	}
}

// TestV1HookVerifyMNsApothemSkip: on Apothem (chain 51) the hook returns nil
// and increments the apothem-skips counter even when Validators bytes are
// wrong. This mirrors canonical shouldDisableFullVerify behaviour.
// Hacken F-2026-19462 residual: the bypass is now observable via Warn + metric.
func TestV1HookVerifyMNsApothemSkip(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	// Chain ID 51 = Apothem testnet.
	chainConfig := *params.AllEthashProtocolChanges
	chainConfig.ChainID = new(big.Int).Set(params.XDCApothemChainConfig.ChainID)
	chainConfig.XDPoS = &params.XDPoSConfig{Epoch: 900, Gap: 450, Period: 2}
	adaptor := XDPoS.New(chainConfig.XDPoS, db)
	AttachConsensusV1Hooks(adaptor, bc, &chainConfig)

	before := hookVerifyMNsApothemSkips.Snapshot().Count()

	// A clearly wrong Validators field: the hook must accept it on chain 51.
	h := makeCheckpointHeader(900, []byte("wrong"))
	if err := adaptor.HookVerifyMNs(h, nil); err != nil {
		t.Fatalf("Apothem skip: expected nil, got %v", err)
	}

	after := hookVerifyMNsApothemSkips.Snapshot().Count()
	if after-before != 1 {
		t.Fatalf("apothem_skips counter: got delta %d, want 1", after-before)
	}
}

// TestV1HookVerifyMNsMissingParentSkip: when the parent header is unknown the
// hook returns nil and increments the missing-state-skips counter.
// Hacken F-2026-19462 residual: the bypass is now observable via Warn + metric.
func TestV1HookVerifyMNsMissingParentSkip(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	// Mainnet-like chain (not Apothem), so the bypass does not short-circuit.
	chainConfig := *params.AllEthashProtocolChanges
	chainConfig.ChainID = big.NewInt(50)
	chainConfig.XDPoS = &params.XDPoSConfig{Epoch: 900, Gap: 450, Period: 2}
	adaptor := XDPoS.New(chainConfig.XDPoS, db)
	AttachConsensusV1Hooks(adaptor, bc, &chainConfig)

	before := hookVerifyMNsMissingStateSkips.Snapshot().Count()

	// ParentHash 0xff…ff does not exist in the chain: parent lookup returns nil.
	h := &types.Header{
		Number:     big.NewInt(900),
		ParentHash: common.Hash{0xff},
		Validators: []byte("irrelevant"),
	}
	masternodes := []common.Address{{0x01}}
	if err := adaptor.HookVerifyMNs(h, masternodes); err != nil {
		t.Fatalf("missing-parent skip: expected nil, got %v", err)
	}

	after := hookVerifyMNsMissingStateSkips.Snapshot().Count()
	if after-before != 1 {
		t.Fatalf("missing_state_skips counter: got delta %d, want 1", after-before)
	}
}
