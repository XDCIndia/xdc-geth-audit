// Copyright (c) 2026 XDC Network

package privacy

import (
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

func TestF202620591OversizedCommitmentsSkipKeygen(t *testing.T) {
	m := params.MaxBlockSize/PubKeyBytesLenCompressed + 1
	mrp := &MultiRangeProof{Comms: make([]ECPoint, m)}
	if MRPVerify(mrp) {
		t.Fatal("oversized commitment count was accepted")
	}
}

func TestF202620591AbortReleasesBeforeKeygen(t *testing.T) {
	mrp := &MultiRangeProof{Comms: make([]ECPoint, 1)}
	if MRPVerify(mrp, func() bool { return true }) {
		t.Fatal("aborted verification returned true")
	}
}
