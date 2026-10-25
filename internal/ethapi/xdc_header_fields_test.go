package ethapi

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// TestRPCMarshalHeaderXDCFields checks the XDPoS header fields are returned
// with the same names and encoding as canonical XDPoSChain, including when empty.
func TestRPCMarshalHeaderXDCFields(t *testing.T) {
	head := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		Validators: []byte{0x01, 0x02},
		Validator:  []byte{0xab, 0xcd},
		Penalties:  []byte{0x0f},
	}
	before := head.Hash()
	want := map[string]string{"validators": "0x0102", "validator": "0xabcd", "penalties": "0x0f"}
	check := func(h *types.Header, want map[string]string) {
		out, err := json.Marshal(RPCMarshalHeader(h))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: got %v, want %q", k, got[k], v)
			}
		}
	}
	check(head, want)
	if head.Hash() != before {
		t.Fatal("header hash changed")
	}
	check(&types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(1)},
		map[string]string{"validators": "0x", "validator": "0x", "penalties": "0x"})
}
