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
// non-mainnet chain: an invalid block clears XdcBulkSyncMode only when the
// local head is fresh (within atTipFreshnessWindow), not while we are far
// behind. Refs #1427, Hacken F-2026-19724.
//
// The at_tip case uses a fresh genesis timestamp (time.Now()) so headFresh=true.
// The far_behind case uses the default genesis timestamp (0, epoch 1970) so
// headFresh=false and XdcBulkSyncMode must stay true.
func TestFullSyncInvalidNextBlockBulkMode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		remote      int
		genesisTime uint64 // 0 = stale (far behind); >0 = fresh (at tip)
		wantBulk    bool
	}{
		{"far behind", 640, 0, true},
		{"at tip", 5, uint64(time.Now().Unix()), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n *xdcFakeNet
			if tc.genesisTime > 0 {
				n = newXDCFakeNetTime(t, 0, tc.remote, tc.genesisTime)
			} else {
				n = newXDCFakeNet(t, 0, tc.remote)
			}
			// Block 1 with a wrong difficulty: a validation error, not a missing parent.
			h := types.CopyHeader(n.remote[0].Header())
			h.Difficulty = new(big.Int).Add(h.Difficulty, big.NewInt(1))
			bad := types.NewBlockWithHeader(h).WithBody(*n.remote[0].Body())
			n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
				if origin == 1 {
					if tc.remote == 5 {
						// at_tip: serve only the bad block so the single-block batch
						// reaches InsertChain (no contiguity pre-check failure) and
						// gets the "invalid difficulty" error that isDefinitiveInvalidBlockError
						// recognises. headFresh=true (fresh genesis) → atTip=true →
						// XdcBulkSyncMode cleared.
						return []*types.Header{bad.Header()}
					}
					// far_behind: return the full batch (bad block + canonical blocks
					// 2..N). InsertChain's contiguity pre-check fires first because
					// canonical block 2's ParentHash != bad.Hash(). The resulting
					// "non contiguous insert" error is not in isDefinitiveInvalidBlockError's
					// allowlist, so no bulk-mode change — len(batch)=192 = xdpos2MaxHeaderFetch
					// would in any case mark the peer as not-at-tip. wantBulk=true. ✓
				}
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
