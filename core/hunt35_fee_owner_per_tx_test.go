// Copyright 2026 The go-ethereum Authors
// XDC-HUNT-35 (#2088): the fee recipient (candidate owner of the block's
// validator, read from the 0x88 contract) is read from state for each
// transaction, as canonical does (core/state_processor.go:442-443 in XDPoSChain
// v3.0.0-testnet-2). When tx 0 changes the owner, tx 1 pays its fee to the new
// owner.

package core

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestHunt35FeeOwnerReadPerTransaction(t *testing.T) {
	var (
		cb       = common.HexToAddress("0xc0ffee0000000000000000000000000000000001")
		oldOwner = common.HexToAddress("0x0a00000000000000000000000000000000000001")
		newOwner = common.HexToAddress("0x0b00000000000000000000000000000000000002")
		price    = big.NewInt(10 * params.GWei)
	)
	cfg := *params.AllEthashProtocolChanges
	cfg.TIPTRC21FeeBlock = big.NewInt(0)

	// validatorsState[cb].owner lives at keccak(cb . slot 1). The contract at 0x88
	// sets it to newOwner when called.
	ownerSlot := common.BigToHash(state.GetLocMappingAtKey(common.BytesToHash(cb.Bytes()), 1))
	code := append([]byte{0x73}, newOwner.Bytes()...) // PUSH20 newOwner
	code = append(code, 0x7f)                         // PUSH32 slot
	code = append(code, ownerSlot.Bytes()...)
	code = append(code, 0x55, 0x00) // SSTORE, STOP

	k1, _ := crypto.GenerateKey()
	k2, _ := crypto.GenerateKey()
	s1, s2 := crypto.PubkeyToAddress(k1.PublicKey), crypto.PubkeyToAddress(k2.PublicKey)
	gspec := &Genesis{
		Config: &cfg,
		Alloc: types.GenesisAlloc{
			s1: {Balance: big.NewInt(1e18)},
			s2: {Balance: big.NewInt(1e18)},
			common.MasternodeVotingSMCBinary: {
				Balance: big.NewInt(0),
				Code:    code,
				Storage: map[common.Hash]common.Hash{ownerSlot: common.BytesToHash(oldOwner.Bytes())},
			},
		},
	}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	signer := types.LatestSigner(&cfg)
	tx0, _ := types.SignTx(types.NewTx(&types.LegacyTx{To: &common.MasternodeVotingSMCBinary, Gas: 100_000, GasPrice: price}), signer, k1)
	other := common.Address{0x42}
	tx1, _ := types.SignTx(types.NewTx(&types.LegacyTx{To: &other, Gas: 21_000, GasPrice: price}), signer, k2)

	parent := bc.CurrentBlock()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(1),
		Coinbase:   cb,
		Difficulty: big.NewInt(1),
		GasLimit:   parent.GasLimit,
		Time:       parent.Time + 1,
		BaseFee:    big.NewInt(params.GWei),
	}
	block := types.NewBlockWithHeader(header).WithBody(types.Body{Transactions: types.Transactions{tx0, tx1}})
	statedb, err := bc.State()
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewStateProcessor(bc).Process(context.Background(), block, statedb, nil, nil, vm.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fee := func(i int) *big.Int {
		return new(big.Int).Mul(new(big.Int).SetUint64(res.Receipts[i].GasUsed), price)
	}
	if got, want := statedb.GetBalance(oldOwner).ToBig(), fee(0); got.Cmp(want) != 0 {
		t.Errorf("old owner got %v, want the fee of tx 0 (%v)", got, want)
	}
	if got, want := statedb.GetBalance(newOwner).ToBig(), fee(1); got.Cmp(want) != 0 {
		t.Errorf("new owner got %v, want the fee of tx 1 (%v)", got, want)
	}
}
