// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// CertiK XDC-16: NodeData responses must not exceed the requested item count.
// If a peer returns more blobs than were requested, the response is dropped
// and the peer is penalised before any blob is hashed.
//
// Refs #1713 / XDC-16.

package downloader

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/crypto/sha3"
)

// TestProcessRejectsOversizedNodeDataResponse verifies that process() returns
// (0, nil) without hashing any blob when the peer returns more blobs than
// nItems, and that dropPeer is called with the misbehaving peer's id.
//
// On origin/main the oversized-response guard does not exist, so all five
// blobs are hashed and the peer is not dropped; the "dropped" variable stays
// empty and the assertion at the end fails.
// With the fix, process() short-circuits on the oversized check, calls
// dropPeer before hashing anything, and returns (0, nil).
func TestProcessRejectsOversizedNodeDataResponse(t *testing.T) {
	tester := newTester(t, ethconfig.FullSync)
	t.Cleanup(tester.terminate)

	// Override dropPeer so we can observe whether it was called.
	dropped := ""
	tester.downloader.dropPeer = func(id string) { dropped = id }

	// Construct a minimal peerConnection — process() only reads id and log.
	pc := &peerConnection{
		id:         "misbehaving",
		lacking:    make(map[common.Hash]struct{}),
		lackingBAL: make(map[common.Hash]struct{}),
		log:        log.New("peer", "misbehaving"),
	}

	// Build a stateReq that claims 2 items but carries 5 blobs.
	req := &stateReq{
		peer:       pc,
		nItems:     2,
		trieTasks:  make(map[string]*trieTask),
		codeTasks:  make(map[common.Hash]*codeTask),
		trieByHash: make(map[common.Hash]string),
		response:   [][]byte{{0x01}, {0x02}, {0x03}, {0x04}, {0x05}},
	}

	s := &fastStateSync{
		d:                 tester.downloader,
		trieTasks:         make(map[string]*trieTask),
		codeTasks:         make(map[common.Hash]*codeTask),
		inflightHashes:    make(map[common.Hash]struct{}),
		recentHashes:      make(map[common.Hash]struct{}),
		recentHashesOrder: make([]common.Hash, 0),
		keccak:            sha3.NewLegacyKeccak256().(crypto.KeccakState),
	}

	n, err := s.process(req)
	if err != nil {
		t.Fatalf("process returned unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("process returned %d successful blobs, want 0", n)
	}
	if dropped != "misbehaving" {
		t.Fatalf("dropPeer not called with peer id: got %q, want %q", dropped, "misbehaving")
	}
}
