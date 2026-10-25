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

package legacypool

// Behavioural tests for TRC21 token-fee pool admission.
//
// Evidence: on net2022 B2 run (v2.9.3-testnet legacy vs modern main), a
// zero-XDC-balance holder with TRC21 token balance submits transfer(R,1) to
// a registered TRC21 token. Legacy pool accepts and mines it (gasUsed 68644,
// status 1). Modern pool rejects with "insufficient funds" before this fix.
// Tests FAIL on origin/main and PASS with the fix. Refs #1601 / #1534.

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
)

// trc21TestConfig is a minimal XDC chain config (IsXDC() = true).
var trc21TestConfig = &params.ChainConfig{
	ChainID:             big.NewInt(551),
	HomesteadBlock:      common.Big0,
	EIP150Block:         common.Big0,
	EIP155Block:         common.Big0,
	EIP158Block:         common.Big0,
	ByzantiumBlock:      common.Big0,
	ConstantinopleBlock: common.Big0,
	PetersburgBlock:     common.Big0,
	IstanbulBlock:       common.Big0,
	BerlinBlock:         common.Big0,
	LondonBlock:         common.Big0,
	XDPoS: &params.XDPoSConfig{
		Period: 2,
		Epoch:  900,
	},
}

// trc21TokenAddr is the token contract used in TRC21 pool tests.
var trc21TokenAddr = common.HexToAddress("0xDeadBeef00000000000000000000000000000001")

// setupTRC21State writes the minimum TRC21 issuer and token storage into statedb so
// that GetTRC21FeeCapacityFromState returns capacity for trc21TokenAddr, and
// ValidateTRC21Tx passes for sender with tokenBalance tokens.
//
// Storage layout (mirroring XDPoSChain core/state/trc21_reader.go):
//
//	TRC21Issuer slot 1 (tokens length)          = 1
//	TRC21Issuer slot 1 dyn[0]                   = trc21TokenAddr
//	TRC21Issuer slot 2 mapping(token → capacity) = capacity
//	Token slot 0 mapping(sender → tokenBalance)  = tokenBalance
//	Token slot 1 (minFee)                        = minFee
func setupTRC21State(statedb *state.StateDB, sender common.Address, tokenBalance, capacity, minFee *big.Int) {
	issuer := common.TRC21IssuerSMC

	// Slot 1: tokens[] length = 1
	tokensSlot := common.BigToHash(new(big.Int).SetUint64(1))
	statedb.SetState(issuer, tokensSlot, common.BigToHash(common.Big1))

	// Slot 1 dyn[0]: tokens[0] = trc21TokenAddr
	elem := state.GetLocDynamicArrAtElement(tokensSlot, 0, 1)
	statedb.SetState(issuer, elem, common.BytesToHash(trc21TokenAddr.Bytes()))

	// Slot 2 mapping: tokensState[trc21TokenAddr] = capacity
	capKey := common.BigToHash(state.GetLocMappingAtKey(common.BytesToHash(trc21TokenAddr.Bytes()), 2))
	statedb.SetState(issuer, capKey, common.BigToHash(capacity))

	// Token slot 0 mapping: balances[sender] = tokenBalance
	balKey := common.BigToHash(state.GetLocMappingAtKey(common.BytesToHash(sender.Bytes()), 0))
	statedb.SetState(trc21TokenAddr, balKey, common.BigToHash(tokenBalance))

	// Token slot 1: minFee = minFee
	statedb.SetState(trc21TokenAddr, common.BigToHash(new(big.Int).SetUint64(1)), common.BigToHash(minFee))
}

// trc21TransferData returns ABI-encoded transfer(to, 1) calldata (68 bytes).
func trc21TransferData(to common.Address) []byte {
	// selector: transfer(address,uint256) = 0xa9059cbb
	data := make([]byte, 68)
	data[0], data[1], data[2], data[3] = 0xa9, 0x05, 0x9c, 0xbb
	copy(data[16:36], to.Bytes()) // address padded to 32 bytes at offset 4
	data[67] = 1                  // uint256 value = 1 at offset 36 (last byte)
	return data
}

// TestTRC21PoolAdmission verifies that a zero-XDC-balance sender whose TRC21
// token-fee tx can be covered by the issuer's capacity is:
//  1. accepted into pending (not rejected with ErrInsufficientFunds), and
//  2. NOT dropped during demoteUnexecutables (survives a pool reset).
//
// It also verifies that:
//  3. a tx to a non-TRC21 address from the same zero-balance sender IS rejected, and
//  4. a TRC21 tx where the issuer capacity is too small IS rejected.
func TestTRC21PoolAdmission(t *testing.T) {
	senderKey, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)
	recipient := common.HexToAddress("0x0000000000000000000000000000000000000002")

	// minFee = 1e15 tokens, tokenBalance = 1e24 (well above minFee+1), capacity = 100 XDC
	minFee := new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil)
	tokenBalance := new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)
	capacity := new(big.Int).Mul(big.NewInt(100), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) // 100 XDC

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	// Sender has 0 XDC balance.
	setupTRC21State(statedb, sender, tokenBalance, capacity, minFee)

	bc := newTestBlockChain(trc21TestConfig, 10000000, statedb, new(event.Feed))
	pool := New(testTxPoolConfig, bc)
	if err := pool.Init(testTxPoolConfig.PriceLimit, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatal(err)
	}
	<-pool.initDoneCh
	defer pool.Close()

	gasPrice := big.NewInt(1000e9) // 1000 gwei (user price; TRC21 uses its own price)

	// Case 1: TRC21 tx from zero-balance sender — must be accepted.
	trc21Tx, _ := types.SignTx(
		types.NewTransaction(0, trc21TokenAddr, big.NewInt(0), 150000, gasPrice, trc21TransferData(recipient)),
		types.HomesteadSigner{},
		senderKey,
	)
	if err := pool.addRemoteSync(trc21Tx); err != nil {
		t.Fatalf("case 1: expected TRC21 tx accepted, got: %v", err)
	}

	// Verify it landed in pending.
	pool.mu.RLock()
	_, inPending := pool.pending[sender]
	pool.mu.RUnlock()
	if !inPending {
		t.Fatal("case 1: TRC21 tx not in pending after acceptance")
	}

	// Case 2: survive demoteUnexecutables — simulate a head reset that keeps
	// the sender's XDC balance at zero.
	pool.mu.Lock()
	pool.demoteUnexecutables()
	pool.mu.Unlock()

	pool.mu.RLock()
	pendingList := pool.pending[sender]
	pool.mu.RUnlock()
	if pendingList == nil || pendingList.Len() == 0 {
		t.Fatal("case 2: TRC21 tx dropped by demoteUnexecutables, should survive")
	}

	// Case 3: non-TRC21 tx from zero-balance sender — must be rejected.
	otherTx, _ := types.SignTx(
		types.NewTransaction(1, recipient, big.NewInt(0), 21000, gasPrice, nil),
		types.HomesteadSigner{},
		senderKey,
	)
	if err := pool.addRemoteSync(otherTx); err == nil {
		t.Fatal("case 3: expected non-TRC21 tx rejected from zero-balance sender, but it was accepted")
	}

	// Case 4: TRC21 tx where capacity is zero — must be rejected.
	statedb2, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	setupTRC21State(statedb2, sender, tokenBalance, new(big.Int), minFee) // capacity = 0
	bc2 := newTestBlockChain(trc21TestConfig, 10000000, statedb2, new(event.Feed))
	pool2 := New(testTxPoolConfig, bc2)
	if err := pool2.Init(testTxPoolConfig.PriceLimit, bc2.CurrentBlock(), newReserver()); err != nil {
		t.Fatal(err)
	}
	<-pool2.initDoneCh
	defer pool2.Close()

	trc21TxAgain, _ := types.SignTx(
		types.NewTransaction(0, trc21TokenAddr, big.NewInt(0), 150000, gasPrice, trc21TransferData(recipient)),
		types.HomesteadSigner{},
		senderKey,
	)
	if err := pool2.addRemoteSync(trc21TxAgain); err == nil {
		t.Fatal("case 4: expected TRC21 tx rejected when capacity = 0, but it was accepted")
	}
}
