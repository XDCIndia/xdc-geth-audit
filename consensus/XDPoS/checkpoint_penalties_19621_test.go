// Copyright (c) 2026 XDC Network
// F-2026-19621: a V1 checkpoint's penalty list is not part of the seal hash.
// Two headers that differ only in Penalties share a seal. Full verification on
// chain 50 (and any non-Apothem chain, including net 2040) still rejects a list
// that disagrees with the penalty hook. The compare is skipped on chain 51, on
// the bulk-sync light path, and when the hook has no state. Those skips stay:
// Apothem history does not reproduce the live hook, and failing closed without
// state wedges a non-archive node. Putting Penalties into the seal would
// invalidate every historical V1 seal.

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestF202619621PenaltiesOutsideSeal(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sealer := crypto.PubkeyToAddress(key.PublicKey)
	penA, penB := mn(0xb1), mn(0xb2)

	base := &types.Header{
		Number:     big.NewInt(900),
		Time:       1000,
		Extra:      make([]byte, extraVanity+common.AddressLength+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Difficulty: big.NewInt(1),
		Penalties:  append([]byte(nil), penA[:]...),
		Coinbase:   sealer,
	}
	copy(base.Extra[extraVanity:], sealer[:])
	sig, err := crypto.Sign(sigHash(base).Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	copy(base.Extra[len(base.Extra)-extraSeal:], sig)

	twin := types.CopyHeader(base)
	twin.Penalties = append([]byte(nil), penB[:]...)
	if sigHash(base) != sigHash(twin) || SealHash(base) != SealHash(twin) {
		t.Fatal("V1 seal hash changed when only Penalties changed")
	}
	got, err := ecrecover(twin, lru.NewCache[common.Hash, common.Address](8))
	if err != nil {
		t.Fatal(err)
	}
	if got != sealer {
		t.Fatalf("seal over one penalty list recovered %s for a different list", got.Hex())
	}

	changed := types.CopyHeader(base)
	changed.Extra = append([]byte(nil), base.Extra...)
	changed.Extra[0] ^= 0xff
	if sigHash(base) == sigHash(changed) {
		t.Fatal("flipping a vanity byte left the seal hash unchanged")
	}
}
