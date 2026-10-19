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

package XDPoS

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// commonXDCGlobals renders the common-package globals that CopyXDCConstants sets.
func commonXDCGlobals() string {
	return fmt.Sprintf("trc21Issuer=%s gas50x=%v tipSigning=%v tipTRC21Fee=%v noHalving=%v cancelFee=%v xdcx=%v lending=%v minerDisable=%v receiverDisable=%v blacklist=%d tip2019=%v randomize=%v",
		common.TRC21IssuerSMC.Hex(), common.BlockNumberGas50x, common.TIPSigning, common.TIPTRC21Fee, common.TIPNoHalvingMNReward,
		common.TIPXDCXCancellationFee, common.TIPXDCX, common.TIPXDCXLending, common.TIPXDCXMinerDisable, common.TIPXDCXReceiverDisable,
		common.BlackListHFNumber, common.TIP2019Block, common.TIPRandomize)
}

// saveCommonXDCGlobals returns a func that restores the globals CopyXDCConstants sets.
func saveCommonXDCGlobals() func() {
	trc21, gas50x, signing, trc21Fee, noHalving := common.TRC21IssuerSMC, common.BlockNumberGas50x, common.TIPSigning, common.TIPTRC21Fee, common.TIPNoHalvingMNReward
	cancelFee, xdcx, lending, minerDis, recvDis := common.TIPXDCXCancellationFee, common.TIPXDCX, common.TIPXDCXLending, common.TIPXDCXMinerDisable, common.TIPXDCXReceiverDisable
	blacklist, tip2019, randomize := common.BlackListHFNumber, common.TIP2019Block, common.TIPRandomize
	return func() {
		common.TRC21IssuerSMC, common.BlockNumberGas50x, common.TIPSigning, common.TIPTRC21Fee, common.TIPNoHalvingMNReward = trc21, gas50x, signing, trc21Fee, noHalving
		common.TIPXDCXCancellationFee, common.TIPXDCX, common.TIPXDCXLending, common.TIPXDCXMinerDisable, common.TIPXDCXReceiverDisable = cancelFee, xdcx, lending, minerDis, recvDis
		common.BlackListHFNumber, common.TIP2019Block, common.TIPRandomize = blacklist, tip2019, randomize
	}
}

// TestDevnet551ConstantsMatchCanonical checks the chain-551 network constants
// (consensus/XDPoS and common) against XinFinOrg/XDPoSChain v2.9.3-testnet
// params/config_networks.go:471 DevnetChainConfig.
func TestDevnet551ConstantsMatchCanonical(t *testing.T) {
	t.Cleanup(saveCommonXDCGlobals())
	t.Cleanup(func() { SetNetworkConstants(50) })

	SetNetworkConstants(551)
	c := GetCurrentConstants()
	for _, tc := range []struct {
		name string
		got  *big.Int
		want int64
	}{
		{"TIP2019Block", c.TIP2019Block, 0},
		{"TIPSigning", c.TIPSigning, 0},
		{"TIPRandomize", c.TIPRandomize, 0},
		{"TIPNoHalvingMNReward", c.TIPNoHalvingMNReward, 0},
		{"TIPXDCX", c.TIPXDCX, 0},
		{"TIPXDCXLending", c.TIPXDCXLending, 0},
		{"TIPXDCXCancellationFee", c.TIPXDCXCancellationFee, 0},
		{"TIPTRC21Fee", c.TIPTRC21Fee, 0},
		{"TIPIncreaseMasternodes", c.TIPIncreaseMasternodes, 0},
		{"TIPBerlinBlock", c.TIPBerlinBlock, 0},
		{"TIPLondonBlock", c.TIPLondonBlock, 0},
		{"TIPMergeBlock", c.TIPMergeBlock, 0},
		{"TIPShanghaiBlock", c.TIPShanghaiBlock, 0},
		{"BlockNumberGas50x", c.BlockNumberGas50x, 0},
		{"TIPV2SwitchBlock", c.TIPV2SwitchBlock, 2700},
		{"TIPXDCXMinerDisable", c.TIPXDCXMinerDisable, 0},
		{"TIPXDCXReceiverDisable", c.TIPXDCXReceiverDisable, 0},
		{"TIPEIP1559Block", c.TIPEIP1559Block, 25000},
		{"TIPCancunBlock", c.TIPCancunBlock, 25000},
		{"TIPUpgradeReward", c.TIPUpgradeReward, 50000},
		{"TIPUpgradePenalty", c.TIPUpgradePenalty, 50000},
		{"TIPEpochHalving", c.TIPEpochHalving, 9999999999}, // canonical nil: never
		{"global TIPV2SwitchBlock", GetTIPV2SwitchBlock(), 2700},
	} {
		if tc.got == nil || tc.got.Int64() != tc.want {
			t.Errorf("551 %s = %v, want %d", tc.name, tc.got, tc.want)
		}
	}
	if c.BlackListHFNumber != 0 {
		t.Errorf("551 BlackListHFNumber = %d, want 0", c.BlackListHFNumber)
	}
	if c.MaxMasternodesV2 != 18 || GetMaxMasternodesV2() != 18 {
		t.Errorf("551 MaxMasternodesV2 = %d (global %d), want 18", c.MaxMasternodesV2, GetMaxMasternodesV2())
	}

	common.CopyXDCConstants(551)
	const want = "trc21Issuer=0x8c0faeb5C6bEd2129b8674F262Fd45c4e9468bee gas50x=0 tipSigning=0 tipTRC21Fee=0 noHalving=0 cancelFee=0 xdcx=0 lending=0 minerDisable=0 receiverDisable=0 blacklist=0 tip2019=0 randomize=0"
	if got := commonXDCGlobals(); got != want {
		t.Errorf("551 common constants:\n have %s\n want %s", got, want)
	}
}

// TestRegisteredNetworkConstantsUnchanged pins the mainnet (50), Apothem (51)
// and legacy devnet (5551) constants, so a 551 change cannot move them.
func TestRegisteredNetworkConstantsUnchanged(t *testing.T) {
	restore := saveCommonXDCGlobals()
	t.Cleanup(restore)
	t.Cleanup(func() { SetNetworkConstants(50) })

	for _, tc := range []struct {
		id         uint64
		nc         *NetworkConstants
		wantNC     string
		wantCommon string
	}{
		{50, mainnetConstants,
			"&{1 3000000 3464000 38383838 38383838 38383838 38383838 38383838 5000000 76321000 76321000 76321000 76321000 80370000 80370000 80370000 80370900 98800200 98802000 9999999999 9999999999 9999999999 38383838 108}",
			"trc21Issuer=0x8c0faeb5C6bEd2129b8674F262Fd45c4e9468bee gas50x=80370000 tipSigning=3000000 tipTRC21Fee=38383838 noHalving=38383838 cancelFee=38383838 xdcx=38383838 lending=38383838 minerDisable=80370000 receiverDisable=80370900 blacklist=38383838 tip2019=1 randomize=3464000"},
		{51, apothemConstants,
			"&{1 3000000 3464000 23779191 23779191 23779191 23779191 23779191 5000000 61290000 61290000 61290000 61290000 56828700 56828700 61290000 66825000 71550000 71551800 83600000 83600000 9999999999 23779191 15}",
			"trc21Issuer=0x0E2C88753131CE01c7551B726b28BFD04e44003F gas50x=56828700 tipSigning=3000000 tipTRC21Fee=23779191 noHalving=23779191 cancelFee=23779191 xdcx=23779191 lending=23779191 minerDisable=61290000 receiverDisable=66825000 blacklist=23779191 tip2019=1 randomize=3464000"},
		{5551, devnetConstants,
			"&{0 0 0 0 0 0 0 0 0 250000 250000 0 0 0 2700 0 0 250000 250000 5000000 5000000 9223372036854775807 0 108}",
			"trc21Issuer=0x8c0faeb5C6bEd2129b8674F262Fd45c4e9468bee gas50x=0 tipSigning=0 tipTRC21Fee=0 noHalving=0 cancelFee=0 xdcx=0 lending=0 minerDisable=0 receiverDisable=100000000000000000 blacklist=0 tip2019=0 randomize=3464000"},
	} {
		SetNetworkConstants(tc.id)
		if GetCurrentConstants() != tc.nc {
			t.Errorf("chain %d: SetNetworkConstants selected another profile", tc.id)
		}
		if got := fmt.Sprintf("%v", tc.nc); got != tc.wantNC {
			t.Errorf("chain %d constants changed:\n have %s\n want %s", tc.id, got, tc.wantNC)
		}
		restore()
		common.CopyXDCConstants(tc.id)
		if got := commonXDCGlobals(); got != tc.wantCommon {
			t.Errorf("chain %d common constants changed:\n have %s\n want %s", tc.id, got, tc.wantCommon)
		}
	}
}
