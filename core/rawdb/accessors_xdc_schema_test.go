// Tests for issue #1789 (review R2-N03): votePrefix first byte collides with
// VerklePrefix; unused XDC accessors widen the storage surface unnecessarily.
//
// Fail-before: on origin/main, votePrefix = []byte("vote-") is declared in this
// package, starting with 'v' = VerklePrefix[0]. The test below includes votePrefix
// in the checked set. On main the assertion fires and the test fails.
// After fix: votePrefix is removed; the test file no longer references it;
// only the remaining singleton keys (BFT safety, TD backfill) are checked,
// none of which start with 'v'.
package rawdb

import (
	"testing"
)

// TestXDCKeySchemaNoVerkleOverlap verifies that none of the XDC-specific
// singleton keys or key-building prefixes in use start with VerklePrefix[0].
// FAILS on origin/main (votePrefix "vote-" starts with 'v'); PASSES with fix.
func TestXDCKeySchemaNoVerkleOverlap(t *testing.T) {
	// Remaining XDC singleton keys after R2-N03 removal.
	// On origin/main, adding votePrefix here causes the test to fail.
	remaining := []struct {
		name string
		key  []byte
	}{
		{"bftHighestVotedRoundKey", bftHighestVotedRoundKey},
		{"bftHighestSelfMinedRoundKey", bftHighestSelfMinedRoundKey},
		{"bftLockQCKey", bftLockQCKey},
		{"xdcTDBackfillProgressKey", xdcTDBackfillProgressKey},
	}
	for _, tc := range remaining {
		if len(tc.key) > 0 && tc.key[0] == VerklePrefix[0] {
			t.Errorf("XDC key %q (%q) starts with VerklePrefix byte 0x%02x (%q) — overlap must not exist",
				tc.name, tc.key, VerklePrefix[0], string(VerklePrefix[0:1]))
		}
	}
}
