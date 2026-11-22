package hooks

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// With the deferred list full of entries that were never rechecked (header-only
// sync), a new checkpoint must still get an entry, so body validation replays
// its check. The oldest entry is the one that goes (#2084).
func TestV1DeferredRecheckKeepsNewestWhenFull(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	bc, err := core.NewBlockChain(db, &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()
	chainConfig := *params.AllEthashProtocolChanges
	chainConfig.ChainID = big.NewInt(50)
	chainConfig.XDPoS = &params.XDPoSConfig{Epoch: 900, Gap: 450, Period: 2}
	adaptor := XDPoS.New(chainConfig.XDPoS, db)
	AttachConsensusV1Hooks(adaptor, bc, &chainConfig)

	// The parent is never written, so every checkpoint is deferred.
	const n = 40
	headers := make([]*types.Header, n)
	for i := range headers {
		headers[i] = &types.Header{
			Number:     big.NewInt(int64(900 * (i + 1))),
			ParentHash: common.Hash{0xff, byte(i)},
		}
		if err := adaptor.HookVerifyMNs(headers[i], []common.Address{{0x01}}); err != nil {
			t.Fatal(err)
		}
	}
	// A recheck that finds an entry runs the hook again, which counts a skip
	// (the parent is still missing); one that finds none does nothing.
	replayed := func(h *types.Header) bool {
		before := hookVerifyMNsMissingStateSkips.Snapshot().Count()
		if err := adaptor.RecheckV1Validators(h); err != nil {
			t.Fatal(err)
		}
		return hookVerifyMNsMissingStateSkips.Snapshot().Count() > before
	}
	if !replayed(headers[n-1]) {
		t.Fatal("newest checkpoint has no deferred entry: its check would be skipped")
	}
	if !replayed(headers[n-2]) {
		t.Fatal("second newest checkpoint has no deferred entry")
	}
	if replayed(headers[0]) {
		t.Fatal("oldest checkpoint still has an entry: the list is not bounded")
	}
}
