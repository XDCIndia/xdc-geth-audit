package XDPoS

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func balanceState(t *testing.T, addr common.Address, v uint64) *state.StateDB {
	t.Helper()
	s, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	s.SetBalance(addr, uint256.NewInt(v), 0)
	return s
}

// Two block builds that share one engine (import, miner, RPC) must each get
// their own pre-tx state in Finalize, whatever the order of the calls (#2097).
// Run with -race: the old single field was written and read without a lock.
func TestParentStateIsKeyedPerStateDB(t *testing.T) {
	cfg := *testConfig
	cfg.Epoch = 10
	cfg.RewardCheckpoint = 10
	engine := New(&cfg, nil)
	chain := preTxChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg}}
	sentinel := common.HexToAddress("0x1234000000000000000000000000000000000000")

	var mu sync.Mutex
	seen := map[*state.StateDB]uint64{}
	engine.HookReward = func(_ consensus.ChainHeaderReader, post *state.StateDB, pre *state.StateDB, _ *types.Header) (map[string]interface{}, error) {
		mu.Lock()
		seen[post] = pre.GetBalance(sentinel).Uint64()
		mu.Unlock()
		return nil, nil
	}

	const n = 8
	posts := make([]*state.StateDB, n)
	for i := range posts {
		posts[i] = balanceState(t, sentinel, 1000)
		engine.SetParentState(posts[i], balanceState(t, sentinel, uint64(i+1)))
	}
	var wg sync.WaitGroup
	for i := n - 1; i >= 0; i-- { // finalize in the reverse order of the writes
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			header := &types.Header{
				Number:     new(big.Int).SetUint64(cfg.Epoch),
				GasLimit:   8000000,
				Extra:      make([]byte, extraVanity+extraSeal),
				Difficulty: big.NewInt(1),
				UncleHash:  types.EmptyUncleHash,
			}
			if _, err := engine.FinalizeAndAssemble(chain, header, posts[i], &types.Body{}, nil); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for i, p := range posts {
		if seen[p] != uint64(i+1) {
			t.Errorf("block build %d got pre-tx balance %d, want %d", i, seen[p], i+1)
		}
	}

	// A Finalize that has no entry falls back and leaves nothing behind; entries
	// of builds that never finalize stay bounded.
	for i := 0; i < 3*maxParentStates; i++ {
		engine.SetParentState(balanceState(t, sentinel, 1), balanceState(t, sentinel, 1))
	}
	if got := len(engine.parentStates); got != maxParentStates {
		t.Fatalf("parentStates holds %d entries, want %d", got, maxParentStates)
	}
}
