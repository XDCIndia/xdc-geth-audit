// Copyright (c) 2026 XDC Network Authors
//
// Test the XDC Gas2500x tier min-gas-price enforcement at pool admission.
//
// Canonical reference: XDPoSChain core/txpool/validation.go
// ValidateTransactionWithState, end of function:
//
//	if !tx.IsSpecialTransaction() {
//	    minGasPrice := params.GetMinGasPrice(pendingNumber, opts.Config)
//	    if tx.GasPriceIntCmp(minGasPrice) < 0 {
//	        return ErrUnderMinGasPrice
//	    }
//	}
//
// Issue: #1534

package legacypool

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// xdcGas2500xConfig returns an XDC chain config with Gas2500x active at block 0.
// IsXDC() requires XDPoS != nil.
func xdcGas2500xConfig() *params.ChainConfig {
	cpy := *params.TestChainConfig
	// Activate XDPoS so IsXDC() returns true.
	cpy.XDPoS = &params.XDPoSConfig{Epoch: 900}
	// Gas50x and Gas2500x both active from block 0.
	cpy.Gas50xBlock = common.Big0
	cpy.Gas2500xBlock = common.Big0
	// Disable Ethash so XDPoS is the consensus engine.
	cpy.Ethash = nil
	cpy.LondonBlock = common.Big0
	cpy.BerlinBlock = common.Big0
	return &cpy
}

// xdcGas2500xPool returns a LegacyPool on an XDC chain with Gas2500x active
// from block 0. MinTip is set to 0 so that the ordinary MinTip gate cannot
// be the source of the rejection — only the tier-floor check fires.
func xdcGas2500xPool(t *testing.T) (*LegacyPool, func()) {
	t.Helper()
	return xdcPoolWith(t, xdcGas2500xConfig())
}

// xdcPoolWith returns a LegacyPool on cfg with MinTip 0 (see xdcGas2500xPool).
func xdcPoolWith(t *testing.T, cfg *params.ChainConfig) (*LegacyPool, func()) {
	t.Helper()

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	bc := newTestBlockChain(cfg, 10_000_000, statedb, new(event.Feed))

	pcfg := DefaultConfig
	pcfg.Journal = ""
	pcfg.NoLocals = true
	pcfg.PriceLimit = 0

	pool := New(pcfg, bc)
	if err := pool.Init(0 /* priceLimit */, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatalf("pool init: %v", err)
	}
	<-pool.initDoneCh
	pool.SetGasTip(new(big.Int)) // 0 Gwei MinTip

	return pool, func() { pool.Close() }
}

// gas2500xFloor is 625 gwei = XDCBaseFee(12.5 gwei) * 50.
var gas2500xFloor = new(big.Int).Mul(
	new(big.Int).SetUint64(params.XDCBaseFee),
	big.NewInt(50),
)

// oneETH is 1e18 wei; used to fund test accounts so balance is not the limiter.
var oneETH = new(big.Int).Mul(big.NewInt(1e9), big.NewInt(1e9))

// addBelowFloor offers a legacy tx priced at 40 gwei, well below the 625 gwei
// Gas2500x floor, to a Gas2500x-active XDC pool and returns the admission error.
//
// This is the net2022 B2 finding: a legacy (type-0) deployment tx at 40 gwei
// was accepted by modern geth (pre-fix) but rejected by v2.9.3-testnet.
func addBelowFloor(t *testing.T) error {
	pool, close := xdcGas2500xPool(t)
	defer close()

	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, uint256.MustFromBig(oneETH), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	belowFloor := new(big.Int).Mul(big.NewInt(40), big.NewInt(1e9))
	dest := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	raw := types.NewTransaction(0, dest, big.NewInt(0), 21000, belowFloor, nil)
	tx, _ := types.SignTx(raw, types.HomesteadSigner{}, key)

	if tx.IsSpecialTransaction() {
		t.Fatal("test tx must not be a special transaction")
	}
	return pool.Add([]*types.Transaction{tx}, true)[0]
}

// TestXDCTierMinGasPrice_BelowFloorRejected verifies that a non-special tx
// whose gasPrice is below the Gas2500x tier floor (625 gwei) is rejected when
// Gas2500x is active. The error value is checked in
// legacypool_xdc_tier_gas_err_test.go.
func TestXDCTierMinGasPrice_BelowFloorRejected(t *testing.T) {
	t.Parallel()

	if err := addBelowFloor(t); err == nil {
		t.Fatal("tx below Gas2500x tier floor must be rejected")
	}
}

// TestXDCTierMinGasPrice_AtFloorAccepted verifies that a non-special tx priced
// exactly at the Gas2500x floor (625 gwei) is accepted into the pool.
func TestXDCTierMinGasPrice_AtFloorAccepted(t *testing.T) {
	t.Parallel()

	pool, close := xdcGas2500xPool(t)
	defer close()

	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, uint256.MustFromBig(oneETH), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	dest := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	raw := types.NewTransaction(0, dest, big.NewInt(0), 21000, new(big.Int).Set(gas2500xFloor), nil)
	tx, _ := types.SignTx(raw, types.HomesteadSigner{}, key)

	errs := pool.Add([]*types.Transaction{tx}, true)
	if errs[0] != nil {
		t.Fatalf("tx at exactly Gas2500x floor (625 gwei) must be accepted, got: %v", errs[0])
	}

	pending, _ := pool.Pending(txpool.PendingFilter{})
	if _, ok := pending[addr]; !ok {
		t.Fatal("accepted tx not found in pending")
	}
}

// TestXDCTierMinGasPrice_SpecialTxExempt verifies that a special tx (to
// BlockSignersBinary, gasPrice=0) from a known masternode signer is still
// accepted even with Gas2500x active.
//
// Canonical: ValidateTransactionWithState guards the check with
// !tx.IsSpecialTransaction(), so special txs are never price-rejected.
func TestXDCTierMinGasPrice_SpecialTxExempt(t *testing.T) {
	t.Parallel()

	pool, close := xdcGas2500xPool(t)
	defer close()

	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)

	pool.SetIsSigner(func(a common.Address) bool { return a == addr })

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// gasPrice=0 special tx to BlockSignersBinary.
	tx := makeSpecialTx(0, common.BlockSignersBinary, key)

	if !tx.IsSpecialTransaction() {
		t.Fatal("tx to BlockSignersBinary must be IsSpecialTransaction()==true")
	}

	errs := pool.Add([]*types.Transaction{tx}, true)
	if errs[0] != nil {
		t.Fatalf("gasPrice=0 special tx must be accepted despite Gas2500x floor: %v", errs[0])
	}
}

// TestXDCTierMinGasPrice_PreGas50xBaseline verifies the floor before any tier
// fork is canonical's common.DefaultMinGasPrice (0.25 gwei), not the Gas50x
// price: XDPoSChain chainDefaultGasPrice falls back to the baseline tier.
func TestXDCTierMinGasPrice_PreGas50xBaseline(t *testing.T) {
	t.Parallel()

	cfg := xdcGas2500xConfig()
	cfg.Gas50xBlock = big.NewInt(1_000_000)
	cfg.Gas2500xBlock = big.NewInt(1_000_000)
	pool, close := xdcPoolWith(t, cfg)
	defer close()

	dest := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	add := func(price int64) error {
		key, _ := crypto.GenerateKey()
		pool.mu.Lock()
		pool.currentState.AddBalance(crypto.PubkeyToAddress(key.PublicKey), uint256.MustFromBig(oneETH), tracing.BalanceChangeUnspecified)
		pool.mu.Unlock()
		tx, _ := types.SignTx(types.NewTransaction(0, dest, big.NewInt(0), 21000, big.NewInt(price), nil), types.HomesteadSigner{}, key)
		return pool.Add([]*types.Transaction{tx}, true)[0]
	}
	if err := add(1_000_000_000); err != nil {
		t.Fatalf("1 gwei is above the 0.25 gwei baseline and must be accepted, got: %v", err)
	}
	if err := add(200_000_000); err == nil {
		t.Fatal("0.2 gwei is below the 0.25 gwei baseline and must be rejected")
	}
}
