// Copyright 2026 XDC Network
// Behavioral test for the V2 penalty hook's post-TIPUpgradePenalty comeback
// branch (fix for #1471).
//
// This file is intentionally separate from penalty_upgrade_comeback_test.go
// so that it compiles against origin/main (where resolvePostUpgradeLimitPenaltyEpoch
// is absent) and produces a clear runtime failure, not a compile error.

package hooks

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// comebackFakeV2 implements XDPoS.EngineV2Iface for comeback-branch testing.
// It captures the penalty closure wired by AttachConsensusV2Hooks, stops the
// epoch walk immediately via IsEpochSwitch, and returns configurable penalty
// lists per epoch offset.
type comebackFakeV2 struct {
	capturedFn       func(consensus.ChainReader, *big.Int, common.Hash, []common.Address) ([]common.Address, error)
	penaltiesAtEpoch func(limit int) []common.Address
}

var _ XDPoS.EngineV2Iface = (*comebackFakeV2)(nil)

func (f *comebackFakeV2) VerifyHeader(_ consensus.ChainReader, _ *types.Header, _ bool) error {
	return nil
}
func (f *comebackFakeV2) VerifyHeaderWithParents(_ consensus.ChainReader, _ *types.Header, _ []*types.Header, _ bool) error {
	return nil
}
func (f *comebackFakeV2) GetRoundNumber(_ *types.Header) (types.Round, error) { return 1, nil }
func (f *comebackFakeV2) UpdateMasternodesFromHeader(_ consensus.ChainReader, _ *types.Header, _ *state.StateDB) error {
	return nil
}
func (f *comebackFakeV2) UpdateMasternodes(_ consensus.ChainReader, _ *types.Header, _ []common.Address) error {
	return nil
}
func (f *comebackFakeV2) UpdateMasternodesWithPenalties(_ consensus.ChainReader, _ *types.Header, _, _ []common.Address) error {
	return nil
}
func (f *comebackFakeV2) Author(_ *types.Header) (common.Address, error) {
	return common.Address{}, nil
}

// IsEpochSwitch returns true for every header so the epoch walk in HookPenalty
// terminates on the first iteration, leaving listBlockHash=[currentHash] and
// statMiners={} — no initial block-production penalties.
func (f *comebackFakeV2) IsEpochSwitch(_ *types.Header) (bool, uint64, error) { return true, 0, nil }

func (f *comebackFakeV2) GetMasternodesByHash(_ consensus.ChainReader, _ common.Hash) []common.Address {
	return nil
}
func (f *comebackFakeV2) GetPreviousPenaltyByHash(_ consensus.ChainReader, _ common.Hash, limit int) []common.Address {
	if f.penaltiesAtEpoch != nil {
		return f.penaltiesAtEpoch(limit)
	}
	return nil
}
func (f *comebackFakeV2) GetMasternodesFromEpochSwitchHeader(_ consensus.ChainReader, _ *types.Header) []common.Address {
	return nil
}
func (f *comebackFakeV2) GetStandbynodes(_ consensus.ChainReader, _ *types.Header) []common.Address {
	return nil
}
func (f *comebackFakeV2) GetMasternodesV2(_ consensus.ChainReader, _ *types.Header) []common.Address {
	return nil
}
func (f *comebackFakeV2) Prepare(_ consensus.ChainReader, _ *types.Header) error { return nil }
func (f *comebackFakeV2) Finalize(_ consensus.ChainReader, _ *types.Header, _ *state.StateDB, _ *state.StateDB, _ []*types.Transaction, _ []*types.Header, _ []*types.Receipt) (*types.Block, error) {
	return nil, nil
}
func (f *comebackFakeV2) Seal(_ consensus.ChainReader, _ *types.Block, _ <-chan struct{}) (*types.Block, error) {
	return nil, nil
}
func (f *comebackFakeV2) CalcDifficulty(_ consensus.ChainReader, _ uint64, _ *types.Header) *big.Int {
	return big.NewInt(1)
}
func (f *comebackFakeV2) SignHash(_ *types.Header) common.Hash             { return common.Hash{} }
func (f *comebackFakeV2) Authorize(_ common.Address, _ engine_v2.SignerFn) {}
func (f *comebackFakeV2) SetHookReward(_ func(consensus.ChainReader, *state.StateDB, *state.StateDB, *types.Header) (map[string]interface{}, error)) {
}
func (f *comebackFakeV2) SetHookPenalty(fn func(consensus.ChainReader, *big.Int, common.Hash, []common.Address) ([]common.Address, error)) {
	f.capturedFn = fn
}
func (f *comebackFakeV2) ProposedBlockHandler(_ consensus.ChainReader, _ *types.Header) error {
	return nil
}
func (f *comebackFakeV2) ReinitBFT(_ consensus.ChainReader, _ *types.Header) error { return nil }

// comebackTestChain is a minimal ChainReader that returns a fixed parent header
// for every GetHeader call (so the round-resolution loop in HookPenalty exits
// on the first iteration without sleeping) and nil for all other lookups.
type comebackTestChain struct {
	parent *types.Header
	cfg    *params.ChainConfig
}

var _ consensus.ChainReader = (*comebackTestChain)(nil)

func (c *comebackTestChain) Config() *params.ChainConfig                     { return c.cfg }
func (c *comebackTestChain) CurrentHeader() *types.Header                    { return c.parent }
func (c *comebackTestChain) GetHeader(_ common.Hash, _ uint64) *types.Header { return c.parent }
func (c *comebackTestChain) GetHeaderByNumber(_ uint64) *types.Header        { return nil }
func (c *comebackTestChain) GetHeaderByHash(_ common.Hash) *types.Header     { return nil }
func (c *comebackTestChain) GetBlock(_ common.Hash, _ uint64) *types.Block   { return nil }

// addrIn reports whether target appears in addrs.
func addrIn(addrs []common.Address, target common.Address) bool {
	for _, a := range addrs {
		if a == target {
			return true
		}
	}
	return false
}

// TestHookPenaltyV2PostUpgradeComeback exercises the actual V2 penalty hook
// (the closure wired by AttachConsensusV2Hooks / SetHookPenalty) for the
// post-TIPUpgradePenalty comeback path.  Expected values are derived from
// canonical XDPoSChain v2.9.3-testnet eth/hooks/engine_v2_hooks.go:210-272.
//
// Configuration (test-sized; canonical Apothem values are LimitPenaltyEpoch=5,
// MinimumSigningTx=30):
//
//	LimitPenaltyEpoch = 2   (consecutive penalty epochs required for comeback)
//	MinimumSigningTx  = 1   (signing txs required per epoch for comeback)
//	Epoch             = 10  (blocks per epoch)
//	comebackHeight    = 2*10+0 = 20; test block 31 > 20 ✓
//	(31-1) % MergeSignRange = 30 % 15 = 0 → mapBlockHash[currentHash]=true ✓
//
// Fail case — must FAIL on origin/main, PASS on this branch:
//
//	signerA is in lastPenalty (epoch offset 0) but absent from epoch offset 1
//	→ penParoleeMap[A]=1 < LimitPenaltyEpoch=2 → must remain penalised.
//	On origin/main the else branch is absent; HookPenalty never re-adds signerA;
//	returns []; test expects [signerA] → FAIL:
//
//	    fail case: signerA penalised in 1/2 epochs with 0 signing txs must remain
//	    penalised; got [], want [...0xAAAA...]
//
// Pass case — verifies comeback release works correctly (passes on both):
//
//	signerA penalised in all 2 epochs (penParoleeMap[A]=2) AND has 1 signing tx
//	(≥ MinimumSigningTx=1) → released; must be absent from returned list.
func TestHookPenaltyV2PostUpgradeComeback(t *testing.T) {
	const (
		epoch             = 10 // compact epoch; canonical Apothem is 900
		limitPenaltyEpoch = 2  // canonical Apothem is 5; 2 probes the counter loop
		minimumSigningTx  = 1  // canonical Apothem is 30; 1 simplifies fixture
	)

	keyA, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signerA := crypto.PubkeyToAddress(keyA.PublicKey)

	// Chain config: TIPUpgradePenalty active from genesis.
	// London at 0 → MakeSigner returns a consistent EIP-155 signer, required
	// for signing-tx sender recovery inside the comeback scan.
	chainConfig := &params.ChainConfig{
		ChainID:                big.NewInt(551),
		HomesteadBlock:         big.NewInt(0),
		EIP150Block:            big.NewInt(0),
		EIP155Block:            big.NewInt(0),
		EIP158Block:            big.NewInt(0),
		ByzantiumBlock:         big.NewInt(0),
		ConstantinopleBlock:    big.NewInt(0),
		PetersburgBlock:        big.NewInt(0),
		IstanbulBlock:          big.NewInt(0),
		BerlinBlock:            big.NewInt(0),
		LondonBlock:            big.NewInt(0),
		TIPUpgradePenaltyBlock: big.NewInt(0),
		XDPoS: &params.XDPoSConfig{
			Epoch: epoch,
			V2: &params.V2{
				SwitchBlock: big.NewInt(0),
				CurrentConfig: &params.V2Config{
					LimitPenaltyEpoch: limitPenaltyEpoch,
					MinimumSigningTx:  minimumSigningTx,
				},
			},
		},
	}

	currentHash := common.HexToHash("0xabcdef01")
	number := big.NewInt(31) // comebackHeight=20; 31>20; (31-1)%15=0

	// Build adaptor (carries the BlockSigners LRU cache) and inject fake V2.
	adaptor := XDPoS.New(&params.XDPoSConfig{
		Epoch: epoch,
		V2:    &params.V2{SwitchBlock: big.NewInt(0)},
	}, rawdb.NewMemoryDatabase())
	fakeV2 := &comebackFakeV2{}
	adaptor.SetEngineV2(fakeV2)
	// bc=nil: AttachConsensusV2Hooks only uses bc inside HookReward, which is
	// never called from this test.
	AttachConsensusV2Hooks(adaptor, nil, chainConfig)
	fn := fakeV2.capturedFn
	if fn == nil {
		t.Fatal("AttachConsensusV2Hooks did not wire the penalty hook")
	}

	// Mock chain: GetHeader returns the same parent header every call so the
	// round-resolution loop exits without sleeping; IsEpochSwitch=true then stops
	// the epoch walk, leaving listBlockHash=[currentHash], statMiners={},
	// preMasternodes=[] → no block-production penalties.
	chain := &comebackTestChain{
		parent: &types.Header{Number: big.NewInt(int64(number.Uint64() - 1))},
		cfg:    chainConfig,
	}

	// --- FAIL case (differentiates origin/main from this branch) ---
	//
	// signerA is in lastPenalty (epoch offset 0) but absent from epoch offset 1:
	//   penParoleeMap[A] = 1, LimitPenaltyEpoch = 2 → 1 != 2 → must stay penalised.
	//
	// On origin/main: else branch absent; signerA never re-added; returns [];
	// test fails with:
	//   fail case: signerA penalised in 1/2 epochs with 0 signing txs must remain
	//   penalised; got [], want [...<signerA>...]
	fakeV2.penaltiesAtEpoch = func(limit int) []common.Address {
		if limit == 0 {
			return []common.Address{signerA}
		}
		return nil
	}
	pensFail, err := fn(chain, number, currentHash, nil)
	if err != nil {
		t.Fatalf("fail case: hook error: %v", err)
	}
	if !addrIn(pensFail, signerA) {
		t.Fatalf("fail case: signerA penalised in 1/%d epochs with 0 signing txs must remain penalised; got %v, want [...%v...]",
			limitPenaltyEpoch, pensFail, signerA)
	}

	// --- PASS case (comeback release) ---
	//
	// signerA penalised in all 2 consecutive epochs (penParoleeMap[A]=2=LimitPenaltyEpoch)
	// AND 1 signing tx (≥ MinimumSigningTx=1) → must be released from penalties.
	fakeV2.penaltiesAtEpoch = func(_ int) []common.Address {
		return []common.Address{signerA} // all offsets → penParoleeMap[A]=limitPenaltyEpoch
	}

	// Build a valid BlockSigner signing tx from signerA referencing currentHash.
	// Data: 4-byte HexSignMethod | 32-byte block number | 32-byte block hash (68 total).
	// blkHash = currentHash is in mapBlockHash because (number-1)=30, 30%MergeSignRange=0.
	txData := common.Hex2Bytes(common.HexSignMethod)
	txData = append(txData, common.LeftPadBytes(big.NewInt(30).Bytes(), 32)...)
	txData = append(txData, currentHash.Bytes()...)
	rawTx := types.NewTransaction(0, common.BlockSignersBinary, big.NewInt(0), 200_000, big.NewInt(0), txData)
	txSigner := types.MakeSigner(chainConfig, number, 0) // LondonSigner(chainID=551)
	signedTx, err := types.SignTx(rawTx, txSigner, keyA)
	if err != nil {
		t.Fatalf("pass case: sign tx: %v", err)
	}
	// Pre-populate the BlockSigners cache so GetCachedSigningTxs hits on the first
	// call (cache miss would fall back to chain.GetBlock which returns nil here).
	adaptor.BlockSigners.Add(currentHash, []*types.Transaction{signedTx})

	pensPass, err := fn(chain, number, currentHash, nil)
	if err != nil {
		t.Fatalf("pass case: hook error: %v", err)
	}
	if addrIn(pensPass, signerA) {
		t.Fatalf("pass case: signerA penalised in %d/%d epochs with ≥%d signing tx must be released; got %v",
			limitPenaltyEpoch, limitPenaltyEpoch, minimumSigningTx, pensPass)
	}
}

// Remaining EngineV2Iface methods; the comeback test does not use them.
func (f *comebackFakeV2) GetCurrentEpochSwitchBlock(consensus.ChainReader, *big.Int) (uint64, uint64, error) {
	return 0, 0, nil
}
func (f *comebackFakeV2) GetEpochSwitchInfoBetween(consensus.ChainReader, *types.Header, *types.Header) ([]*types.EpochSwitchInfo, error) {
	return nil, nil
}
func (f *comebackFakeV2) GetBlockByEpochNumber(consensus.ChainReader, uint64) (*types.BlockInfo, error) {
	return nil, nil
}
func (f *comebackFakeV2) CalculateMissingRounds(consensus.ChainReader, *types.Header) (*utils.PublicApiMissedRoundsMetadata, error) {
	return nil, nil
}
func (f *comebackFakeV2) GetLatestCommittedBlockInfo() *types.BlockInfo { return nil }
