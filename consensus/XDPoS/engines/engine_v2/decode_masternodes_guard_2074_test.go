package engine_v2

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// decodeMasternodesFromHeaderExtra must not panic on a short extra field and
// must reject a payload that is not a multiple of 20 bytes, as canonical does (#2074).
func TestDecodeMasternodesFromHeaderExtraGuards(t *testing.T) {
	for _, n := range []int{0, 40, 77, 96} {
		if got := decodeMasternodesFromHeaderExtra(&types.Header{Extra: make([]byte, n)}); got != nil {
			t.Fatalf("len %d: want nil, got %v", n, got)
		}
	}
	if got := decodeMasternodesFromHeaderExtra(&types.Header{Extra: make([]byte, 32+65+7)}); got != nil {
		t.Fatalf("misaligned payload: want nil, got %v", got)
	}

	extra := make([]byte, 32+2*common.AddressLength+65)
	extra[32+common.AddressLength] = 0xaa
	got := decodeMasternodesFromHeaderExtra(&types.Header{Extra: extra})
	if len(got) != 2 || got[1][0] != 0xaa {
		t.Fatalf("valid extra: got %v", got)
	}
}
