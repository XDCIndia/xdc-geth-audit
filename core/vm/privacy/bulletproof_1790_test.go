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

import "testing"

// TestDeserializePointArrayOverflowReturnsError verifies that
// deserializePointArray returns an error — not a panic — when the encoded point
// count would cause uint32 arithmetic to overflow the length check.
//
// Input {0x07,0xC1,0xF0,0x7C,0x00} encodes numPoint = 0x07C1F07C (≈130 M).
// In uint32: numPointSize(4) + 33*numPoint wraps to 0, so the old check
// (uint32(len(input)) < 0) was false and execution proceeded to
// make([]ECPoint, numPoint) followed by input[4:37] on a 5-byte slice —
// a guaranteed panic.  The fix uses 64-bit arithmetic for the comparison.
//
// Reproduced via MultiRangeProof.Deserialize, which is the entry point that
// the bulletproofVerifier precompile (0x28) calls on untrusted input.
func TestDeserializePointArrayOverflowReturnsError(t *testing.T) {
	proof := []byte{0x07, 0xC1, 0xF0, 0x7C, 0x00}
	mrp := new(MultiRangeProof)
	if err := mrp.Deserialize(proof); err == nil {
		t.Fatal("Deserialize must return an error for overflow input, got nil")
	}
}
