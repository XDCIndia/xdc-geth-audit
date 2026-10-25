// Copyright 2026 XDC Network
// Tests for the hard-pinned 420M block gas limit (xdcGasCeil / params.XDCBlockGasLimit).

package miner

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func TestXdcGasCeil_HardPin420M(t *testing.T) {
	if params.XDCBlockGasLimit != 420_000_000 {
		t.Fatalf("params.XDCBlockGasLimit = %d, want 420000000", params.XDCBlockGasLimit)
	}
	w := &XdcWorker{} // receiver is intentionally unused by xdcGasCeil
	cases := []struct {
		name   string
		parent uint64
		want   uint64
	}{
		{"at plateau", 420_000_000, 420_000_000},
		{"below plateau (upstream 60M default)", 60_000_000, 420_000_000},
		{"genesis-ish low (apothem genesis 4.7M)", 4_712_388, 420_000_000},
		{"zero parent", 0, 420_000_000},
		{"above plateau -> hold parent (anti-#952 down-step guard)", 500_000_000, 500_000_000},
	}
	for _, c := range cases {
		if got := w.xdcGasCeil(c.parent); got != c.want {
			t.Errorf("%s: xdcGasCeil(%d) = %d, want %d", c.name, c.parent, got, c.want)
		}
	}
}

// Steady state: with the hard-pinned target, core.CalcGasLimit holds 420M flat
// block-over-block — the invariant #952 violated when the target was 60M.
func TestXdcGasCeil_HoldsPlateauFlat(t *testing.T) {
	w := &XdcWorker{}
	const plateau = 420_000_000
	gl := uint64(plateau)
	for i := 0; i < 64; i++ {
		gl = core.CalcGasLimit(gl, w.xdcGasCeil(gl))
		if gl != plateau {
			t.Fatalf("gas limit drifted off plateau after %d blocks: %d (want %d)", i+1, gl, plateau)
		}
	}
}

// A minter that (mis)starts at the upstream 60M default must CLIMB back toward
// 420M via CalcGasLimit's 1/1024 ramp, never hone DOWN, never overshoot — the
// exact opposite of the #952 failure where a 60M target honed the chain down.
func TestXdcGasCeil_ClimbsFromLowNeverDown(t *testing.T) {
	w := &XdcWorker{}
	const plateau = 420_000_000
	gl := uint64(60_000_000)
	prev := gl
	for i := 0; i < 100_000; i++ {
		gl = core.CalcGasLimit(gl, w.xdcGasCeil(gl))
		if gl < prev {
			t.Fatalf("gas limit honed DOWN at block %d: %d < %d", i+1, gl, prev)
		}
		if gl > plateau {
			t.Fatalf("gas limit overshot plateau at block %d: %d", i+1, gl)
		}
		prev = gl
		if gl == plateau {
			t.Logf("climbed 60M -> 420M plateau in %d blocks (1/1024 ramp), then holds", i+1)
			return
		}
	}
	t.Fatalf("did not reach plateau within 100000 blocks; stuck at %d", gl)
}

// A chain that sets DynamicGasLimitBlock seals like canonical XDPoSChain
// (v2.9.3 miner/worker.go:844-848): the static ceiling before the fork, then
// core.CalcGasLimit toward the ceiling. Values are from net2012 (fork 4500,
// ceiling raised to 500M): block 4628 must rise to 448,821,589, not stay flat.
func TestXdcGasLimit_DynamicGasLimitBlock(t *testing.T) {
	gl := func(dyn *big.Int, number, parent uint64) uint64 {
		w := &XdcWorker{miner: &Miner{
			config:      &Config{GasCeil: 500_000_000},
			chainConfig: &params.ChainConfig{DynamicGasLimitBlock: dyn},
		}}
		return w.xdcGasLimit(&types.Header{Number: new(big.Int).SetUint64(number - 1), GasLimit: parent})
	}
	cases := []struct {
		name                 string
		dyn                  *big.Int
		number, parent, want uint64
	}{
		{"after fork: CalcGasLimit toward ceiling", big.NewInt(4500), 4628, 448_383_716, 448_821_589},
		{"at fork block", big.NewInt(4500), 4500, 420_000_000, 420_410_155},
		{"before fork: static ceiling", big.NewInt(4500), 4499, 420_000_000, 500_000_000},
		{"no DynamicGasLimitBlock: 420M plateau rule", nil, 4628, 448_383_716, 448_383_716},
	}
	for _, c := range cases {
		if got := gl(c.dyn, c.number, c.parent); got != c.want {
			t.Errorf("%s: gas limit = %d, want %d", c.name, got, c.want)
		}
	}
}

// Apothem seals like canonical XDPoSChain v2.9.3-testnet, whose
// genesis/testnet.json sets dynamicGasLimitBlock 83,600,000 with the v2.9
// reset: the static ceiling before it, then core.CalcGasLimit (#1539).
func TestXdcGasLimit_ApothemDynamicGasLimitBlock(t *testing.T) {
	w := &XdcWorker{miner: &Miner{
		config:      &Config{GasCeil: 500_000_000},
		chainConfig: params.XDCApothemChainConfig,
	}}
	gl := func(number, parent uint64) uint64 {
		return w.xdcGasLimit(&types.Header{Number: new(big.Int).SetUint64(number - 1), GasLimit: parent})
	}
	if got, want := gl(83_599_999, 420_000_000), uint64(500_000_000); got != want {
		t.Errorf("before 83,600,000: gas limit = %d, want the static ceiling %d", got, want)
	}
	if got, want := gl(83_600_000, 420_000_000), uint64(420_410_155); got != want {
		t.Errorf("at 83,600,000: gas limit = %d, want CalcGasLimit %d", got, want)
	}
}
