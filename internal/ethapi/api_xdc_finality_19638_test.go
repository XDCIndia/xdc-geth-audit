// Copyright (c) 2026 XDC Network
// F-2026-19638: block finality RPCs must report a QC-committed V2 block as
// final (100), not as signer count / masternode count (about 1 %).
//
// Run: go test ./internal/ethapi/ -run TestFindFinalityOfBlockCommittedV2 -count=1

package ethapi

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// finalityBackend serves the finalized header and canonical headers by number.
type finalityBackend struct {
	Backend
	engine    consensus.Engine
	finalized *types.Header
	canonical map[uint64]*types.Header
}

func (b *finalityBackend) Engine() consensus.Engine { return b.engine }
func (b *finalityBackend) HeaderByNumber(_ context.Context, n rpc.BlockNumber) (*types.Header, error) {
	if n == rpc.FinalizedBlockNumber {
		return b.finalized, nil
	}
	return b.canonical[uint64(n)], nil
}

func TestFindFinalityOfBlockCommittedV2(t *testing.T) {
	engine := XDPoS.New(&params.XDPoSConfig{Period: 2, Epoch: 900, V2: &params.V2{SwitchBlock: big.NewInt(900)}}, rawdb.NewMemoryDatabase())
	mk := func(n int64, salt byte) *types.Block {
		return types.NewBlockWithHeader(&types.Header{Number: big.NewInt(n), Difficulty: big.NewInt(1), Extra: []byte{salt}})
	}
	committed, fork, recent := mk(1000, 1), mk(1000, 2), mk(1300, 1)
	be := &finalityBackend{
		engine:    engine,
		finalized: &types.Header{Number: big.NewInt(1200)},
		canonical: map[uint64]*types.Header{1000: committed.Header(), 1300: recent.Header()},
	}
	api := &BlockChainAPI{b: be}
	masternodes := make([]common.Address, 108)

	if f, err := api.findFinalityOfBlock(context.Background(), committed, masternodes); err != nil || f != 100 {
		t.Errorf("committed V2 block: finality %d (err %v), want 100", f, err)
	}
	if api.isAtOrBelowFinalized(context.Background(), fork) {
		t.Error("block on a fork at a committed height reported as committed")
	}
	if api.isAtOrBelowFinalized(context.Background(), recent) {
		t.Error("block above the finalized block reported as committed")
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	proposer := crypto.PubkeyToAddress(key.PublicKey)
	cfg := &params.ChainConfig{ChainID: big.NewInt(551), XDPoS: &params.XDPoSConfig{
		Epoch: 900, Gap: 450,
		V2: &params.V2{SwitchBlock: big.NewInt(0), CurrentConfig: &params.V2Config{MinePeriod: 2}},
	}}
	v2 := engine_v2.New(cfg, rawdb.NewMemoryDatabase(), nil, nil)
	xdpos := XDPoS.New(cfg.XDPoS, rawdb.NewMemoryDatabase())
	xdpos.SetEngineV2(v2)
	v2Header := &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(1), Coinbase: proposer, UncleHash: types.EmptyUncleHash}
	sig, err := crypto.Sign(v2.SigningHash(v2Header).Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	v2Header.Validator = sig
	signers, err := (&BlockChainAPI{b: &finalityBackend{engine: xdpos}}).rpcOutputBlockSigners(types.NewBlockWithHeader(v2Header), context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 1 || signers[0] != proposer {
		t.Fatalf("V2 proposer = %v, want %s", signers, proposer.Hex())
	}

	v1 := mk(500, 3)
	be.canonical[500] = v1.Header()
	if f, err := api.findFinalityOfBlock(context.Background(), v1, masternodes); err != nil || f != 100 {
		t.Errorf("V1 block below finalized: finality %d (err %v), want 100", f, err)
	}
}
