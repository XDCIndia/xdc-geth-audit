// Copyright 2026 The XDC Network Authors
// Tests for XDC Osaka block-gating and Multicall3 install (issue #1533).
//
// This file references APIs that are NEW in this PR and do NOT exist on
// origin/main: OsakaBlock, IsOsakaBlock, ApplyMulticall3HardFork,
// params.Multicall3Address, params.Multicall3RuntimeCode.
// It does not compile on origin/main; compile on the fix branch only.

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// xdcOsakaConfig returns an XDC ChainConfig with both PragueBlock and OsakaBlock set.
// Reuses xdcPragueConfig from prague_xdc_test.go (same package).
func xdcOsakaConfig() *params.ChainConfig {
	cfg := xdcPragueConfig()
	cfg.OsakaBlock = big.NewInt(20)
	return cfg
}

// TestU3_XDCOsakaTxGasCap verifies that IsOsaka returns true for XDC at
// OsakaBlock, enabling the EIP-7825 tx gas cap in txpool validation.
//
// Only compiles on fix branch (OsakaBlock / IsOsakaBlock are new).
// PASSES with fix: IsOsaka dispatches to IsOsakaBlock → true at OsakaBlock.
func TestU3_XDCOsakaTxGasCap(t *testing.T) {
	config := xdcOsakaConfig()
	osakaBlock := config.OsakaBlock
	before := new(big.Int).Sub(osakaBlock, common.Big1)

	if config.IsOsaka(before, 0) {
		t.Error("IsOsaka should be false before OsakaBlock")
	}
	if !config.IsOsaka(osakaBlock, 0) {
		t.Errorf("IsOsaka(OsakaBlock=%v, 0) = false for XDC config, want true\n"+
			"(fix: IsOsaka must dispatch to IsOsakaBlock for XDC)", osakaBlock)
	}
	rules := config.Rules(osakaBlock, true, 0)
	if !rules.IsOsaka {
		t.Error("rules.IsOsaka should be true at OsakaBlock for XDC config")
	}
}

// TestIsOsakaBlockMethod verifies the new IsOsakaBlock method.
func TestIsOsakaBlockMethod(t *testing.T) {
	config := xdcOsakaConfig()
	osakaBlock := config.OsakaBlock
	before := new(big.Int).Sub(osakaBlock, common.Big1)

	if config.IsOsakaBlock(before) {
		t.Error("IsOsakaBlock should be false before OsakaBlock")
	}
	if !config.IsOsakaBlock(osakaBlock) {
		t.Error("IsOsakaBlock should be true at OsakaBlock")
	}
	// nil OsakaBlock: always false.
	cfgNoOsaka := xdcPragueConfig()
	if cfgNoOsaka.IsOsakaBlock(big.NewInt(999999)) {
		t.Error("IsOsakaBlock should be false when OsakaBlock is nil")
	}
}

// TestMulticall3HardForkInstall verifies ApplyMulticall3HardFork installs code
// at Multicall3Address from PragueBlock on XDC, is a no-op before it and when
// the code is present, and never fires on non-XDC chains.
func TestMulticall3HardForkInstall(t *testing.T) {
	config := xdcPragueConfig()
	pragueBlock := config.PragueBlock
	before := new(big.Int).Sub(pragueBlock, common.Big1)
	after := new(big.Int).Add(pragueBlock, common.Big1)

	t.Run("before_prague_no_install", func(t *testing.T) {
		stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		ApplyMulticall3HardFork(config, stateDB, before)
		if code := stateDB.GetCode(params.Multicall3Address); len(code) > 0 {
			t.Error("Multicall3 code installed before PragueBlock")
		}
	})

	t.Run("at_prague_installs_code_and_nonce", func(t *testing.T) {
		stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		ApplyMulticall3HardFork(config, stateDB, pragueBlock)
		code := stateDB.GetCode(params.Multicall3Address)
		if len(code) == 0 {
			t.Fatal("Multicall3 code not installed at PragueBlock")
		}
		nonce := stateDB.GetNonce(params.Multicall3Address)
		if nonce != 1 {
			t.Errorf("Multicall3 nonce = %d, want 1", nonce)
		}
	})

	t.Run("after_prague_idempotent", func(t *testing.T) {
		stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		stateDB.SetCode(params.Multicall3Address, params.Multicall3RuntimeCode, tracing.CodeChangeGenesis)
		stateDB.SetNonce(params.Multicall3Address, 1, tracing.NonceChangeUnspecified)
		ApplyMulticall3HardFork(config, stateDB, after) // must not panic
		if code := stateDB.GetCode(params.Multicall3Address); len(code) == 0 {
			t.Error("code removed by idempotent call at block after PragueBlock")
		}
	})

	// Canonical installs on any Prague block after genesis whose address has
	// no code (XDPoSChain v2.9.3 core/state_processor.go:101-120).
	t.Run("after_prague_installs_when_missing", func(t *testing.T) {
		stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		ApplyMulticall3HardFork(config, stateDB, after)
		if code := stateDB.GetCode(params.Multicall3Address); len(code) == 0 || stateDB.GetNonce(params.Multicall3Address) != 1 {
			t.Error("Multicall3 not installed at a Prague block with no code")
		}
	})

	t.Run("prague_at_genesis_installs_at_block_1", func(t *testing.T) {
		genesisPrague := xdcPragueConfig()
		genesisPrague.PragueBlock = big.NewInt(0)
		for n, want := range map[int64]bool{0: false, 1: true} {
			stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			ApplyMulticall3HardFork(genesisPrague, stateDB, big.NewInt(n))
			if got := len(stateDB.GetCode(params.Multicall3Address)) > 0; got != want {
				t.Errorf("block %d: installed = %v, want %v", n, got, want)
			}
		}
	})

	t.Run("non_xdc_no_install", func(t *testing.T) {
		nonXDC := params.AllEthashProtocolChanges
		stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		ApplyMulticall3HardFork(nonXDC, stateDB, big.NewInt(1))
		if code := stateDB.GetCode(params.Multicall3Address); len(code) > 0 {
			t.Error("Multicall3 code installed on non-XDC chain")
		}
	})
}

// TestApplyMulticall3HardForkIdempotentOnMatchingCode verifies that calling
// ApplyMulticall3HardFork when correct code is already installed is a no-op.
func TestApplyMulticall3HardForkIdempotentOnMatchingCode(t *testing.T) {
	config := xdcPragueConfig()
	stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	stateDB.SetCode(params.Multicall3Address, params.Multicall3RuntimeCode, tracing.CodeChangeGenesis)
	stateDB.SetNonce(params.Multicall3Address, 1, tracing.NonceChangeUnspecified)
	// Call at a later Prague block — must be a no-op, no panic.
	later := new(big.Int).Add(config.PragueBlock, big.NewInt(5))
	ApplyMulticall3HardFork(config, stateDB, later)
	if code := stateDB.GetCode(params.Multicall3Address); len(code) == 0 {
		t.Error("code should still be present after idempotent call")
	}
}

// TestMulticall3NotInstalledOnDevnet5551 checks that the legacy devnet 5551
// gets no Multicall3 at its PragueBlock (50,000). Its sealers (XDC
// v2.7.0-devnet 3bacbce5) never install it, so the live block 50,000 has
// stateRoot c0060c78... only without it.
func TestMulticall3NotInstalledOnDevnet5551(t *testing.T) {
	config := params.XDCDevnetChainConfig
	for _, n := range []int64{50000, 50001} {
		stateDB, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		ApplyMulticall3HardFork(config, stateDB, big.NewInt(n))
		if code := stateDB.GetCode(params.Multicall3Address); len(code) > 0 {
			t.Errorf("block %d: Multicall3 installed on devnet 5551, want none", n)
		}
	}
}
