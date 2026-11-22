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

// TestMRPVerifyAbortKeepsEC checks XDC-HUNT-22: a verification that is aborted
// while the generators are built must not replace the shared EC with an empty
// value, and the next verification must work.
func TestMRPVerifyAbortKeepsEC(t *testing.T) {
	proof, err := MRPProve([]*big.Int{big.NewInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	// Abort on the second check, which is inside the generator loop.
	calls := 0
	abort := func() bool {
		calls++
		return calls > 1
	}
	if MRPVerify(&proof, abort) {
		t.Fatal("aborted verification returned true")
	}
	if calls < 2 {
		t.Fatalf("abort was checked %d times, test did not reach the generator loop", calls)
	}
	ecMu.Lock()
	n, v, g := EC.N, EC.V, len(EC.BPG)
	ecMu.Unlock()
	if n == nil || v == 0 || g == 0 {
		t.Fatalf("EC was emptied by an aborted call: N=%v V=%d len(BPG)=%d", n, v, g)
	}
	if !MRPVerify(&proof) {
		t.Fatal("verification after an aborted call failed")
	}
}
