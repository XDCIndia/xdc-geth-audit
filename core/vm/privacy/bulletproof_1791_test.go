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
	"sync"
	"testing"
)

// TestMRPVerifyConcurrentNoRace verifies that concurrent MRPVerify calls with
// different proof sizes do not race on the package-level EC variable.
//
// Before the fix MRPVerify wrote EC = genECPrimeGroupKey(m*bitsPerValue) with
// no synchronisation; two goroutines with different m overwrite each other's
// generators, causing the race detector to fire and in-process verification to
// return incorrect results (or panic with an index-out-of-range when EC.BPG
// shrinks under a call that indexed into the larger slice).
//
// Run with -race to catch the data race on origin/main.
func TestMRPVerifyConcurrentNoRace(t *testing.T) {
	// Pre-generate proofs outside the concurrent section so MRPProve's own
	// write to EC does not interfere with the verify race we are testing.
	proof1, err := MRPProve([]*big.Int{big.NewInt(1)})
	if err != nil {
		t.Fatalf("MRPProve(m=1): %v", err)
	}
	proof2, err := MRPProve([]*big.Int{big.NewInt(1), big.NewInt(2)})
	if err != nil {
		t.Fatalf("MRPProve(m=2): %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			// Alternate between the two proof sizes so concurrent goroutines
			// always disagree on the required generator length.
			if idx%2 == 0 {
				if !MRPVerify(&proof1) {
					t.Errorf("MRPVerify(m=1) returned false")
				}
			} else {
				if !MRPVerify(&proof2) {
					t.Errorf("MRPVerify(m=2) returned false")
				}
			}
		}(i)
	}
	wg.Wait()
}
