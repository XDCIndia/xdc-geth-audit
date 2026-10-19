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
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// TestXDCSyncerPeerIterationVsChurn walks the peer set from the xdcSyncer while
// peers register and unregister concurrently. Iterating peerSet.peers without
// peerSet.lock is a data race (and can kill the process with "concurrent map
// iteration and map write"); run with -race.
func TestXDCSyncerPeerIterationVsChurn(t *testing.T) {
	s := &xdcSyncer{handler: &handler{peers: newPeerSet()}}
	newPeer := func(id byte) *eth.Peer {
		app, _ := p2p.MsgPipe()
		t.Cleanup(func() { app.Close() })
		p := eth.NewPeer(eth.XDPOS2, p2p.NewPeer(enode.ID{id}, "churn", nil), app, nil, nil, params.TestChainConfig)
		t.Cleanup(p.Close)
		return p
	}
	// Half the peers stay registered so every walk has work to do; the other
	// half churn.
	peers := make([]*eth.Peer, 32)
	for i := range peers {
		peers[i] = newPeer(byte(i + 1))
		if i < len(peers)/2 {
			s.handler.peers.registerPeer(peers[i], nil)
		}
	}
	churn := peers[len(peers)/2:]
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			for _, p := range churn {
				select {
				case <-stop:
					return
				default:
				}
				s.handler.peers.registerPeer(p, nil)
				s.handler.peers.unregisterPeer(p.ID())
			}
		}
	}()
	for i := 0; i < 20000; i++ {
		s.fetchPeerHeadHeader() // peers have no BlockRange: iterates, no network I/O
	}
	close(stop)
	wg.Wait()
}
