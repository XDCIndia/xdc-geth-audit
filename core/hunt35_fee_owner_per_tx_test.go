// Copyright 2026 The go-ethereum Authors
// The fee recipient (candidate owner of the block's validator, read from the
// 0x88 contract) is read at a consensus-defined point (F-2026-20641):
//   - before TIPUpgradePenalty (mainnet; canonical XDPoSChain v2.7.1) once per
//     block, before the tx loop: when tx 0 changes the owner, tx 1 still pays
//     its fee to the old owner;
//   - from TIPUpgradePenalty (Apothem 83,600,000; canonical v2.8.0 onward,
//     and later) for each tx: tx 1 pays its fee to the new owner.

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

// feeOwnerFixture holds a chain whose 0x88 contract moves the owner of cb from
// oldOwner to newOwner when called, and a block of two txs: tx 0 calls 0x88,
// tx 1 is a plain transfer.
type feeOwnerFixture struct {
	bc                 *BlockChain
	cfg                *params.ChainConfig
	block              *types.Block
	cb                 common.Address
	oldOwner, newOwner common.Address
	price              *big.Int
}

func newFeeOwnerFixture(t *testing.T, penaltyFork *big.Int) *feeOwnerFixture {
	t.Helper()
	var (
		cb       = common.HexToAddress("0xc0ffee0000000000000000000000000000000001")
		oldOwner = common.HexToAddress("0x0a00000000000000000000000000000000000001")
		newOwner = common.HexToAddress("0x0b00000000000000000000000000000000000002")
		price    = big.NewInt(10 * params.GWei)
	)
	cfg := *params.AllEthashProtocolChanges
	cfg.TIPTRC21FeeBlock = big.NewInt(0)
	cfg.TIPUpgradePenaltyBlock = penaltyFork

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
	t.Cleanup(bc.Stop)

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
	return &feeOwnerFixture{bc, &cfg, block, cb, oldOwner, newOwner, price}
}

func TestFeeOwnerReadPoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		fork *big.Int
		// who gets the fee of tx 1, which runs after tx 0 changed the owner
		tx1ToNew bool
	}{
		{"TIPUpgradePenalty nil: owner read once per block", nil, false},
		{"TIPUpgradePenalty 0: owner read per tx", big.NewInt(0), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFeeOwnerFixture(t, tc.fork)
			statedb, err := f.bc.State()
			if err != nil {
				t.Fatal(err)
			}
			res, err := NewStateProcessor(f.bc).Process(context.Background(), f.block, statedb, nil, nil, vm.Config{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			fee := func(i int) *big.Int {
				return new(big.Int).Mul(new(big.Int).SetUint64(res.Receipts[i].GasUsed), f.price)
			}
			wantOld, wantNew := fee(0), new(big.Int)
			if tc.tx1ToNew {
				wantNew = fee(1)
			} else {
				wantOld = new(big.Int).Add(wantOld, fee(1))
			}
			if got := statedb.GetBalance(f.oldOwner).ToBig(); got.Cmp(wantOld) != 0 {
				t.Errorf("old owner got %v, want %v", got, wantOld)
			}
			if got := statedb.GetBalance(f.newOwner).ToBig(); got.Cmp(wantNew) != 0 {
				t.Errorf("new owner got %v, want %v", got, wantNew)
			}
		})
	}
}
