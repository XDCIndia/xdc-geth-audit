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
	"math/big"
	"testing"
)

// genCompressedPoint returns a valid compressed secp256k1 point (33 bytes).
// It uses the curve's generator point: G = (79be...1798, 483a...b10d).
func genCompressedPoint() []byte {
	return []byte{
		0x02, 0x79, 0xbe, 0x66, 0x7e, 0xf9, 0xdc, 0xbb, 0xac, 0x55, 0xa0, 0x62, 0x95, 0xce, 0x87, 0x0b,
		0x07, 0x02, 0x9b, 0xfc, 0xdb, 0x2d, 0xce, 0x28, 0xd9, 0x59, 0xf2, 0x81, 0x5b, 0x16, 0xf8, 0x17, 0x98,
	}
}

// TestZeroCommitmentsNoDeltaDivisionByZero verifies that MRPVerify returns false
// (not panics with division by zero) when a proof with zero commitments is provided.
//
// A proof with len(mrp.Comms) == 0 would cause DeltaMRP to compute EC.V/m
// with m=0, panicking. The fix is to check len(mrp.Comms) == 0 in MRPVerify
// and return false early.
func TestZeroCommitmentsNoDeltaDivisionByZero(t *testing.T) {
	// Build a minimal proof with 0 commitments.
	// Layout: [4-byte count=0] [A,S,T1,T2 as 4 compressed points] [Tau,Th,Mu as 3*32 bytes] [IPP section]
	proof := []byte{}

	// 0 commitments
	proof = append(proof, 0x00, 0x00, 0x00, 0x00)

	// A, S, T1, T2 (4 compressed points, 33 bytes each = 132 bytes)
	point := genCompressedPoint()
	for i := 0; i < 4; i++ {
		proof = append(proof, point...)
	}

	// Tau, Th, Mu (3 scalars, 32 bytes each = 96 bytes)
	scalar32 := make([]byte, 32)
	for i := 0; i < 3; i++ {
		proof = append(proof, scalar32...)
	}

	// IPP section: minimal with 0 L/R points, A=32 bytes, B=32 bytes, Challenges=32 bytes
	// numChallenges = log2(0*64) + 1 = log2(0) + 1, but since log2(0) is undefined,
	// the input will have IPP in unpredictable state. Add minimal IPP data.
	proof = append(proof, scalar32...) // IPP.A
	proof = append(proof, scalar32...) // IPP.B
	proof = append(proof, scalar32...) // IPP.Challenges[0]
	proof = append(proof, scalar32...) // Cy
	proof = append(proof, scalar32...) // Cz
	proof = append(proof, scalar32...) // Cx

	mrp := new(MultiRangeProof)
	if err := mrp.Deserialize(proof); err != nil {
		// If deserialize fails, the fix (propagating IPP error) caught it early.
		// That's also acceptable — we just need to not panic.
		return
	}

	// If deserialization succeeded, MRPVerify should return false (not panic).
	result := MRPVerify(mrp)
	if result {
		t.Fatal("MRPVerify must return false for zero commitments, got true")
	}
}

// TestIPPDeserializeErrorPropagation verifies that MultiRangeProof.Deserialize
// returns an error (not silently ignores it) when IPP.Deserialize fails.
//
// The original code at line 927 called mrp.IPP.Deserialize(proof[offset:], numChallenges)
// but did not check the returned error. If the IPP section was truncated or malformed,
// the error was dropped, and subsequent verification would use a partially-deserialized
// (zero-initialized) IPP object.
//
// This test crafts a proof where the IPP section is cut off at the challenges boundary.
func TestIPPDeserializeErrorPropagation(t *testing.T) {
	// Build a proof that deserializes up to the IPP section, then truncates it.
	// Layout: [4-byte count] [commitments] [A,S,T1,T2] [Tau,Th,Mu] [IPP truncated] [Cy,Cz,Cx]
	proof := []byte{}

	// 1 commitment
	proof = append(proof, 0x00, 0x00, 0x00, 0x01)

	// 1 commitment (compressed point)
	point := genCompressedPoint()
	proof = append(proof, point...)

	// A, S, T1, T2 (4 compressed points)
	for i := 0; i < 4; i++ {
		proof = append(proof, point...)
	}

	// Tau, Th, Mu (3 scalars, 32 bytes each)
	scalar32 := make([]byte, 32)
	for i := 0; i < 3; i++ {
		proof = append(proof, scalar32...)
	}

	// For 1 commitment and bitsPerValue=64:
	// numChallenges = log2(1*64) + 1 = log2(64) + 1 = 6 + 1 = 7
	// IPP.Deserialize expects: (7-1)=6 L points, (7-1)=6 R points, A, B, 7 Challenges
	// Total IPP: 6*33 + 6*33 + 32 + 32 + 7*32 = 198 + 64 + 224 = 486 bytes
	// Truncate to only 100 bytes (less than needed), so IPP.Deserialize fails with "input data too short"
	for i := 0; i < 100; i++ {
		proof = append(proof, byte(i%256))
	}

	// Cy, Cz, Cx (3 scalars) — these come after the full IPP
	for i := 0; i < 3; i++ {
		proof = append(proof, scalar32...)
	}

	mrp := new(MultiRangeProof)
	err := mrp.Deserialize(proof)

	// After the fix, Deserialize must return an error (from IPP.Deserialize)
	// because the IPP section is truncated.
	if err == nil {
		t.Fatal("Deserialize must return an error when IPP section is truncated, got nil")
	}
}

// TestInnerProductVerifyFastEmptyChallengesNoIndexPanic verifies that
// InnerProductVerifyFast returns false (not panics) when ipp.Challenges is empty.
//
// If len(ipp.Challenges) == 0, then curIt = -1, and the loop:
//
//	for j := curIt - 1; j >= 0; j--
//
// tries to access ipp.Challenges[-2], or later accesses ipp.L[-1], ipp.R[-1],
// causing an index-out-of-bounds panic. The fix is to check len(ipp.Challenges) == 0
// and return false early.
func TestInnerProductVerifyFastEmptyChallengesNoIndexPanic(t *testing.T) {
	// Create an IPP with empty Challenges
	ipp := InnerProdArg{
		L:          []ECPoint{},
		R:          []ECPoint{},
		A:          big.NewInt(0),
		B:          big.NewInt(0),
		Challenges: []*big.Int{}, // Empty challenges
	}

	// Create dummy point and parameter
	point := ECPoint{big.NewInt(1), big.NewInt(1)}
	G := make([]ECPoint, 1)
	G[0] = point
	H := make([]ECPoint, 1)
	H[0] = point

	// Call InnerProductVerifyFast with empty Challenges.
	// Before the fix, this would panic on index access.
	// After the fix, it should return false.
	result := InnerProductVerifyFast(big.NewInt(0), point, point, G, H, ipp)
	if result {
		t.Fatal("InnerProductVerifyFast must return false for empty Challenges, got true")
	}
}
