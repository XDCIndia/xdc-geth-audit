// Copyright (c) 2026 XDC Network

package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestF202619459EmptyRouteNonceOnDevnetChains(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	dev := &params.ChainConfig{ChainID: big.NewInt(5551), ByzantiumBlock: big.NewInt(0)}
	mainnet := &params.ChainConfig{ChainID: big.NewInt(50), ByzantiumBlock: big.NewInt(0)}
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	tx := emptySigned(t, key, dev)
	if _, err = ApplyEmptyTransaction(mainnet, statedb, big.NewInt(1), common.Hash{1}, tx, 0); err != nil {
		t.Fatal(err)
	}
	if statedb.GetNonce(from) != 0 {
		t.Fatal("mainnet empty route changed the nonce")
	}
	if _, err = ApplyEmptyTransaction(dev, statedb, big.NewInt(1), common.Hash{1}, tx, 0); err != nil {
		t.Fatal(err)
	}
	if statedb.GetNonce(from) != 1 {
		t.Fatalf("devnet nonce = %d, want 1", statedb.GetNonce(from))
	}
	if _, err = ApplyEmptyTransaction(dev, statedb, big.NewInt(1), common.Hash{1}, tx, 0); !errors.Is(err, ErrNonceTooLow) {
		t.Fatalf("repeat err = %v, want ErrNonceTooLow", err)
	}
}

func emptySigned(t *testing.T, key *ecdsa.PrivateKey, cfg *params.ChainConfig) *types.Transaction {
	t.Helper()
	raw := types.NewTransaction(0, common.TradingStateAddrBinary, big.NewInt(0), 0, big.NewInt(0), nil)
	tx, err := types.SignTx(raw, types.LatestSigner(cfg), key)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
