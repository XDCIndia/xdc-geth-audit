// Tests for issue #1482: epoch RPCs use block number arithmetic instead of V2 round.
// Every test here FAILS on origin/main and PASSES with the fix.
package XDPoS

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
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
func (s *stubV2Engine) GetPreviousPenaltyByHash(consensus.ChainReader, common.Hash, int) []common.Address {
	return nil
}
func (s *stubV2Engine) GetMasternodesFromEpochSwitchHeader(consensus.ChainReader, *types.Header) []common.Address {
	return nil
}
func (s *stubV2Engine) GetStandbynodes(consensus.ChainReader, *types.Header) []common.Address {
	return nil
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

// TestGetMissedRoundsDispatchesToV2 verifies that GetMissedRoundsInEpochByBlockNum
// dispatches to the V2 engine for V2 blocks instead of using block-number arithmetic.
// FAILS on main (no dispatch), PASSES with fix.
func TestGetMissedRoundsDispatchesToV2(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{
		missingRoundsResult: &utils.PublicApiMissedRoundsMetadata{
			EpochRound:       types.Round(1800), // round-based, not block-number-based
			EpochBlockNumber: big.NewInt(1820),
		},
	}
	engine.EngineV2 = stub

	// V2 header (number > SwitchBlock=900)
	v2Header := &types.Header{Number: big.NewInt(1850)}
	chain := &stubChain{
		currentHeader: v2Header,
		headers:       map[uint64]*types.Header{1850: v2Header},
	}
	api := makeTestAPI(engine, chain)

	num := rpc.BlockNumber(1850)
	result, err := api.GetMissedRoundsInEpochByBlockNum(&num)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.missingRoundsCalled {
		t.Fatal("expected V2 engine CalculateMissingRounds to be called, but it was not (issue #1482: still using block-number arithmetic)")
	}
	if result.EpochRound != 1800 {
		t.Fatalf("expected EpochRound=1800 from V2 engine, got %d", result.EpochRound)
	}
}

// TestGetMissedRoundsV1FallbackUnchanged verifies that V1 blocks still use block-number arithmetic.
func TestGetMissedRoundsV1FallbackUnchanged(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{}
	engine.EngineV2 = stub

	// V1 header (number <= SwitchBlock=900)
	v1Header := &types.Header{Number: big.NewInt(450)}
	chain := &stubChain{
		currentHeader: v1Header,
		headers:       map[uint64]*types.Header{450: v1Header},
	}
	api := makeTestAPI(engine, chain)

	num := rpc.BlockNumber(450)
	result, err := api.GetMissedRoundsInEpochByBlockNum(&num)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.missingRoundsCalled {
		t.Fatal("V2 engine should NOT be called for V1 blocks")
	}
	// V1 arithmetic: epochStart = 450 - 450%900 = 0
	if result.EpochBlockNumber.Uint64() != 0 {
		t.Fatalf("expected epoch block 0 for V1, got %d", result.EpochBlockNumber.Uint64())
	}
}

// TestGetEpochNumbersBetweenDispatchesToV2 verifies that GetEpochNumbersBetween
// calls the V2 engine for V2 blocks and returns actual switch-block numbers.
// FAILS on main (uses num/epoch arithmetic), PASSES with fix.
func TestGetEpochNumbersBetweenDispatchesToV2(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{
		epochSwitchResult: []*types.EpochSwitchInfo{
			{
				EpochSwitchBlockInfo: &types.BlockInfo{
					Number: big.NewInt(1823), // off-multiple-of-900 due to timeout round
					Round:  types.Round(923),
					Hash:   common.HexToHash("0xabc"),
				},
			},
		},
	}
	engine.EngineV2 = stub

	v2Begin := &types.Header{Number: big.NewInt(1001)}
	v2End := &types.Header{Number: big.NewInt(2000)}
	chain := &stubChain{
		currentHeader: v2End,
		headers: map[uint64]*types.Header{
			1001: v2Begin,
			2000: v2End,
		},
	}
	api := makeTestAPI(engine, chain)

	beginNum := rpc.BlockNumber(1001)
	endNum := rpc.BlockNumber(2000)
	result, err := api.GetEpochNumbersBetween(&beginNum, &endNum)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.epochSwitchInfoCalled {
		t.Fatal("expected V2 engine GetEpochSwitchInfoBetween to be called (issue #1482: still using block-number division)")
	}
	if len(result) != 1 || result[0] != 1823 {
		t.Fatalf("expected [1823] from V2 engine, got %v", result)
	}
}

// TestGetCurrentEpochSwitchBlockDispatchesToV2 verifies that the wrapper
// dispatches to the V2 engine for V2 block numbers (issue #1482).
// FAILS on main (always uses block-number arithmetic), PASSES with fix.
func TestGetCurrentEpochSwitchBlockDispatchesToV2(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{currentSwitchResult: 1823} // non-multiple-of-900
	engine.EngineV2 = stub

	chain := &stubChain{
		currentHeader: &types.Header{Number: big.NewInt(2000)},
		headers:       map[uint64]*types.Header{2000: {Number: big.NewInt(2000)}},
	}

	// Block 2000 is a V2 block (> switchBlock=900)
	switchBlock, _, err := engine.GetCurrentEpochSwitchBlock(chain, big.NewInt(2000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.currentSwitchCalled {
		t.Fatal("expected V2 engine GetCurrentEpochSwitchBlock to be called (issue #1482)")
	}
	if switchBlock != 1823 {
		t.Fatalf("expected switch block 1823 from V2 engine, got %d", switchBlock)
	}
}

// TestGetCurrentEpochSwitchBlockV1Path verifies the V1 arithmetic path is unchanged.
func TestGetCurrentEpochSwitchBlockV1Path(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{}
	engine.EngineV2 = stub

	chain := &stubChain{
		currentHeader: &types.Header{Number: big.NewInt(450)},
		headers:       map[uint64]*types.Header{450: {Number: big.NewInt(450)}},
	}

	// Block 450 is V1 (<= switchBlock=900)
	switchBlock, epochNum, err := engine.GetCurrentEpochSwitchBlock(chain, big.NewInt(450))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.currentSwitchCalled {
		t.Fatal("V2 engine should NOT be called for V1 blocks")
	}
	// V1 arithmetic: 450/900*900 = 0, epochNum = 0
	if switchBlock != 0 || epochNum != 0 {
		t.Fatalf("expected V1 arithmetic (0,0), got (%d,%d)", switchBlock, epochNum)
	}
}

// TestGetV2BlockByHeaderPopulatesRound verifies that GetV2BlockByHeader
// populates Round from the V2 engine (previously always 0).
// FAILS on main (Round always 0), PASSES with fix.
func TestGetV2BlockByHeaderPopulatesRound(t *testing.T) {
	engine := makeTestXDPoSWithV2()

	v2Header := &types.Header{Number: big.NewInt(1800)}
	headerHash := v2Header.Hash()

	stub := &stubV2Engine{
		roundByHeader: map[common.Hash]types.Round{headerHash: types.Round(1823)},
	}
	engine.EngineV2 = stub

	chain := &stubChain{
		currentHeader: v2Header,
		headers:       map[uint64]*types.Header{1800: v2Header},
	}
	api := makeTestAPI(engine, chain)

	info := api.GetV2BlockByHeader(v2Header, false)
	if info.Error != "" {
		t.Fatalf("unexpected error: %s", info.Error)
	}
	if info.Round != types.Round(1823) {
		t.Fatalf("expected Round=1823 from V2 engine, got %d (issue #1482: GetV2BlockByHeader always returned Round=0)", info.Round)
	}
}
