// Copyright (c) 2026 XDC Network
// XDC-HUNT-56 (#2113): with RoundSkipTCBlock set, the verdict for the timeout
// certificate in a header depends on the header and its ancestors only, not on
// where the local head is.

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

func TestRoundSkipCertificateVerdictIndependentOfHead(t *testing.T) {
	eng, chain, keys, parent, h := skipFixture(t, 1000, 1005, nil, big.NewInt(903))
	gap := eng.gapFromSwitch(901)
	tc := &types.TimeoutCert{Round: 1004, GapNumber: gap, Signatures: signTimeoutGap(t, 1004, gap, keys[:3]...)}
	resealWithCertificate(t, h, keys, eng.config.Epoch, tc)

	realHead := chain.head
	// An older block with no V2 epoch behind it, like a node whose head has not
	// reached the epoch of the header yet.
	for name, head := range map[string]*types.Header{
		"head in the epoch of the header": realHead,
		"head far behind":                 chain.GetHeaderByNumber(450),
	} {
		chain.head = head
		eng.verifiedHeaders.Purge()
		if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err != nil {
			t.Errorf("%s: certificate of a valid skipped header rejected: %v", name, err)
		}
	}

	// A certificate the committee did not sign is rejected whatever the head is.
	bad := &types.TimeoutCert{Round: 1004, GapNumber: gap, Signatures: signTimeoutGap(t, 1004, gap, keys[:1]...)}
	resealWithCertificate(t, h, keys, eng.config.Epoch, bad)
	for name, head := range map[string]*types.Header{
		"head in the epoch of the header": realHead,
		"head far behind":                 chain.GetHeaderByNumber(450),
	} {
		chain.head = head
		eng.verifiedHeaders.Purge()
		if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err == nil {
			t.Errorf("%s: certificate with too few signers accepted", name)
		}
	}
}
