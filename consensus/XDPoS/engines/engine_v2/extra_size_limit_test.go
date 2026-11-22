// Copyright (c) 2026 XDC Network
// XDC-HUNT-15 (#2068): both V2 extra decoders refuse an extra field longer than
// 20 KiB, as canonical DecodeBytesExtraFields does
// (consensus/XDPoS/utils/utils.go:80-84 in XDPoSChain v3.0.0-testnet-2).
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestV2ExtraSizeLimit -count=1

package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// extraOfLen returns a valid V2 extra field of exactly n bytes: the version
// byte plus an RLP list whose last item is a padded signature slice.
func extraOfLen(t *testing.T, n int) []byte {
	t.Helper()
	build := func(pad int) []byte {
		qc := &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{Number: big.NewInt(1)},
			Signatures:        []types.Signature{make([]byte, pad)},
		}
		b, err := rlp.EncodeToBytes(&types.ExtraFields_v2{Round: 1, QuorumCert: qc})
		if err != nil {
			t.Fatal(err)
		}
		return append([]byte{2}, b...)
	}
	// The encoded length grows with the padding; find the padding that gives n.
	for pad := n - 200; pad <= n; pad++ {
		if pad < 0 {
			continue
		}
		if b := build(pad); len(b) == n {
			return b
		}
	}
	t.Fatalf("no padding gives %d bytes", n)
	return nil
}

func TestV2ExtraSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		size    int
		wantErr bool
	}{
		{utils.MaxV2ExtraSize, false},
		{utils.MaxV2ExtraSize + 1, true},
	} {
		extra := extraOfLen(t, tc.size)

		var decoded types.ExtraFields_v2
		if err := DecodeExtraFields(extra, &decoded); (err != nil) != tc.wantErr {
			t.Errorf("DecodeExtraFields(%d bytes): err = %v, want error %v", tc.size, err, tc.wantErr)
		}
		var viaUtils types.ExtraFields_v2
		if err := utils.DecodeBytesExtraFields(extra, &viaUtils); (err != nil) != tc.wantErr {
			t.Errorf("DecodeBytesExtraFields(%d bytes): err = %v, want error %v", tc.size, err, tc.wantErr)
		}
	}
}
