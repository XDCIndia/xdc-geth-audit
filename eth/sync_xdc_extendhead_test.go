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
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

// siblingBranch returns blocks 1..length of a chain that shares blocks
// 1..forkAt with n.remote and differs after it.
func siblingBranch(n *xdcFakeNet, forkAt, length int) []*types.Block {
	_, blocks, _ := core.GenerateChainWithGenesis(n.gspec, ethash.NewFaker(), length, func(i int, gen *core.BlockGen) {
		if i >= forkAt {
			gen.SetCoinbase(common.Address{0xbb})
		}
	})
	return blocks
}

// servesFrom answers every header request with headers starting at `start`
// of `chain`, whatever was asked: a reply to some other request.
func servesFrom(chain []*types.Block, start uint64) func(uint64, int) []*types.Header {
	return func(_ uint64, amount int) []*types.Header {
		var out []*types.Header
		for num := start; num <= uint64(len(chain)) && len(out) < amount; num++ {
			out = append(out, chain[num-1].Header())
		}
		return out
	}
}

// TestFullSyncRejectsSiblingBranch checks that a peer answering the header
// request for head+1 with a sibling branch forking at head-5 cannot reorg the
// local chain, and that an honest peer's batch still imports. Refs #1423.
func TestFullSyncRejectsSiblingBranch(t *testing.T) {
	const local, forkAt = 20, 15 // head 20, sibling branch forks after block 15
	t.Run("sibling only", func(t *testing.T) {
		n := newXDCFakeNet(t, local, 40)
		sib := siblingBranch(n, forkAt, 40)
		if sib[forkAt-1].Hash() != n.remote[forkAt-1].Hash() || sib[forkAt].Hash() == n.remote[forkAt].Hash() {
			t.Fatal("sibling branch does not fork after block 15")
		}
		bad := n.addPeer(t, 1, servesFrom(sib, forkAt+1), sib[forkAt:]...)
		s := n.newTestXDCSyncer(t)
		head := n.chain.CurrentBlock().Hash()

		s.fullSyncOnce()

		if got := n.chain.CurrentBlock(); got.Hash() != head {
			t.Fatalf("head moved to %d %x, want local head %d %x", got.Number, got.Hash(), local, head)
		}
		if !s.isPeerTainted(bad.ID()) {
			t.Error("peer serving a non-requested range was not tainted")
		}
	})
	t.Run("sibling and honest", func(t *testing.T) {
		n := newXDCFakeNet(t, local, 40)
		sib := siblingBranch(n, forkAt, 40)
		n.addPeer(t, 1, servesFrom(sib, forkAt+1), sib[forkAt:]...)
		n.addPeer(t, 2, n.canonicalHeaders)
		s := n.newTestXDCSyncer(t)

		s.fullSyncOnce()

		want := n.remote[len(n.remote)-1]
		if got := n.chain.CurrentBlock(); got.Hash() != want.Hash() {
			t.Fatalf("head = %d %x, want canonical %d %x", got.Number, got.Hash(), want.Number(), want.Hash())
		}
	})
	t.Run("right number, wrong parent", func(t *testing.T) {
		n := newXDCFakeNet(t, local, 40)
		sib := siblingBranch(n, forkAt, 40)
		n.addPeer(t, 1, servesFrom(sib, local+1), sib[forkAt:]...)
		s := n.newTestXDCSyncer(t)
		head := n.chain.CurrentBlock().Hash()

		s.fullSyncOnce()

		if got := n.chain.CurrentBlock(); got.Hash() != head {
			t.Fatalf("head moved to %d %x", got.Number, got.Hash())
		}
		if s.stuckFrom != local+1 || s.stuckFailures != 1 {
			t.Errorf("parent mismatch not routed to the rollback trigger: stuckFrom=%d stuckFailures=%d", s.stuckFrom, s.stuckFailures)
		}
	})
}
