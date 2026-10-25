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

// Tests for Hacken finding F-2026-19724: "A Non-Mainnet Insert Error Clears
// Bulk Sync Away From The Tip".
//
// Root cause (residual after the #1671/#1675 partial fix):
//
//	The at-tip signal inside fullSyncOnce's invalid-block handler used
//	len(res.headers) < xdpos2MaxHeaderFetch (192) to decide whether to
//	clear XdcBulkSyncMode. This is peer-controlled: a malicious peer can
//	return a short batch (e.g. one invalid block) while the local node is
//	still far behind the network tip, causing bulk mode to be cleared
//	prematurely. Once cleared, xdcFullVerify becomes true while the node
//	is still catching up, stalling import.
//
// Fix: replace the short-batch check with headFresh — the local head's
// timestamp must be within atTipFreshnessWindow(config). A node far behind
// has a head that is hours old; this check is local state that a peer
// cannot forge. allNonTaintedPeersKnownBad() remains as a secondary signal
// for the multi-peer long-fork case.

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestBulkModeStaysSetFarBehind_19724 is the regression test for the residual.
//
// A non-mainnet node is far behind (local=genesis, stale timestamp 0) and a
// single malicious peer returns one invalid block (batch len=1 < 192). Under
// the old code the short-batch signal cleared XdcBulkSyncMode even though the
// node was not at tip. Under the fixed code headFresh=false (genesis Time=0 is
// ~55 years old) and allNonTaintedPeersKnownBad=false (no non-tainted peers
// after the only peer is tainted), so XdcBulkSyncMode must stay true.
//
// This test MUST fail on the unfixed code and pass after the fix.
func TestBulkModeStaysSetFarBehind_19724(t *testing.T) {
	// 200 remote blocks, default genesis (Timestamp=0 → 1970, very stale).
	// The local chain is at genesis only.
	n := newXDCFakeNet(t, 0, 200)

	// Build a bad block 1: same parent as canonical block 1 but wrong difficulty.
	canonical := n.remote[0]
	h := types.CopyHeader(canonical.Header())
	h.Difficulty = new(big.Int).Add(h.Difficulty, big.NewInt(1))
	bad := types.NewBlockWithHeader(h).WithBody(*canonical.Body())

	// Peer returns a short batch (one invalid block), simulating a malicious
	// peer that sends len=1 < 192 while the node is actually far behind.
	n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
		if origin == 1 {
			return []*types.Header{bad.Header()}
		}
		return n.canonicalHeaders(origin, amount)
	}, bad)

	s := n.newTestXDCSyncer(t)
	core.XdcBulkSyncMode.Store(true)

	s.fullSyncOnce()

	// The node must still be at genesis — bad block not imported.
	if head := n.chain.CurrentBlock().Number.Uint64(); head != 0 {
		t.Fatalf("bad block imported: head=%d, want 0", head)
	}

	// XdcBulkSyncMode must remain true: the node is far behind (stale head)
	// and a short batch from a peer must not clear bulk mode.
	// Verify the guard: genesis Time=0 is far outside the freshness window.
	freshnessSecs := atTipFreshnessWindow(n.chain.Config())
	genesisAge := time.Now().Unix() - int64(n.chain.Genesis().Time())
	if genesisAge <= freshnessSecs {
		t.Fatalf("test setup error: genesis age %ds is within freshness window %ds — headFresh would be true", genesisAge, freshnessSecs)
	}
	if got := core.XdcBulkSyncMode.Load(); !got {
		t.Error("XdcBulkSyncMode was cleared on a far-behind node by a peer-controlled short batch — Hacken F-2026-19724 not fixed")
	}
}

// TestBulkModeClearsAtTip_19724 is the counterpart: a node genuinely at tip
// (fresh local head) still clears XdcBulkSyncMode when an invalid block is
// returned by a peer, so the miner can seal the valid successor.
func TestBulkModeClearsAtTip_19724(t *testing.T) {
	// Genesis timestamp = now so the local head (genesis, block 0) is fresh.
	// 3 remote blocks: the node is effectively at tip.
	n := newXDCFakeNetTime(t, 0, 3, uint64(time.Now().Unix()))

	canonical := n.remote[0]
	h := types.CopyHeader(canonical.Header())
	h.Difficulty = new(big.Int).Add(h.Difficulty, big.NewInt(1))
	bad := types.NewBlockWithHeader(h).WithBody(*canonical.Body())

	n.addPeer(t, 1, func(origin uint64, amount int) []*types.Header {
		if origin == 1 {
			return []*types.Header{bad.Header()}
		}
		return n.canonicalHeaders(origin, amount)
	}, bad)

	s := n.newTestXDCSyncer(t)
	core.XdcBulkSyncMode.Store(true)

	s.fullSyncOnce()

	if head := n.chain.CurrentBlock().Number.Uint64(); head != 0 {
		t.Fatalf("bad block imported: head=%d, want 0", head)
	}

	// headFresh=true (genesis Time ≈ now) → atTip=true → bulk mode must clear.
	if got := core.XdcBulkSyncMode.Load(); got {
		t.Error("XdcBulkSyncMode still true at genuine tip after invalid peer block — miner cannot seal")
	}
}
