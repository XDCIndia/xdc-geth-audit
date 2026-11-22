// Copyright (c) 2026 XDC Network

package ethapi

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

var privacyPrecompileAddrs = []common.Address{
	common.BytesToAddress([]byte{0x1e}),
	common.BytesToAddress([]byte{0x28}),
}

// limitPrivacyPrecompiles keeps 0x1e and 0x28 only when both the loaded
// state header and the execution header are inside the privacy window.
// An RPC block-number override cannot turn them on for state that is
// outside that window, and cannot keep them on when the override leaves it.
// Other precompiles stay on the execution rules. With no override the two
// headers match and the set is unchanged.
func limitPrivacyPrecompiles(cfg *params.ChainConfig, have vm.PrecompiledContracts, stateNum *big.Int, stateMerge bool, stateTime uint64, execNum *big.Int, execMerge bool, execTime uint64) vm.PrecompiledContracts {
	if have == nil || cfg == nil || stateNum == nil || execNum == nil {
		return have
	}
	if stateNum.Cmp(execNum) == 0 && stateTime == execTime && stateMerge == execMerge {
		return have
	}
	stateSet := vm.ActivePrecompiledContracts(cfg.Rules(stateNum, stateMerge, stateTime))
	execSet := vm.ActivePrecompiledContracts(cfg.Rules(execNum, execMerge, execTime))
	for _, addr := range privacyPrecompileAddrs {
		_, inState := stateSet[addr]
		_, inExec := execSet[addr]
		if inState && inExec {
			continue
		}
		delete(have, addr)
	}
	return have
}
