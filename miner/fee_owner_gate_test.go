// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package miner

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// TestFeeOwnerMinerMatchesImport (F-2026-20641): a block whose tx 0 changes the
// candidate owner of its validator gets the same fee routing, and so the same
// state root, when the miner builds it as when core.Process imports it, both
// before TIPUpgradePenalty (owner read once per block) and from it (per tx).
func TestFeeOwnerMinerMatchesImport(t *testing.T) {
	for _, tc := range []struct {
		name string
		fork *big.Int
	}{
		{"TIPUpgradePenalty nil", nil},
		{"TIPUpgradePenalty 0", big.NewInt(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				cb       = common.HexToAddress("0xc0ffee0000000000000000000000000000000001")
				oldOwner = common.HexToAddress("0x0a00000000000000000000000000000000000001")
				newOwner = common.HexToAddress("0x0b00000000000000000000000000000000000002")
				price    = big.NewInt(10 * params.GWei)
			)
			cfg := *params.AllEthashProtocolChanges
			cfg.TIPTRC21FeeBlock = big.NewInt(0)
			cfg.TIPUpgradePenaltyBlock = tc.fork

			// The contract at 0x88 moves the owner of cb to newOwner when called.
			ownerSlot := common.BigToHash(state.GetLocMappingAtKey(common.BytesToHash(cb.Bytes()), 1))
			code := append([]byte{0x73}, newOwner.Bytes()...)
			code = append(code, 0x7f)
			code = append(code, ownerSlot.Bytes()...)
			code = append(code, 0x55, 0x00)

			k1, _ := crypto.GenerateKey()
			k2, _ := crypto.GenerateKey()
			gspec := &core.Genesis{
				Config: &cfg,
				Alloc: types.GenesisAlloc{
					crypto.PubkeyToAddress(k1.PublicKey): {Balance: big.NewInt(1e18)},
					crypto.PubkeyToAddress(k2.PublicKey): {Balance: big.NewInt(1e18)},
					common.MasternodeVotingSMCBinary: {
						Balance: big.NewInt(0),
						Code:    code,
						Storage: map[common.Hash]common.Hash{ownerSlot: common.BytesToHash(oldOwner.Bytes())},
					},
				},
			}
			bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer bc.Stop()

			signer := types.LatestSigner(&cfg)
			other := common.Address{0x42}
			tx0, _ := types.SignTx(types.NewTx(&types.LegacyTx{To: &common.MasternodeVotingSMCBinary, Gas: 100_000, GasPrice: price}), signer, k1)
			tx1, _ := types.SignTx(types.NewTx(&types.LegacyTx{Nonce: 0, To: &other, Gas: 21_000, GasPrice: price}), signer, k2)

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

			// Import.
			imported, err := bc.State()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := core.NewStateProcessor(bc).Process(context.Background(), block, imported, nil, nil, vm.Config{}, nil); err != nil {
				t.Fatal(err)
			}

			// Build, the way makeEnv + applyTransaction do.
			built, err := bc.State()
			if err != nil {
				t.Fatal(err)
			}
			env := &environment{
				state:      built,
				header:     header,
				gasPool:    core.NewGasPool(header.GasLimit),
				coinbase:   cb,
				blockOwner: state.GetCandidateOwner(built, cb),
				evm:        vm.NewEVM(core.NewEVMBlockContext(header, bc, &cb), built, &cfg, vm.Config{}),
			}
			m := &Miner{chainConfig: &cfg}
			for i, tx := range block.Transactions() {
				if _, _, err := m.applyTransaction(context.Background(), env, tx); err != nil {
					t.Fatalf("tx %d: %v", i, err)
				}
			}
			rules := cfg.Rules(header.Number, true, header.Time)
			// Process pays the ethash block reward to cb, which the miner leaves to
			// Finalize; give it to the built state so the roots compare the fees.
			built.AddBalance(cb, imported.GetBalance(cb), tracing.BalanceChangeUnspecified)
			if got, want := built.IntermediateRoot(rules), imported.IntermediateRoot(rules); got != want {
				t.Fatalf("miner-built root %x != imported root %x", got, want)
			}
			// The two settings must differ in who is paid, or this proves nothing.
			if got, want := built.GetBalance(newOwner).IsZero(), tc.fork == nil; got != want {
				t.Fatalf("new owner has no fee = %v, want %v", got, want)
			}
		})
	}
}
