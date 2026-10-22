// Copyright (c) 2026 XDC Network
// F-2026-19469: findAncestorQCs must return an error, not panic, when a parent
// header's Extra does not decode or carries no quorum certificate.
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestFindAncestorQCsBadParentExtra -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestFindAncestorQCsBadParentExtra(t *testing.T) {
	cases := map[string][]byte{
		"undecodable extra": {0x02, 0xff, 0xff, 0xff},
		"extra without QC":  mustEncodeExtra(t, &types.ExtraFields_v2{Round: 5}),
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			chain := &esChain{byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
			parent := &types.Header{Number: big.NewInt(10), Extra: extra}
			chain.add(parent)
			qc := types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Hash: parent.Hash(), Number: parent.Number, Round: 6}}

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("findAncestorQCs panicked: %v", r)
				}
			}()
			if _, err := NewForensics().findAncestorQCs(chain, qc, 1); err == nil {
				t.Fatal("findAncestorQCs returned no error for a bad parent extra")
			}
		})
	}
}

func mustEncodeExtra(t *testing.T, e *types.ExtraFields_v2) []byte {
	t.Helper()
	b, err := e.EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
