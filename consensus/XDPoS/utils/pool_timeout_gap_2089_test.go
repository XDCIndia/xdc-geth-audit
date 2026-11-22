package utils

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// A signer that re-sends its timeout in the same round with the gap number of a
// new epoch must be kept under the new key. Canonical keeps both (#2089).
func TestPoolTimeoutNewGapSameRoundIsKept(t *testing.T) {
	p := NewPool()
	signer := common.HexToAddress("0x00000000000000000000000000000000000000a1")

	p.Add(&testPoolObj{hash: common.Hash{1}, key: "7:450", signer: signer})
	n, _ := p.Add(&testPoolObj{hash: common.Hash{2}, key: "7:1350", signer: signer})
	if n != 1 || len(p.GetByPoolKey("7:1350")) != 1 {
		t.Fatalf("timeout with the new gap was dropped: count %d", n)
	}
	// Same round and gap from the same signer is still one slot.
	p.Add(&testPoolObj{hash: common.Hash{3}, key: "7:1350", signer: signer})
	if got := p.Size(); got != 2 {
		t.Fatalf("pool size %d, want 2", got)
	}
}

// ClearByPoolKey must free the signer slot even when the stored vote's signer
// was blanked after it was pooled (#2089).
func TestPoolClearByPoolKeyFreesBlankedSignerSlot(t *testing.T) {
	p := NewPool()
	signer := common.HexToAddress("0x00000000000000000000000000000000000000a1")
	const key = "9:450:1001:0xabc"
	obj := &testPoolObj{hash: common.Hash{1}, key: key, signer: signer}
	p.Add(obj)
	obj.SetSigner(common.Address{}) // what verifyVotes does to a vote that left the masternode list

	p.ClearByPoolKey(key)
	if len(p.signerRound) != 0 {
		t.Fatalf("signer slot leaked: %v", p.signerRound)
	}
	if n, _ := p.Add(&testPoolObj{hash: common.Hash{2}, key: key, signer: signer}); n != 1 {
		t.Fatalf("signer could not vote again after the key was cleared: count %d", n)
	}
}
