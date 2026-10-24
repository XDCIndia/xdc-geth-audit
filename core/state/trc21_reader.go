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

package state

import (
	"bytes"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"
)

// ABI function selectors for TRC21 token-fee transaction validation.
// transfer(address,uint256)            = 0xa9059cbb
// transferFrom(address,address,uint256) = 0x23b872dd
var (
	trc21TransferSig     = []byte{0xa9, 0x05, 0x9c, 0xbb}
	trc21TransferFromSig = []byte{0x23, 0xb8, 0x72, 0xdd}
)

// TRC21 token-fee capacity, ported from XinFinOrg/XDPoSChain core/state/trc21_reader.go
// (v2.7.1 mainnet, unchanged in v2.9.1-testnet). A token registered in the TRC21
// issuer contract (common.TRC21IssuerSMC) has an XDC deposit there, its fee
// capacity; transactions sent to the token pay their fee from that capacity
// instead of the sender's balance.

var (
	slotTRC21IssuerTokens      = uint64(1) // issuer: address[] tokens
	slotTRC21IssuerTokensState = uint64(2) // issuer: mapping(address => uint256) tokensState (capacity)
	slotTRC21TokenBalances     = uint64(0) // token: mapping(address => uint256) balances
	slotTRC21TokenMinFee       = uint64(1) // token: uint256 minFee
	slotTRC21TokenIssuer       = uint64(2) // token: address issuer
)

// GetTRC21FeeCapacityFromState returns the fee capacity of every token
// registered in the TRC21 issuer contract.
func GetTRC21FeeCapacityFromState(statedb *StateDB) map[common.Address]*big.Int {
	capacity := map[common.Address]*big.Int{}
	issuer := common.TRC21IssuerSMC
	tokensSlot := common.BigToHash(new(big.Int).SetUint64(slotTRC21IssuerTokens))
	tokenCount := statedb.GetState(issuer, tokensSlot).Big().Uint64()
	for i := uint64(0); i < tokenCount; i++ {
		value := statedb.GetState(issuer, GetLocDynamicArrAtElement(tokensSlot, i, 1))
		if (value == common.Hash{}) {
			continue
		}
		token := common.BytesToAddress(value.Bytes())
		key := GetLocMappingAtKey(common.BytesToHash(token.Bytes()), slotTRC21IssuerTokensState)
		capacity[token] = statedb.GetState(issuer, common.BigToHash(key)).Big()
	}
	return capacity
}

// PayFeeWithTRC21TxFail charges the token's minFee, in tokens, from the sender
// to the token issuer when a token-fee transaction fails.
func PayFeeWithTRC21TxFail(statedb *StateDB, from, token common.Address) {
	balanceKey := common.BigToHash(GetLocMappingAtKey(common.BytesToHash(from.Bytes()), slotTRC21TokenBalances))
	balanceHash := statedb.GetState(token, balanceKey)
	if (balanceHash == common.Hash{}) {
		return
	}
	balance := balanceHash.Big()
	if balance.Sign() <= 0 {
		return
	}
	issuerAddr := common.BytesToAddress(statedb.GetState(token, GetLocSimpleVariable(slotTRC21TokenIssuer)).Bytes())
	fee := statedb.GetState(token, GetLocSimpleVariable(slotTRC21TokenMinFee)).Big()
	feeUsed := fee
	if balance.Cmp(fee) < 0 {
		feeUsed = balance
	}
	balance = new(big.Int).Sub(balance, feeUsed)
	statedb.SetState(token, balanceKey, common.BigToHash(balance))

	issuerBalanceKey := common.BigToHash(GetLocMappingAtKey(common.BytesToHash(issuerAddr.Bytes()), slotTRC21TokenBalances))
	issuerBalance := statedb.GetState(token, issuerBalanceKey).Big()
	statedb.SetState(token, issuerBalanceKey, common.BigToHash(new(big.Int).Add(issuerBalance, feeUsed)))
}

// ValidateTRC21Tx checks whether from has sufficient token balance to send a
// TRC21 token-fee transaction to token. It mirrors XDPoSChain
// StateDB.ValidateTRC21Tx (core/state/trc21_reader.go v2.9.3-testnet):
//
//   - If the sender holds token balance, it must be ≥ minFee + transfer value
//     (parsed from a transfer/transferFrom ABI call in data).
//   - If the sender holds no token balance, the tx is accepted anyway (canonical
//     behaviour: "we both accept tx with balance = 0 and fee = 0").
//
// Returns false only when data is too short to contain a selector or when the
// sender's token balance is insufficient.
func ValidateTRC21Tx(statedb *StateDB, from, token common.Address, data []byte) bool {
	if statedb == nil || len(data) < 4 {
		return false
	}
	balanceKey := common.BigToHash(GetLocMappingAtKey(common.BytesToHash(from.Bytes()), slotTRC21TokenBalances))
	balanceHash := statedb.GetState(token, balanceKey)
	if (balanceHash == common.Hash{}) {
		// No token balance: accept, mirroring canonical "balance = 0 and fee = 0" path.
		return true
	}
	balance := balanceHash.Big()
	minFee := statedb.GetState(token, GetLocSimpleVariable(slotTRC21TokenMinFee)).Big()
	// Parse the transfer value from standard ABI-encoded transfer / transferFrom.
	// transfer(address,uint256): selector 0xa9059cbb, 68 bytes total, value at [36:68].
	// transferFrom(address,address,uint256): selector 0x23b872dd, 80 bytes (canonical
	// packs value into 12 bytes), value at [68:80] — mirrors XDPoSChain exactly.
	value := new(big.Int)
	switch {
	case bytes.Equal(data[:4], trc21TransferSig) && len(data) == 68:
		value = common.BytesToHash(data[36:]).Big()
	case bytes.Equal(data[:4], trc21TransferFromSig) && len(data) == 80:
		value = common.BytesToHash(data[68:]).Big()
	}
	required := new(big.Int).Add(minFee, value)
	return balance.Cmp(required) >= 0
}

// UpdateTRC21Fee writes the tokens' new fee capacities back to the issuer
// contract and takes the fees paid from the issuer's XDC balance.
func UpdateTRC21Fee(statedb *StateDB, newBalance map[common.Address]*big.Int, totalFeeUsed *big.Int) {
	if len(newBalance) == 0 {
		return
	}
	issuer := common.TRC21IssuerSMC
	for token, value := range newBalance {
		key := GetLocMappingAtKey(common.BytesToHash(token.Bytes()), slotTRC21IssuerTokensState)
		statedb.SetState(issuer, common.BigToHash(key), common.BigToHash(value))
	}
	fee, _ := uint256.FromBig(totalFeeUsed)
	statedb.SubBalance(issuer, fee, tracing.BalanceChangeUnspecified)
}
