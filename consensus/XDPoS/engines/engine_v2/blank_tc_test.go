// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package engine_v2

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// TestIsBlankTC pins which timeout certificates VerifySyncInfoMessage lets
// through unverified (XinFinOrg/XDPoSChain #2525): only the bootstrap TC, round
// 0 without signatures. Anything else is still verified.
func TestIsBlankTC(t *testing.T) {
	for _, tc := range []struct {
		name string
		cert *types.TimeoutCert
		want bool
	}{
		{"nil", nil, false},
		{"bootstrap: round 0, no signatures", &types.TimeoutCert{Round: 0}, true},
		{"round 0 with signatures", &types.TimeoutCert{Round: 0, Signatures: []types.Signature{{1}}}, false},
		{"real round, no signatures", &types.TimeoutCert{Round: 7}, false},
		{"real round with signatures", &types.TimeoutCert{Round: 7, Signatures: []types.Signature{{1}}}, false},
	} {
		if got := isBlankTC(tc.cert); got != tc.want {
			t.Errorf("%s: isBlankTC = %v, want %v", tc.name, got, tc.want)
		}
	}
}
