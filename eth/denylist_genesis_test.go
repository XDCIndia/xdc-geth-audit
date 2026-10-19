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

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/params"
)

// TestGenesisDenylistBlockActivatesBlacklist: canonical XDPoSChain rejects
// transactions from or to denylisted addresses from the genesis denylistBlock
// (core/state_processor.go:170-180, params/config_forks.go:97). A private net
// whose genesis sets denylistBlock must activate the blacklist there, not at
// the 38,383,838 default for unregistered chain IDs.
func TestGenesisDenylistBlockActivatesBlacklist(t *testing.T) {
	old := common.BlackListHFNumber
	t.Cleanup(func() { common.BlackListHFNumber = old })

	stack, err := node.New(&node.Config{P2P: p2p.Config{ListenAddr: "127.0.0.1:0", NoDiscovery: true, MaxPeers: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()

	signer := common.HexToAddress("0x2022")
	extra := make([]byte, 32+common.AddressLength+65)
	copy(extra[32:], signer.Bytes())
	cfg := &params.ChainConfig{
		ChainID:       big.NewInt(2022),
		XDPoS:         &params.XDPoSConfig{Period: 2, Epoch: 900, Gap: 450, V2: &params.V2{SwitchBlock: big.NewInt(1800)}},
		DenylistBlock: big.NewInt(1200),
	}
	gspec := &core.Genesis{Config: cfg, ExtraData: extra, GasLimit: 420_000_000, Difficulty: big.NewInt(1)}
	if _, err := New(stack, &ethconfig.Config{Genesis: gspec, SyncMode: ethconfig.FullSync, Miner: ethconfig.Defaults.Miner}); err != nil {
		t.Fatal(err)
	}
	if common.BlackListHFNumber != 1200 {
		t.Fatalf("blacklist active from %d, want the genesis denylistBlock 1200", common.BlackListHFNumber)
	}
}
