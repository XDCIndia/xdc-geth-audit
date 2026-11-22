// Copyright (c) 2026 XDC Network
// F-2026-19615: V1 header verification does not bound the gas limit against
// the parent. Canonical XDPoSChain has the same rule. Every V1 block on
// mainnet (before 80,370,000), Apothem (before 56,828,700) and a V1 chain
// such as an unconfigured net 2040 is historical. A new bound would reject
// blocks those networks already imported, so this file only locks the
// current rule: a large gas-limit change is accepted, on the full path and
// on the bulk-sync path, which is what a current-main node accepts.

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func TestF202619615V1GasLimitUnbounded(t *testing.T) {
	net2040 := &params.ChainConfig{ChainID: big.NewInt(2040), XDPoS: testConfig}
	cases := []struct {
		name string
		cfg  *params.ChainConfig
		xdc  *params.XDPoSConfig
	}{
		{"mainnet", params.XDCMainnetChainConfig, params.XDCMainnetChainConfig.XDPoS},
		{"apothem", params.XDCApothemChainConfig, params.XDCApothemChainConfig.XDPoS},
		{"net2040", net2040, testConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := crypto.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			signer := crypto.PubkeyToAddress(key.PublicKey)
			extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
			copy(extra[extraVanity:], signer[:])
			genesis := &types.Header{
				Number:     big.NewInt(0),
				Difficulty: big.NewInt(1),
				Extra:      extra,
				UncleHash:  types.EmptyUncleHash,
				GasLimit:   420_000_000,
			}
			chain := &unsealedChain{cfg: tc.cfg, headers: map[uint64]*types.Header{0: genesis}}
			engine := New(tc.xdc, rawdb.NewMemoryDatabase())

			// Parent 420,000,000. Both a floor of 1 and a multi-billion limit
			// are outside the 1/1024 band V2 enforces.
			for _, limit := range []uint64{1, 8_000_000_000} {
				for _, bulk := range []bool{false, true} {
					h := &types.Header{
						ParentHash: genesis.Hash(),
						Number:     big.NewInt(1),
						Time:       genesis.Time + tc.xdc.Period,
						Extra:      make([]byte, extraVanity+extraSeal),
						UncleHash:  types.EmptyUncleHash,
						GasLimit:   limit,
						Difficulty: engine.calcDifficulty(chain, genesis, signer),
					}
					sig, err := crypto.Sign(sigHash(h).Bytes(), key)
					if err != nil {
						t.Fatal(err)
					}
					copy(h.Extra[len(h.Extra)-extraSeal:], sig)

					savedBulk := core.XdcBulkSyncMode.Load()
					savedForce := core.XdcForceFullVerify.Load()
					core.XdcBulkSyncMode.Store(bulk)
					core.XdcForceFullVerify.Store(false)
					_, results := engine.VerifyHeaders(chain, []*types.Header{h})
					err = <-results
					core.XdcBulkSyncMode.Store(savedBulk)
					core.XdcForceFullVerify.Store(savedForce)
					if err != nil {
						t.Fatalf("gas limit %d bulk=%v rejected on the V1 path: %v", limit, bulk, err)
					}
				}
			}

			// GasUsed above GasLimit is not a V1 header-consensus reject.
			// Block processing still cannot spend more gas than the limit.
			over := &types.Header{
				ParentHash: genesis.Hash(),
				Number:     big.NewInt(1),
				Time:       genesis.Time + tc.xdc.Period,
				Extra:      make([]byte, extraVanity+extraSeal),
				UncleHash:  types.EmptyUncleHash,
				GasLimit:   genesis.GasLimit,
				GasUsed:    genesis.GasLimit + 1,
				Difficulty: engine.calcDifficulty(chain, genesis, signer),
			}
			sig, err := crypto.Sign(sigHash(over).Bytes(), key)
			if err != nil {
				t.Fatal(err)
			}
			copy(over.Extra[len(over.Extra)-extraSeal:], sig)
			if err := engine.VerifyHeader(chain, over); err != nil {
				t.Fatalf("V1 verifyHeader rejected GasUsed above GasLimit: %v", err)
			}
		})
	}
}
