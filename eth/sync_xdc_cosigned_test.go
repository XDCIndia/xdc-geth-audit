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

// Tests for fix/1546: adoptCoSignedSibling Case B — adopt a V1 co-signed twin
// that is already stored in the local block DB as a non-canonical side-chain block.
//
// Test naming convention:
//   _KnownNonCanonical — the new Case B path introduced by this fix (fails on origin/main)
//   _AlreadyCanonical  — Case A (sibling is already canonical; returns false, no action)
//   _FlipFlop          — repeated attempts are idempotent after first adoption
//   _Guard*            — early-return guard conditions (V1 epoch, V2 range, nil config)

package eth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	eth "github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// xdposMiniCfg returns a minimal chain config with XDPoS set (Epoch = epoch, no V2).
//
// London is disabled (LondonBlock = nil) for two reasons:
//  1. XDC EIP-1559 is gated by Eip1559Block, not LondonBlock. With XDPoS set and
//     Eip1559Block==nil, IsEIP1559 returns false regardless of LondonBlock.
//  2. ethash's block generator (chain_makers.go) stamps baseFee when IsLondon==true,
//     but VerifyEIP1559Header (XDC path) rejects non-nil baseFee when IsEIP1559==false.
//     Setting LondonBlock=nil stops the generator from stamping any baseFee, keeping
//     generated blocks valid under the XDC validator.
func xdposMiniCfg(epoch uint64) *params.ChainConfig {
	return &params.ChainConfig{
		ChainID:             big.NewInt(1337),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         nil, // see docstring above
		Ethash:              new(params.EthashConfig),
		XDPoS:               &params.XDPoSConfig{Epoch: epoch},
	}
}

// newDummyPeer returns a non-nil *eth.Peer backed by a dead MsgPipe.
// Suitable wherever adoptCoSignedSibling needs a non-nil peer but will not
// actually communicate (guard returns and Case B both avoid peer calls).
func newDummyPeer(t *testing.T, bc *core.BlockChain) *eth.Peer {
	t.Helper()
	app, remote := p2p.MsgPipe()
	t.Cleanup(func() { _ = app.Close(); _ = remote.Close() })
	return eth.NewPeer(eth.XDPOS2, p2p.NewPeer(enode.ID{1}, "dummy", nil), app, nil, nil, bc.Config())
}

// newCosignedSyncer creates a handler + xdcSyncer for bc with startup repairs
// already marked done (same pattern as xdcFakeNet.newTestXDCSyncer).
func newCosignedSyncer(t *testing.T, bc *core.BlockChain) *xdcSyncer {
	t.Helper()
	h, err := newHandler(&handlerConfig{
		Database:   rawdb.NewMemoryDatabase(),
		Chain:      bc,
		TxPool:     newTestTxPool(),
		BlobPool:   newTestTxPool(),
		Network:    1,
		Sync:       ethconfig.FullSync,
		BloomCache: 1,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	s := newXDCSyncer(h)
	s.anchorGapRepaired.Store(true)
	t.Cleanup(func() { close(s.quitCh) })
	bulk, fork := core.XdcBulkSyncMode.Load(), core.XdcForkRecovering.Load()
	t.Cleanup(func() { core.XdcBulkSyncMode.Store(bulk); core.XdcForkRecovering.Store(fork) })
	return s
}

// makeSiblingSetup builds the canonical test scenario for Defect 1c Case B:
//
//   - A blockchain with genesis → block 1 … block(canonHead) (canonical, head = canonHead).
//   - A "sibling" block at height sibHeight whose parent is the same as the
//     canonical block at that height but whose Extra differs (→ different hash).
//     The sibling is inserted into the block DB; because it has equal total
//     difficulty to the canonical block it does NOT become the canonical head.
//   - sibExtLen blocks extending from the sibling (the "peer batch").
//
// Returns the blockchain, the sibling block, and the extension blocks.
// Asserts: bc.HasBlock(sibling.Hash(), sibHeight) == true
//
//	AND bc.GetCanonicalHash(sibHeight) != sibling.Hash().
func makeSiblingSetup(t *testing.T, sibHeight, canonHead, sibExtLen int) (
	bc *core.BlockChain, sibling *types.Block, sibExt types.Blocks,
) {
	t.Helper()
	// sibHeight must be ≥ 2 (parent is a normal block, not genesis) and ≤ canonHead.
	// It must also be > Epoch (3) so the V1 guard in adoptCoSignedSibling passes.
	if sibHeight < 2 || sibHeight > canonHead {
		t.Fatalf("makeSiblingSetup: sibHeight %d out of range [2, %d]", sibHeight, canonHead)
	}

	cfg := xdposMiniCfg(3) // Epoch=3 < sibHeight=5, so V1 guards pass
	gspec := &core.Genesis{
		Config:     cfg,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	engine := ethash.NewFaker()

	// Generate canonical chain genesis→canonHead.
	// Keep genDB: it holds all intermediate state tries used by GenerateChain.
	// We reuse it when generating the sibling and extensions so that the parent's
	// state (at sibHeight-1) is available without re-executing the entire chain.
	genDB, canonBlocks, _ := core.GenerateChainWithGenesis(gspec, engine, canonHead, nil)

	// Build a sibling block at sibHeight: same parent as canonBlocks[sibHeight-1]
	// but different Extra → different hash.
	// canonBlocks[sibHeight-2] is the block at height sibHeight-1 (the parent).
	// Use genDB so that the parent's state is available for state-transition.
	parent := canonBlocks[sibHeight-2]
	sibSlice, _ := core.GenerateChain(cfg, parent, engine, genDB, 1, func(_ int, b *core.BlockGen) {
		b.SetExtra([]byte("sibling")) // distinct Extra → distinct hash from canonical block at sibHeight
	})
	sibling = sibSlice[0]

	// Generate extension blocks off the sibling (the "peer batch").
	// Use genDB: sibling's state was written to genDB by the GenerateChain above.
	if sibExtLen > 0 {
		sibExt, _ = core.GenerateChain(cfg, sibling, engine, genDB, sibExtLen, nil)
	}

	// Open blockchain and insert canonical chain up to canonHead.
	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	t.Cleanup(bc.Stop)
	if _, err := bc.InsertChain(canonBlocks); err != nil {
		t.Fatalf("InsertChain (canonical): %v", err)
	}

	// Insert sibling into the DB.
	//
	// In this XDC geth fork, writeBlockAndSetHead() ALWAYS makes the new block
	// canonical (there is no TD comparison gate — XDPoS consensus determines
	// canonical by signature, not accumulated difficulty). Inserting the sibling
	// therefore makes it the canonical head temporarily.
	//
	// We then re-insert the canonical block at sibHeight (which is known to bc
	// since it was already inserted above). ValidateBody returns ErrKnownBlock
	// → insertChain's skipBlock second loop calls writeKnownBlock → reorg swaps
	// canonical back from sibling to the original block, leaving the sibling in
	// the DB as a non-canonical side-chain block. This is Case B's precondition.
	if _, err := bc.InsertChain(types.Blocks{sibling}); err != nil {
		t.Fatalf("InsertChain (sibling): %v", err)
	}
	// Re-instate the canonical block at sibHeight to make sibling non-canonical.
	if _, err := bc.InsertChain(types.Blocks{canonBlocks[sibHeight-1]}); err != nil {
		t.Fatalf("InsertChain (canonical re-insert to restore head): %v", err)
	}

	// Verify preconditions.
	if !bc.HasBlock(sibling.Hash(), uint64(sibHeight)) {
		t.Fatalf("precondition: sibling not found in block DB at height %d", sibHeight)
	}
	if bc.GetCanonicalHash(uint64(sibHeight)) == sibling.Hash() {
		t.Fatalf("precondition: sibling unexpectedly became canonical at height %d (test setup error)", sibHeight)
	}

	return bc, sibling, sibExt
}

// ---------------------------------------------------------------------------
// Guard tests — all should return false quickly without touching the peer.
// ---------------------------------------------------------------------------

// TestAdoptCoSignedSibling_GuardNilXDPoS verifies that the function returns
// false immediately when the chain config has no XDPoS section.
// Passes on both origin/main and the fix branch.
func TestAdoptCoSignedSibling_GuardNilXDPoS(t *testing.T) {
	// AllEthashProtocolChanges has no XDPoS field → cfg.XDPoS == nil.
	gspec := &core.Genesis{
		Config:     params.AllEthashProtocolChanges,
		Difficulty: common.Big1,
		GasLimit:   8_000_000,
	}
	engine := ethash.NewFaker()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, engine, 5, nil)

	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}

	s := newCosignedSyncer(t, bc)
	peer := newDummyPeer(t, bc)
	if s.adoptCoSignedSibling(types.Blocks{blocks[4]}, peer) {
		t.Fatal("expected false (no XDPoS config), got true")
	}
}

// TestAdoptCoSignedSibling_GuardEpochBoundary verifies that the function returns
// false when the batch's parent number is at or below the epoch boundary.
// (V1 double-validation only applies above the first epoch.)
func TestAdoptCoSignedSibling_GuardEpochBoundary(t *testing.T) {
	// Epoch=10; batch[0] at height 5 has parentNum=4 ≤ Epoch(10) → guard fires.
	cfg := xdposMiniCfg(10) // Epoch=10 is large enough that block 5's parent (4) is below it
	gspec := &core.Genesis{Config: cfg, Difficulty: common.Big1, GasLimit: 8_000_000}
	engine := ethash.NewFaker()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, engine, 5, nil)

	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}

	s := newCosignedSyncer(t, bc)
	peer := newDummyPeer(t, bc)
	// batch[0] = blocks[4] at height 5; parentNum = 4 ≤ Epoch(10) → false.
	if s.adoptCoSignedSibling(types.Blocks{blocks[4]}, peer) {
		t.Fatal("expected false (parentNum ≤ epoch), got true")
	}
}

// TestAdoptCoSignedSibling_GuardV2Range verifies that the function returns false
// for blocks above the V2 switch block (V2 uses QC-chain fork choice, not Validator field).
func TestAdoptCoSignedSibling_GuardV2Range(t *testing.T) {
	cfg := xdposMiniCfg(3)
	cfg.XDPoS.V2 = &params.V2{SwitchBlock: big.NewInt(4)} // switch at block 4 → parentNum=5 is V2
	gspec := &core.Genesis{Config: cfg, Difficulty: common.Big1, GasLimit: 8_000_000}
	engine := ethash.NewFaker()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, engine, 6, nil)

	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}

	s := newCosignedSyncer(t, bc)
	peer := newDummyPeer(t, bc)
	// batch[0] = blocks[5] at height 6; parentNum = 5 > SwitchBlock(4) → V2 guard → false.
	if s.adoptCoSignedSibling(types.Blocks{blocks[5]}, peer) {
		t.Fatal("expected false (V2 range), got true")
	}
}

// ---------------------------------------------------------------------------
// Core parity tests — Case B is the new code path introduced by fix/1546.
// ---------------------------------------------------------------------------

// TestAdoptCoSignedSibling_KnownNonCanonical is the primary regression test for
// issue #1546. It reproduces the live net2002 wedge: the co-signed twin (sibling)
// of the local canonical head is already stored in the block DB as a non-canonical
// side-chain block, but the old HasBlock guard returned false immediately, leaving
// the node permanently stuck.
//
// On origin/main: adoptCoSignedSibling encounters HasBlock(sibling.Hash(), N)==true
// and immediately returns false → batch is not imported → maybeRollbackOnUnknownAncestor
// fires → node stuck → TEST FAILS.
//
// With fix: adoptCoSignedSibling detects non-canonical known sibling (Case B),
// calls InsertChain([sibling]) to trigger writeKnownBlock → bc.reorg, making the
// sibling canonical, then re-imports the batch → TEST PASSES.
func TestAdoptCoSignedSibling_KnownNonCanonical(t *testing.T) {
	// Chain: genesis → 1 → 2 → 3 → 4 → 5(a) [canonical head at 5]
	// Sibling 5(b): same parent as 5(a), different Extra → different hash, non-canonical.
	// Batch: block 6 extending from 5(b) (batch[0].ParentHash() = 5(b).Hash()).
	const sibHeight = 5

	bc, sibling, sibExt := makeSiblingSetup(t, sibHeight, sibHeight, 1)
	batch := types.Blocks{sibExt[0]}

	headBefore := bc.CurrentBlock().Number.Uint64()

	s := newCosignedSyncer(t, bc)
	peer := newDummyPeer(t, bc)

	// This call FAILS on origin/main (HasBlock guard) and PASSES with fix/1546.
	if !s.adoptCoSignedSibling(batch, peer) {
		t.Fatal("adoptCoSignedSibling returned false for known non-canonical sibling (Case B); " +
			"this is the Defect 1c regression — fix/1546 should return true here")
	}

	// Post-conditions: sibling is now canonical AND chain advanced.
	if bc.GetCanonicalHash(uint64(sibHeight)) != sibling.Hash() {
		t.Fatalf("sibling not canonical after adoption: canonical at %d = %s, want %s",
			sibHeight, bc.GetCanonicalHash(uint64(sibHeight)), sibling.Hash())
	}
	headAfter := bc.CurrentBlock().Number.Uint64()
	if headAfter <= headBefore {
		t.Fatalf("chain head did not advance after adoption: was %d, still %d", headBefore, headAfter)
	}
	t.Logf("OK: head advanced %d → %d; sibling at %d is now canonical", headBefore, headAfter, sibHeight)
}

// TestAdoptCoSignedSibling_AlreadyCanonical verifies Case A: when the parent
// block the batch needs is already canonical, adoptCoSignedSibling must return
// false (a different error caused ErrUnknownAncestor, not a V1 twin swap).
// Correct on both origin/main and the fix branch.
func TestAdoptCoSignedSibling_AlreadyCanonical(t *testing.T) {
	// Chain: genesis → 1 → 2 → 3 → 4 → 5 [canonical head = 5]
	// Batch: block 6 whose ParentHash = 5.Hash() = canonical head.
	// adoptCoSignedSibling must return false: parent IS already canonical (Case A).
	cfg := xdposMiniCfg(3)
	gspec := &core.Genesis{Config: cfg, Difficulty: common.Big1, GasLimit: 8_000_000}
	engine := ethash.NewFaker()
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, engine, 6, nil)

	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	// Insert only 1–5; block 6 becomes the "batch".
	if _, err := bc.InsertChain(blocks[:5]); err != nil {
		t.Fatal(err)
	}

	s := newCosignedSyncer(t, bc)
	peer := newDummyPeer(t, bc)
	// blocks[5] = block 6; its parent (block 5) IS canonical → Case A → false.
	if s.adoptCoSignedSibling(types.Blocks{blocks[5]}, peer) {
		t.Fatal("expected false (parent already canonical, Case A), got true")
	}
}

// TestAdoptCoSignedSibling_FlipFlop verifies the flip-flop scenario from net2002:
// once adoption succeeds (first call), subsequent calls are idempotent — the second
// call returns false because the sibling is now canonical (Case A), and the chain
// head stays stable (no backward oscillation).
func TestAdoptCoSignedSibling_FlipFlop(t *testing.T) {
	const sibHeight = 5
	bc, sibling, sibExt := makeSiblingSetup(t, sibHeight, sibHeight, 1)
	batch := types.Blocks{sibExt[0]}

	s := newCosignedSyncer(t, bc)
	peer := newDummyPeer(t, bc)

	// First call: adoption succeeds (Case B fix path).
	if !s.adoptCoSignedSibling(batch, peer) {
		t.Fatal("first adoptCoSignedSibling call should return true (Case B)")
	}
	if bc.GetCanonicalHash(uint64(sibHeight)) != sibling.Hash() {
		t.Fatal("sibling not canonical after first adoption")
	}
	headAfterFirst := bc.CurrentBlock().Number.Uint64()

	// Second call with the same batch: sibling is canonical → Case A → false.
	// This is correct: Case A signals "not our situation", not "stuck".
	if s.adoptCoSignedSibling(batch, peer) {
		t.Fatal("second adoptCoSignedSibling call should return false (Case A, sibling now canonical)")
	}

	// Chain head must not have changed — no flip-flop.
	if bc.CurrentBlock().Number.Uint64() != headAfterFirst {
		t.Fatalf("head changed on idempotent second call: was %d, now %d",
			headAfterFirst, bc.CurrentBlock().Number.Uint64())
	}
}
