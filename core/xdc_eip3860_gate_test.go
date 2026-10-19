// Copyright 2026 XDC Network

package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

// TestXDCEIP3860GatedOnEIP1559: on XDC the EIP-3860 initcode word cost and the
// initcode size limit start at Eip1559Block, not at London/Shanghai (canonical
// XDPoSChain IntrinsicGas(..., rules.IsEIP1559) and CheckMaxInitCodeSize). On
// mainnet London is 76,321,000 and EIP-1559 98,800,200 (#842).
func TestXDCEIP3860GatedOnEIP1559(t *testing.T) {
	cfg := params.XDCMainnetChainConfig
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	initcode := make([]byte, 47) // 2 words, all zero bytes
	base := params.TxGasContractCreation + 47*params.TxDataZeroGas
	for _, tc := range []struct {
		num      int64
		words    bool
		sizeErrs bool
	}{
		{76_321_136, false, false}, // London..EIP-1559 (#842 block)
		{98_800_200, true, true},   // EIP-1559
	} {
		rules := cfg.Rules(big.NewInt(tc.num), true, 0)
		gas, err := IntrinsicGas(initcode, nil, nil, from, nil, nil, rules)
		if err != nil {
			t.Fatal(err)
		}
		want := base
		if tc.words {
			want += 2 * params.InitCodeWordGas
		}
		if gas != want {
			t.Errorf("block %d: intrinsic gas %d, want %d", tc.num, gas, want)
		}
		err = vm.CheckMaxInitCodeSize(&rules, params.MaxInitCodeSize+1)
		if got := errors.Is(err, vm.ErrMaxInitCodeSizeExceeded); got != tc.sizeErrs {
			t.Errorf("block %d: initcode size limit enforced = %v, want %v", tc.num, got, tc.sizeErrs)
		}
	}
}
