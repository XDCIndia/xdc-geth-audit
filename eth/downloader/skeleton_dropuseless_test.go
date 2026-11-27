// Copyright 2026 XDC Network

package downloader

import "testing"

// TestSkeletonDropUselessPeerRouting: a peer that is only useless to the skeleton
// goes to dropUseless when set, and to drop otherwise (XDC-HUNT-04).
func TestSkeletonDropUselessPeerRouting(t *testing.T) {
	var dropped, useless []string
	s := &skeleton{drop: func(id string) { dropped = append(dropped, id) }}

	s.dropUselessPeer("a")
	if len(dropped) != 1 || len(useless) != 0 {
		t.Fatalf("unset dropUseless: dropped=%v useless=%v", dropped, useless)
	}
	s.dropUseless = func(id string) { useless = append(useless, id) }
	s.dropUselessPeer("b")
	if len(dropped) != 1 || len(useless) != 1 {
		t.Fatalf("set dropUseless: dropped=%v useless=%v", dropped, useless)
	}
}
