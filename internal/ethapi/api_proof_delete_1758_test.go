// Tests for issue #1758: proofPairList.Delete must return an error instead of
// panicking (review R1-R04).
// This test FAILS on origin/main (Delete panics) and PASSES with the fix.
package ethapi

import (
	"testing"
)

// TestProofPairListDeleteNoPoanic verifies that proofPairList.Delete returns an
// error rather than panicking. On origin/main this test fails because the panic
// kills the goroutine; with the fix it passes.
func TestProofPairListDeleteNoPanic(t *testing.T) {
	var n proofPairList
	err := n.Delete([]byte("somekey"))
	if err == nil {
		t.Fatal("expected non-nil error from Delete, got nil")
	}
}

// TestProofPairListPutStillWorks confirms Put is unaffected by the Delete fix.
func TestProofPairListPutStillWorks(t *testing.T) {
	var n proofPairList
	if err := n.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Put returned unexpected error: %v", err)
	}
	if len(n.keys) != 1 || len(n.values) != 1 {
		t.Fatalf("expected 1 key/value after Put, got %d/%d", len(n.keys), len(n.values))
	}
}
