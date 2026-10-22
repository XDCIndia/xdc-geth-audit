// Copyright (c) 2026 XDC Network
// Tests for Pool.Add signer cap (CertiK XDC-07).
//
// Pool.Add must keep at most one entry per (pool key, recovered signer).
// Before the fix, Vote.Hash() includes the signature bytes so a single
// signer key can occupy arbitrarily many pool slots by re-signing with
// different nonces/malleated bytes — causing unbounded memory growth.

package utils

import (
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
func TestPoolAdd_SignerCap(t *testing.T) {
	const key = "10:450:1001:0xabc123"
	signer := common.HexToAddress("0xAAAA000000000000000000000000000000000001")

	p := NewPool()

	for i := 0; i < 1000; i++ {
		var h common.Hash
		h[0] = byte(i)
		h[1] = byte(i >> 8)
		obj := &testPoolObj{hash: h, key: key, signer: signer}
		p.Add(obj)
	}

	got := p.Size()
	if got != 1 {
		t.Errorf("Pool.Add signer cap (XDC-07): got %d entries for 1 signer, want 1", got)
	}
}

// TestPoolAdd_MultiSignerDistinct verifies that distinct signers each get
// one slot — the cap must not merge different signers.
func TestPoolAdd_MultiSignerDistinct(t *testing.T) {
	const key = "10:450:1001:0xabc123"
	p := NewPool()

	for i := 0; i < 5; i++ {
		var h common.Hash
		h[0] = byte(i)
		var s common.Address
		s[0] = byte(i + 1)
		obj := &testPoolObj{hash: h, key: key, signer: s}
		p.Add(obj)
	}

	// Then add 10 duplicate hashes for each signer — they must all be dropped.
	for i := 0; i < 5; i++ {
		for j := 1; j <= 10; j++ {
			var h common.Hash
			h[0] = byte(i)
			h[1] = byte(j)
			var s common.Address
			s[0] = byte(i + 1)
			obj := &testPoolObj{hash: h, key: key, signer: s}
			p.Add(obj)
		}
	}

	got := p.Size()
	if got != 5 {
		t.Errorf("distinct signers: got %d entries, want 5", got)
	}
}

// TestPoolAdd_ZeroSignerNotDeduplicated verifies that objects whose signer has
// not been set yet (zero address) are NOT collapsed to one — we must not
// accidentally deduplicate unverified objects.
func TestPoolAdd_ZeroSignerNotDeduplicated(t *testing.T) {
	const key = "10:450:1001:0xdef456"
	p := NewPool()
	zero := common.Address{}

	for i := 0; i < 3; i++ {
		var h common.Hash
		h[0] = byte(i + 1)
		obj := &testPoolObj{hash: h, key: key, signer: zero}
		p.Add(obj)
	}

	got := p.Size()
	if got != 3 {
		t.Errorf("zero-signer objects should not be collapsed: got %d, want 3", got)
	}
}
