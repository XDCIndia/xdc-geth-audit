// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// cpByNumberChain serves headers by number only (the canonical chain).
type cpByNumberChain map[uint64]*types.Header

func (c cpByNumberChain) Config() *params.ChainConfig                 { return &params.ChainConfig{} }
func (c cpByNumberChain) CurrentHeader() *types.Header                { return nil }
func (c cpByNumberChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c cpByNumberChain) GetHeaderByNumber(n uint64) *types.Header    { return c[n] }
func (c cpByNumberChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// TestForgedCheckpointDoesNotPoisonMasternodes: looking up masternodes for a
// forged side-chain checkpoint at N must not change the list later returned for
// the canonical chain at N+1 (#1406).
func TestForgedCheckpointDoesNotPoisonMasternodes(t *testing.T) {
	checkpoint := func(signer common.Address) *types.Header {
		extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
		copy(extra[extraVanity:], signer[:])
		return &types.Header{Number: big.NewInt(int64(testConfig.Epoch)), Extra: extra}
	}
	canonicalMN, forgedMN := mn(0xaa), mn(0xbb)
	canonical, forged := checkpoint(canonicalMN), checkpoint(forgedMN)
	chain := cpByNumberChain{testConfig.Epoch: canonical}

	engine := New(testConfig, rawdb.NewMemoryDatabase())
	if got := engine.GetMasternodesWithParents(chain, forged, nil); len(got) != 1 || got[0] != forgedMN {
		t.Fatalf("forged checkpoint list: got %v", got)
	}
	next := &types.Header{Number: big.NewInt(int64(testConfig.Epoch + 1))}
	if got := engine.GetMasternodesWithParents(chain, next, nil); len(got) != 1 || got[0] != canonicalMN {
		t.Fatalf("canonical lookup after forged checkpoint: got %v, want [%v]", got, canonicalMN)
	}
}
