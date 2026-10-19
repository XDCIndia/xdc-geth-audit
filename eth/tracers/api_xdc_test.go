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

package tracers

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// TestTraceBlockXDCxEmptyTx traces a block that holds an XDCx tx (to 0x92
// inside the TIPXDCXReceiver window) and then a transfer with the same nonce
// from the same sender, as XDC blocks do: block processing routes the 0x92 tx
// to ApplyEmptyTransaction, which leaves the nonce alone. The tracer must
// replay the block the same way, or the transfer fails with "nonce too low"
// where XDPoSChain traces the block.
func TestTraceBlockXDCxEmptyTx(t *testing.T) {
	t.Parallel()

	accounts := newAccounts(2)
	config := *params.TestChainConfig
	config.TIPXDCXBlock = big.NewInt(1)
	config.TIPXDCXReceiverDisableBlock = big.NewInt(100)
	genesis := &core.Genesis{
		Config: &config,
		Alloc:  types.GenesisAlloc{accounts[0].addr: {Balance: big.NewInt(params.Ether)}},
	}
	signer := types.HomesteadSigner{}
	var empty, transfer common.Hash
	backend := newTestBackend(t, 1, genesis, func(i int, b *core.BlockGen) {
		tx0, _ := types.SignTx(types.NewTransaction(0, common.TradingStateAddrBinary, new(big.Int), 100000, b.BaseFee(), nil), signer, accounts[0].key)
		b.AddUncheckedTx(tx0)
		receipt := &types.Receipt{
			Type:   types.LegacyTxType,
			Status: types.ReceiptStatusSuccessful,
			TxHash: tx0.Hash(),
			Logs:   []*types.Log{{Address: common.TradingStateAddrBinary, BlockNumber: 1}},
		}
		receipt.Bloom = types.CreateBloom(receipt)
		b.AddUncheckedReceipt(receipt)
		tx1, _ := types.SignTx(types.NewTransaction(0, accounts[1].addr, big.NewInt(1000), params.TxGas, b.BaseFee(), nil), signer, accounts[0].key)
		b.AddTx(tx1)
		empty, transfer = tx0.Hash(), tx1.Hash()
	})
	defer backend.teardown()
	api := NewAPI(backend)

	results, err := api.TraceBlockByNumber(context.Background(), rpc.BlockNumber(1), nil)
	if err != nil {
		t.Fatalf("tracing the block failed: %v", err)
	}
	if len(results) != 2 || results[0].TxHash != empty || results[1].TxHash != transfer {
		t.Fatalf("unexpected results: %+v", results)
	}
	for i, wantGas := range []uint64{0, params.TxGas} {
		var res struct {
			Gas    uint64 `json:"gas"`
			Failed bool   `json:"failed"`
		}
		blob, _ := json.Marshal(results[i].Result)
		if err := json.Unmarshal(blob, &res); err != nil {
			t.Fatalf("tx %d: %v (%s)", i, err, blob)
		}
		if res.Gas != wantGas || res.Failed {
			t.Errorf("tx %d: gas %d failed %v, want gas %d, not failed", i, res.Gas, res.Failed, wantGas)
		}
	}
}
