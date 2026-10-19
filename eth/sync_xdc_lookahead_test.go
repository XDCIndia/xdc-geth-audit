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
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/core"
)

// TestFullSyncLookaheadContiguous checks that, with peers serving 192 headers
// but only 128 bodies per request, the A.77 lookahead ranges start exactly where
// range 0's import ends, import cleanly, and never feed the fork-rollback
// trigger. Refs #1429.
func TestFullSyncLookaheadContiguous(t *testing.T) {
	n := newXDCFakeNet(t, 0, 640)
	// fetchHeadersParallel races the top xdcMaxPeerTriesPerSync peers; the
	// remaining xdcLookaheadStreams peers serve the lookahead ranges.
	var peers []*fakeXDCPeer
	for i := 0; i < xdcMaxPeerTriesPerSync+xdcLookaheadStreams; i++ {
		peers = append(peers, n.addPeer(t, byte(i+1), n.canonicalHeaders))
	}
	s := n.newTestXDCSyncer(t)

	s.fullSyncOnce()

	want := uint64(xdcLookaheadRangeSize * (1 + xdcLookaheadStreams))
	if head := n.chain.CurrentBlock().Number.Uint64(); head != want {
		t.Errorf("head after one round = %d, want %d (range 0 + %d contiguous lookahead ranges)", head, want, xdcLookaheadStreams)
	}
	if s.stuckFrom != 0 || s.stuckFailures != 0 || core.XdcForkRecovering.Load() {
		t.Errorf("lookahead fed the rollback trigger: stuckFrom=%d stuckFailures=%d forkRecovering=%v",
			s.stuckFrom, s.stuckFailures, core.XdcForkRecovering.Load())
	}
	var lookahead []uint64
	for _, p := range peers {
		p.mu.Lock()
		for _, o := range p.headerReqs {
			if o != 1 {
				lookahead = append(lookahead, o)
			}
		}
		p.mu.Unlock()
	}
	slices.Sort(lookahead)
	var wantStarts []uint64
	for i := 1; i <= xdcLookaheadStreams; i++ {
		wantStarts = append(wantStarts, uint64(1+i*xdcLookaheadRangeSize))
	}
	if !slices.Equal(lookahead, wantStarts) {
		t.Errorf("lookahead range starts = %v, want %v", lookahead, wantStarts)
	}
}

// TestFullSyncLookaheadGapNoRollback checks that when range 0 imports fewer
// blocks than a lookahead range size (peers serve only 100 bodies), the
// non-contiguous lookahead ranges are dropped without feeding the fork-rollback
// trigger. Refs #1429.
func TestFullSyncLookaheadGapNoRollback(t *testing.T) {
	n := newXDCFakeNet(t, 0, 640)
	n.bodyCap = 100
	for i := 0; i < xdcMaxPeerTriesPerSync+xdcLookaheadStreams; i++ {
		n.addPeer(t, byte(i+1), n.canonicalHeaders)
	}
	s := n.newTestXDCSyncer(t)

	s.fullSyncOnce()

	if head := n.chain.CurrentBlock().Number.Uint64(); head != 100 {
		t.Errorf("head after one round = %d, want 100", head)
	}
	if s.stuckFrom != 0 || s.stuckFailures != 0 || core.XdcForkRecovering.Load() {
		t.Errorf("lookahead gap fed the rollback trigger: stuckFrom=%d stuckFailures=%d forkRecovering=%v",
			s.stuckFrom, s.stuckFailures, core.XdcForkRecovering.Load())
	}
}
