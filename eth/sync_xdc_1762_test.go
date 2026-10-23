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

package eth

import "testing"

// TestRollbackWindowNeverZero verifies that rollbackWindow returns a positive
// value in [xdcRollbackWindowInit, xdcRollbackWindowMax] for every
// stuckRolledBack value in the live range [0, xdcRollbackWindowMax).
//
// On origin/main the inline computation
//
//	uint64(xdcRollbackWindowInit) * (1 << (rolledBack / xdcRollbackWindowInit))
//
// overflows to 0 for rolledBack in 7296..8191 (shift 57..63 makes the
// multiplication wrap in uint64). This test fails on main and passes after
// the rollbackWindow helper caps the shift at 6 (issue #1762).
func TestRollbackWindowNeverZero(t *testing.T) {
	for rolledBack := uint64(0); rolledBack < xdcRollbackWindowMax; rolledBack++ {
		w := rollbackWindow(rolledBack)
		if w == 0 {
			t.Errorf("rollbackWindow(%d) == 0, want > 0", rolledBack)
		}
		if w > xdcRollbackWindowMax {
			t.Errorf("rollbackWindow(%d) == %d, want <= %d", rolledBack, w, xdcRollbackWindowMax)
		}
	}
}
