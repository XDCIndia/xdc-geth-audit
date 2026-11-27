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

// TestBulletproofZeroTauThNoPanic checks XDC-HUNT-01: a proof whose Tau and Th
// are zero must fail verification, not panic. The challenges do not depend on
// Tau or Th, so the attacker only has to zero these two fields of a valid proof.
func TestBulletproofZeroTauThNoPanic(t *testing.T) {
	proof, err := MRPProve([]*big.Int{big.NewInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	proof.Tau = new(big.Int)
	proof.Th = new(big.Int)
	if MRPVerify(&proof) {
		t.Fatal("proof with zero Tau and Th was accepted")
	}
}

// TestMultZeroScalarIsInfinity checks that a zero scalar gives the point at
// infinity as (0, 0), as the canonical btcec curve does, and not nil
// coordinates.
func TestMultZeroScalarIsInfinity(t *testing.T) {
	ecMu.Lock()
	defer ecMu.Unlock()
	EC = genECPrimeGroupKey(64)
	got := EC.H.Mult(new(big.Int))
	if got.X == nil || got.Y == nil || got.X.Sign() != 0 || got.Y.Sign() != 0 {
		t.Fatalf("Mult(0) = (%v, %v), want (0, 0)", got.X, got.Y)
	}
}
