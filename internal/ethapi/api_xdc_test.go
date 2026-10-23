// Copyright 2023 The XDC Network Authors
// This file is part of the XDC Network library.

package ethapi

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// signerOnlyBackend satisfies Backend using the embedded interface value (nil).
// Only Engine() is implemented; any other call panics — acceptable for this test
// since rpcOutputBlockSigners only calls Engine() after the fix.
type signerOnlyBackend struct {
	Backend
	engine consensus.Engine
}

func (b *signerOnlyBackend) Engine() consensus.Engine { return b.engine }

// TestRpcOutputBlockSignersV2Seal verifies that rpcOutputBlockSigners recovers
// the real signer from a 65-byte V2 Validator field instead of splitting it into
// three bogus 20-byte addresses.
//
// Fails on origin/main (returns 3 addresses); passes after fix (returns 1).
func TestRpcOutputBlockSignersV2Seal(t *testing.T) {
	// Create a key that will sign the Validator field.
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	expected := crypto.PubkeyToAddress(key.PublicKey)

	// Build a minimal header with an Extra large enough for V1 sigHash
	// (extraVanity=32 + extraSeal=65).
	header := &types.Header{
		Number:     big.NewInt(901),
		Difficulty: big.NewInt(1),
		Extra:      make([]byte, 32+65),
	}

	// Compute the hash that RecoverValidator uses (SealHash = sigHash, exported).
	hash := XDPoS.SealHash(header)
	sig, err := crypto.Sign(hash.Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte sig, got %d", len(sig))
	}
	header.Validator = sig

	block := types.NewBlockWithHeader(header)

	engine := XDPoS.New(&params.XDPoSConfig{Period: 2, Epoch: 900}, rawdb.NewMemoryDatabase())
	api := &BlockChainAPI{b: &signerOnlyBackend{engine: engine}}

	signers, err := api.rpcOutputBlockSigners(block, context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 1 {
		t.Fatalf("expected 1 signer, got %d: %v", len(signers), signers)
	}
	if signers[0] != expected {
		t.Errorf("wrong signer: want %s, got %s", expected.Hex(), signers[0].Hex())
	}
}

// TestRpcOutputBlockSignersV1BothSigners verifies that rpcOutputBlockSigners returns
// both the M1 creator (from Extra seal) and M2 validator (from Validator seal) for V1
// double-validation blocks.
//
// Fails on origin/main (RecoverSigner not called, only M2 returned); passes after fix.
func TestRpcOutputBlockSignersV1BothSigners(t *testing.T) {
	m1Key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	m2Key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	m1Addr := crypto.PubkeyToAddress(m1Key.PublicKey)
	m2Addr := crypto.PubkeyToAddress(m2Key.PublicKey)

	// Build a V1 header: Extra = 32-byte vanity + 65-byte M1 seal, Validator = 65-byte M2 seal.
	// No V2 config → IsV2Block returns false.
	header := &types.Header{
		Number:     big.NewInt(901),
		Difficulty: big.NewInt(1),
		Extra:      make([]byte, 32+65),
	}

	hash := XDPoS.SealHash(header)

	// M1 signs Extra[32:].
	m1Sig, err := crypto.Sign(hash.Bytes(), m1Key)
	if err != nil {
		t.Fatal(err)
	}
	copy(header.Extra[32:], m1Sig)

	// M2 signs Validator.
	m2Sig, err := crypto.Sign(hash.Bytes(), m2Key)
	if err != nil {
		t.Fatal(err)
	}
	header.Validator = m2Sig

	block := types.NewBlockWithHeader(header)

	// No V2.SwitchBlock → IsV2Block(header) = false.
	engine := XDPoS.New(&params.XDPoSConfig{Period: 2, Epoch: 900}, rawdb.NewMemoryDatabase())
	api := &BlockChainAPI{b: &signerOnlyBackend{engine: engine}}

	signers, err := api.rpcOutputBlockSigners(block, context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 2 {
		t.Fatalf("expected 2 signers (M1+M2), got %d: %v", len(signers), signers)
	}
	addrSet := map[common.Address]bool{signers[0]: true, signers[1]: true}
	if !addrSet[m1Addr] {
		t.Errorf("M1 creator %s missing from signers %v", m1Addr.Hex(), signers)
	}
	if !addrSet[m2Addr] {
		t.Errorf("M2 validator %s missing from signers %v", m2Addr.Hex(), signers)
	}
}

func TestGetMasternodes(t *testing.T) {
	// Setup mock backend
	// This is a placeholder test - full implementation would use testutil

	t.Run("returns masternode list", func(t *testing.T) {
		// Test would verify GetMasternodes returns correct list
	})

	t.Run("handles empty epoch", func(t *testing.T) {
		// Test would verify behavior when epoch has no masternodes
	})
}

func TestGetCandidates(t *testing.T) {
	t.Run("returns candidate list", func(t *testing.T) {
		// Test would verify GetCandidates returns correct list
	})
}

func TestGetCandidateInfo(t *testing.T) {
	t.Run("returns candidate info", func(t *testing.T) {
		// Test would verify candidate info fields
	})

	t.Run("handles non-existent candidate", func(t *testing.T) {
		// Test error handling for invalid candidate
	})
}

func TestGetBlockSignersByNumber(t *testing.T) {
	t.Run("returns signers for block", func(t *testing.T) {
		// Test would verify signers list
	})
}

func TestXDCRewardCalculation(t *testing.T) {
	tests := []struct {
		name           string
		blockNumber    uint64
		expectedReward *big.Int
	}{
		{
			name:           "genesis block has no reward",
			blockNumber:    0,
			expectedReward: big.NewInt(0),
		},
		{
			name:           "regular block has standard reward",
			blockNumber:    100,
			expectedReward: new(big.Int).Mul(big.NewInt(250), big.NewInt(1e18)), // 250 XDC
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test implementation would go here
		})
	}
}

func TestXDCEpochCalculation(t *testing.T) {
	epochSize := uint64(900)

	tests := []struct {
		blockNumber   uint64
		expectedEpoch uint64
	}{
		{0, 0},
		{899, 0},
		{900, 1},
		{1800, 2},
		{2700, 3},
	}

	for _, tt := range tests {
		epoch := tt.blockNumber / epochSize
		if epoch != tt.expectedEpoch {
			t.Errorf("Block %d: expected epoch %d, got %d", tt.blockNumber, tt.expectedEpoch, epoch)
		}
	}
}

func TestGapBlockCalculation(t *testing.T) {
	epochSize := uint64(900)
	gapSize := uint64(50)

	tests := []struct {
		blockNumber uint64
		isGapBlock  bool
	}{
		{0, false},
		{849, false},
		{850, true}, // epoch 1 gap block (900 - 50 = 850)
		{851, false},
		{1750, true}, // epoch 2 gap block (1800 - 50 = 1750)
	}

	for _, tt := range tests {
		isGap := tt.blockNumber%epochSize == epochSize-gapSize
		if isGap != tt.isGapBlock {
			t.Errorf("Block %d: expected isGapBlock=%v, got %v", tt.blockNumber, tt.isGapBlock, isGap)
		}
	}
}

func TestHexutilConversions(t *testing.T) {
	// Test hexutil conversions used in XDC API

	t.Run("uint64 conversion", func(t *testing.T) {
		val := uint64(12345)
		hex := hexutil.Uint64(val)
		if uint64(hex) != val {
			t.Errorf("Expected %d, got %d", val, uint64(hex))
		}
	})

	t.Run("big int conversion", func(t *testing.T) {
		val := big.NewInt(1000000000000000000)
		hex := (*hexutil.Big)(val)
		if hex.ToInt().Cmp(val) != 0 {
			t.Errorf("Big int conversion mismatch")
		}
	})
}

func TestMasternodeAddress(t *testing.T) {
	// Test masternode address validation
	validAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")

	if validAddr == (common.Address{}) {
		t.Error("Expected non-zero address")
	}
}

// Mock backend for testing
type mockXDCBackend struct {
	masternodes map[uint64][]common.Address
	candidates  []common.Address
}

func newMockXDCBackend() *mockXDCBackend {
	return &mockXDCBackend{
		masternodes: make(map[uint64][]common.Address),
		candidates:  make([]common.Address, 0),
	}
}

func (b *mockXDCBackend) GetMasternodes(epoch uint64) []common.Address {
	return b.masternodes[epoch]
}

func (b *mockXDCBackend) GetCandidates() []common.Address {
	return b.candidates
}

func (b *mockXDCBackend) SetMasternodes(epoch uint64, addrs []common.Address) {
	b.masternodes[epoch] = addrs
}

func (b *mockXDCBackend) SetCandidates(addrs []common.Address) {
	b.candidates = addrs
}

// Benchmark tests
func BenchmarkGetMasternodes(b *testing.B) {
	backend := newMockXDCBackend()

	// Setup 150 masternodes
	masternodes := make([]common.Address, 150)
	for i := 0; i < 150; i++ {
		masternodes[i] = common.BigToAddress(big.NewInt(int64(i + 1)))
	}
	backend.SetMasternodes(1, masternodes)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = backend.GetMasternodes(1)
	}
}

func BenchmarkEpochCalculation(b *testing.B) {
	epochSize := uint64(900)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = uint64(i) / epochSize
	}
}

// TestGetStakerROIMasternode verifies that GetStakerROIMasternode uses its
// masternode argument and returns an error for the current client.
func TestGetStakerROIMasternode(t *testing.T) {
	api := &BlockChainAPI{b: &signerOnlyBackend{}}

	masternode := common.HexToAddress("0x1234567890123456789012345678901234567890")

	roi, err := api.GetStakerROIMasternode(masternode)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if roi != 0 {
		t.Errorf("expected roi=0, got %v", roi)
	}

	// Verify error is clear about limitation
	if err.Error() != "GetStakerROIMasternode: voter reward data not available in this client" {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify that invalid address is rejected
	_, err = api.GetStakerROIMasternode(common.Address{})
	if err == nil {
		t.Fatal("expected error for zero address, got nil")
	}
}

// Context helpers for tests
func testContext() context.Context {
	return context.Background()
}
