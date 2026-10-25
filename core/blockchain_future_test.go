// Copyright 2026 XDC Network

package core

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/params"
)

// TestFutureBlockNotMarkedBad: a block a little ahead of the local clock is
// rejected for now but not recorded as a bad block (canonical XDPoSChain
// queues blocks up to 30 s ahead); a block far ahead still is.
func TestFutureBlockNotMarkedBad(t *testing.T) {
	for _, tc := range []struct {
		ahead int64 // seconds past the default 10 s spacing
		bad   bool
	}{{10, false}, {60, true}} {
		gspec := &Genesis{
			Config:    params.AllEthashProtocolChanges,
			BaseFee:   big.NewInt(params.InitialBaseFee),
			Timestamp: uint64(time.Now().Unix()),
		}
		_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 1, func(i int, b *BlockGen) { b.OffsetTime(tc.ahead) })
		db := rawdb.NewMemoryDatabase()
		chain, err := NewBlockChain(db, gspec, ethash.NewFaker(), DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chain.InsertChain(blocks); !errors.Is(err, consensus.ErrFutureBlock) {
			t.Fatalf("ahead %d: want ErrFutureBlock, got %v", tc.ahead, err)
		}
		if got := rawdb.ReadBadBlock(db, blocks[0].Hash()) != nil; got != tc.bad {
			t.Errorf("ahead %d: bad block recorded = %v, want %v", tc.ahead, got, tc.bad)
		}
		chain.Stop()
	}
}
