// Copyright (c) 2026 XDC Network

package eth

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// newXDCFakeNetWithTxs is copied from sync_xdc_1848_test.go, which is not in the
// audit tree. It builds a fake net whose remote chain has one signed
// self-transfer per block, giving every block a non-empty TxHash.
func newXDCFakeNetWithTxs(t *testing.T, local, remote int) *xdcFakeNet {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("crypto.GenerateKey: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
		Alloc:      core.GenesisAlloc{addr: {Balance: new(big.Int).Mul(big.NewInt(1e18), big.NewInt(1000))}},
	}
	signer := types.LatestSigner(gspec.Config)
	gen := func(i int, b *core.BlockGen) {
		tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
			ChainID:   gspec.Config.ChainID,
			Nonce:     b.TxNonce(addr),
			GasFeeCap: b.BaseFee(),
			GasTipCap: big.NewInt(1),
			Gas:       21000,
			To:        &addr,
			Value:     big.NewInt(0),
		}), signer, key)
		if err != nil {
			panic(err)
		}
		b.AddTx(tx)
	}
	return newXDCFakeNetGspecGen(t, local, remote, ethash.NewFaker(), gspec, gen)
}

// TestF202620612EmptyBodiesTaintRaceWinner checks that a peer which wins the
// header race and then withholds bodies is tainted, so the next round can
// import from an honest peer. The parent check stays in fullSyncOnce: a batch
// that does not extend the head is still eligible for the rollback path.
func TestF202620612EmptyBodiesTaintRaceWinner(t *testing.T) {
	const local = 20
	n := newXDCFakeNet(t, local, 40)
	sib := siblingBranch(n, 15, 40)
	// Sibling headers, and no bodies for those hashes.
	bad := n.addPeer(t, 1, servesFrom(sib, local+1))
	honest := n.addPeer(t, 2, n.canonicalHeaders)
	honest.headerDelay = 200 * time.Millisecond

	s := n.newTestXDCSyncer(t)
	s.fullSyncOnce()

	if got := n.chain.CurrentBlock().Number.Uint64(); got != local {
		t.Fatalf("head moved to %d on a batch with no bodies", got)
	}
	if !s.isPeerTainted(bad.ID()) {
		t.Fatal("header-race winner that withheld bodies was not tainted")
	}

	s.fullSyncOnce()
	if got := n.chain.CurrentBlock().Number.Uint64(); got <= local {
		t.Fatalf("head stayed at %d after the winner was tainted", got)
	}
}

// TestF202620612MismatchedBodiesTaintRaceWinner covers the second exit: the
// race winner serves headers that extend the head and a non-empty reply of
// bodies that do not match them. With no other peer to hedge to, the batch is
// dropped and the winner is tainted.
func TestF202620612MismatchedBodiesTaintRaceWinner(t *testing.T) {
	const local = 20
	n := newXDCFakeNetWithTxs(t, local, 40)
	bad := n.addPeer(t, 1, n.canonicalHeaders)
	bad.corrupt = true

	s := n.newTestXDCSyncer(t)
	s.fullSyncOnce()

	if got := n.chain.CurrentBlock().Number.Uint64(); got != local {
		t.Fatalf("head moved to %d on mismatched bodies", got)
	}
	if !s.isPeerTainted(bad.ID()) {
		t.Fatal("header-race winner that served mismatched bodies was not tainted")
	}
}

// TestF202620612HedgeRetriesOnMismatchedReply checks that a mismatched primary
// reply starts the hedge at once: the honest peer supplies matching bodies in
// the same round, so the head moves, and the race winner is tainted.
func TestF202620612HedgeRetriesOnMismatchedReply(t *testing.T) {
	const local = 20
	n := newXDCFakeNetWithTxs(t, local, 40)
	bad := n.addPeer(t, 1, n.canonicalHeaders)
	bad.corrupt = true
	honest := n.addPeer(t, 2, n.canonicalHeaders)
	honest.headerDelay = 200 * time.Millisecond

	s := n.newTestXDCSyncer(t)
	s.fullSyncOnce()

	if !s.isPeerTainted(bad.ID()) {
		t.Fatal("header-race winner that served mismatched bodies was not tainted")
	}
	if got := n.chain.CurrentBlock().Number.Uint64(); got <= local {
		t.Fatalf("head stayed at %d: the hedge was not retried on the mismatched reply", got)
	}
	if s.isPeerTainted(honest.ID()) {
		t.Fatal("honest hedge peer was tainted")
	}
}
