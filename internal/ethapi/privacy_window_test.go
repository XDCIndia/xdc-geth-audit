// Copyright (c) 2026 XDC Network

package ethapi

import (
	"encoding/binary"
	"math/big"
	"strings"
	"testing"
	"time"

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

// F-2026-20591: RPC execution caps the bulletproof commitment count, with or
// without a block override. The consensus precompile set stays uncapped.
func TestF202620591RPCBulletproofCommitmentCap(t *testing.T) {
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
	rules := cfg.Rules(big.NewInt(50), false, 0)
	n := big.NewInt(50)
	proof := func(comms uint32) []byte {
		in := make([]byte, 4+33*int(comms))
		binary.BigEndian.PutUint32(in, comms)
		return in
	}
	run := func(set vm.PrecompiledContracts, in []byte) error {
		c := set[bulletproofAddr].(interface {
			RunAbort([]byte, func() bool) ([]byte, error)
		})
		_, err := c.RunAbort(in, func() bool { return false })
		return err
	}
	for name, call := range map[string]func(vm.PrecompiledContracts) vm.PrecompiledContracts{
		"no override": func(h vm.PrecompiledContracts) vm.PrecompiledContracts {
			return limitPrivacyPrecompiles(cfg, h, n, false, 0, n, false, 0)
		},
		"nil config": func(h vm.PrecompiledContracts) vm.PrecompiledContracts {
			return limitPrivacyPrecompiles(nil, h, nil, false, 0, nil, false, 0)
		},
	} {
		set := call(vm.ActivePrecompiledContracts(rules))
		start := time.Now()
		err := run(set, proof(257))
		if err == nil || !strings.Contains(err.Error(), "too many") {
			t.Fatalf("%s: 257 commitments not rejected by the cap: %v", name, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("%s: cap rejection took %v", name, d)
		}
		// 256 is within the cap, so any failure is a decode failure.
		if err := run(set, proof(256)); err == nil || strings.Contains(err.Error(), "too many") {
			t.Fatalf("%s: 256 commitments hit the cap: %v", name, err)
		}
	}
	// The consensus set is uncapped.
	if err := run(vm.ActivePrecompiledContracts(rules), proof(257)); err == nil || strings.Contains(err.Error(), "too many") {
		t.Fatalf("consensus precompile applied the cap: %v", err)
	}
}
