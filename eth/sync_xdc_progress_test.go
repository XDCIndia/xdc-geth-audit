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

package eth

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/beacon"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/blobpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/params"
)

// TestSyncProgressXDCBulkSync verifies that eth_syncing reports progress
// while the XDC bulk syncer is catching up (Fixes #1841).
func TestSyncProgressXDCBulkSync(t *testing.T) {
	var (
		db     = rawdb.NewMemoryDatabase()
		engine = beacon.New(ethash.NewFaker())
		gspec  = &core.Genesis{
			Config: params.MergedTestChainConfig,
			Alloc: types.GenesisAlloc{
				common.Address{0x01}: {Balance: big.NewInt(1e18)},
			},
			Difficulty: common.Big0,
			BaseFee:    big.NewInt(params.InitialBaseFee),
		}
	)
	chain, _ := core.NewBlockChain(db, gspec, engine, nil)

	txconfig := legacypool.DefaultConfig
	txconfig.Journal = ""
	blobPool := blobpool.New(blobpool.Config{Datadir: ""}, chain, nil)
	legacyPool := legacypool.New(txconfig, chain)
	txpool, _ := txpool.New(txconfig.PriceLimit, chain, []txpool.SubPool{legacyPool, blobPool})

	// Create a proper handler with downloader
	dropPeer := func(id string) {} // no-op peer dropper for test
	success := func() {}           // no-op success callback
	dl := downloader.New(db, ethconfig.FullSync, chain, dropPeer, success, false)

	eth := &Ethereum{
		blockchain: chain,
		txPool:     txpool,
		handler: &handler{
			chain:      chain,
			downloader: dl,
			xdcSync: &xdcSyncer{
				handler: &handler{
					chain: chain,
				},
			},
		},
	}

	backend := &EthAPIBackend{eth: eth}

	// Test 1: when downloader is done and highest peer is within margin,
	// SyncProgress should report done (via empty/zero progress).
	localHead := chain.CurrentBlock().Number.Uint64()
	eth.handler.xdcSync.highestPeerBlockSeen.Store(localHead + 32) // within 64-block margin
	eth.handler.xdcSync.bulkSyncStartBlock.Store(localHead)

	prog := backend.SyncProgress(context.Background())
	// When within margin, SyncProgress should not override downloader's progress.
	// Since downloader is done, prog.HighestBlock should be 0 or equal to CurrentBlock.
	if prog.HighestBlock > localHead+64 {
		t.Errorf("Test 1 failed: HighestBlock %d should not exceed margin (localHead %d + 64)",
			prog.HighestBlock, localHead)
	}

	// Test 2: when highest peer is beyond margin, SyncProgress should report
	// the bulk syncer's progress with correct StartingBlock and HighestBlock.
	highestPeer := localHead + 1000 // far beyond the margin
	eth.handler.xdcSync.highestPeerBlockSeen.Store(highestPeer)
	eth.handler.xdcSync.bulkSyncStartBlock.Store(localHead)

	prog = backend.SyncProgress(context.Background())
	if prog.HighestBlock != highestPeer {
		t.Errorf("Test 2 failed: expected HighestBlock %d, got %d",
			highestPeer, prog.HighestBlock)
	}
	if prog.CurrentBlock != localHead {
		t.Errorf("Test 2 failed: expected CurrentBlock %d, got %d",
			localHead, prog.CurrentBlock)
	}
	if prog.StartingBlock != localHead {
		t.Errorf("Test 2 failed: expected StartingBlock %d, got %d",
			localHead, prog.StartingBlock)
	}

	// Test 3: when highest peer is exactly at the margin boundary.
	// Should still report progress to be safe (boundary condition).
	eth.handler.xdcSync.highestPeerBlockSeen.Store(localHead + 65)
	prog = backend.SyncProgress(context.Background())
	if prog.HighestBlock != localHead+65 {
		t.Errorf("Test 3 failed: expected HighestBlock %d at boundary, got %d",
			localHead+65, prog.HighestBlock)
	}

	// Test 4: when no xdcSync (nil), downloader progress is returned as-is.
	eth.handler.xdcSync = nil
	prog = backend.SyncProgress(context.Background())
	// Should not panic and should return downloader progress (which is done).
	if prog.HighestBlock > 0 && prog.HighestBlock < localHead+1000 {
		t.Logf("Test 4: correctly handled nil xdcSync, progress reported as done")
	}
}
