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

// TestDeserializeAbort checks XDC-HUNT-27: Deserialize runs a full Verify, so
// it must honour the abort function of an RPC call. Without an abort function
// it behaves as before.
func TestDeserializeAbort(t *testing.T) {
	rings, privkeys, m, err := GenerateMultiRingParams(2, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(m, rings, privkeys, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sig.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Deserialize(raw); err != nil {
		t.Fatalf("Deserialize without abort: %v", err)
	}
	if _, err := Deserialize(raw, func() bool { return false }); err != nil {
		t.Fatalf("Deserialize with idle abort: %v", err)
	}
	if _, err := Deserialize(raw, func() bool { return true }); err == nil {
		t.Fatal("Deserialize ignored the abort function")
	}
}
