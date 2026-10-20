// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Ported from canonical XDPoSChain v2.9.3-testnet
// core/vm/privacy/ringct_test.go and bulletproof_test.go.
//
// These tests guard the btcec → crypto/secp256k1 substitution in bulletproof.go:
// if the curve arithmetic diverges after the replacement, sign/verify or
// prove/verify round-trips fail here.
//
// The ring-signature code uses secp256k1 via cgo, so these tests require cgo.
//
//go:build cgo

package privacy

import (
	"crypto/rand"
	"math/big"
	"testing"
)

// --- Ring signature tests (ported from canonical ringct_test.go) ---

// TestXDCxRingSignRoundTrip signs a message with a random ring and verifies
// the serialised signature. Mirrors canonical TestSign.
func TestXDCxRingSignRoundTrip(t *testing.T) {
	numRing, ringSize, s := 5, 10, 9
	rings, privkeys, m, err := GenerateMultiRingParams(numRing, ringSize, s)
	if err != nil {
		t.Fatalf("GenerateMultiRingParams: %v", err)
	}
	sig, err := Sign(m, rings, privkeys, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := sig.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	dsig, err := Deserialize(raw)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if !Verify(dsig, false) {
		t.Fatal("Verify returned false for a freshly generated ring signature")
	}
}

// TestXDCxRingSignVerify mirrors canonical TestVerify1: different ring position.
func TestXDCxRingSignVerify(t *testing.T) {
	numRing, ringSize, s := 5, 10, 7
	rings, privkeys, m, err := GenerateMultiRingParams(numRing, ringSize, s)
	if err != nil {
		t.Fatalf("GenerateMultiRingParams: %v", err)
	}
	sig, err := Sign(m, rings, privkeys, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := sig.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	dsig, err := Deserialize(raw)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if !Verify(dsig, false) {
		t.Fatal("Verify returned false")
	}
}

// TestXDCxRingSignModifiedRejected mirrors canonical TestOnCurveVerify: mutating
// a ring point must cause Verify to return false.
func TestXDCxRingSignModifiedRejected(t *testing.T) {
	rings, privkeys, m, err := GenerateMultiRingParams(5, 10, 5)
	if err != nil {
		t.Fatalf("GenerateMultiRingParams: %v", err)
	}
	sig, err := Sign(m, rings, privkeys, 5)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !Verify(sig, false) {
		t.Fatal("unmodified signature must be valid")
	}
	// Corrupt the first ring element.
	sig.Ring[0][0].X = big.NewInt(1)
	sig.Ring[0][0].Y = big.NewInt(1)
	if Verify(sig, false) {
		t.Fatal("modified signature must be invalid")
	}
}

// TestXDCxRingDeserializeInvalidScalar mirrors canonical TestNilPointerDereferencePanic:
// a scalar equal to the curve order must be rejected by Deserialize.
func TestXDCxRingDeserializeInvalidScalar(t *testing.T) {
	rings, privkeys, m, err := GenerateMultiRingParams(5, 10, 7)
	if err != nil {
		t.Fatalf("GenerateMultiRingParams: %v", err)
	}
	sig, err := Sign(m, rings, privkeys, 7)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sig.S[0][0] = curve.Params().N // invalid: scalar == order
	raw, err := sig.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if _, err := Deserialize(raw); err == nil {
		t.Fatal("Deserialize must reject a signature with scalar == curve order")
	}
}

// --- Bulletproof tests (ported from canonical bulletproof_test.go) ---
// These exercise parseSEC1PubKeyXY (the btcec replacement) through the full
// prove-and-verify path.

// TestXDCxBulletproofZero mirrors canonical TestMRPProveZERO.
func TestXDCxBulletproofZero(t *testing.T) {
	proof, err := MRPProve([]*big.Int{new(big.Int).SetInt64(0)})
	if err != nil {
		t.Fatalf("MRPProve(0): %v", err)
	}
	if !MRPVerify(&proof) {
		t.Fatal("MRPVerify returned false for value=0")
	}
}

// TestXDCxBulletproofMaxUint64 mirrors canonical TestMRPProve_MAX_2_POW_64.
func TestXDCxBulletproofMaxUint64(t *testing.T) {
	proof, err := MRPProve([]*big.Int{new(big.Int).SetUint64(0xFFFFFFFFFFFFFFFF)})
	if err != nil {
		t.Fatalf("MRPProve(max_uint64): %v", err)
	}
	if !MRPVerify(&proof) {
		t.Fatal("MRPVerify returned false for value=max_uint64")
	}
}

// TestXDCxBulletproofOutOfRange mirrors canonical TestMRPProveOutOfSupportedRange:
// values larger than 2^64-1 must be rejected.
func TestXDCxBulletproofOutOfRange(t *testing.T) {
	value, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFF", 16)
	_, err := MRPProve([]*big.Int{value})
	if err == nil {
		t.Fatal("MRPProve must reject values > 2^64-1")
	}
}

// TestXDCxBulletproofRandom mirrors canonical TestMRPProve_RANDOM: random
// 64-bit values produce valid proofs.
func TestXDCxBulletproofRandom(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8} {
		vals := make([]*big.Int, n)
		for i := range vals {
			v, err := rand.Int(rand.Reader, new(big.Int).SetUint64(0xFFFFFFFFFFFFFFF))
			if err != nil {
				t.Fatalf("rand.Int: %v", err)
			}
			vals[i] = v
		}
		proof, err := MRPProve(vals)
		if err != nil {
			t.Fatalf("MRPProve(n=%d): %v", n, err)
		}
		if !MRPVerify(&proof) {
			t.Fatalf("MRPVerify returned false for n=%d random values", n)
		}
	}
}

// TestXDCxBulletproofSerializeRoundTrip verifies that a proof survives
// serialise → deserialise and still passes MRPVerify.  This exercises
// the Deserialize path that the precompile (0x28) calls on its input.
func TestXDCxBulletproofSerializeRoundTrip(t *testing.T) {
	proof, err := MRPProve([]*big.Int{big.NewInt(42)})
	if err != nil {
		t.Fatalf("MRPProve: %v", err)
	}
	raw := proof.Serialize()
	var proof2 MultiRangeProof
	if err := proof2.Deserialize(raw); err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if !MRPVerify(&proof2) {
		t.Fatal("MRPVerify returned false after serialise-deserialise round trip")
	}
}

// TestXDCxBulletproofInvalidNotSupported mirrors canonical
// TestMRPProveValueNumberNotSupported: 3, 5, 6, 7 inputs must be rejected.
func TestXDCxBulletproofInvalidNotSupported(t *testing.T) {
	for _, n := range []int{3, 5, 6, 7} {
		vals := make([]*big.Int, n)
		for i := range vals {
			vals[i] = big.NewInt(int64(i + 1))
		}
		if _, err := MRPProve(vals); err == nil {
			t.Errorf("MRPProve must reject n=%d inputs", n)
		}
	}
}
