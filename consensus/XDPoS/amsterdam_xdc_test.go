// Copyright 2026 The XDC Network Authors
// Behavioural tests for the Amsterdam placeholder-field seal rules (issue #1536).

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// xdcAmsterdamCfg returns a minimal XDC ChainConfig with Amsterdam active at block 100.
func xdcAmsterdamCfg() *params.ChainConfig {
	cfg := params.ChainConfig{
		ChainID:             big.NewInt(20999),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		Eip1559Block:        big.NewInt(0),
		CancunBlock:         big.NewInt(0),
		PragueBlock:         big.NewInt(0),
		OsakaBlock:          big.NewInt(0),
		AmsterdamBlock:      big.NewInt(100),
		XDPoS: &params.XDPoSConfig{
			Period: 2,
			Epoch:  900,
		},
		BlobScheduleConfig: nil, // XDC: no blob transactions
	}
	return &cfg
}

// TestU6b_XDCAmsterdamUnsealedExcessBlobGas verifies that verifyNoUnsealedFields
// rejects an Amsterdam header where ExcessBlobGas != 0 (must be ptr(0) per the
// canonical XDC Amsterdam placeholder rule).
func TestU6b_XDCAmsterdamUnsealedExcessBlobGas(t *testing.T) {
	cfg := xdcAmsterdamCfg()
	zeroHash := common.Hash{}
	blobZero := uint64(0)
	balHash := common.Hash{0x01}

	// Valid Amsterdam header.
	validHdr := &types.Header{
		Number:              big.NewInt(100),
		Difficulty:          big.NewInt(1),
		WithdrawalsHash:     &types.EmptyWithdrawalsHash,
		BlobGasUsed:         &blobZero,
		ExcessBlobGas:       &blobZero,
		ParentBeaconRoot:    &zeroHash,
		RequestsHash:        &types.EmptyRequestsHash,
		BlockAccessListHash: &balHash,
	}
	if err := verifyNoUnsealedFields(cfg, validHdr); err != nil {
		t.Fatalf("valid Amsterdam header: unexpected error: %v", err)
	}

	// Tampered: ExcessBlobGas=1 — must be rejected.
	blobOne := uint64(1)
	badHdr := *validHdr
	badHdr.ExcessBlobGas = &blobOne
	if err := verifyNoUnsealedFields(cfg, &badHdr); err != errUnsealedHeaderFields {
		t.Errorf("ExcessBlobGas=1: want errUnsealedHeaderFields, got %v", err)
	}
}

// TestU6b_XDCAmsterdamUnsealedNilBALHash verifies that verifyNoUnsealedFields
// rejects an Amsterdam header where BlockAccessListHash is nil.
func TestU6b_XDCAmsterdamUnsealedNilBALHash(t *testing.T) {
	cfg := xdcAmsterdamCfg()
	zeroHash := common.Hash{}
	blobZero := uint64(0)

	hdr := &types.Header{
		Number:           big.NewInt(100),
		Difficulty:       big.NewInt(1),
		WithdrawalsHash:  &types.EmptyWithdrawalsHash,
		BlobGasUsed:      &blobZero,
		ExcessBlobGas:    &blobZero,
		ParentBeaconRoot: &zeroHash,
		RequestsHash:     &types.EmptyRequestsHash,
		// BlockAccessListHash deliberately nil
	}
	if err := verifyNoUnsealedFields(cfg, hdr); err != errUnsealedHeaderFields {
		t.Errorf("nil BlockAccessListHash: want errUnsealedHeaderFields, got %v", err)
	}
}
