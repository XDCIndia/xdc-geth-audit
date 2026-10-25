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

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

// awaitM2Engine refuses block `at` while its Validator is empty, as XDPoS does
// for a V1 block whose assigned M2 has not co-signed it yet.
type awaitM2Engine struct {
	consensus.Engine
	at uint64
}

func (e awaitM2Engine) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	if header.Number.Uint64() == e.at && len(header.Validator) == 0 {
		return XDPoS.ErrNoValidatorSignature
	}
	return e.Engine.VerifyHeader(chain, header)
}

func (e awaitM2Engine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	abort, inner := e.Engine.VerifyHeaders(chain, headers)
	results := make(chan error, len(headers))
	go func() {
		for _, h := range headers {
			err := <-inner
			if h.Number.Uint64() == e.at && len(h.Validator) == 0 {
				err = XDPoS.ErrNoValidatorSignature
			}
			results <- err
		}
	}()
	return abort, results
}

// TestFullSyncSkipsBlockAwaitingM2 checks that a peer at the tip serving a V1
// block that still waits for its M2 co-signature (a legacy creator's own head)
// does not make the syncer import it: it is not reported or stored as a bad
// block, and the co-signed block is fetched on a later tick.
func TestFullSyncSkipsBlockAwaitingM2(t *testing.T) {
	const local, remote = 7, 8 // the peer's next block is its un-co-signed head
	n := newXDCFakeNetEngine(t, local, remote, awaitM2Engine{Engine: ethash.NewFaker(), at: remote})
	n.addPeer(t, 1, n.canonicalHeaders)
	s := n.newTestXDCSyncer(t)

	s.fullSyncOnce()

	if head := n.chain.CurrentBlock().Number.Uint64(); head != remote-1 {
		t.Fatalf("head = %d, want %d", head, remote-1)
	}
	if bad := rawdb.ReadAllBadBlocks(n.db); len(bad) != 0 {
		t.Fatalf("block awaiting its M2 co-signature stored as a bad block: %d", len(bad))
	}
}
