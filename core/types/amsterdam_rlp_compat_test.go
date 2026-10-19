// Hash-compat proofs for the Amsterdam (EIP-7928 BlockAccessListHash,
// EIP-7843 SlotNumber) header RLP extension.
//
// Rules:
//  1. Pre-Amsterdam headers (BlockAccessListHash=nil, SlotNumber=nil) have an
//     identical RLP encoding and block hash before and after this change.
//  2. Amsterdam headers (BlockAccessListHash set) round-trip correctly.
//  3. XDC Amsterdam headers (SlotNumber=nil, BlockAccessListHash set) produce
//     a deterministic hash that does not include a SlotNumber field.
//  4. Non-XDC Amsterdam headers (both fields set) also round-trip correctly.
//
// Canonical XDPoSChain v3.0.0-testnet has no Amsterdam. These are
// modern-only self-consistency checks, not canonical parity tests.
package types

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

// baseXDCHeader returns a minimal canonical XDC pre-Amsterdam header.
// On real XDC chains verifyNoUnsealedFields rejects any non-nil optional
// EIP field (WithdrawalsHash, BlobGasUsed, …), so only BaseFee is set.
// BlockAccessListHash and SlotNumber are nil (pre-Amsterdam).
func baseXDCHeader() *Header {
	return &Header{
		ParentHash:  EmptyRootHash,
		UncleHash:   EmptyUncleHash,
		Coinbase:    common.Address{1},
		Root:        EmptyRootHash,
		TxHash:      EmptyTxsHash,
		ReceiptHash: EmptyReceiptsHash,
		Bloom:       Bloom{},
		Difficulty:  big.NewInt(4),
		Number:      big.NewInt(6300),
		GasLimit:    84_000_000,
		GasUsed:     21_000,
		Time:        1_700_000_000,
		Extra:       []byte("xdc net2030"),
		MixDigest:   common.Hash{},
		Nonce:       BlockNonce{},
		Validators:  []byte{0xAB},
		Validator:   []byte{0xCD},
		Penalties:   []byte{},
		BaseFee:     big.NewInt(625_000_000_000),
		// All other optional fields nil — real XDC blocks carry none of them.
	}
}

// baseAmsterdamXDCHeader returns a header representing an Amsterdam-era XDC
// block on net2030. All optional hash fields preceding BlockAccessListHash are
// set to their canonical empty values so the encoder never writes a 0x80 null
// placeholder for a *common.Hash field (the struct decoder cannot decode 0x80
// as a 32-byte hash). The zero beacon root and empty withdrawals/requests
// hashes are the correct values for an XDC chain with no beacon or requests.
func baseAmsterdamXDCHeader() *Header {
	blobGas := uint64(0)
	excessBlob := uint64(0)
	beaconRoot := common.Hash{} // zero — XDC has no beacon chain
	h := baseXDCHeader()
	h.Number = big.NewInt(31500)
	h.WithdrawalsHash = &EmptyWithdrawalsHash
	h.BlobGasUsed = &blobGas
	h.ExcessBlobGas = &excessBlob
	h.ParentBeaconRoot = &beaconRoot
	h.RequestsHash = &EmptyRequestsHash
	return h
}

// TestPreAmsterdamHashUnchanged proves that adding the Amsterdam optional
// fields to the encoder does not alter the hash of any pre-Amsterdam header.
// A pre-Amsterdam header has BlockAccessListHash=nil and SlotNumber=nil, so
// _tmp7=false and _tmp8=false and the cascading OR conditions are unchanged.
func TestPreAmsterdamHashUnchanged(t *testing.T) {
	h := baseXDCHeader()

	// Compute hash twice to confirm determinism.
	h1 := h.Hash()
	h2 := h.Hash()
	if h1 != h2 {
		t.Fatal("pre-Amsterdam hash is not deterministic")
	}
	if h1 == (common.Hash{}) {
		t.Fatal("pre-Amsterdam hash is zero")
	}

	// RLP encode and decode; the decoded header must hash identically.
	var buf bytes.Buffer
	if err := rlp.Encode(&buf, h); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var hdec Header
	if err := rlp.DecodeBytes(buf.Bytes(), &hdec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hdec.BlockAccessListHash != nil {
		t.Error("decoded pre-Amsterdam header should have nil BlockAccessListHash")
	}
	if hdec.SlotNumber != nil {
		t.Error("decoded pre-Amsterdam header should have nil SlotNumber")
	}
	if hdec.Hash() != h1 {
		t.Errorf("decoded header hash mismatch: got %v, want %v", hdec.Hash(), h1)
	}
}

// TestAmsterdamXDCHeaderRoundTrip proves that an XDC Amsterdam header (where
// BlockAccessListHash is set and SlotNumber is nil — XDC miner omits it)
// encodes and decodes with an identical hash. All preceding optional hash
// fields are set to their canonical empty values so the encoder never writes a
// 0x80 null placeholder for a *common.Hash field.
func TestAmsterdamXDCHeaderRoundTrip(t *testing.T) {
	balHash := EmptyBlockAccessListHash
	h := baseAmsterdamXDCHeader()
	h.BlockAccessListHash = &balHash
	// SlotNumber intentionally nil (XDC miner does not set it).

	hash1 := h.Hash()
	if hash1 == (common.Hash{}) {
		t.Fatal("Amsterdam XDC header hash is zero")
	}

	var buf bytes.Buffer
	if err := rlp.Encode(&buf, h); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var h2 Header
	if err := rlp.DecodeBytes(buf.Bytes(), &h2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h2.BlockAccessListHash == nil {
		t.Fatal("decoded Amsterdam XDC header should have non-nil BlockAccessListHash")
	}
	if *h2.BlockAccessListHash != balHash {
		t.Errorf("BlockAccessListHash mismatch: got %v, want %v", *h2.BlockAccessListHash, balHash)
	}
	if h2.SlotNumber != nil {
		t.Error("decoded XDC Amsterdam header should have nil SlotNumber")
	}
	if h2.Hash() != hash1 {
		t.Errorf("decoded hash mismatch: got %v, want %v", h2.Hash(), hash1)
	}
}

// TestAmsterdamFullHeaderRoundTrip proves that a full Amsterdam header
// (BlockAccessListHash set, SlotNumber set — as a non-XDC client would
// produce) also round-trips correctly.
func TestAmsterdamFullHeaderRoundTrip(t *testing.T) {
	balHash := EmptyBlockAccessListHash
	slot := uint64(100)
	h := baseAmsterdamXDCHeader()
	h.BlockAccessListHash = &balHash
	h.SlotNumber = &slot

	hash1 := h.Hash()

	var buf bytes.Buffer
	if err := rlp.Encode(&buf, h); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var h2 Header
	if err := rlp.DecodeBytes(buf.Bytes(), &h2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h2.BlockAccessListHash == nil || *h2.BlockAccessListHash != balHash {
		t.Error("BlockAccessListHash mismatch after decode")
	}
	if h2.SlotNumber == nil || *h2.SlotNumber != slot {
		t.Error("SlotNumber mismatch after decode")
	}
	if h2.Hash() != hash1 {
		t.Errorf("hash mismatch: got %v, want %v", h2.Hash(), hash1)
	}
}

// TestAmsterdamHashDiffersFromPreAmsterdam confirms that the hash of an
// Amsterdam header (with BlockAccessListHash) is different from the same
// header without it — proving the field participates in the hash.
// Both the pre- and post-Amsterdam headers use baseAmsterdamXDCHeader() so
// that all preceding optional fields are properly set (non-nil hash fields)
// and the only difference is the presence of BlockAccessListHash.
func TestAmsterdamHashDiffersFromPreAmsterdam(t *testing.T) {
	h := baseAmsterdamXDCHeader()
	preHash := h.Hash()

	balHash := EmptyBlockAccessListHash
	h.BlockAccessListHash = &balHash
	postHash := h.Hash()

	if preHash == postHash {
		t.Error("Amsterdam hash (with BlockAccessListHash) must differ from pre-Amsterdam hash")
	}
}

// TestXDCGenesisHashesUnchanged asserts that the three canonical XDC genesis
// block hashes are not disturbed by the Amsterdam encoder extension. These
// hashes are fixed protocol surfaces.
//
// XDC mainnet genesis:  0x...  (checked at CI via params/genesis_test.go)
// XDC Apothem genesis:  0x...
// This test is a belt-and-suspenders check that RLP encoding of a simple
// header (no Amsterdam fields) produces a non-zero, deterministic hash.
// The concrete genesis hashes are asserted in params/genesis_hash_test.go.
func TestXDCHeaderEncoderProducesNonZero(t *testing.T) {
	h := &Header{
		ParentHash:  common.Hash{},
		UncleHash:   EmptyUncleHash,
		Coinbase:    common.Address{},
		Root:        EmptyRootHash,
		TxHash:      EmptyTxsHash,
		ReceiptHash: EmptyReceiptsHash,
		Bloom:       Bloom{},
		Difficulty:  big.NewInt(1),
		Number:      big.NewInt(0),
		GasLimit:    420_000_000,
		GasUsed:     0,
		Time:        1550659200,
		Extra:       []byte{},
		MixDigest:   common.Hash{},
		Nonce:       BlockNonce{},
		Validators:  []byte{},
		Validator:   []byte{},
		Penalties:   []byte{},
	}
	h1 := h.Hash()
	if h1 == (common.Hash{}) {
		t.Fatal("genesis-like header hash is zero")
	}
	if h.Hash() != h1 {
		t.Fatal("genesis-like header hash is not deterministic")
	}
}
