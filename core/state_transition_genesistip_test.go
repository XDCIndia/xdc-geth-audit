// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Tests that TRC21 fee routing reads TIPTRC21FeeBlock from the genesis config
// rather than the package-level common.TIPTRC21Fee constant. Fixes the net2002
// consensus split at block 12134 (refs #1549, canonical XDPoSChain
// state_transition.go:507 checks cfg.TIPTRC21FeeBlock, not the global).

package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// TestTRC21FeeRoutingFromGenesisConfig verifies that the TIPTRC21FeeBlock
// value from the genesis config (not the package-level common.TIPTRC21Fee)
// controls TRC21 fee routing. A custom chain with tipTRC21FeeBlock=2200 must
// credit the owner at block 2201, even though common.TIPTRC21Fee=38,383,838.
//
// On origin/main this test FAILS because state_transition.go:881 compares
// against common.TIPTRC21Fee (38,383,838), so 2201 > 38383838 is false and
// the fee is not routed to the owner.
//
// With the fix, state_transition.go uses EffectiveTIPTRC21FeeBlock() which
// reads the genesis field (2200), so 2201 > 2200 is true and the owner is
// credited gasUsed*gasPrice = 21000*12.5gwei = 262,500,000,000,000 wei.
func TestTRC21FeeRoutingFromGenesisConfig(t *testing.T) {
	const (
		tipTRC21FeeBlock = 2200 // genesis value — much lower than mainnet 38,383,838
		testBlock        = 2201 // first block where TRC21 routing must be active
	)
	baseFeeWei := big.NewInt(12_500_000_000) // 12.5 gwei — net2002 genesis baseFee

	// Custom config with genesis TIPTRC21FeeBlock=2200 and no XDPoS overlay
	// (XDPoS: nil → IsXDC()=false; avoids XDC-specific Rules branching).
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(2002),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),   // London active so BaseFee is respected
		Eip1559Block:        big.NewInt(2250), // EIP-1559 active after 2250 (net2002 genesis)
		TIPTRC21FeeBlock:    big.NewInt(tipTRC21FeeBlock),
	}

	var (
		sender   = common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		to       = common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		owner    = common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
		coinbase = common.HexToAddress("0xdddddddddddddddddddddddddddddddddddddddd")
	)

	// gasPrice == baseFee → effectiveTip = 0 on the non-TRC21 path,
	// so nothing is credited to coinbase. On the TRC21 path the owner
	// gets gasUsed * gasPrice regardless.
	gasPrice := uint256.MustFromBig(baseFeeWei)

	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	statedb.AddBalance(sender, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)

	evm := vm.NewEVM(vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		Coinbase:    coinbase,
		BlockNumber: big.NewInt(testBlock),
		BaseFee:     baseFeeWei,
		GasLimit:    1_000_000,
		Difficulty:  big.NewInt(1),
	}, statedb, cfg, vm.Config{})

	msg := &Message{
		From:      sender,
		To:        &to,
		Value:     new(uint256.Int),
		GasLimit:  21_000,
		GasPrice:  gasPrice,
		GasFeeCap: gasPrice,
		GasTipCap: new(uint256.Int),
	}
	res, err := ApplyMessage(evm, msg, NewGasPool(1_000_000), owner)
	if err != nil {
		t.Fatal(err)
	}

	wantOwner := new(uint256.Int).Mul(uint256.NewInt(res.UsedGas), gasPrice)
	gotOwner := statedb.GetBalance(owner)
	if !gotOwner.Eq(wantOwner) {
		t.Errorf("owner balance = %v, want %v (gasUsed=%d * gasPrice=%v)\n"+
			"  tipTRC21FeeBlock=%d testBlock=%d common.TIPTRC21Fee=%v\n"+
			"  This means the fee gate read the global constant instead of the genesis config field.",
			gotOwner, wantOwner, res.UsedGas, gasPrice,
			tipTRC21FeeBlock, testBlock, common.TIPTRC21Fee)
	}
	gotCoinbase := statedb.GetBalance(coinbase)
	if !gotCoinbase.IsZero() {
		t.Errorf("coinbase balance = %v, want 0 (should not receive fee after TIPTRC21FeeBlock)", gotCoinbase)
	}
}
