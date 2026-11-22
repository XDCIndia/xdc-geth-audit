// Copyright (c) 2026 XDC Network

package ethapi

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

func TestF202620591OverrideCannotEnablePrivacyPrecompiles(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:                  big.NewInt(50),
		ByzantiumBlock:           big.NewInt(0),
		ConstantinopleBlock:      big.NewInt(0),
		PetersburgBlock:          big.NewInt(0),
		IstanbulBlock:            big.NewInt(0),
		BerlinBlock:              big.NewInt(0),
		TIPXDCXMinerDisableBlock: big.NewInt(100),
		XDPoS:                    &params.XDPoSConfig{Period: 2},
	}
	ring := common.BytesToAddress([]byte{0x1e})
	bullet := common.BytesToAddress([]byte{0x28})
	inside := vm.ActivePrecompiledContracts(cfg.Rules(big.NewInt(50), false, 0))
	if _, ok := inside[ring]; !ok {
		t.Fatal("privacy precompile missing inside the window")
	}
	outside := vm.ActivePrecompiledContracts(cfg.Rules(big.NewInt(100), false, 0))
	if _, ok := outside[ring]; ok {
		t.Fatal("privacy precompile active outside the window")
	}
	// State is past the window. The override number is inside it.
	got := limitPrivacyPrecompiles(cfg, vm.ActivePrecompiledContracts(cfg.Rules(big.NewInt(50), false, 0)), big.NewInt(100), false, 0, big.NewInt(50), false, 0)
	if _, ok := got[ring]; ok {
		t.Fatal("override enabled 0x1e")
	}
	if _, ok := got[bullet]; ok {
		t.Fatal("override enabled 0x28")
	}
	// State is inside the window. The override number has left it.
	got = limitPrivacyPrecompiles(cfg, vm.ActivePrecompiledContracts(cfg.Rules(big.NewInt(150), false, 0)), big.NewInt(50), false, 0, big.NewInt(150), false, 0)
	if _, ok := got[ring]; ok {
		t.Fatal("override kept 0x1e outside the state window")
	}
	// No override: the set is the execution set.
	plain := vm.ActivePrecompiledContracts(cfg.Rules(big.NewInt(50), false, 0))
	got = limitPrivacyPrecompiles(cfg, plain, big.NewInt(50), false, 0, big.NewInt(50), false, 0)
	if _, ok := got[ring]; !ok {
		t.Fatal("matching headers dropped 0x1e")
	}
}
