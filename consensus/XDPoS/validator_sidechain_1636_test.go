// Copyright (c) 2026 XDC Network
//
// Tests for #1636 V1 fast-path ancestry safety:
// getValidatorWithParents must use the sidechain checkpoint when the canonical
// checkpoint at the same number differs (i.e., cpNo is in the parents slice).

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// mockChainReader1636 is a minimal ChainHeaderReader for V1 tests.
// GetHeaderByNumber returns a pre-set "canonical" checkpoint; everything else is nil.
type mockChainReader1636 struct {
	cfg            *params.ChainConfig
	canonicalAt900 *types.Header
}

func (m *mockChainReader1636) Config() *params.ChainConfig                        { return m.cfg }
func (m *mockChainReader1636) CurrentHeader() *types.Header                       { return nil }
func (m *mockChainReader1636) GetHeader(hash common.Hash, n uint64) *types.Header { return nil }
func (m *mockChainReader1636) GetHeaderByHash(hash common.Hash) *types.Header     { return nil }
func (m *mockChainReader1636) GetHeaderByNumber(number uint64) *types.Header {
	if number == 900 {
		return m.canonicalAt900
	}
	return nil
}

// makeV1CheckpointHeader builds a minimal V1 checkpoint header (number % EpocBlockRandomize == 0)
// with the given masternode addresses packed into Extra and a single validator index 0.
func makeV1CheckpointHeader(number uint64, mns []common.Address, salt byte) *types.Header {
	// Extra = extraVanity (32) + addresses + extraSeal (65)
	extra := make([]byte, extraVanity+len(mns)*common.AddressLength+extraSeal)
	// Differentiate headers with different salts so their hashes differ.
	extra[0] = salt
	for i, mn := range mns {
		copy(extra[extraVanity+i*common.AddressLength:], mn[:])
	}
	// Validators: one 4-byte ASCII entry per masternode, index 0 → maps each M1 to itself.
	validators := make([]byte, len(mns)*M2ByteLength)
	for i := range mns {
		validators[i*M2ByteLength] = '0' // ASCII "0" → int 0; remaining bytes are \x00 (trimmed)
	}
	return &types.Header{
		Number:     big.NewInt(int64(number)),
		Difficulty: big.NewInt(1),
		Extra:      extra,
		Validators: validators,
	}
}

// TestSideChainCheckpointGetValidatorWithParents proves that fast path A
// (cpNo in the parents slice) selects the sidechain checkpoint rather than
// the canonical checkpoint returned by GetHeaderByNumber.
//
// Setup:
//   - Canonical block 900: masternodes = [A]
//   - Sidechain block 900: masternodes = [D] (different Extra → different hash)
//   - Sidechain block 901 has ParentHash = sidechain block 900's hash
//   - parents = [sidechain block 900]
//
// With fast path A: cpNo=900 is in parents[0].Number range → use parents[0] (sidechain).
// D is in sidechain masternodes → getValidatorWithParents returns D (D→D map).
// Without fast path A (canonical): D not in canonical masternodes → returns Address{}.
func TestSideChainCheckpointGetValidatorWithParents(t *testing.T) {
	const epoch = 900
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(999),
		XDPoS: &params.XDPoSConfig{
			Epoch: epoch,
		},
	}

	addrA := common.BytesToAddress([]byte{0x0a}) // canonical masternode
	addrD := common.BytesToAddress([]byte{0x0d}) // sidechain masternode

	canonicalCP := makeV1CheckpointHeader(900, []common.Address{addrA}, 0x01)
	sidechainCP := makeV1CheckpointHeader(900, []common.Address{addrD}, 0x02)

	// Sanity: the two checkpoints must have different hashes.
	if canonicalCP.Hash() == sidechainCP.Hash() {
		t.Fatal("canonical and sidechain checkpoints have the same hash — test bug")
	}

	// Sidechain block 901 with ParentHash pointing to sidechain checkpoint.
	header901 := &types.Header{
		Number:     big.NewInt(901),
		ParentHash: sidechainCP.Hash(),
		Difficulty: big.NewInt(1),
		// Minimal valid V1 Extra (non-checkpoint block needs no masternodes in Extra,
		// but requires at least extraSeal bytes so sigHash doesn't panic).
		Extra: make([]byte, extraVanity+extraSeal),
	}

	chain := &mockChainReader1636{cfg: cfg, canonicalAt900: canonicalCP}

	// XDPoS engine with V1-only config (no V2 switch block).
	eng := New(&params.XDPoSConfig{Epoch: epoch}, rawdb.NewMemoryDatabase())

	// parents slice contains the sidechain checkpoint (cpNo=900 is in range).
	parents := []*types.Header{sidechainCP}

	got, err := eng.getValidatorWithParents(addrD, chain, header901, parents)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// D→D mapping from sidechain cp; zero address means canonical was used instead.
	if got != addrD {
		t.Errorf("got M2 %v, want sidechain M2 %v (sidechain checkpoint not used — ancestry bug)", got, addrD)
	}

	// Negative: verify that WITHOUT the parents (empty slice), canonical is used.
	// D is not in canonical masternodes [A], so the result should be A→A only,
	// and creator=D gives Address{} (not in the map).
	gotCanonical, err := eng.getValidatorWithParents(addrD, chain, header901, nil)
	if err != nil {
		t.Fatalf("canonical path: unexpected error: %v", err)
	}
	// D not in canonical masternodes → zero address from the canonical m[D] lookup.
	if gotCanonical == addrD {
		t.Error("canonical path: unexpectedly returned D — sidechain checkpoint leaked into canonical path")
	}
}
