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

package ethapi

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

type masternodesBackend struct {
	Backend
	engine consensus.Engine
}

func (b *masternodesBackend) BlockByNumber(ctx context.Context, n rpc.BlockNumber) (*types.Block, error) {
	return types.NewBlockWithHeader(&types.Header{Number: big.NewInt(int64(n))}), nil
}
func (b *masternodesBackend) Engine() consensus.Engine     { return b.engine }
func (b *masternodesBackend) BlockChain() *core.BlockChain { return nil }

// TestGetMasternodesCallableOverRPC checks XDC-HUNT-42: eth_getMasternodes took a
// *types.Block, which a JSON argument cannot fill, so every call failed. It now
// takes a block number.
func TestGetMasternodesCallableOverRPC(t *testing.T) {
	cfg := &params.ChainConfig{XDPoS: &params.XDPoSConfig{Epoch: 900}}
	engine := XDPoS.New(cfg.XDPoS, rawdb.NewMemoryDatabase())

	server := rpc.NewServer()
	defer server.Stop()
	if err := server.RegisterName("eth", &BlockChainAPI{b: &masternodesBackend{engine: engine}}); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	defer client.Close()

	var got []common.Address
	if err := client.Call(&got, "eth_getMasternodes", "0x384"); err != nil {
		t.Fatalf("eth_getMasternodes: %v", err)
	}
}
