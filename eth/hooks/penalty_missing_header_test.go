// Copyright 2026 XDC Network

package hooks

import (
	"math/big"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// missingFakeV2 implements XDPoS.EngineV2Iface.  IsEpochSwitch returns false
// so the epoch walk in HookPenalty runs at least one iteration and can hit
// the nil-header path.
type missingFakeV2 struct {
	capturedFn func(consensus.ChainReader, *big.Int, common.Hash, []common.Address) ([]common.Address, error)
}

var _ XDPoS.EngineV2Iface = (*missingFakeV2)(nil)

func (f *missingFakeV2) VerifyHeader(_ consensus.ChainReader, _ *types.Header, _ bool) error {
	return nil
}
func (f *missingFakeV2) VerifyHeaderWithParents(_ consensus.ChainReader, _ *types.Header, _ []*types.Header, _ bool) error {
	return nil
}
func (f *missingFakeV2) GetRoundNumber(_ *types.Header) (types.Round, error) { return 1, nil }
func (f *missingFakeV2) UpdateMasternodesFromHeader(_ consensus.ChainReader, _ *types.Header, _ *state.StateDB) error {
	return nil
}
func (f *missingFakeV2) UpdateMasternodes(_ consensus.ChainReader, _ *types.Header, _ []common.Address) error {
	return nil
}
func (f *missingFakeV2) UpdateMasternodesWithPenalties(_ consensus.ChainReader, _ *types.Header, _, _ []common.Address) error {
	return nil
}
func (f *missingFakeV2) Author(_ *types.Header) (common.Address, error) {
	return common.Address{}, nil
}

// IsEpochSwitch returns false so the epoch walk does not stop immediately.
func (f *missingFakeV2) IsEpochSwitch(_ *types.Header) (bool, uint64, error) { return false, 0, nil }
func (f *missingFakeV2) GetMasternodesByHash(_ consensus.ChainReader, _ common.Hash) []common.Address {
	return nil
}
func (f *missingFakeV2) GetPreviousPenaltyByHash(_ consensus.ChainReader, _ common.Hash, _ int) []common.Address {
	return nil
}
func (f *missingFakeV2) GetMasternodesFromEpochSwitchHeader(_ consensus.ChainReader, _ *types.Header) []common.Address {
	return nil
}
func (f *missingFakeV2) GetStandbynodes(_ consensus.ChainReader, _ *types.Header) []common.Address {
	return nil
}
func (f *missingFakeV2) GetMasternodesV2(_ consensus.ChainReader, _ *types.Header) []common.Address {
	return nil
}
func (f *missingFakeV2) Prepare(_ consensus.ChainReader, _ *types.Header) error { return nil }
func (f *missingFakeV2) Finalize(_ consensus.ChainReader, _ *types.Header, _ *state.StateDB, _ *state.StateDB, _ []*types.Transaction, _ []*types.Header, _ []*types.Receipt) (*types.Block, error) {
	return nil, nil
}
func (f *missingFakeV2) Seal(_ consensus.ChainReader, _ *types.Block, _ <-chan struct{}) (*types.Block, error) {
	return nil, nil
}
func (f *missingFakeV2) CalcDifficulty(_ consensus.ChainReader, _ uint64, _ *types.Header) *big.Int {
	return big.NewInt(1)
}
func (f *missingFakeV2) SignHash(_ *types.Header) common.Hash             { return common.Hash{} }
func (f *missingFakeV2) Authorize(_ common.Address, _ engine_v2.SignerFn) {}
func (f *missingFakeV2) SetHookReward(_ func(consensus.ChainReader, *state.StateDB, *state.StateDB, *types.Header) (map[string]interface{}, error)) {
}
func (f *missingFakeV2) SetHookPenalty(fn func(consensus.ChainReader, *big.Int, common.Hash, []common.Address) ([]common.Address, error)) {
	f.capturedFn = fn
}
func (f *missingFakeV2) ProposedBlockHandler(_ consensus.ChainReader, _ *types.Header) error {
	return nil
}
func (f *missingFakeV2) ReinitBFT(_ consensus.ChainReader, _ *types.Header) error { return nil }
func (f *missingFakeV2) GetCurrentEpochSwitchBlock(_ consensus.ChainReader, _ *big.Int) (uint64, uint64, error) {
	return 0, 0, nil
}
func (f *missingFakeV2) GetEpochSwitchInfoBetween(_ consensus.ChainReader, _, _ *types.Header) ([]*types.EpochSwitchInfo, error) {
	return nil, nil
}
func (f *missingFakeV2) GetBlockByEpochNumber(_ consensus.ChainReader, _ uint64) (*types.BlockInfo, error) {
	return nil, nil
}
func (f *missingFakeV2) CalculateMissingRounds(_ consensus.ChainReader, _ *types.Header) (*utils.PublicApiMissedRoundsMetadata, error) {
	return nil, nil
}
func (f *missingFakeV2) GetLatestCommittedBlockInfo() *types.BlockInfo { return nil }

// missingHeaderChain returns a valid parent header for the FIRST GetHeader call
// (round-resolution loop) and nil for subsequent calls (epoch walk).
type missingHeaderChain struct {
	parent *types.Header
	cfg    *params.ChainConfig
	callN  atomic.Int32
}

var _ consensus.ChainReader = (*missingHeaderChain)(nil)

func (c *missingHeaderChain) Config() *params.ChainConfig  { return c.cfg }
func (c *missingHeaderChain) CurrentHeader() *types.Header { return c.parent }
func (c *missingHeaderChain) GetHeader(_ common.Hash, _ uint64) *types.Header {
	n := c.callN.Add(1)
	if n == 1 {
		// Round-resolution loop: return a valid header so we get past it.
		return c.parent
	}
	// Epoch walk: nil triggers the error path under test.
	return nil
}
func (c *missingHeaderChain) GetHeaderByNumber(_ uint64) *types.Header      { return nil }
func (c *missingHeaderChain) GetHeaderByHash(_ common.Hash) *types.Header   { return nil }
func (c *missingHeaderChain) GetBlock(_ common.Hash, _ uint64) *types.Block { return nil }

// TestHookPenaltyV2MissingParentHeaderReturnsError verifies that when a
// parent header is unavailable during the V2 epoch walk, HookPenalty returns
// an error rather than silently building a partial penalty list.
//
// Before the fix the code hit a silent break; the penalty closure returned
// (partialList, nil). After the fix it returns ([], error).
func TestHookPenaltyV2MissingParentHeaderReturnsError(t *testing.T) {
	chainConfig := &params.ChainConfig{
		ChainID: big.NewInt(50),
		XDPoS: &params.XDPoSConfig{
			Epoch: 10,
			V2:    &params.V2{SwitchBlock: big.NewInt(0)},
		},
	}

	parent := &types.Header{
		Number:     big.NewInt(9),
		Difficulty: big.NewInt(1),
	}

	chain := &missingHeaderChain{
		parent: parent,
		cfg:    chainConfig,
	}

	fakeV2 := &missingFakeV2{}
	adaptor := XDPoS.New(chainConfig.XDPoS, rawdb.NewMemoryDatabase())
	adaptor.SetEngineV2(fakeV2)
	AttachConsensusV2Hooks(adaptor, nil, chainConfig)

	if fakeV2.capturedFn == nil {
		t.Fatal("AttachConsensusV2Hooks did not wire the penalty hook")
	}

	currentHash := common.Hash{0x01}
	_, err := fakeV2.capturedFn(chain, big.NewInt(10), currentHash, []common.Address{{0x02}})
	if err == nil {
		t.Fatal("expected an error when parent header is missing in the epoch walk, got nil")
	}
}
