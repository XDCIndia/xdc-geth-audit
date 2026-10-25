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
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// TestXdcMintEnvAppliesPreExecution: the env a block is minted in carries the
// Prague pre-execution state changes InsertChain's Process applies (Multicall3
// install, EIP-2935 parent hash), as canonical miner/worker.go:889-893 does.
// Without them every block the modern sealer minted from Prague on failed its
// own import with "invalid merkle root" (net2022, blocks 2,251+).
func TestXdcMintEnvAppliesPreExecution(t *testing.T) {
	cfg := *params.AllEthashProtocolChanges
	cfg.ChainID = big.NewInt(2022)
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900}
	cfg.ShanghaiTime, cfg.CancunTime, cfg.PragueTime, cfg.OsakaTime = nil, nil, nil, nil
	cfg.BlobScheduleConfig = nil
	cfg.PragueBlock = big.NewInt(1)

	gspec := &core.Genesis{Config: &cfg, GasLimit: 30_000_000}
	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), &core.BlockChainConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)

	parent := bc.Genesis().Header()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(1),
		GasLimit:   parent.GasLimit,
		Time:       parent.Time + 2,
		Difficulty: big.NewInt(1),
	}
	w := &XdcWorker{coinbase: common.HexToAddress("0x2022"), miner: &Miner{chain: bc, chainConfig: bc.Config()}}
	env, err := w.makeMintEnv(parent, header)
	if err != nil {
		t.Fatal(err)
	}
	defer env.discard()

	if code := env.state.GetCode(params.Multicall3Address); !bytes.Equal(code, params.Multicall3RuntimeCode) {
		t.Errorf("Multicall3 not installed in the mint env (code %d bytes)", len(code))
	}
	slot := common.BigToHash(new(big.Int).SetUint64(parent.Number.Uint64() % params.HistoryServeWindow))
	if got := env.state.GetState(params.HistoryStorageAddress, slot); got != parent.Hash() {
		t.Errorf("EIP-2935 parent hash slot = %x, want %x", got, parent.Hash())
	}
}

// TestXdcMintEnvAppliesTIPSigning: a block minted in the mint env at the
// TIPSigning block carries the 0x89 BlockSigners wipe Process applies on import
// (canonical miner/worker.go:889), so the node accepts the block it minted.
// Without it the minted state root keeps 0x89 and import rejects it (#1595).
func TestXdcMintEnvAppliesTIPSigning(t *testing.T) {
	cfg := *params.AllEthashProtocolChanges
	cfg.ChainID = big.NewInt(2022)
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900}
	cfg.ShanghaiTime, cfg.CancunTime, cfg.PragueTime, cfg.OsakaTime = nil, nil, nil, nil
	cfg.BlobScheduleConfig = nil
	cfg.TIPSigningBlock = big.NewInt(1)

	gspec := &core.Genesis{Config: &cfg, GasLimit: 30_000_000, Alloc: types.GenesisAlloc{
		common.BlockSignersBinary: {Code: []byte{0x60, 0x00}, Storage: map[common.Hash]common.Hash{{1}: {1}}, Balance: big.NewInt(0)},
	}}
	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFullFaker(), &core.BlockChainConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)

	w := &XdcWorker{coinbase: common.HexToAddress("0x2022"), miner: &Miner{chain: bc, chainConfig: bc.Config()}}
	parent := bc.Genesis().Header()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(1),
		GasLimit:   parent.GasLimit,
		Time:       parent.Time + 2,
		Difficulty: big.NewInt(1),
		Coinbase:   w.coinbase,
	}
	if eip1559.FeeMarketActive(bc.Config(), header.Number) {
		header.BaseFee = eip1559.CalcBaseFee(bc.Config(), parent)
	}
	env, err := w.makeMintEnv(parent, header)
	if err != nil {
		t.Fatal(err)
	}
	defer env.discard()
	bc.Engine().Finalize(bc, header, env.state, &types.Body{})
	header.Root = env.state.IntermediateRoot(bc.Config().Rules(header.Number, false, header.Time))
	block := types.NewBlock(header, &types.Body{}, nil, trie.NewStackTrie(nil))
	if _, err := bc.InsertChain(types.Blocks{block}); err != nil {
		t.Fatalf("import rejects the minted TIPSigning block: %v", err)
	}
	st, err := bc.State()
	if err != nil {
		t.Fatal(err)
	}
	if st.Exist(common.BlockSignersBinary) {
		t.Error("BlockSigners (0x89) survived the TIPSigning block")
	}
}
