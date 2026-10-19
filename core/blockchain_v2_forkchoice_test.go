// Copyright 2026 XDC Network

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/params"
)

// TestV2SiblingDoesNotReplaceHead: past the XDPoS V2 switch every block has
// difficulty 1, so canonical XDPoSChain fork choice (forkChoiceBeatsHead) keeps
// the head when a same-number sibling arrives and moves to the sibling's branch
// only when a higher block on it is imported.
func TestV2SiblingDoesNotReplaceHead(t *testing.T) {
	cfg := *params.AllEthashProtocolChanges
	cfg.XDPoS = &params.XDPoSConfig{Epoch: 900, V2: &params.V2{SwitchBlock: big.NewInt(0)}}
	cfg.Eip1559Block = big.NewInt(0)
	gspec := &Genesis{Config: &cfg, BaseFee: big.NewInt(params.InitialBaseFee)}
	engine := ethash.NewFaker()
	_, main, _ := GenerateChainWithGenesis(gspec, engine, 3, func(i int, b *BlockGen) {})
	_, fork, _ := GenerateChainWithGenesis(gspec, engine, 4, func(i int, b *BlockGen) {
		if i >= 2 {
			b.SetCoinbase(common.Address{0x01})
		}
	})
	if fork[2].Hash() == main[2].Hash() || fork[1].Hash() != main[1].Hash() {
		t.Fatal("bad test chains")
	}
	chain, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	if _, err := chain.InsertChain(main); err != nil {
		t.Fatal(err)
	}
	if _, err := chain.InsertChain(fork[2:3]); err != nil {
		t.Fatal(err)
	}
	if got := chain.CurrentBlock().Hash(); got != main[2].Hash() {
		t.Fatalf("same-number sibling replaced the head: head %x, want %x", got, main[2].Hash())
	}
	if !chain.HasBlockAndState(fork[2].Hash(), 3) {
		t.Fatal("sibling not stored as a side block")
	}
	if _, err := chain.InsertChain(fork[3:]); err != nil {
		t.Fatal(err)
	}
	if got := chain.CurrentBlock().Hash(); got != fork[3].Hash() {
		t.Fatalf("higher block on the sibling's branch did not become head: head %x, want %x", got, fork[3].Hash())
	}
}
