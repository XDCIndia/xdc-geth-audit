// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"crypto/ecdsa"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// F-2026-19596: a TC built by canonical carries every pooled signer. Standby
// candidate signatures are ignored, and only active-set signers count.
func TestF202619596VerifyTCIgnoresStandbySigners(t *testing.T) {
	const round = types.Round(5)
	f := newTCFixture(t, 0)
	for _, tc := range []struct {
		name string
		keys []*ecdsa.PrivateKey
		ok   bool
	}{
		// threshold is 2 of 3 active masternodes
		{"threshold active + one standby", []*ecdsa.PrivateKey{f.active[0], f.active[1], f.stale}, true},
		{"threshold active + standby + penalized + unknown", []*ecdsa.PrivateKey{f.active[0], f.active[1], f.stale, f.penalized, f.other}, true},
		{"threshold-1 active + standby", []*ecdsa.PrivateKey{f.active[0], f.stale}, false},
		{"threshold-1 active + all extras", []*ecdsa.PrivateKey{f.active[0], f.stale, f.penalized, f.other}, false},
	} {
		sigs := signTimeout(t, round, tc.keys...)
		err := f.x.verifyTC(f.chain, &types.TimeoutCert{Round: round, GapNumber: 450, Signatures: sigs})
		if (err == nil) != tc.ok {
			t.Errorf("%s: verifyTC err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
