// Copyright (c) 2026 XDC Network

package miner

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestF202619643MinerSkipsBlacklistedAddress(t *testing.T) {
	old := common.BlackListHFNumber
	common.BlackListHFNumber = 100
	t.Cleanup(func() { common.BlackListHFNumber = old })
	var listed common.Address
	for addr := range commonBlacklist() {
		listed = addr
		break
	}
	if (listed == common.Address{}) {
		t.Fatal("blacklist is empty")
	}
	if minerSkipsBlacklisted(99, listed, nil) {
		t.Fatal("blacklist applied before the fork block")
	}
	if !minerSkipsBlacklisted(100, listed, nil) {
		t.Fatal("blacklisted sender was not skipped")
	}
	other := common.HexToAddress("0x1111111111111111111111111111111111111111")
	if minerSkipsBlacklisted(100, other, &listed) == false {
		t.Fatal("blacklisted recipient was not skipped")
	}
}

func commonBlacklist() map[common.Address]struct{} {
	// Touch the same list the miner uses.
	out := map[common.Address]struct{}{}
	sample := common.HexToAddress("0x5248bfb72fd4f234e062d3e9bb76f08643004fcd")
	if common.IsInBlacklist(&sample) {
		out[sample] = struct{}{}
	}
	return out
}
