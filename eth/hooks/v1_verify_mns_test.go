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

// TestV1HookVerifyMNsRecomputesValidators: the checkpoint Validators field is
// checked against the M2 list derived from the parent's 0x90 randomize values;
// wrong bytes are rejected and the header is never rewritten (#1412).
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
}
