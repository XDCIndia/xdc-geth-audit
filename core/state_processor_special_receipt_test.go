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

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
)

// TestSpecialRouteReceiptIsLegacy pins the receipt of a typed tx on the XDC
// special routes (sign to 0x89, XDCx notifier to 0x91-0x94) to a legacy
// receipt, as canonical XDPoSChain builds it with types.NewReceipt
// (core/state_processor.go:505 and :529). With the tx type instead, the
// receipts root of network 2022 block 2538 (a type-2 sign tx) did not match
// the canonical root and the block was rejected (#1600).
func TestSpecialRouteReceiptIsLegacy(t *testing.T) {
	var (
		config = params.MergedTestChainConfig
		key, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		signer = types.LatestSigner(config)
		number = big.NewInt(1)
	)
	dynamic := func(to common.Address) types.TxData {
		return &types.DynamicFeeTx{ChainID: config.ChainID, Nonce: 0, To: &to, Gas: 100000, Data: make([]byte, 68)}
	}
	setCode := func(to common.Address) types.TxData {
		auth, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.MustFromBig(config.ChainID), Address: common.Address{0x42}, Nonce: 1})
		if err != nil {
			t.Fatal(err)
		}
		return &types.SetCodeTx{ChainID: uint256.MustFromBig(config.ChainID), Nonce: 0, To: to, Gas: 100000, Data: make([]byte, 68),
			GasFeeCap: new(uint256.Int), GasTipCap: new(uint256.Int), AuthList: []types.SetCodeAuthorization{auth}}
	}
	apply := map[string]func(*state.StateDB, *types.Transaction) (*types.Receipt, error){
		"sign": func(db *state.StateDB, tx *types.Transaction) (*types.Receipt, error) {
			return ApplySigningTransaction(config, db, number, common.Hash{}, tx, 0)
		},
		"empty": func(db *state.StateDB, tx *types.Transaction) (*types.Receipt, error) {
			return ApplyEmptyTransaction(config, db, number, common.Hash{}, tx, 0)
		},
	}
	cases := []struct {
		name  string
		route string
		data  types.TxData
	}{
		{"type-2 sign tx to 0x89", "sign", dynamic(common.BlockSignersBinary)},
		{"type-4 sign tx to 0x89", "sign", setCode(common.BlockSignersBinary)},
		{"type-2 XDCx tx to 0x91", "empty", dynamic(common.XDCXAddrBinary)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx := types.MustSignNewTx(key, signer, c.data)
			db, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			receipt, err := apply[c.route](db, tx)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Type != types.LegacyTxType {
				t.Fatalf("receipt type %d, want legacy (%d)", receipt.Type, types.LegacyTxType)
			}
			// The root canonical commits to: the same receipt, legacy-encoded.
			want := &types.Receipt{Type: types.LegacyTxType, Status: receipt.Status, CumulativeGasUsed: receipt.CumulativeGasUsed, Bloom: receipt.Bloom, Logs: receipt.Logs}
			have := types.DeriveSha(types.Receipts{receipt}, trie.NewStackTrie(nil))
			if root := types.DeriveSha(types.Receipts{want}, trie.NewStackTrie(nil)); have != root {
				t.Fatalf("receipts root %x, canonical %x", have, root)
			}
		})
	}
}
