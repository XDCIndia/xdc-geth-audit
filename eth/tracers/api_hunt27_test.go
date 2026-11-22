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
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/internal/ethapi/override"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// TestTraceCallBlockOverrideKeepsPrivacyLimit checks XDC-HUNT-27: a block number
// override that moves the execution rules into the privacy window must not turn
// on precompile 0x28 when the loaded state is past the window (as eth_call does).
func TestTraceCallBlockOverrideKeepsPrivacyLimit(t *testing.T) {
	t.Parallel()

	accounts := newAccounts(1)
	genesis := &core.Genesis{
		Config: params.TestChainConfig,
		Alloc:  types.GenesisAlloc{accounts[0].addr: {Balance: big.NewInt(params.Ether)}},
	}
	backend := newTestBackend(t, 5, genesis, nil)
	defer backend.teardown()
	// The chain holds empty blocks only. Give the RPC layer an XDC config, in
	// which 0x28 is active up to block 3 (TIPXDCXMinerDisableBlock).
	xdcConfig := *params.TestChainConfig
	xdcConfig.XDPoS = &params.XDPoSConfig{Period: 2}
	xdcConfig.TIPXDCXMinerDisableBlock = big.NewInt(3)
	backend.chainConfig = &xdcConfig
	api := NewAPI(backend)

	bullet := common.BytesToAddress([]byte{0x28})
	gas := hexutil.Uint64(1_000_000)
	args := ethapi.TransactionArgs{From: &accounts[0].addr, To: &bullet, Gas: &gas}
	call := func(number int64, atBlock rpc.BlockNumber) bool {
		t.Helper()
		var cfg *TraceCallConfig
		if number >= 0 {
			cfg = &TraceCallConfig{BlockOverrides: &override.BlockOverrides{Number: (*hexutil.Big)(big.NewInt(number))}}
		}
		blk := rpc.BlockNumberOrHashWithNumber(atBlock)
		res, err := api.TraceCall(context.Background(), args, &blk, cfg)
		if err != nil {
			t.Fatalf("TraceCall: %v", err)
		}
		var out struct {
			Failed bool `json:"failed"`
		}
		if err := json.Unmarshal(res.(json.RawMessage), &out); err != nil {
			t.Fatal(err)
		}
		// An empty call to 0x28 fails when the precompile is active.
		return out.Failed
	}
	if !call(-1, 2) {
		t.Fatal("0x28 is not active at block 2, test setup is wrong")
	}
	if call(-1, 5) {
		t.Fatal("0x28 is active at block 5, test setup is wrong")
	}
	// State at block 5 is past the window. The override number 1 is inside it.
	if call(1, 5) {
		t.Fatal("block override turned on 0x28 for state past the privacy window")
	}
	// State at block 2 is inside the window. The override number 5 is past it.
	if call(5, 2) {
		t.Fatal("block override kept 0x28 on for execution rules past the privacy window")
	}
}
