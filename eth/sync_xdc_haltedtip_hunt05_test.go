package eth

import (
	"testing"
)

// Helpers and tests in this file were copied from sync_xdc_haltedtip_test.go because that file is not in the audit tree.

// A low head advertised by the only eth/69 peer must not read as "at tip" when
// the syncer has verified a much higher tip from another peer (XDC-HUNT-05).
func TestPeerAdvertisesTip(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		cur, peerHead, networkTip uint64
		want                      bool
	}{
		{"at the advertised head", 1000, 1000, 0, true},
		{"within tolerance", 1000, 1003, 0, true},
		{"beyond tolerance", 1000, 1004, 0, false},
		{"no peer head", 1000, 0, 1000, false},
		{"low advertised head, far behind verified tip", 1000, 900, 5000, false},
		{"verified tip within tolerance", 1000, 900, 1002, true},
		{"verified tip unknown", 1000, 900, 0, true},
	} {
		if got := peerAdvertisesTip(tc.cur, tc.peerHead, tc.networkTip); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
