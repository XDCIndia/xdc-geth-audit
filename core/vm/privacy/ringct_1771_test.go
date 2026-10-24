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

//go:build cgo

package privacy

import (
	"testing"
)

// TestDeserializeRejectsOffCurveRingMember verifies that Deserialize returns an
// error — not a panic — when a ring member's compressed key bytes or key image
// bytes decode to a point that is not on the curve (issue #1771).
//
// x = 0 with prefix 0x03 is used as the off-curve key: 0^3 + 7 = 7, and 7 is
// not a quadratic residue mod secp256k1's P (verified by Euler's criterion), so
// DeserializeCompressed returns a key where Y^2 ≡ P-7 (mod P) ≠ 7 = X^3+7.
// Verify's IsOnCurve check catches this and returns false.
//
// Note: DeserializeCompressed's internal "return nil" branch is mathematically
// unreachable for secp256k1 (the parity-adjustment step always makes isOdd(y)
// == ybit before the second check). The nil guards added by #1771 are therefore
// defensive: if the underlying function ever returns nil — due to a future
// algorithm change or a different curve — Deserialize now returns an explicit
// error rather than storing nil in sig.Ring or sig.I and panicking in Verify.
// A strict "fails on origin/main" test for the nil path is not possible because
// the nil case is unreachable for secp256k1.
func TestDeserializeRejectsOffCurveRingMember(t *testing.T) {
	const numRing, ringSize, s = 1, 2, 0

	rings, privkeys, m, err := GenerateMultiRingParams(numRing, ringSize, s)
	if err != nil {
		t.Fatalf("GenerateMultiRingParams: %v", err)
	}
	sig, err := Sign(m, rings, privkeys, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The serialised layout:
	//   8 (numRing) + 8 (size) + 32 (M) + 32 (C)
	//   + numRing*ringSize*32 (S scalars)
	//   + numRing*ringSize*33 (ring keys)   ← corrupt first key here
	//   + numRing*33 (key images)
	const ringKeyOffset = 8 + 8 + 32 + 32 + numRing*ringSize*32

	// offCurveKey: compressed key with x=0 (all-zero x bytes) and odd-y prefix.
	// x=0 → x^3+7=7, which is NOT a QR mod secp256k1 P, so the point decoded by
	// DeserializeCompressed is off-curve: IsOnCurve returns false.
	var offCurveKey [33]byte
	offCurveKey[0] = 0x03 // odd-y prefix; x bytes [1:33] are zero

	t.Run("ring_member", func(t *testing.T) {
		raw, _ := sig.Serialize()
		copy(raw[ringKeyOffset:ringKeyOffset+33], offCurveKey[:])
		if _, err := Deserialize(raw); err == nil {
			t.Fatal("Deserialize must return error for off-curve ring member, got nil error")
		}
	})

	t.Run("key_image", func(t *testing.T) {
		raw, _ := sig.Serialize()
		imageOffset := ringKeyOffset + numRing*ringSize*33
		copy(raw[imageOffset:imageOffset+33], offCurveKey[:])
		if _, err := Deserialize(raw); err == nil {
			t.Fatal("Deserialize must return error for off-curve key image, got nil error")
		}
	})
}
