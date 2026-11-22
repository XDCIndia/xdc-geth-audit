// Copyright (c) 2026 XDC Network

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

func TestF202620609GuardIgnoresADifferentNumber(t *testing.T) {
	other := &types.Header{Number: big.NewInt(10)}
	if guardHeaderMatches([]*types.Header{other}, 20) {
		t.Fatal("a header at another number was treated as the guard block")
	}
	match := &types.Header{Number: big.NewInt(20)}
	if !guardHeaderMatches([]*types.Header{match}, 20) {
		t.Fatal("the guard number was not recognised")
	}
}
