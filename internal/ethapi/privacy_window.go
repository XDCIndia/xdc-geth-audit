// Copyright (c) 2026 XDC Network

package ethapi

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

// maxRPCBulletproofComms bounds the commitments a bulletproof proof may carry
// in eth_call, eth_estimateGas, eth_simulateV1 and debug_traceCall. 256 is
// well above any proof seen on chain. F-2026-20591.
const maxRPCBulletproofComms = 256

var bulletproofAddr = common.BytesToAddress([]byte{0x28})

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
	if have == nil {
		return have
	}
	// RPC execution only: cap the bulletproof commitment count. This runs
	// before the early return so it also covers calls with no override.
	// Consensus execution does not pass through here and stays uncapped.
	if _, ok := have[bulletproofAddr]; ok {
		have[bulletproofAddr] = vm.CappedBulletproofVerifier(maxRPCBulletproofComms)
	}
	if cfg == nil || stateNum == nil || execNum == nil {
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

// LimitPrivacyPrecompiles is limitPrivacyPrecompiles for callers outside this
// package, such as debug_traceCall.
func LimitPrivacyPrecompiles(cfg *params.ChainConfig, have vm.PrecompiledContracts, stateNum *big.Int, stateMerge bool, stateTime uint64, execNum *big.Int, execMerge bool, execTime uint64) vm.PrecompiledContracts {
	return limitPrivacyPrecompiles(cfg, have, stateNum, stateMerge, stateTime, execNum, execMerge, execTime)
}
