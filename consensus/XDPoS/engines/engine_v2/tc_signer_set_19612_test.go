package engine_v2

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Helpers and tests in this file were copied from tc_signer_set_test.go because that file is not in the audit tree.

// tcChain serves a head header and the gap header at block 450.
type tcChain struct {
	mockChainForA894
	head, gap *types.Header
}

func (c *tcChain) CurrentHeader() *types.Header { return c.head }

func (c *tcChain) GetHeaderByNumber(n uint64) *types.Header {
	if n == c.gap.Number.Uint64() {
		return c.gap
	}
	return nil
}

// tcFixture: the head epoch (switch round epochRound) has masternodes active.
// The gap snapshot also lists stale (in NextEpochCandidates, not an epoch
// masternode) and penalized (only in Penalties).
type tcFixture struct {
	x                       *XDPoS_v2
	chain                   *tcChain
	active                  []*ecdsa.PrivateKey
	stale, penalized, other *ecdsa.PrivateKey
}

func newTCFixture(t *testing.T, epochRound types.Round) *tcFixture {
	t.Helper()
	x, _ := newA894Engine(t)
	x.isInitialized = true
	keys, addrs := genNKeys(t, 6)
	f := &tcFixture{x: x, active: keys[:3], stale: keys[3], penalized: keys[4], other: keys[5]}

	gap := &types.Header{Number: big.NewInt(450)}
	head := &types.Header{Number: big.NewInt(1000), Extra: []byte{1}}
	f.chain = &tcChain{head: head, gap: gap}

	switchInfo := &types.BlockInfo{Hash: common.Hash{0xee}, Round: epochRound, Number: big.NewInt(901)}
	info := &types.EpochSwitchInfo{
		Masternodes:          addrs[:3],
		MasternodesLen:       3,
		EpochSwitchBlockInfo: switchInfo,
	}
	x.epochSwitches.Add(head.Hash(), info)
	x.epochSwitches.Add(switchInfo.Hash, info)

	snap := &SnapshotV2{
		Number:              450,
		Hash:                gap.Hash(),
		NextEpochCandidates: []common.Address{addrs[0], addrs[1], addrs[2], addrs[3]},
		Penalties:           []common.Address{addrs[4]},
	}
	x.snapshots.Add(gap.Hash(), snap)
	return f
}

func signTimeout(t *testing.T, round types.Round, keys ...*ecdsa.PrivateKey) []types.Signature {
	t.Helper()
	h := types.TimeoutSigHash(&types.TimeoutForSign{Round: round, GapNumber: 450})
	sigs := make([]types.Signature, len(keys))
	for i, k := range keys {
		sig, err := crypto.Sign(h[:], k)
		if err != nil {
			t.Fatal(err)
		}
		sigs[i] = sig
	}
	return sigs
}
