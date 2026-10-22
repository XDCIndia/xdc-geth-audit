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

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestFullSyncInvalidNextBlockBulkMode checks the #952 relaxation on a
// non-mainnet chain: an invalid block at head+1 clears XdcBulkSyncMode only
// when the local head is fresh (within atTipFreshnessWindow), not while we
// are far behind (F-2026-19724).
func TestFullSyncInvalidNextBlockBulkMode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		remote      int
		genesisTime uint64 // 0: stale head (far behind); now: fresh head (at tip)
		wantBulk    bool
	}{
		{"far behind", 640, 0, true},
		{"at tip", 5, uint64(time.Now().Unix()), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newXDCFakeNetTime(t, 0, tc.remote, tc.genesisTime)
			// Block 1 with a wrong difficulty: a validation error, not a missing parent.
			h := types.CopyHeader(n.remote[0].Header())
			h.Difficulty = new(big.Int).Add(h.Difficulty, big.NewInt(1))
			bad := types.NewBlockWithHeader(h).WithBody(*n.remote[0].Body())
			n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
				hs := n.canonicalHeaders(origin, amount)
				if origin == 1 && len(hs) > 0 {
					hs[0] = bad.Header()
				}
				return hs
			}, bad)
			s := n.newTestXDCSyncer(t)
			core.XdcBulkSyncMode.Store(true)

			s.fullSyncOnce()

			if head := n.chain.CurrentBlock().Number.Uint64(); head != 0 {
				t.Fatalf("invalid block imported: head=%d", head)
			}
			if got := core.XdcBulkSyncMode.Load(); got != tc.wantBulk {
				t.Errorf("XdcBulkSyncMode = %v, want %v", got, tc.wantBulk)
			}
		})
	}
}
