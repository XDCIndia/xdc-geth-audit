// Copyright 2026 The XDC Network Authors
// Regression test for review R4-P02: Neg on the point at infinity.

package privacy

import (
	"math/big"
	"testing"
)

// TestNegPointAtInfinity checks that negating the result of a zero-scalar
// multiplication (the point at infinity) no longer panics, as MRPVerify does
// with an attacker-chosen Mu = 0.
func TestNegPointAtInfinity(t *testing.T) {
	ecMu.Lock()
	defer ecMu.Unlock()
	EC = genECPrimeGroupKey(64)
	inf := EC.H.Mult(big.NewInt(0))
	got := inf.Neg()
	if got.X != nil || got.Y != nil {
		t.Fatalf("Neg(infinity) = (%v, %v), want infinity", got.X, got.Y)
	}
	// A real point still negates normally.
	if n := EC.H.Neg(); n.Y.Cmp(EC.H.Y) == 0 {
		t.Fatal("Neg(H) returned H")
	}
}
