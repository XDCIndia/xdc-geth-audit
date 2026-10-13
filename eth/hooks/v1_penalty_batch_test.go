// Copyright 2026 XDC Network

package hooks

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

// noHeaders is the chain DB before a verify batch is written: it knows no headers.
type noHeaders struct{ cfg *params.ChainConfig }

func (c noHeaders) Config() *params.ChainConfig                 { return c.cfg }
func (c noHeaders) CurrentHeader() *types.Header                { return nil }
func (c noHeaders) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c noHeaders) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (c noHeaders) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// v1PenaltySetup returns a blockchain whose head state holds one account (so its
// root is a real trie node), the V1-hooked engine, and a batch of headers
// epoch..2*epoch-1 whose first header is a checkpoint listing masternodes a, b.
func v1PenaltySetup(t *testing.T) (ethdb.Database, *core.BlockChain, *XDPoS.XDPoS, *params.ChainConfig, []*types.Header, []common.Address) {
	t.Helper()
	db := rawdb.NewMemoryDatabase()
	gspec := &core.Genesis{
		Config: params.TestChainConfig,
		Alloc:  types.GenesisAlloc{common.HexToAddress("0x01"): {Balance: big.NewInt(1)}},
	}
	cfg := core.DefaultConfig()
	cfg.TrieCleanLimit = 0
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFaker(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)

	xcfg := &params.XDPoSConfig{Epoch: 30, Gap: 5, Period: 2}
	chainConfig := *params.TestChainConfig
	chainConfig.XDPoS = xcfg
	engine := XDPoS.New(xcfg, rawdb.NewMemoryDatabase())
	AttachConsensusV1Hooks(engine, bc, &chainConfig)

	masternodes := []common.Address{common.HexToAddress("0xa1"), common.HexToAddress("0xb2")}
	extra := make([]byte, 32)
	for _, m := range masternodes {
		extra = append(extra, m[:]...)
	}
	extra = append(extra, make([]byte, 65)...)
	var parents []*types.Header
	parent := common.Hash{}
	for n := xcfg.Epoch; n < 2*xcfg.Epoch; n++ {
		h := &types.Header{Number: new(big.Int).SetUint64(n), ParentHash: parent, Difficulty: big.NewInt(1), Extra: make([]byte, 32+65)}
		if n == xcfg.Epoch {
			h.Extra = extra
		}
		parents = append(parents, h)
		parent = h.Hash()
	}
	return db, bc, engine, &chainConfig, parents, masternodes
}

// TestV1HookPenaltyBatchParents: a batch that crosses a checkpoint verifies it
// before its earlier blocks are written. HookPenalty must find them among the
// batch's parents, as canonical's batch chain reader serves them; replay-parity
// failed with "v1 HookPenalty: no header at 484170".
func TestV1HookPenaltyBatchParents(t *testing.T) {
	_, _, engine, chainConfig, parents, masternodes := v1PenaltySetup(t)
	chain := noHeaders{chainConfig}

	// Nobody signed in the 0x89 state, so both masternodes are penalized.
	pen, err := engine.HookPenalty(chain, 60, parents)
	if err != nil {
		t.Fatalf("checkpoint batch in parents: %v", err)
	}
	if len(pen) != 2 || pen[0] != masternodes[0] || pen[1] != masternodes[1] {
		t.Fatalf("penalties = %v, want %v", pen, masternodes)
	}
	// Without the batch the header is genuinely missing: still an error, and
	// not one that skips the compare.
	if _, err := engine.HookPenalty(chain, 60, nil); err == nil || errors.Is(err, XDPoS.ErrPenaltyDataUnavailable) {
		t.Fatalf("no batch: got %v, want a missing-header error", err)
	}
}

// TestV1HookPenaltyStateUnavailable: canonical reads the signer mapping at the
// canonical head. After a rewind a non-archive node can lack that state; the
// hook must report ErrPenaltyDataUnavailable so verification skips the compare
// instead of wedging.
func TestV1HookPenaltyStateUnavailable(t *testing.T) {
	db, bc, engine, chainConfig, parents, _ := v1PenaltySetup(t)
	rawdb.DeleteLegacyTrieNode(db, bc.CurrentBlock().Root)
	if _, err := bc.State(); err == nil {
		t.Fatal("test setup: head state still available")
	}
	_, err := engine.HookPenalty(noHeaders{chainConfig}, 60, parents)
	if !errors.Is(err, XDPoS.ErrPenaltyDataUnavailable) {
		t.Fatalf("got %v, want ErrPenaltyDataUnavailable", err)
	}
}
