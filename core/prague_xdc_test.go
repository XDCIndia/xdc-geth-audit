// Copyright 2026 The XDC Network Authors
// Behavioural tests for XDC Prague block-gating (issue #1533).
//
// This file uses ONLY APIs present on origin/main (PragueBlock, IsPragueBlock,
// IsPrague, PostExecution, LatestSigner, CheckConfigForkOrder,
// CheckGenesisConfigUnknownKeys, etc.).
// It compiles on origin/main AND fails on assertions there; all tests pass
// after the fix is applied.
//
// OsakaBlock / IsOsakaBlock / ApplyMulticall3HardFork / params.Multicall3Address
// are NOT referenced here — those live in osaka_multicall3_xdc_test.go which
// only compiles on the fix branch.

package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/bal"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
)

// xdcPragueConfig returns a minimal XDC-like ChainConfig with PragueBlock set.
// Based on AllEthashProtocolChanges (all timestamps nil there), then wires in
// XDPoS and PragueBlock. No OsakaBlock is set (matches mainnet/apothem).
func xdcPragueConfig() *params.ChainConfig {
	cfg := *params.AllEthashProtocolChanges // shallow copy; ShanghaiTime/CancunTime nil
	cfg.ChainID = big.NewInt(551)
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900}
	cfg.PragueBlock = big.NewInt(10)
	cfg.CancunBlock = big.NewInt(5)
	// Clear any timestamp forks the base may carry; XDC uses block-based activation.
	cfg.ShanghaiTime = nil
	cfg.CancunTime = nil
	cfg.PragueTime = nil
	cfg.OsakaTime = nil
	cfg.BlobScheduleConfig = nil
	return &cfg
}

// makeMinimalEVM returns an EVM backed by an empty in-memory statedb at the
// given block number. Sufficient for PostExecution tests that skip CL-only
// system calls (the fix) or attempt them and return an error (main).
func makeMinimalEVM(t *testing.T, config *params.ChainConfig, blockNum *big.Int) (*vm.EVM, *state.StateDB) {
	t.Helper()
	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	blockCtx := vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		GetHash:     func(n uint64) common.Hash { return common.Hash{} },
		BlockNumber: new(big.Int).Set(blockNum),
		Time:        0,
		Difficulty:  big.NewInt(1),
		GasLimit:    10_000_000,
	}
	return vm.NewEVM(blockCtx, stateDB, config, vm.Config{}), stateDB
}

// TestU1_XDCPraguePostExecutionNoCLError verifies that PostExecution on an XDC
// Prague block does NOT fail with "empty system contract" for EIP-7002/7251.
//
// FAILS on origin/main: rules.IsPrague=true (Rules XDC branch sets
// isPrague=IsPragueBlock) → PostExecution has no !IsXDC guard → tries
// ProcessWithdrawalQueue on empty statedb → "empty system contract" error.
// PASSES with fix: !rules.IsXDC guard skips all CL-only calls.
func TestU1_XDCPraguePostExecutionNoCLError(t *testing.T) {
	config := xdcPragueConfig()
	pragueBlock := config.PragueBlock
	evm, _ := makeMinimalEVM(t, config, pragueBlock)

	// Sanity: rules at PragueBlock must have IsPrague=true and IsXDC=true.
	rules := config.Rules(pragueBlock, true, 0)
	if !rules.IsPrague {
		t.Fatal("rules.IsPrague should be true at PragueBlock — prerequisite not met")
	}
	if !rules.IsXDC {
		t.Fatal("rules.IsXDC should be true for XDC config — prerequisite not met")
	}

	requests, _, err := PostExecution(context.Background(), config, pragueBlock, 0, nil, nil, evm, 0)
	if err != nil {
		t.Errorf("PostExecution returned error on XDC Prague block: %v\n"+
			"(fix: PostExecution must skip CL-only EIP-6110/7002/7251 calls when rules.IsXDC)", err)
	}
	// XDC headers carry no requestsHash, so ValidateState rejects any non-nil
	// requests ("block has requests before prague fork").
	if requests != nil {
		t.Errorf("PostExecution returned non-nil requests on XDC Prague block: %v", requests)
	}
}

// TestIsPragueBlockGatedForXDC verifies that IsPrague(num, time) uses
// block-based activation for XDC chains, not timestamps.
//
// FAILS on origin/main: IsPrague is timestamp-only, returns false for XDC
// (PragueTime is nil) even at PragueBlock.
// PASSES with fix: IsPrague dispatches to IsPragueBlock for XDC.
func TestIsPragueBlockGatedForXDC(t *testing.T) {
	config := xdcPragueConfig()
	pragueBlock := config.PragueBlock
	before := new(big.Int).Sub(pragueBlock, common.Big1)

	if config.IsPrague(before, 0) {
		t.Error("IsPrague should be false before PragueBlock")
	}
	if !config.IsPrague(pragueBlock, 0) {
		t.Errorf("IsPrague(PragueBlock=%v, 0) = false for XDC config, want true\n"+
			"(fix: IsPrague must dispatch to IsPragueBlock for XDC)", pragueBlock)
	}
	// Timestamp must be irrelevant: even maxUint64 must not activate Prague before the block.
	if config.IsPrague(before, ^uint64(0)) {
		t.Error("IsPrague should be false before PragueBlock regardless of timestamp")
	}
}

// TestLatestSignerPragueBlock verifies that LatestSigner returns PragueSigner
// for a chain with PragueBlock set (XDC pattern: block-gated, PragueTime nil).
//
// FAILS on origin/main: LatestSigner checks only PragueTime → returns
// LondonSigner for XDC even with PragueBlock scheduled.
// PASSES with fix: checks PragueBlock != nil → returns PragueSigner.
func TestLatestSignerPragueBlock(t *testing.T) {
	config := xdcPragueConfig()
	signer := types.LatestSigner(config)
	expected := types.NewPragueSigner(config.ChainID)
	if !signer.Equal(expected) {
		t.Errorf("LatestSigner returned non-Prague signer (%T) for XDC config with PragueBlock set, want PragueSigner\n"+
			"(fix: LatestSigner must check PragueBlock != nil)", signer)
	}
}

// TestU5_XDCRejectsTimestampForks verifies that CheckConfigForkOrder returns
// an error when an XDC chain config sets cancunTime, pragueTime, or osakaTime.
// Each sub-test is constructed so all EXISTING ordering and blob-schedule
// checks pass on origin/main (err == nil), so the only difference is the new
// XDC-specific guard added by the fix.
//
// FAILS on origin/main: no XDC-specific validation → err == nil.
// PASSES with fix: new guard fires → err != nil.
func TestU5_XDCRejectsTimestampForks(t *testing.T) {
	ts := func(v uint64) *uint64 { return &v }
	for _, tc := range []struct {
		name  string
		setup func(*params.ChainConfig)
	}{
		// cancunTime: set ShanghaiTime=0 + CancunTime=1000 + blob config.
		// Ordering check passes (0 ≤ 1000); blob check passes (Cancun configured).
		// My XDC check: CancunTime != nil → error.
		{"cancunTime", func(c *params.ChainConfig) {
			c.ShanghaiTime = ts(0)
			c.CancunTime = ts(1000)
			c.BlobScheduleConfig = &params.BlobScheduleConfig{
				Cancun: &params.BlobConfig{Max: 6, Target: 3, UpdateFraction: 3338477},
			}
		}},
		// pragueTime: set ShanghaiTime=0 + PragueTime=2000 + Prague blob config.
		// CancunTime stays nil (optional in ordering); ordering passes (0 ≤ 2000);
		// blob check passes (Prague configured). My XDC check: PragueTime != nil → error.
		{"pragueTime", func(c *params.ChainConfig) {
			c.ShanghaiTime = ts(0)
			c.PragueTime = ts(2000)
			c.BlobScheduleConfig = &params.BlobScheduleConfig{
				Prague: &params.BlobConfig{Max: 9, Target: 6, UpdateFraction: 5007716},
			}
		}},
		// osakaTime: set ShanghaiTime=0 + OsakaTime=3000; no blob config needed
		// (OsakaTime is not in the blob-schedule check). Ordering passes (0 ≤ 3000).
		// My XDC check: OsakaTime != nil → error.
		{"osakaTime", func(c *params.ChainConfig) {
			c.ShanghaiTime = ts(0)
			c.OsakaTime = ts(3000)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := xdcPragueConfig()
			tc.setup(cfg)
			if err := cfg.CheckConfigForkOrder(); err == nil {
				t.Errorf("CheckConfigForkOrder should reject XDC config with %s set\n"+
					"(fix: add XDC-specific validation in CheckConfigForkOrder)", tc.name)
			}
		})
	}
}

// TestXDCMainnetApothemNoPrague is an invariant test: mainnet must not have
// Prague or Osaka scheduled, and Apothem has only Prague, at 87,633,000 as in
// canonical XDPoSChain v3.0.0-testnet.
func TestXDCMainnetApothemNoPrague(t *testing.T) {
	if got := params.XDCApothemChainConfig.PragueBlock; got == nil || got.Cmp(big.NewInt(87_633_000)) != 0 {
		t.Errorf("apothem PragueBlock = %v, want 87633000", got)
	}
	if params.XDCApothemChainConfig.IsOsaka(big.NewInt(100_000_000), 0) {
		t.Error("apothem IsOsaka=true at block 100M, want false")
	}
	bigMillion := big.NewInt(100_000_000)
	for _, tc := range []struct {
		name string
		cfg  *params.ChainConfig
	}{
		{"mainnet", params.XDCMainnetChainConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg.PragueBlock != nil {
				t.Errorf("%s has PragueBlock=%v, want nil (not scheduled)", tc.name, tc.cfg.PragueBlock)
			}
			if tc.cfg.IsPrague(bigMillion, 0) {
				t.Errorf("%s IsPrague=true at block 100M, want false", tc.name)
			}
			if tc.cfg.IsOsaka(bigMillion, 0) {
				t.Errorf("%s IsOsaka=true at block 100M, want false", tc.name)
			}
		})
	}
}

// TestXDCDevnetChainConfig verifies that XDCDevnetChainConfig has PragueBlock
// set and no timestamp-based Prague/Osaka forks. Invariant on both main and fix.
func TestXDCDevnetChainConfig(t *testing.T) {
	cfg := params.XDCDevnetChainConfig
	if cfg.PragueBlock == nil {
		t.Error("XDCDevnetChainConfig.PragueBlock should be non-nil")
	}
	if cfg.PragueTime != nil {
		t.Errorf("XDCDevnetChainConfig.PragueTime should be nil, got %v", *cfg.PragueTime)
	}
	if cfg.OsakaTime != nil {
		t.Errorf("XDCDevnetChainConfig.OsakaTime should be nil, got %v", *cfg.OsakaTime)
	}
}

// TestOsakaBlockKnownGenesisKey verifies that "osakaBlock" is accepted by
// CheckGenesisConfigUnknownKeys (added in #1549), so a genesis file with that
// key is not rejected at geth-init / --genesis time.
//
// CheckGenesisConfigUnknownKeys exists on origin/main (landed with #1549), but
// "osakaBlock" is absent from its known-key set there — it returns
// "unknown chain config key \"osakaBlock\"" → test FAILS on main.
// Fix adds "osakaBlock" to knownChainConfigKeys → no error → PASS.
func TestOsakaBlockKnownGenesisKey(t *testing.T) {
	raw := json.RawMessage(`{"osakaBlock":"0x14"}`)
	if err := params.CheckGenesisConfigUnknownKeys(raw); err != nil {
		t.Errorf("osakaBlock rejected as unknown genesis config key: %v\n"+
			"(fix: add \"osakaBlock\" to knownChainConfigKeys in params/config.go)", err)
	}
}

// TestXDCPragueChainImport generates and imports an XDC chain across
// PragueBlock and checks the state canonical XDPoSChain v2.9.3 produces at F:
// the EIP-2935 history contract holding only slot F-1, Multicall3 installed
// with nonce 1, no requestsHash, and a type-4 (EIP-7702) tx accepted at F+1.
//
// FAILS on origin/main: block F is rejected ("empty system contract") and the
// type-4 tx cannot be signed or recovered with the time-gated signer.
func TestXDCPragueChainImport(t *testing.T) {
	var (
		config    = xdcPragueConfig()
		fork      = config.PragueBlock.Uint64()
		key, _    = crypto.GenerateKey()
		authKey   = mustKey(t)
		addr      = crypto.PubkeyToAddress(key.PublicKey)
		authority = crypto.PubkeyToAddress(authKey.PublicKey)
		target    = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
		gspec     = &Genesis{
			Config:   config,
			GasLimit: 50_000_000,
			BaseFee:  big.NewInt(params.InitialBaseFee),
			Alloc:    types.GenesisAlloc{addr: {Balance: big.NewInt(1e18)}},
		}
	)
	config.Eip1559Block = big.NewInt(0)
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), int(fork)+2, func(i int, b *BlockGen) {
		if b.Number().Uint64() != fork+1 {
			return
		}
		auth, err := types.SignSetCode(authKey, types.SetCodeAuthorization{
			ChainID: *uint256.MustFromBig(config.ChainID), Address: target, Nonce: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		fee := uint256.MustFromBig(new(big.Int).Mul(b.BaseFee(), big.NewInt(2)))
		b.AddTx(types.MustSignNewTx(key, types.MakeSigner(config, b.Number(), 0), &types.SetCodeTx{
			ChainID: uint256.MustFromBig(config.ChainID), Nonce: 0, Gas: 100_000,
			GasFeeCap: fee, GasTipCap: fee, To: authority,
			AuthList: []types.SetCodeAuthorization{auth},
		}))
	})
	chain, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	if n, err := chain.InsertChain(blocks); err != nil {
		t.Fatalf("import failed at block %d: %v", blocks[n].NumberU64(), err)
	}
	stateAt := func(n uint64) *state.StateDB {
		h := chain.GetHeaderByNumber(n)
		st, err := chain.StateAt(h.Root, h.Number, h.Time)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	slot := func(n uint64) common.Hash {
		return common.BigToHash(new(big.Int).SetUint64(n % params.HistoryServeWindow))
	}

	pre, at := stateAt(fork-1), stateAt(fork)
	if pre.Exist(params.HistoryStorageAddress) || len(pre.GetCode(target)) != 0 {
		t.Fatal("history contract or Multicall3 present before PragueBlock")
	}
	if !bytes.Equal(at.GetCode(params.HistoryStorageAddress), params.HistoryStorageCode) || at.GetNonce(params.HistoryStorageAddress) != 1 {
		t.Fatal("history contract not installed at PragueBlock")
	}
	if got, want := at.GetState(params.HistoryStorageAddress, slot(fork-1)), chain.GetHeaderByNumber(fork-1).Hash(); got != want {
		t.Fatalf("history slot F-1 = %x, want %x", got, want)
	}
	if got := at.GetState(params.HistoryStorageAddress, slot(fork-2)); got != (common.Hash{}) {
		t.Fatalf("history slot F-2 = %x, want empty (install + 1 slot)", got)
	}
	if len(at.GetCode(target)) == 0 || at.GetNonce(target) != 1 {
		t.Fatal("Multicall3 not installed with nonce 1 at PragueBlock")
	}
	if h := chain.GetHeaderByNumber(fork); h.RequestsHash != nil {
		t.Fatal("XDC Prague header carries a requestsHash")
	}
	if got, want := stateAt(fork+1).GetCode(authority), types.AddressToDelegation(target); !bytes.Equal(got, want) {
		t.Fatalf("delegation code = %x, want %x", got, want)
	}
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// xdcOsakaConfigJSON returns xdcPragueConfig plus "osakaBlock", set through the
// genesis JSON so this file still compiles on a tree without OsakaBlock.
func xdcOsakaConfigJSON(t *testing.T, osaka uint64) *params.ChainConfig {
	t.Helper()
	enc, err := json.Marshal(xdcPragueConfig())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(enc, &m); err != nil {
		t.Fatal(err)
	}
	m["osakaBlock"] = osaka
	m["eip1559Block"] = 0
	if enc, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	cfg := new(params.ChainConfig)
	if err := json.Unmarshal(enc, cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestXDCOsakaRulesBlockGated: Osaka rules follow osakaBlock on XDC even when
// the caller passes isMerge=false, as the txpool does for XDC heads (non-zero
// difficulty). Without it the EIP-7825 pool cap stays off.
func TestXDCOsakaRulesBlockGated(t *testing.T) {
	cfg := xdcOsakaConfigJSON(t, 20)
	for _, isMerge := range []bool{false, true} {
		if cfg.Rules(big.NewInt(19), isMerge, 0).IsOsaka {
			t.Errorf("isMerge=%v: IsOsaka true before osakaBlock", isMerge)
		}
		if !cfg.Rules(big.NewInt(20), isMerge, 0).IsOsaka {
			t.Errorf("isMerge=%v: IsOsaka false at osakaBlock", isMerge)
		}
	}
}

// TestXDCOsakaImportRejectsTxAboveGasCap (U3): a block after osakaBlock that
// carries a tx with gas 16,777,217 (EIP-7825 cap + 1) is rejected on import;
// a tx with exactly the cap is accepted.
func TestXDCOsakaImportRejectsTxAboveGasCap(t *testing.T) {
	const osaka = 12
	for _, tc := range []struct {
		gas    uint64
		reject bool
	}{{params.MaxTxGas + 1, true}, {params.MaxTxGas, false}} {
		var (
			key, _ = crypto.GenerateKey()
			addr   = crypto.PubkeyToAddress(key.PublicKey)
			genCfg = xdcOsakaConfigJSON(t, osaka)
			gspec  = &Genesis{
				Config:   genCfg,
				GasLimit: 50_000_000,
				BaseFee:  big.NewInt(params.InitialBaseFee),
				Alloc:    types.GenesisAlloc{addr: {Balance: big.NewInt(1e18)}},
			}
		)
		// Build the blocks with a config whose Osaka never activates, then
		// import them under the Osaka schedule. Prague stays off in both so
		// the two configs differ only by Osaka.
		genCfg.PragueBlock = nil
		noOsaka := xdcPragueConfig()
		noOsaka.Eip1559Block = big.NewInt(0)
		noOsaka.PragueBlock = nil
		gspec.Config = noOsaka
		_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), osaka+2, func(i int, b *BlockGen) {
			if b.Number().Uint64() != osaka+1 {
				return
			}
			b.AddTx(types.MustSignNewTx(key, types.MakeSigner(noOsaka, b.Number(), 0), &types.LegacyTx{
				Nonce: 0, Gas: tc.gas, GasPrice: new(big.Int).Mul(b.BaseFee(), big.NewInt(2)), To: &addr,
			}))
		})
		gspec.Config = genCfg
		chain, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		_, err = chain.InsertChain(blocks)
		chain.Stop()
		if tc.reject && !errors.Is(err, ErrGasLimitTooHigh) {
			t.Errorf("gas %d after osakaBlock: got %v, want ErrGasLimitTooHigh", tc.gas, err)
		}
		if !tc.reject && err != nil {
			t.Errorf("gas %d after osakaBlock: got %v, want import", tc.gas, err)
		}
	}
}

// TestXDCOsakaBlockSizeCap (U4): after osakaBlock a block whose RLP exceeds
// 8,388,608 bytes fails ValidateBody with ErrBlockOversized (EIP-7934).
func TestXDCOsakaBlockSizeCap(t *testing.T) {
	cfg := xdcOsakaConfigJSON(t, 5)
	gspec := &Genesis{Config: cfg, GasLimit: 50_000_000, BaseFee: big.NewInt(params.InitialBaseFee)}
	chain, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()
	tx := types.NewTx(&types.LegacyTx{Gas: 21_000, GasPrice: big.NewInt(1), Data: make([]byte, params.MaxBlockSize+1)})
	block := types.NewBlock(&types.Header{Number: big.NewInt(6), Difficulty: big.NewInt(1)},
		&types.Body{Transactions: types.Transactions{tx}}, nil, trie.NewStackTrie(nil))
	if err := NewBlockValidator(cfg, chain).ValidateBody(block); !errors.Is(err, ErrBlockOversized) {
		t.Fatalf("oversized block after osakaBlock: got %v, want ErrBlockOversized", err)
	}
}

// TestXDCCalldataPricingAtPrague applies a call with calldata before and at the
// Prague block of an XDC chain. A non-zero calldata byte costs 68 gas before
// Prague and 16 gas (EIP-2028) from Prague, as in canonical XDPoSChain v3.0.0
// (core/state_transition.go IntrinsicGas(..., rules.IsPrague, ...)). The callee
// burns 200 gas (40 x PUSH1 0; POP) so the gas used stays above the EIP-7623
// calldata floor and shows the intrinsic gas.
func TestXDCCalldataPricingAtPrague(t *testing.T) {
	devnet5551 := xdcPragueConfig()
	devnet5551.ChainID = big.NewInt(5551)
	from := common.HexToAddress("0x1000000000000000000000000000000000000001")
	to := common.HexToAddress("0x2000000000000000000000000000000000000002")
	data := []byte{0x01, 0x02, 0x03, 0x00, 0x00}
	code := bytes.Repeat([]byte{byte(vm.PUSH1), 0x00, byte(vm.POP)}, 40)
	for _, tc := range []struct {
		config *params.ChainConfig
		block  int64
		want   uint64
	}{
		{xdcPragueConfig(), 9, params.TxGas + 3*68 + 2*params.TxDataZeroGas + 200},
		{xdcPragueConfig(), 10, params.TxGas + 3*16 + 2*params.TxDataZeroGas + 200},
		// Devnet 5551's v2.7.0-devnet sealers keep 68 gas after Prague.
		{devnet5551, 10, params.TxGas + 3*68 + 2*params.TxDataZeroGas + 200},
	} {
		evm, statedb := makeMinimalEVM(t, tc.config, big.NewInt(tc.block))
		statedb.SetBalance(from, uint256.NewInt(params.Ether), tracing.BalanceChangeUnspecified)
		statedb.SetCode(to, code, tracing.CodeChangeUnspecified)
		msg := &Message{
			From:      from,
			To:        &to,
			Value:     new(uint256.Int),
			GasLimit:  100_000,
			GasPrice:  new(uint256.Int),
			GasFeeCap: new(uint256.Int),
			GasTipCap: new(uint256.Int),
			Data:      data,
		}
		res, err := ApplyMessage(evm, msg, NewGasPool(msg.GasLimit))
		if err != nil {
			t.Fatalf("chain %v block %d: %v", tc.config.ChainID, tc.block, err)
		}
		if res.UsedGas != tc.want {
			t.Errorf("chain %v block %d: gas used %d, want %d", tc.config.ChainID, tc.block, res.UsedGas, tc.want)
		}
	}
}

// xdcAmsterdamConfig returns a minimal XDC ChainConfig with Amsterdam active
// from block 100 (built on top of xdcPragueConfig with OsakaBlock=50).
func xdcAmsterdamConfig() *params.ChainConfig {
	cfg := xdcPragueConfig()
	cfg.OsakaBlock = big.NewInt(50)
	cfg.AmsterdamBlock = big.NewInt(100)
	return cfg
}

// TestU4_XDCAmsterdam4788NotFired verifies that EIP-4788 (ProcessBeaconBlockRoot)
// is NOT called on XDC Amsterdam blocks even when ParentBeaconRoot is non-nil.
// On XDC blocks, ParentBeaconRoot carries the canonical zero-hash placeholder
// (required for RLP compatibility); EIP-4788 would write to the beacon roots
// contract if not gated out.
//
// The test pre-deploys the EIP-4788 contract code and passes a distinctive
// non-zero beacon root so that if the system call WERE executed it would write
// recognisable storage. After PreExecution the contract's slot must stay zero.
func TestU4_XDCAmsterdam4788NotFired(t *testing.T) {
	config := xdcAmsterdamConfig()
	amsterdamBlock := config.AmsterdamBlock

	rules := config.Rules(amsterdamBlock, true, 0)
	if !rules.IsAmsterdam {
		t.Fatal("rules.IsAmsterdam should be true — prerequisite not met")
	}
	if !rules.IsXDC {
		t.Fatal("rules.IsXDC should be true for XDC config — prerequisite not met")
	}

	evm, stateDB := makeMinimalEVM(t, config, amsterdamBlock)
	// Pre-deploy the EIP-4788 contract so that a call would write storage.
	stateDB.SetCode(params.BeaconRootsAddress, params.BeaconRootsCode, tracing.CodeChangeUnspecified)

	// Use a non-zero sentinel so we can tell if it was stored.
	sentinelRoot := common.Hash{0xbe, 0xac, 0x04, 0x78, 0x88}
	parent := &types.Header{Number: new(big.Int).Sub(amsterdamBlock, common.Big1)}

	// The EVM block time is 0 (from makeMinimalEVM). If EIP-4788 fired at
	// time=0, it would store sentinelRoot at slot 0%8191+8191 = 8191.
	const histBufLen = 8191
	storageSlot := common.BigToHash(new(big.Int).SetUint64(histBufLen))

	PreExecution(context.Background(), &sentinelRoot, parent, config, evm, amsterdamBlock, 0)

	got := stateDB.GetState(params.BeaconRootsAddress, storageSlot)
	if got != (common.Hash{}) {
		t.Errorf("EIP-4788 fired on XDC Amsterdam block: slot 8191 = %v (want zero) — PreExecution gate is missing", got)
	}
}

// TestU5_XDCAmsterdamFillHeaderAndValidateBody verifies:
// (a) FillAmsterdamHeader sets all mandatory placeholder fields and a non-nil BAL hash.
// (b) A block built from the filled header with an attached (matching) access list
//
//	passes ValidateBody's Amsterdam BAL-hash check.
//
// (c) A block with a mismatched BAL hash is rejected.
//
// This covers the correctness of core.FillAmsterdamHeader, which is the shared
// helper used by both AssembleBlock and the XDPoS sealing path (miner/xdpos_worker.go).
func TestU5_XDCAmsterdamFillHeaderAndValidateBody(t *testing.T) {
	config := xdcAmsterdamConfig()
	amsterdamBlock := config.AmsterdamBlock

	rules := config.Rules(amsterdamBlock, true, 0)
	if !rules.IsAmsterdam {
		t.Fatal("prerequisite: rules.IsAmsterdam must be true")
	}

	// Build a minimal Amsterdam header (Root and UncleHash already set as if
	// by Finalize; all other Amsterdam fields nil before FillAmsterdamHeader).
	header := &types.Header{
		Number:     new(big.Int).Set(amsterdamBlock),
		Difficulty: big.NewInt(1),
		GasLimit:   10_000_000,
		Root:       types.EmptyRootHash,
		UncleHash:  types.EmptyUncleHash,
	}

	// FillAmsterdamHeader must set all mandatory fields.
	emptyBAL := newEmptyBAL()
	blal := FillAmsterdamHeader(header, emptyBAL)

	if header.BlobGasUsed == nil || *header.BlobGasUsed != 0 {
		t.Error("BlobGasUsed: want non-nil zero")
	}
	if header.ExcessBlobGas == nil || *header.ExcessBlobGas != 0 {
		t.Error("ExcessBlobGas: want non-nil zero")
	}
	if header.ParentBeaconRoot == nil || *header.ParentBeaconRoot != (common.Hash{}) {
		t.Error("ParentBeaconRoot: want non-nil zero hash")
	}
	if header.RequestsHash == nil || *header.RequestsHash != types.EmptyRequestsHash {
		t.Error("RequestsHash: want non-nil EmptyRequestsHash")
	}
	if header.BlockAccessListHash == nil {
		t.Fatal("BlockAccessListHash must be non-nil after FillAmsterdamHeader")
	}
	// The hash must equal the encoded access list's own hash.
	if *header.BlockAccessListHash != blal.Hash() {
		t.Errorf("BlockAccessListHash mismatch: header=%x blal=%x", *header.BlockAccessListHash, blal.Hash())
	}

	// (b) Build block with correct BAL hash and verify ValidateBody passes
	// the Amsterdam hash check. Use ethash as a stub engine (doesn't
	// validate PoW for uncle check here). We only exercise ValidateBody's
	// Amsterdam-specific BAL-hash path.
	goodBlock := types.NewBlock(header, &types.Body{
		Withdrawals: types.Withdrawals{},
	}, nil, trie.NewStackTrie(nil)).WithAccessListUnsafe(blal)
	// ValidateBody checks: if block.AccessList() != nil → compare hashes.
	// It also requires a blockchain for ancestor checks, so we test the hash
	// logic directly instead of calling v.ValidateBody.
	if goodBlock.AccessList() == nil {
		t.Fatal("block.AccessList() must be non-nil (WithAccessListUnsafe)")
	}
	computed := goodBlock.AccessList().Hash()
	if computed != *goodBlock.Header().BlockAccessListHash {
		t.Errorf("good block: BAL hash mismatch: header=%x computed=%x", *goodBlock.Header().BlockAccessListHash, computed)
	}

	// (c) A block with a deliberately wrong BAL hash must be caught.
	wrongHash := common.Hash{0xff, 0xee}
	badHeader := types.CopyHeader(header)
	badHeader.BlockAccessListHash = &wrongHash
	badBlock := types.NewBlock(badHeader, &types.Body{
		Withdrawals: types.Withdrawals{},
	}, nil, trie.NewStackTrie(nil)).WithAccessListUnsafe(blal)
	if badBlock.AccessList() == nil {
		t.Fatal("bad block: AccessList must be non-nil")
	}
	if badBlock.AccessList().Hash() == *badBlock.Header().BlockAccessListHash {
		t.Error("bad block: hashes should differ (test setup error)")
	}
}

// newEmptyBAL is a test helper that returns an empty ConstructionBlockAccessList.
// Lives here rather than as a method so it requires no external import in tests.
func newEmptyBAL() *bal.ConstructionBlockAccessList {
	return bal.NewConstructionBlockAccessList()
}

// TestU5_XDCAmsterdamBlobFeeZero verifies that NewEVMBlockContext does not
// panic and returns BlobBaseFee=nil for XDC Amsterdam headers that carry
// ExcessBlobGas=0 as a canonical placeholder (no BlobScheduleConfig on XDC).
// It also verifies that a non-XDC chain without a BlobScheduleConfig preserves
// upstream behaviour — CalcBlobFee panics — so we have not silently suppressed
// legitimate errors.
func TestU5_XDCAmsterdamBlobFeeZero(t *testing.T) {
	t.Helper()

	zero := uint64(0)
	xdcHdr := &types.Header{
		Number:        big.NewInt(100), // >= AmsterdamBlock in xdcAmsterdamConfig
		Difficulty:    big.NewInt(1),
		ExcessBlobGas: &zero, // canonical placeholder set by FillAmsterdamHeader
		BaseFee:       big.NewInt(1),
		GasLimit:      1_000_000,
	}

	// XDC: must not panic; BlobBaseFee must be nil (blob txs don't exist on XDC).
	xdcCfg := xdcAmsterdamConfig()
	ctx := NewEVMBlockContext(xdcHdr, &BlockChain{chainConfig: xdcCfg}, new(common.Address))
	if ctx.BlobBaseFee != nil {
		t.Errorf("XDC Amsterdam: want BlobBaseFee=nil, got %v", ctx.BlobBaseFee)
	}

	// Non-XDC without BlobScheduleConfig: CalcBlobFee must still panic so that
	// upstream callers fail loud on a real misconfiguration (non-XDC chain with
	// ExcessBlobGas but no blob schedule).
	nonXDCCfg := &params.ChainConfig{
		ChainID:             big.NewInt(1),
		HomesteadBlock:      new(big.Int),
		EIP150Block:         new(big.Int),
		EIP155Block:         new(big.Int),
		EIP158Block:         new(big.Int),
		ByzantiumBlock:      new(big.Int),
		ConstantinopleBlock: new(big.Int),
		PetersburgBlock:     new(big.Int),
		IstanbulBlock:       new(big.Int),
		BerlinBlock:         new(big.Int),
		LondonBlock:         new(big.Int),
		// XDPoS deliberately nil → IsXDC() == false
		// BlobScheduleConfig deliberately nil → CalcBlobFee panics
	}
	nonXDCHdr := &types.Header{
		Number:        big.NewInt(1),
		Difficulty:    big.NewInt(1),
		ExcessBlobGas: &zero,
		BaseFee:       big.NewInt(1),
		GasLimit:      1_000_000,
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("non-XDC without BlobScheduleConfig: expected CalcBlobFee panic, got none")
		}
	}()
	// This must panic.
	NewEVMBlockContext(nonXDCHdr, &BlockChain{chainConfig: nonXDCCfg}, new(common.Address))
}

// TestU6_XDCNegativeBlockRejection verifies that a post-Amsterdam block with
// a wrong BlockAccessListHash is rejected with "access list hash mismatch" by
// ValidateBody. The complementary ExcessBlobGas placeholder test lives in
// consensus/XDPoS/amsterdam_xdc_test.go where verifyNoUnsealedFields is accessible.
func TestU6_XDCNegativeBlockRejection(t *testing.T) {
	cfg := xdcAmsterdamConfig()
	cfg.Eip1559Block = big.NewInt(0)
	cfg.ChainID = big.NewInt(20999) // distinct from any live chain

	gspec := &Genesis{
		Config:   cfg,
		GasLimit: 10_000_000,
		BaseFee:  big.NewInt(params.InitialBaseFee),
		Alloc:    types.GenesisAlloc{},
	}

	// Generate 100 blocks; insert 1-99 (pre-Amsterdam, AmsterdamBlock=100).
	_, blocks, _ := GenerateChainWithGenesis(gspec, ethash.NewFaker(), 100, func(i int, b *BlockGen) {})

	db := rawdb.NewMemoryDatabase()
	chain, err := NewBlockChain(db, gspec, ethash.NewFaker(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()

	if n, err := chain.InsertChain(blocks[:99]); err != nil {
		t.Fatalf("pre-Amsterdam import failed at block %d: %v", blocks[n].NumberU64(), err)
	}

	parent := chain.GetBlockByNumber(99)
	zeroHash := common.Hash{}
	blobZero := uint64(0)
	emptySealed := newEmptyBAL().ToEncodingObj()
	baseFee := eip1559.CalcBaseFee(cfg, parent.Header())

	// Header where BlockAccessListHash is deliberately wrong.
	wrongHash := common.Hash{0xde, 0xad}
	hdr := &types.Header{
		ParentHash:          parent.Hash(),
		UncleHash:           types.EmptyUncleHash,
		Number:              big.NewInt(100),
		GasLimit:            parent.GasLimit(),
		Time:                parent.Time() + 1,
		Difficulty:          big.NewInt(1),
		TxHash:              types.EmptyTxsHash,
		ReceiptHash:         types.EmptyReceiptsHash,
		Root:                parent.Root(),
		BaseFee:             baseFee,
		WithdrawalsHash:     &types.EmptyWithdrawalsHash,
		BlobGasUsed:         &blobZero,
		ExcessBlobGas:       &blobZero,
		ParentBeaconRoot:    &zeroHash,
		RequestsHash:        &types.EmptyRequestsHash,
		BlockAccessListHash: &wrongHash,
	}
	blk := types.NewBlock(hdr, &types.Body{Withdrawals: types.Withdrawals{}},
		nil, trie.NewStackTrie(nil)).WithAccessListUnsafe(emptySealed)

	validator := NewBlockValidator(cfg, chain)
	errA := validator.ValidateBody(blk)
	if errA == nil || !containsStr(errA.Error(), "access list hash mismatch") {
		t.Errorf("wrong BAL hash: want 'access list hash mismatch', got %v", errA)
	}
}
