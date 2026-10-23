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

package vm

import (
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// TestXDCRunsTableLoop pins that XDC chains never take the generated fast path,
// which bakes in upstream's per-fork gas and gates and cannot see the XDC
// instruction sets.
func TestXDCRunsTableLoop(t *testing.T) {
	evm := &EVM{chainRules: params.Rules{IsXDC: true}}
	if !evm.useTableLoop() {
		t.Fatal("XDC chain would run the generated fast path")
	}
	evm = &EVM{chainRules: params.Rules{}}
	if evm.useTableLoop() {
		t.Fatal("non-XDC chain without tracer/extra EIPs should use the generated fast path")
	}
}
