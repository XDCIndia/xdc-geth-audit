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

package params

import (
	"fmt"
	"math/big"
	"reflect"
)

// xdcForkBlocks lists the XDC fork blocks that checkCompatible (upstream forks
// only) does not compare. SigningTxBound, ValidatorOrder and ParoleDedup are
// compared there already.
var xdcForkBlocks = []struct {
	name string
	get  func(*ChainConfig) *big.Int
}{
	{"Eip1559Block", func(c *ChainConfig) *big.Int { return c.Eip1559Block }},
	{"CancunBlock", func(c *ChainConfig) *big.Int { return c.CancunBlock }},
	{"PragueBlock", func(c *ChainConfig) *big.Int { return c.PragueBlock }},
	{"ShanghaiBlock", func(c *ChainConfig) *big.Int { return c.ShanghaiBlock }},
	{"MergeBlock", func(c *ChainConfig) *big.Int { return c.MergeBlock }},
	{"TIPFixedRewardBlock", func(c *ChainConfig) *big.Int { return c.TIPFixedRewardBlock }},
	{"TIPUpgradePenaltyBlock", func(c *ChainConfig) *big.Int { return c.TIPUpgradePenaltyBlock }},
	{"TIPProportionalRewardBlock", func(c *ChainConfig) *big.Int { return c.TIPProportionalRewardBlock }},
	{"Gas2500xBlock", func(c *ChainConfig) *big.Int { return c.Gas2500xBlock }},
	{"TIPSigningBlock", func(c *ChainConfig) *big.Int { return c.TIPSigningBlock }},
	{"TIP2019Block", func(c *ChainConfig) *big.Int { return c.TIP2019Block }},
	{"TIPRandomizeBlock", func(c *ChainConfig) *big.Int { return c.TIPRandomizeBlock }},
	{"TIPIncreaseMasternodesBlock", func(c *ChainConfig) *big.Int { return c.TIPIncreaseMasternodesBlock }},
	{"DenylistBlock", func(c *ChainConfig) *big.Int { return c.DenylistBlock }},
	{"TIPNoHalvingMNRewardBlock", func(c *ChainConfig) *big.Int { return c.TIPNoHalvingMNRewardBlock }},
	{"TIPXDCXBlock", func(c *ChainConfig) *big.Int { return c.TIPXDCXBlock }},
	{"TIPXDCXLendingBlock", func(c *ChainConfig) *big.Int { return c.TIPXDCXLendingBlock }},
	{"TIPXDCXCancellationFeeBlock", func(c *ChainConfig) *big.Int { return c.TIPXDCXCancellationFeeBlock }},
	{"TIPTRC21FeeBlock", func(c *ChainConfig) *big.Int { return c.TIPTRC21FeeBlock }},
	{"Gas50xBlock", func(c *ChainConfig) *big.Int { return c.Gas50xBlock }},
	{"TIPXDCXMinerDisableBlock", func(c *ChainConfig) *big.Int { return c.TIPXDCXMinerDisableBlock }},
	{"TIPXDCXReceiverDisableBlock", func(c *ChainConfig) *big.Int { return c.TIPXDCXReceiverDisableBlock }},
	{"DynamicGasLimitBlock", func(c *ChainConfig) *big.Int { return c.DynamicGasLimitBlock }},
	{"TIPEpochHalvingBlock", func(c *ChainConfig) *big.Int { return c.TIPEpochHalvingBlock }},
}

// CheckXDCCompatible compares the XDC fork blocks and the XDPoS settings that
// change block validity (epoch, gap, V2 switch block and the V2 settings of
// rounds that are already passed) between the stored config and the new one.
// It returns an error when the new config moves something the chain has
// already passed.
//
// Only what the stored config has is compared: a field that is nil or zero in
// the stored config did not exist, or was not set, when the node wrote it, so a
// binary that sets it is not a mismatch. This is unlike upstream
// CheckCompatible, whose mismatch rewinds the chain: for these fields a rewind
// could mean millions of blocks after a release that only added a field, so
// the caller refuses to start with the returned error instead.
func (c *ChainConfig) CheckXDCCompatible(newcfg *ChainConfig, head *big.Int) error {
	for _, f := range xdcForkBlocks {
		if err := checkStoredForkBlock(f.name, f.get(c), f.get(newcfg), head); err != nil {
			return err
		}
	}
	if c.XDPoS == nil || newcfg.XDPoS == nil {
		return nil
	}
	s, n := c.XDPoS, newcfg.XDPoS
	if s.Epoch != 0 && s.Epoch != n.Epoch {
		return fmt.Errorf("XDPoS epoch changed from %d to %d", s.Epoch, n.Epoch)
	}
	if s.Gap != 0 && s.Gap != n.Gap {
		return fmt.Errorf("XDPoS gap changed from %d to %d", s.Gap, n.Gap)
	}
	if s.V2 == nil || n.V2 == nil {
		return nil
	}
	if err := checkStoredForkBlock("XDPoS V2 switch block", s.V2.SwitchBlock, n.V2.SwitchBlock, head); err != nil {
		return err
	}
	if err := checkStoredForkBlock("XDPoS V2 BALSealBlock", s.V2.BALSealBlock, n.V2.BALSealBlock, head); err != nil {
		return err
	}
	if err := checkStoredForkBlock("XDPoS V2 RoundSkipTCBlock", s.V2.RoundSkipTCBlock, n.V2.RoundSkipTCBlock, head); err != nil {
		return err
	}
	// A V2 block raises the round by at least one, so round(head) is at least
	// head - switch block: every entry at or below that is certainly passed.
	if s.V2.SwitchBlock == nil || head == nil || head.Cmp(s.V2.SwitchBlock) <= 0 {
		return nil
	}
	passed := new(big.Int).Sub(head, s.V2.SwitchBlock).Uint64()
	for round, sc := range s.V2.AllConfigs {
		if round > passed || sc == nil {
			continue
		}
		nc := n.V2.AllConfigs[round]
		if nc == nil {
			return fmt.Errorf("XDPoS V2 config of round %d was removed", round)
		}
		if field, differs := storedFieldDiffers(reflect.ValueOf(*sc), reflect.ValueOf(*nc)); differs {
			return fmt.Errorf("XDPoS V2 config of round %d changed in %s", round, field)
		}
	}
	return nil
}

// checkStoredForkBlock reports a stored fork block that the new config moves,
// removes or adds where either one is already passed. A nil stored value is not
// compared.
func checkStoredForkBlock(name string, stored, next, head *big.Int) error {
	if stored == nil || !isForkBlockIncompatible(stored, next, head) {
		return nil
	}
	return fmt.Errorf("%s changed from %v to %v with the chain head at block %v", name, stored, next, head)
}

// storedFieldDiffers compares two values of the same struct type field by
// field and skips the fields that are zero in the stored one.
func storedFieldDiffers(stored, next reflect.Value) (string, bool) {
	for i := 0; i < stored.NumField(); i++ {
		sf, nf := stored.Field(i), next.Field(i)
		if !sf.CanInterface() || sf.IsZero() {
			continue
		}
		if !reflect.DeepEqual(sf.Interface(), nf.Interface()) {
			return stored.Type().Field(i).Name, true
		}
	}
	return "", false
}
