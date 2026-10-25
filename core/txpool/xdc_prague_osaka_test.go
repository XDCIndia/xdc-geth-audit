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

package txpool

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// xdcForkConfig is an XDC config with Prague at 100 and Osaka at 200, built
// from JSON so the test compiles on trees without the OsakaBlock field.
func xdcForkConfig(t *testing.T) *params.ChainConfig {
	t.Helper()
	cfg := new(params.ChainConfig)
	err := json.Unmarshal([]byte(`{"chainId":2002,"homesteadBlock":0,"eip150Block":0,"eip155Block":0,
		"eip158Block":0,"byzantiumBlock":0,"constantinopleBlock":0,"petersburgBlock":0,"istanbulBlock":0,
		"berlinBlock":0,"londonBlock":0,"eip1559Block":0,"cancunBlock":0,"pragueBlock":100,"osakaBlock":200,
		"xdpos":{"period":2,"epoch":900}}`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// xdcHead is an XDC head: non-zero difficulty, so the pool derives its rules
// with isMerge=false.
func xdcHead(n int64) *types.Header {
	return &types.Header{Number: big.NewInt(n), GasLimit: 50_000_000, BaseFee: big.NewInt(1), Difficulty: big.NewInt(1)}
}

func xdcPoolOpts(cfg *params.ChainConfig) *ValidationOptions {
	return &ValidationOptions{Config: cfg, Accept: 0xFF, MaxSize: 128 * 1024, MaxBlobCount: 6, MinTip: big.NewInt(0)}
}

// TestXDCPoolSetCodeFromPragueBlock: type 4 is rejected before pragueBlock and
// accepted from it with the pool's LatestSigner.
func TestXDCPoolSetCodeFromPragueBlock(t *testing.T) {
	cfg := xdcForkConfig(t)
	key, _ := crypto.GenerateKey()
	auth, _ := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(2002), Address: common.Address{1}})
	tx := types.MustSignNewTx(key, types.NewPragueSigner(cfg.ChainID), &types.SetCodeTx{
		ChainID: uint256.NewInt(2002), Gas: 100_000, GasFeeCap: uint256.NewInt(10), GasTipCap: uint256.NewInt(1),
		To: common.Address{2}, AuthList: []types.SetCodeAuthorization{auth},
	})
	signer := types.LatestSigner(cfg)
	if err := ValidateTransaction(tx, xdcHead(99), signer, xdcPoolOpts(cfg)); !errors.Is(err, core.ErrTxTypeNotSupported) {
		t.Errorf("type 4 before pragueBlock: got %v, want ErrTxTypeNotSupported", err)
	}
	if err := ValidateTransaction(tx, xdcHead(100), signer, xdcPoolOpts(cfg)); err != nil {
		t.Errorf("type 4 at pragueBlock: got %v, want accepted", err)
	}
}

// TestXDCPoolOsakaTxGasCap: from osakaBlock the pool rejects a tx above the
// EIP-7825 cap of 16,777,216 gas and accepts one at the cap.
func TestXDCPoolOsakaTxGasCap(t *testing.T) {
	cfg := xdcForkConfig(t)
	key, _ := crypto.GenerateKey()
	signer := types.LatestSigner(cfg)
	mk := func(gas uint64) *types.Transaction {
		return types.MustSignNewTx(key, signer, &types.LegacyTx{Gas: gas, GasPrice: big.NewInt(10), To: &common.Address{2}})
	}
	if err := ValidateTransaction(mk(params.MaxTxGas+1), xdcHead(199), signer, xdcPoolOpts(cfg)); err != nil {
		t.Errorf("gas cap+1 before osakaBlock: got %v, want accepted", err)
	}
	if err := ValidateTransaction(mk(params.MaxTxGas+1), xdcHead(200), signer, xdcPoolOpts(cfg)); !errors.Is(err, core.ErrGasLimitTooHigh) {
		t.Errorf("gas cap+1 at osakaBlock: got %v, want ErrGasLimitTooHigh", err)
	}
	if err := ValidateTransaction(mk(params.MaxTxGas), xdcHead(200), signer, xdcPoolOpts(cfg)); err != nil {
		t.Errorf("gas cap at osakaBlock: got %v, want accepted", err)
	}
}

// TestXDCPoolRejectsBlobTx: XDC has no blob transactions; the Prague signer
// can recover type 3, so the pool must reject it by type.
func TestXDCPoolRejectsBlobTx(t *testing.T) {
	cfg := xdcForkConfig(t)
	key, _ := crypto.GenerateKey()
	tx := types.MustSignNewTx(key, types.NewCancunSigner(cfg.ChainID), &types.BlobTx{
		ChainID: uint256.NewInt(2002), Gas: 21_000, GasFeeCap: uint256.NewInt(10), GasTipCap: uint256.NewInt(1),
		BlobFeeCap: uint256.NewInt(1), BlobHashes: []common.Hash{{0x01}},
	})
	err := ValidateTransaction(tx, xdcHead(100), types.LatestSigner(cfg), xdcPoolOpts(cfg))
	if !errors.Is(err, core.ErrTxTypeNotSupported) {
		t.Errorf("blob tx on XDC: got %v, want ErrTxTypeNotSupported", err)
	}
}
