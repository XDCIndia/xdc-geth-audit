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
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
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

// penaltyHeader seals a checkpoint. The parent snapshot is [sealer, other].
// The hook reports [other] unless unavailable is set. penalize selects the
// header penalty: "other" (the computed list), "stranger", or "sealer".
// listSealer writes the sealer into the extra; otherwise the extra lists other.
func penaltyHeader(t *testing.T, chainID int64, bulk, unavailable bool, penalize string, listSealer bool) error {
	t.Helper()
	savedTIP := common.TIPSigning
	common.TIPSigning = big.NewInt(3000000)
	t.Cleanup(func() { common.TIPSigning = savedTIP })
	savedBulk := core.XdcBulkSyncMode.Load()
	savedForce := core.XdcForceFullVerify.Load()
	core.XdcBulkSyncMode.Store(bulk)
	core.XdcForceFullVerify.Store(false)
	t.Cleanup(func() {
		core.XdcBulkSyncMode.Store(savedBulk)
		core.XdcForceFullVerify.Store(savedForce)
	})

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sealer := crypto.PubkeyToAddress(key.PublicKey)
	other := mn(0xb1)
	stranger := mn(0xb2)
	chain := cpPenaltiesChain{&params.ChainConfig{ChainID: big.NewInt(chainID), XDPoS: testConfig}}
	engine := New(testConfig, rawdb.NewMemoryDatabase())
	var hookErr error
	if unavailable {
		hookErr = fmt.Errorf("%w: no state", ErrPenaltyDataUnavailable)
	}
	engine.HookPenalty = func(consensus.ChainHeaderReader, uint64, []*types.Header) ([]common.Address, error) {
		return []common.Address{other}, hookErr
	}

	const number = 900
	parent := &types.Header{
		Number:     big.NewInt(number - 1),
		Difficulty: big.NewInt(1),
		Extra:      make([]byte, extraVanity+extraSeal),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   420000000,
		Time:       1000,
	}
	engine.recents.Add(parent.Hash(), newSnapshot(testConfig, engine.signatures, number-1, parent.Hash(), []common.Address{sealer, other}))

	listed := other
	if listSealer {
		listed = sealer
	}
	var penalized common.Address
	switch penalize {
	case "other":
		penalized = other
	case "sealer":
		penalized = sealer
	case "stranger":
		penalized = stranger
	default:
		t.Fatalf("unknown penalize %q", penalize)
	}
	extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(extra[extraVanity:], listed[:])
	cp := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(number),
		Time:       parent.Time + testConfig.Period,
		Extra:      extra,
		Penalties:  append([]byte(nil), penalized[:]...),
		UncleHash:  types.EmptyUncleHash,
		GasLimit:   parent.GasLimit,
		Difficulty: engine.calcDifficulty(chain, parent, sealer),
	}
	sig, err := crypto.Sign(sigHash(cp).Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	copy(cp.Extra[len(cp.Extra)-extraSeal:], sig)

	_, results := engine.VerifyHeaders(chain, []*types.Header{parent, cp})
	<-results
	return <-results
}

func TestF202619621FullVerifyRejectsWrongPenalties(t *testing.T) {
	for _, chainID := range []int64{50, 2040} {
		err := penaltyHeader(t, chainID, false, false, "stranger", true)
		if !errors.Is(err, errInvalidCheckpointPenalties) {
			t.Fatalf("chain %d wrong penalties: got %v, want %v", chainID, err, errInvalidCheckpointPenalties)
		}
	}
}

func TestF202619621SkipsStillAcceptDifferentPenalties(t *testing.T) {
	// Chain 51 never runs the compare. The light path does not either.
	// The header penalizes a stranger and lists the sealer.
	if err := penaltyHeader(t, 51, false, false, "stranger", true); err != nil {
		t.Fatalf("chain 51 rejected a penalty list outside the seal: %v", err)
	}
	for _, chainID := range []int64{50, 51, 2040} {
		if err := penaltyHeader(t, chainID, true, false, "stranger", true); err != nil {
			t.Fatalf("chain %d light path rejected a penalty list outside the seal: %v", chainID, err)
		}
	}
}

func TestF202619621MissingStateTrustsHeaderPenalties(t *testing.T) {
	// The hook has no state. The header penalizes the sealer and lists only
	// the other signer, which is what signers-minus-header-penalties is.
	// Rejecting here would wedge a node that cannot recompute the list.
	if err := penaltyHeader(t, 50, false, true, "sealer", false); err != nil {
		t.Fatalf("missing penalty state rejected the header list: %v", err)
	}
	err := penaltyHeader(t, 50, false, false, "sealer", false)
	if !errors.Is(err, errInvalidCheckpointPenalties) {
		t.Fatalf("hook available, sealer penalized: got %v, want %v", err, errInvalidCheckpointPenalties)
	}
}

func TestF202619621HonestPenaltiesStayAccepted(t *testing.T) {
	for _, chainID := range []int64{50, 51, 2040} {
		for _, bulk := range []bool{false, true} {
			// Computed penalty is the other signer. Header penalties match.
			// Extra lists the sealer, who remains after that penalty is removed.
			if err := penaltyHeader(t, chainID, bulk, false, "other", true); err != nil {
				t.Fatalf("chain %d bulk=%v rejected the computed penalty list: %v", chainID, bulk, err)
			}
		}
	}
}
