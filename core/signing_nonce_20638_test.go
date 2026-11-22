// Copyright (c) 2026 XDC Network

package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestF202620638StaleSigningNonceIsTooLow(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	from := crypto.PubkeyToAddress(key.PublicKey)
	statedb.SetNonce(from, 2, tracing.NonceChangeUnspecified)
	cfg := &params.ChainConfig{ChainID: big.NewInt(50), ByzantiumBlock: big.NewInt(0)}
	tx := signedTo(t, key, 1, common.BlockSignersBinary, cfg)
	_, err = ApplySigningTransaction(cfg, statedb, big.NewInt(1), common.Hash{1}, tx, 0)
	if !errors.Is(err, ErrNonceTooLow) {
		t.Fatalf("err = %v, want ErrNonceTooLow", err)
	}
	if statedb.GetNonce(from) != 2 {
		t.Fatalf("nonce changed to %d", statedb.GetNonce(from))
	}
}

func signedTo(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, to common.Address, cfg *params.ChainConfig) *types.Transaction {
	t.Helper()
	raw := types.NewTransaction(nonce, to, big.NewInt(0), 0, big.NewInt(0), nil)
	tx, err := types.SignTx(raw, types.LatestSigner(cfg), key)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
