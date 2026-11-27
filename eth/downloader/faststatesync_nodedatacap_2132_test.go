package downloader

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/crypto/sha3"
)

// Helpers and tests in this file were copied from faststatesync_nodedatacap_test.go because that file is not in the audit tree.

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

	// Build a stateReq that claims 2 items but carries 5 blobs. It holds real
	// tasks: fillTasks took them out of the global pools, so process() must put
	// them back (XDC-HUNT-16).
	codeHash := common.Hash{0xc0}
	req := &stateReq{
		peer:   pc,
		nItems: 2,
		trieTasks: map[string]*trieTask{
			"p1": {hash: common.Hash{1}, path: "p1", attempts: map[string]struct{}{"misbehaving": {}}},
		},
		codeTasks:  map[common.Hash]*codeTask{codeHash: {attempts: map[string]struct{}{"misbehaving": {}}}},
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
	if _, ok := s.trieTasks["p1"]; !ok || len(s.trieTasks["p1"].attempts) != 0 {
		t.Fatalf("trie task not requeued after oversized response: %v", s.trieTasks)
	}
	if _, ok := s.codeTasks[codeHash]; !ok {
		t.Fatalf("code task not requeued after oversized response: %v", s.codeTasks)
	}
}

// TestAdmitLateDelivery: an unsolicited NodeData pack above MaxStateFetch is
// refused and the peer dropped; late packs queue only up to maxLateDeliveries
// (XDC-HUNT-34).
func TestAdmitLateDelivery(t *testing.T) {
	tester := newTester(t, ethconfig.FullSync)
	t.Cleanup(tester.terminate)
	dropped := ""
	tester.downloader.dropPeer = func(id string) { dropped = id }
	d := tester.downloader

	if !d.admitLateDelivery("ok", make([][]byte, MaxStateFetch), nil) || dropped != "" {
		t.Fatalf("pack of MaxStateFetch blobs refused (dropped=%q)", dropped)
	}
	if d.admitLateDelivery("abuser", make([][]byte, MaxStateFetch+1), nil) || dropped != "abuser" {
		t.Fatalf("oversized pack admitted or peer not dropped (dropped=%q)", dropped)
	}
	finished := make([]*stateReq, 0, maxLateDeliveries)
	for len(finished) < maxLateDeliveries {
		if !d.admitLateDelivery("p", [][]byte{{1}}, finished) {
			t.Fatalf("late pack %d refused below the cap", len(finished))
		}
		finished = append(finished, &stateReq{}) // peer == nil: late delivery
	}
	if d.admitLateDelivery("p", [][]byte{{1}}, finished) {
		t.Fatal("late pack admitted above the cap")
	}
	// Solicited requests in the queue do not count against the cap.
	finished = append(finished[:0], &stateReq{peer: &peerConnection{}})
	if !d.admitLateDelivery("p", [][]byte{{1}}, finished) {
		t.Fatal("late pack refused with only solicited requests queued")
	}
}
