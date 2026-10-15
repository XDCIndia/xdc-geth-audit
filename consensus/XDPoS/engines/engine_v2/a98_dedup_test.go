// Copyright (c) 2024 XDC Network
// A.98 unit tests: byte-level signature deduplication in UniqueSignatures.
//
// IMPORTANT (#1074): UniqueSignatures (byte-dedup) is NO LONGER on the
// verifyQC/verifyTC hot path. It was removed from there because byte-dedup
// is unsafe for threshold counting — see a99_qctc_malleability_test.go for
// the corrected regression tests (RecoverUniqueSigners: address-dedup +
// low-S rejection) that now guard the QC/TC signature-counting hot path.
// The tests below describe ONLY the standalone UniqueSignatures() helper's
// own (unchanged, byte-level) mechanics; they do NOT describe consensus
// signature-counting behaviour and must not be read as such.
//
// Covers:
//   1. Byte-identical duplicates are counted once.
//   2. ECDSA-malleated pairs (same signer, different byte string) are still
//      counted as two distinct byte strings by this low-level helper — this
//      is exactly why the helper is unsafe for threshold counting and is no
//      longer used there.
//   3. Genuinely distinct signatures from different signers are all kept.

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// malleateSig returns the secp256k1 "complementary" signature for sig.
// For a signature (r || s || v), the malleated form is (r || (n-s) || (v^1)).
// Both recover the same public key via Ecrecover.
func malleateSig(t *testing.T, sig types.Signature) types.Signature {
	t.Helper()
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(sig))
	}

	// secp256k1 curve order n.
	n := crypto.S256().Params().N

	s := new(big.Int).SetBytes(sig[32:64])
	sNeg := new(big.Int).Sub(n, s)

	malleated := make([]byte, 65)
	copy(malleated[0:32], sig[0:32]) // r unchanged
	sNegBytes := sNeg.Bytes()
	// left-pad to 32 bytes
	copy(malleated[32+(32-len(sNegBytes)):64], sNegBytes)
	malleated[64] = sig[64] ^ 0x01 // flip v

	return types.Signature(malleated)
}

// TestUniqueSignatures_ByteIdenticalDuplicate verifies that a byte-identical
// copy of a signature is classified as a duplicate (not counted twice).
func TestUniqueSignatures_ByteIdenticalDuplicate(t *testing.T) {
	k := genKey(t)
	hash := common.HexToHash("0xdeadbeef")
	sig, err := crypto.Sign(hash[:], k)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Two identical byte slices.
	sigs := []types.Signature{sig, sig}

	unique, dups := UniqueSignatures(sigs)
	if len(unique) != 1 {
		t.Errorf("want 1 unique signature, got %d", len(unique))
	}
	if len(dups) != 1 {
		t.Errorf("want 1 duplicate, got %d", len(dups))
	}
}

// TestUniqueSignatures_MalleatedPair_ByteDedupStillCountsTwo documents that
// the raw byte-level UniqueSignatures() helper (in isolation) still treats a
// malleated pair as two distinct byte strings — that is simply what
// byte-comparison does. This is NOT the consensus-relevant behaviour: since
// #1074, verifyQC/verifyTC no longer call UniqueSignatures() for threshold
// counting, precisely because byte-dedup collapsing to "two" here would let
// a single signer inflate a QC/TC toward the 2/3 quorum threshold. The
// corrected, consensus-relevant expectation — a malleated pair must count
// as ONE (or be rejected outright) — is asserted against
// RecoverUniqueSigners/verifyQC/verifyTC in a99_qctc_malleability_test.go.
func TestUniqueSignatures_MalleatedPair_ByteDedupStillCountsTwo(t *testing.T) {
	k := genKey(t)
	hash := common.HexToHash("0xcafebabe")
	sig, err := crypto.Sign(hash[:], k)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	malleated := malleateSig(t, sig)

	// Sanity: both recover the same address.
	pub1, err := crypto.Ecrecover(hash[:], sig)
	if err != nil {
		t.Fatalf("Ecrecover orig: %v", err)
	}
	pub2, err := crypto.Ecrecover(hash[:], malleated)
	if err != nil {
		t.Fatalf("Ecrecover malleated: %v", err)
	}
	var addr1, addr2 common.Address
	copy(addr1[:], crypto.Keccak256(pub1[1:])[12:])
	copy(addr2[:], crypto.Keccak256(pub2[1:])[12:])
	if addr1 != addr2 {
		t.Fatalf("malleated sig recovered different address: %v vs %v", addr1, addr2)
	}

	// Byte-dedup must count them as two distinct entries.
	sigs := []types.Signature{sig, malleated}
	unique, dups := UniqueSignatures(sigs)
	if len(unique) != 2 {
		t.Errorf("want 2 unique (malleated pair), got %d unique, %d dups", len(unique), len(dups))
	}
	if len(dups) != 0 {
		t.Errorf("want 0 dups for malleated pair, got %d", len(dups))
	}
}

// TestUniqueSignatures_DistinctSigners verifies that N signatures from N
// different signers are all kept (no false-positive dedup).
func TestUniqueSignatures_DistinctSigners(t *testing.T) {
	const n = 5
	hash := common.HexToHash("0x123456")
	sigs := make([]types.Signature, n)
	for i := 0; i < n; i++ {
		k := genKey(t)
		sig, err := crypto.Sign(hash[:], k)
		if err != nil {
			t.Fatalf("Sign[%d]: %v", i, err)
		}
		sigs[i] = sig
	}

	unique, dups := UniqueSignatures(sigs)
	if len(unique) != n {
		t.Errorf("want %d unique, got %d", n, len(unique))
	}
	if len(dups) != 0 {
		t.Errorf("want 0 dups for distinct signers, got %d", len(dups))
	}
}

// TestUniqueSignatures_Empty checks the empty-slice edge case.
func TestUniqueSignatures_Empty(t *testing.T) {
	unique, dups := UniqueSignatures(nil)
	if len(unique) != 0 || len(dups) != 0 {
		t.Errorf("empty input: want (0,0), got (%d,%d)", len(unique), len(dups))
	}
}

// TestUniqueSignatures_ThresholdImplication is a commentary test documenting
// the #1074/#926 vulnerability at the level of the raw byte-dedup helper: a
// QC carrying 13 legitimate sigs + 1 malleated copy of one of them counts as
// 14 distinct sigs under byte-dedup, inflating the threshold count by one
// signer that never actually cast a second vote. This is exactly why
// UniqueSignatures() is no longer used for QC/TC threshold counting — see
// TestVerifyQC_MalleatedCopy_RejectedNotCountedTwice and
// TestVerifyTC_MalleatedCopy_RejectedNotCountedTwice in
// a99_qctc_malleability_test.go for the corrected hot-path behaviour
// (RecoverUniqueSigners), which does NOT count this pair as two.
func TestUniqueSignatures_ThresholdImplication(t *testing.T) {
	hash := common.HexToHash("0x888")
	const legitimateSigCount = 13

	sigs := make([]types.Signature, 0, legitimateSigCount+1)
	for i := 0; i < legitimateSigCount; i++ {
		k := genKey(t)
		sig, err := crypto.Sign(hash[:], k)
		if err != nil {
			t.Fatalf("Sign[%d]: %v", i, err)
		}
		sigs = append(sigs, sig)
	}
	// Append a malleated copy of the first sig.
	sigs = append(sigs, malleateSig(t, sigs[0]))

	unique, _ := UniqueSignatures(sigs)
	// Byte-dedup: 14 distinct byte strings → 14.
	if len(unique) != 14 {
		t.Errorf("threshold test: want 14 unique (byte-dedup), got %d", len(unique))
	}
}
