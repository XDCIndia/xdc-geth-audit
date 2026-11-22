// Copyright (c) 2026 XDC Network
// F-2026-20590: one signer keeps one pool entry per round across block hashes.

package utils

import (
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// testPoolObj is a minimal PoolObj for pool tests.
type testPoolObj struct {
	hash   common.Hash
	key    string
	signer common.Address
}

func (o *testPoolObj) Hash() common.Hash          { return o.hash }
func (o *testPoolObj) PoolKey() string            { return o.key }
func (o *testPoolObj) GetSigner() common.Address  { return o.signer }
func (o *testPoolObj) SetSigner(a common.Address) { o.signer = a }

// TestPoolAdd_SignerCap verifies that Pool.Add keeps at most one entry per
// (pool key, recovered signer), even when the objects have distinct hashes
// (e.g. from re-signing or ECDSA malleation).
//
// Before fix: 1000 variants from one signer → 1000 pool entries → FAIL
// After fix:  1000 variants from one signer → 1 pool entry       → PASS

// TestPoolAdd_OneSignerPerRoundAcrossHashes checks that one signer cannot fill
// the pool by varying the block hash inside one round.
func TestPoolAdd_OneSignerPerRoundAcrossHashes(t *testing.T) {
	p := NewPool()
	signer := common.HexToAddress("0x00000000000000000000000000000000000000a1")
	for i := 0; i < 500; i++ {
		var h common.Hash
		h[0] = byte(i)
		h[1] = byte(i >> 8)
		p.Add(&testPoolObj{
			hash:   h,
			key:    fmt.Sprintf("5:450:%d:%s", 1000+i, h.Hex()),
			signer: signer,
		})
	}
	if got := p.Size(); got != 1 {
		t.Fatalf("one signer, 500 hashes, one round: pool size %d, want 1", got)
	}
	if got := len(p.PoolObjKeysList()); got != 1 {
		t.Fatalf("pool keys %d, want 1", got)
	}
	p.Add(&testPoolObj{hash: common.Hash{0x09}, key: "6:450:2000:0x09", signer: signer})
	if got := p.Size(); got != 2 {
		t.Fatalf("a second round must be kept: pool size %d, want 2", got)
	}
}
