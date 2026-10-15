// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// emptyChain knows no headers: the batch under verification is not written yet.
type emptyChain struct{ cfg *params.ChainConfig }

func (c emptyChain) Config() *params.ChainConfig                 { return c.cfg }
func (c emptyChain) CurrentHeader() *types.Header                { return nil }
func (c emptyChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c emptyChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (c emptyChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// TestSignersFromContractWalksBatch: when a checkpoint's signer list is
// re-read from the contract, the walk back to its gap block must find the
// blocks among the batch's parents. Otherwise every checkpoint in a batch fails
// once with "no block at N to get the signers from" (replay-parity, #1430).
func TestSignersFromContractWalksBatch(t *testing.T) {
	cfg := *testConfig
	cfg.Gap = 3
	engine := New(&cfg, rawdb.NewMemoryDatabase())
	want := []common.Address{common.HexToAddress("0x01"), common.HexToAddress("0x02")}
	var got *types.Header
	engine.HookGetSignersFromContract = func(gap *types.Header) ([]common.Address, error) {
		got = gap
		return want, nil
	}
	// parents: blocks cp-Gap .. cp-1, linked by ParentHash; cp itself is the header.
	cpNo := cfg.Epoch
	var parents []*types.Header
	parent := common.Hash{}
	for n := cpNo - cfg.Gap; n < cpNo; n++ {
		h := &types.Header{Number: new(big.Int).SetUint64(n), ParentHash: parent, Difficulty: big.NewInt(1)}
		parents = append(parents, h)
		parent = h.Hash()
	}
	cp := &types.Header{Number: new(big.Int).SetUint64(cpNo), ParentHash: parent, Difficulty: big.NewInt(1)}

	chain := emptyChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}}
	signers, err := engine.getSignersFromContract(chain, cp, parents)
	if err != nil {
		t.Fatalf("walk through the batch: %v", err)
	}
	if got == nil || got.Number.Uint64() != cpNo-cfg.Gap {
		t.Fatalf("contract read at %v, want the gap block %d", got.Number, cpNo-cfg.Gap)
	}
	if len(signers) != len(want) || signers[0] != want[0] || signers[1] != want[1] {
		t.Fatalf("signers = %v, want %v", signers, want)
	}
	// Without the batch it still fails as before, on the first missing block.
	if _, err := engine.getSignersFromContract(chain, cp, nil); err == nil {
		t.Fatal("no batch and no stored blocks: want an error")
	}
}
