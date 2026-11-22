// Copyright (c) 2026 XDC Network

package legacypool

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestF202620603ReducedTRC21CapacitySelectsPayer(t *testing.T) {
	token := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	other := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	payer := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	before := map[common.Address]*big.Int{token: big.NewInt(100), other: big.NewInt(5)}
	after := map[common.Address]*big.Int{token: big.NewInt(40), other: big.NewInt(5)}
	reduced := reducedTRC21Issuers(before, after)
	if _, ok := reduced[token]; !ok {
		t.Fatal("token whose fee capacity fell was not selected")
	}
	if _, ok := reduced[other]; ok {
		t.Fatal("token whose fee capacity held was selected")
	}

	txs := newList(false)
	txs.Add(types.NewTx(&types.LegacyTx{To: &token, Gas: 21000, GasPrice: big.NewInt(1)}), 0)
	pending := map[common.Address]*list{payer: txs}
	got := accountsSendingTo(pending, reduced)
	if len(got) != 1 || got[0] != payer {
		t.Fatalf("payers = %v, want %s", got, payer.Hex())
	}
	if len(accountsSendingTo(pending, map[common.Address]struct{}{})) != 0 {
		t.Fatal("an empty reduced set walked the pending accounts")
	}
}
