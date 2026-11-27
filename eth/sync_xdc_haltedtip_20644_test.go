package eth

import (
	"testing"
)

// Helpers and tests in this file were copied from sync_xdc_haltedtip_test.go because that file is not in the audit tree.

// A low head advertised by the only eth/69 peer must not read as "at tip" when
// the syncer has verified a much higher tip from another peer.
func TestPeerAdvertisesTip(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		cur, peerHead, networkTip uint64
		want                      bool
	}{
		{"advertised head equal to ours is no evidence", 1000, 1000, 0, false},
		{"advertised head 1 is no evidence (F-2026-20644)", 1000, 1, 0, false},
		{"within tolerance", 1000, 1003, 0, true},
		{"beyond tolerance", 1000, 1004, 0, false},
		{"no peer head, verified tip equal", 1000, 0, 1000, true},
		{"low advertised head, far behind verified tip", 1000, 900, 5000, false},
		{"verified tip within tolerance", 1000, 900, 1002, true},
		{"low advertised head, verified tip unknown", 1000, 900, 0, false},
	} {
		if got := peerAdvertisesTip(tc.cur, tc.peerHead, tc.networkTip, false); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// F-2026-20644: one eth/69 peer advertising a low head must not end bulk sync
// while XDPOS2 peers are connected and ahead, or while their heads are not yet
// known.
func TestPeerAdvertisesTipXDPOS2Peers(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		cur, peerHead, networkTip uint64
		want                      bool
	}{
		{"low eth/69 head, XDPOS2 tip unknown", 1000, 900, 0, false},
		{"low eth/69 head, XDPOS2 peers far ahead", 1000, 900, 5000, false},
		{"XDPOS2 tip confirms", 1000, 900, 1002, true},
	} {
		if got := peerAdvertisesTip(tc.cur, tc.peerHead, tc.networkTip, true); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
