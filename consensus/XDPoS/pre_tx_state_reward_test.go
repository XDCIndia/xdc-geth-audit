// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// preTxChain satisfies consensus.ChainHeaderReader for this test.
type preTxChain struct{ cfg *params.ChainConfig }

func (c preTxChain) Config() *params.ChainConfig                 { return c.cfg }
func (c preTxChain) CurrentHeader() *types.Header                { return nil }
func (c preTxChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c preTxChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (c preTxChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// TestV1FinalizeAndAssembleUsesPreTxState verifies that FinalizeAndAssemble on
// a V1 checkpoint block passes the pre-transaction state to HookReward, not a
// copy taken after transactions have run.
//
// Setup:
//   - sentinel account has balance 100 in pre-tx state
//   - statedb (post-tx) has sentinel balance 200
//   - SetParentState is called with the pre-tx copy (balance 100)
//   - FinalizeAndAssemble is called; HookReward captures the parentState it receives
//
// Before fix: HookReward receives statedb.Copy() → balance 200 → test fails.
// After fix:  HookReward receives c.parentStateForReward → balance 100 → test passes.
func TestV1FinalizeAndAssembleUsesPreTxState(t *testing.T) {
	cfg := *testConfig
	cfg.Epoch = 10
	cfg.RewardCheckpoint = 10

	engine := New(&cfg, nil)

	chainCfg := &params.ChainConfig{
		ChainID: big.NewInt(50),
		XDPoS:   &cfg,
	}
	chain := preTxChain{chainCfg}

	sentinel := common.HexToAddress("0x1234000000000000000000000000000000000000")

	// Build post-tx statedb: sentinel has balance 200.
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	statedb.SetBalance(sentinel, uint256.NewInt(200), 0)

	// Build pre-tx state: sentinel has balance 100.
	preTx, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	preTx.SetBalance(sentinel, uint256.NewInt(100), 0)

	// Wire HookReward to capture parentState.
	var capturedBalance *big.Int
	engine.HookReward = func(_ consensus.ChainHeaderReader, _ *state.StateDB, parentState *state.StateDB, _ *types.Header) (map[string]interface{}, error) {
		b := parentState.GetBalance(sentinel)
		capturedBalance = b.ToBig()
		return nil, nil
	}

	// Provide the pre-tx state via SetParentState (mirrors what the miner does).
	engine.SetParentState(preTx)

	// Checkpoint header: number = epoch (so number%rCheckpoint == 0).
	header := &types.Header{
		Number:     new(big.Int).SetUint64(cfg.Epoch),
		GasLimit:   8000000,
		Extra:      make([]byte, extraVanity+extraSeal),
		Difficulty: big.NewInt(1),
		UncleHash:  types.EmptyUncleHash,
	}
	body := &types.Body{}

	_, err = engine.FinalizeAndAssemble(chain, header, statedb, body, nil)
	if err != nil {
		t.Fatalf("FinalizeAndAssemble: %v", err)
	}

	if capturedBalance == nil {
		t.Fatal("HookReward was not called")
	}
	// After fix: parentState must have pre-tx balance 100, not post-tx balance 200.
	if capturedBalance.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("parentState balance = %v, want 100 (pre-tx); got post-tx state", capturedBalance)
	}
}
