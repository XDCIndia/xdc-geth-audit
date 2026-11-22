// Test helpers for the F-2026-20639 reward test, copied from
// api_epoch_rpc_test.go, which is not part of the audit tree.
package XDPoS

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// stubChainHeaderReader is a minimal ChainHeaderReader + ChainReader for tests.
type stubChain struct {
	currentHeader *types.Header
	headers       map[uint64]*types.Header
}

func (s *stubChain) Config() *params.ChainConfig  { return &params.ChainConfig{} }
func (s *stubChain) CurrentHeader() *types.Header { return s.currentHeader }
func (s *stubChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	return s.headers[number]
}
func (s *stubChain) GetHeaderByNumber(number uint64) *types.Header { return s.headers[number] }
func (s *stubChain) GetHeaderByHash(hash common.Hash) *types.Header {
	for _, h := range s.headers {
		if h.Hash() == hash {
			return h
		}
	}
	return nil
}
func (s *stubChain) GetBlock(hash common.Hash, number uint64) *types.Block { return nil }

// stubV2Engine is a minimal EngineV2Iface that records which methods were called.
type stubV2Engine struct {
	roundByHeader         map[common.Hash]types.Round
	missingRoundsCalled   bool
	epochSwitchInfoCalled bool
	blockByEpochCalled    bool
	currentSwitchCalled   bool
	missingRoundsResult   *utils.PublicApiMissedRoundsMetadata
	epochSwitchResult     []*types.EpochSwitchInfo
	blockByEpochResult    *types.BlockInfo
	currentSwitchResult   uint64
}

func (s *stubV2Engine) GetRoundNumber(header *types.Header) (types.Round, error) {
	if r, ok := s.roundByHeader[header.Hash()]; ok {
		return r, nil
	}
	return 0, errors.New("no round")
}
func (s *stubV2Engine) CalculateMissingRounds(chain consensus.ChainReader, header *types.Header) (*utils.PublicApiMissedRoundsMetadata, error) {
	s.missingRoundsCalled = true
	return s.missingRoundsResult, nil
}
func (s *stubV2Engine) GetEpochSwitchInfoBetween(chain consensus.ChainReader, begin, end *types.Header) ([]*types.EpochSwitchInfo, error) {
	s.epochSwitchInfoCalled = true
	return s.epochSwitchResult, nil
}
func (s *stubV2Engine) GetBlockByEpochNumber(chain consensus.ChainReader, epochNumber uint64) (*types.BlockInfo, error) {
	s.blockByEpochCalled = true
	if s.blockByEpochResult == nil {
		return nil, errors.New("not found")
	}
	return s.blockByEpochResult, nil
}
func (s *stubV2Engine) GetCurrentEpochSwitchBlock(chain consensus.ChainReader, blockNumber *big.Int) (uint64, uint64, error) {
	s.currentSwitchCalled = true
	return s.currentSwitchResult, 99, nil
}

// Unneeded interface stubs.
func (s *stubV2Engine) VerifyHeader(consensus.ChainReader, *types.Header, bool) error { return nil }
func (s *stubV2Engine) VerifyHeaderWithParents(consensus.ChainReader, *types.Header, []*types.Header, bool) error {
	return nil
}
func (s *stubV2Engine) UpdateMasternodesFromHeader(consensus.ChainReader, *types.Header, *state.StateDB) error {
	return nil
}
func (s *stubV2Engine) UpdateMasternodes(consensus.ChainReader, *types.Header, []common.Address) error {
	return nil
}
func (s *stubV2Engine) UpdateMasternodesWithPenalties(consensus.ChainReader, *types.Header, []common.Address, []common.Address) error {
	return nil
}
func (s *stubV2Engine) Author(*types.Header) (common.Address, error)      { return common.Address{}, nil }
func (s *stubV2Engine) IsEpochSwitch(*types.Header) (bool, uint64, error) { return false, 0, nil }
func (s *stubV2Engine) GetMasternodesByHash(consensus.ChainReader, common.Hash) []common.Address {
	return nil
}
func (s *stubV2Engine) GetPreviousPenaltyByHash(consensus.ChainReader, common.Hash, int) ([]common.Address, error) {
	return nil, nil
}
func (s *stubV2Engine) GetMasternodesFromEpochSwitchHeader(consensus.ChainReader, *types.Header) []common.Address {
	return nil
}
func (s *stubV2Engine) GetStandbynodes(consensus.ChainReader, *types.Header) ([]common.Address, error) {
	return nil, nil
}
func (s *stubV2Engine) GetMasternodesV2(consensus.ChainReader, *types.Header) []common.Address {
	return nil
}
func (s *stubV2Engine) Prepare(consensus.ChainReader, *types.Header) error { return nil }
func (s *stubV2Engine) Finalize(consensus.ChainReader, *types.Header, *state.StateDB, *state.StateDB, []*types.Transaction, []*types.Header, []*types.Receipt) (*types.Block, error) {
	return nil, nil
}
func (s *stubV2Engine) Seal(consensus.ChainReader, *types.Block, <-chan struct{}) (*types.Block, error) {
	return nil, nil
}
func (s *stubV2Engine) CalcDifficulty(consensus.ChainReader, uint64, *types.Header) *big.Int {
	return big.NewInt(1)
}
func (s *stubV2Engine) SignHash(*types.Header) common.Hash           { return common.Hash{} }
func (s *stubV2Engine) Authorize(common.Address, engine_v2.SignerFn) {}
func (s *stubV2Engine) SetHookReward(func(consensus.ChainReader, *state.StateDB, *state.StateDB, *types.Header) (map[string]interface{}, error)) {
}
func (s *stubV2Engine) SetHookPenalty(func(consensus.ChainReader, *big.Int, common.Hash, []common.Address) ([]common.Address, error)) {
}
func (s *stubV2Engine) ProposedBlockHandler(consensus.ChainReader, *types.Header) error { return nil }
func (s *stubV2Engine) ReinitBFT(consensus.ChainReader, *types.Header) error            { return nil }

// makeTestXDPoSWithV2 creates a minimal XDPoS engine with V2 config switch at block 900.
func makeTestXDPoSWithV2() *XDPoS {
	cfg := &params.XDPoSConfig{
		Epoch: 900,
		V2: &params.V2{
			SwitchBlock: big.NewInt(900),
			SwitchEpoch: 1,
		},
	}
	db := rawdb.NewMemoryDatabase()
	return New(cfg, db)
}

// makeTestAPI creates an API wrapping the engine with the given chain.
func makeTestAPI(engine *XDPoS, chain *stubChain) *API {
	return &API{chain: chain, xdpos: engine}
}

func (s *stubV2Engine) GetLatestCommittedBlockInfo() *types.BlockInfo { return nil }
