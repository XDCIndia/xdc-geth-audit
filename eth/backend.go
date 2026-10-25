// Copyright 2014 The go-ethereum Authors
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

// Package eth implements the Ethereum protocol.
package eth

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"runtime"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/filtermaps"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state/pruner"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/blobpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/ethereum/go-ethereum/core/txpool/locals"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/eth/bft"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/fetcher"
	"github.com/ethereum/go-ethereum/eth/gasprice"
	"github.com/ethereum/go-ethereum/eth/hooks"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/internal/shutdowncheck"
	"github.com/ethereum/go-ethereum/internal/version"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/miner"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/dnsdisc"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	gethversion "github.com/ethereum/go-ethereum/version"
)

const (
	// This is the fairness knob for the discovery mixer. When looking for peers, we'll
	// wait this long for a single source of candidates before moving on and trying other
	// sources. If this timeout expires, the source will be skipped in this round, but it
	// will continue to fetch in the background and will have a chance with a new timeout
	// in the next rounds, giving it overall more time but a proportionally smaller share.
	// We expect a normal source to produce ~10 candidates per second.
	discmixTimeout = 100 * time.Millisecond

	// discoveryPrefetchBuffer is the number of peers to pre-fetch from a discovery
	// source. It is useful to avoid the negative effects of potential longer timeouts
	// in the discovery, keeping dial progress while waiting for the next batch of
	// candidates.
	discoveryPrefetchBuffer = 32

	// maxParallelENRRequests is the maximum number of parallel ENR requests that can be
	// performed by a disc/v4 source.
	maxParallelENRRequests = 16
)

// Config contains the configuration options of the ETH protocol.
// Deprecated: use ethconfig.Config instead.
type Config = ethconfig.Config

// Ethereum implements the Ethereum full node service.
type Ethereum struct {
	// core protocol objects
	config         *ethconfig.Config
	txPool         *txpool.TxPool
	blobTxPool     *blobpool.BlobPool
	blobCache      *blobpool.Cache
	localTxTracker *locals.TxTracker
	blockchain     *core.BlockChain

	handler *handler
	discmix *enode.FairMix
	dropper *dropper

	// DB interfaces
	chainDb ethdb.Database // Block chain database

	engine         consensus.Engine
	accountManager *accounts.Manager

	filterMaps      *filtermaps.FilterMaps
	closeFilterMaps chan chan struct{}

	// Chain event subscriptions driving updateFilterMapsHeads. The
	// subscriptions are registered and consumed in Start.
	fmHeadEventCh  chan core.ChainEvent
	fmHeadSub      event.Subscription
	fmBlockProcCh  chan bool
	fmBlockProcSub event.Subscription

	APIBackend *EthAPIBackend

	miner    *miner.Miner
	gasPrice *big.Int

	networkID     uint64
	netRPCService *ethapi.NetAPI

	p2pServer *p2p.Server

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)

	// XDC A.97-M.A: BFT participation channels (owned by Ethereum, not the engine
	// singleton). Allocated before engine_v2.New so producers never see nil.
	// minePeriodCh: carries MinePeriod int updates from UpdateParams/initial.
	// newRoundCh:   carries Round notifications from setNewRound (non-blocking send).
	// Both are exported to xdcWorker (PR-B) via backend accessors.
	// NEVER closed — engine is a singleton; closing would panic its next send.
	minePeriodCh chan int
	newRoundCh   chan types.Round

	// XDC A.97-M.B: XDPoS V2 block-production worker (Task 5).
	// Nil on non-XDPoS chains or before StartMining is called.
	// Protected by lock for start/stop; the worker itself is goroutine-safe.
	xdcWorker *miner.XdcWorker

	shutdownTracker *shutdowncheck.ShutdownTracker // Tracks if and when the node has shutdown ungracefully
}

// logXDCEffectiveTIPs logs every effective TIP fork activation block at startup
// (#1528 G0.3). For registered chains these are the constants set by
// CopyXDCConstants; for custom geneses they come from ChainConfig fields.
func logXDCEffectiveTIPs(c *params.ChainConfig) {
	effBlock := func(field, fallback *big.Int) interface{} {
		if field != nil {
			return field
		}
		if fallback != nil {
			return fallback
		}
		return "nil"
	}
	log.Info("XDC effective TIP fork values",
		"tipSigning", effBlock(c.TIPSigningBlock, common.TIPSigning),
		"tip2019", effBlock(c.TIP2019Block, common.TIP2019Block),
		"tipNoHalvingMNReward", effBlock(c.TIPNoHalvingMNRewardBlock, common.TIPNoHalvingMNReward),
		"tipRandomize", effBlock(c.TIPRandomizeBlock, common.TIPRandomize),
		"tipXDCX", effBlock(c.TIPXDCXBlock, common.TIPXDCX),
		"tipTRC21Fee", effBlock(c.TIPTRC21FeeBlock, common.TIPTRC21Fee),
		"gas50x", effBlock(c.Gas50xBlock, common.BlockNumberGas50x),
		"dynamicGasLimit", c.DynamicGasLimitBlock,
		"tipEpochHalving", c.TIPEpochHalvingBlock,
	)
}

// New creates a new Ethereum object (including the initialisation of the common Ethereum object),
// whose lifecycle will be managed by the provided node.
func New(stack *node.Node, config *ethconfig.Config) (*Ethereum, error) {
	// Ensure configuration values are compatible and sane
	if !config.SyncMode.IsValid() {
		return nil, fmt.Errorf("invalid sync mode %d", config.SyncMode)
	}
	if !config.HistoryMode.Mode.IsValid() {
		return nil, fmt.Errorf("invalid history mode %d", config.HistoryMode.Mode)
	}
	if config.Miner.GasPrice == nil || config.Miner.GasPrice.Sign() <= 0 {
		log.Warn("Sanitizing invalid miner gas price", "provided", config.Miner.GasPrice, "updated", ethconfig.Defaults.Miner.GasPrice)
		config.Miner.GasPrice = new(big.Int).Set(ethconfig.Defaults.Miner.GasPrice)
	}
	if config.NoPruning && config.TrieDirtyCache > 0 && config.StateScheme == rawdb.HashScheme {
		if config.SnapshotCache > 0 {
			config.TrieCleanCache += config.TrieDirtyCache * 3 / 5
			config.SnapshotCache += config.TrieDirtyCache * 2 / 5
		} else {
			config.TrieCleanCache += config.TrieDirtyCache
		}
		config.TrieDirtyCache = 0
	}
	log.Info("Allocated trie memory caches", "clean", common.StorageSize(config.TrieCleanCache)*1024*1024, "dirty", common.StorageSize(config.TrieDirtyCache)*1024*1024)

	dbOptions := node.DatabaseOptions{
		Cache:             config.DatabaseCache,
		Handles:           config.DatabaseHandles,
		AncientsDirectory: config.DatabaseFreezer,
		EraDirectory:      config.DatabaseEra,
		MetricsNamespace:  "eth/db/chaindata/",
	}
	chainDb, err := stack.OpenDatabaseWithOptions("chaindata", dbOptions)
	if err != nil {
		return nil, err
	}
	scheme, err := rawdb.ParseStateScheme(config.StateScheme, chainDb)
	if err != nil {
		return nil, err
	}
	// Try to recover offline state pruning only in hash-based.
	if scheme == rawdb.HashScheme {
		if err := pruner.RecoverPruning(stack.ResolvePath(""), chainDb); err != nil {
			log.Error("Failed to recover state", "error", err)
		}
	}

	// Here we determine genesis hash and active ChainConfig.
	// We need these to figure out the consensus parameters and to set up history pruning.
	chainConfig, genesisHash, err := core.LoadChainConfig(chainDb, config.Genesis)
	if err != nil {
		return nil, err
	}
	// XDC: initialise chain-ID-dependent XDC constants (blacklist HF, masternode
	// limits, fork blocks) and XDPoS network constants (V2 switch, epoch length)
	// before the consensus engine is created, so the engine and all downstream
	// modules observe consistent values.
	if chainConfig.XDPoS != nil {
		common.CopyXDCConstants(chainConfig.ChainID.Uint64())
		XDPoS.SetNetworkConstants(chainConfig.ChainID.Uint64())
		// XDC: a genesis file may explicitly override the BlockSigners (0x89)
		// contract-wipe fork block via "tipSigningBlock" — honor it over the
		// hardcoded per-chainID default set above. nil (no genesis override,
		// true for every existing production/testnet genesis) leaves the
		// CopyXDCConstants/SetNetworkConstants value untouched. Refs
		// devnet-5151 A.94 sticky-checkpointSyncNoState root cause: genesis
		// set tipSigningBlock=4 but ChainConfig had no field to carry it, so
		// common.TIPSigning stayed at the table default (0) and the BlockSigners
		// wipe never fired locally while canonical nodes applied it at block 4,
		// diverging the state root from block 4 onward.
		if chainConfig.TIPSigningBlock != nil {
			common.TIPSigning = new(big.Int).Set(chainConfig.TIPSigningBlock)
			log.Info("XDC: genesis tipSigningBlock override applied", "block", common.TIPSigning)
		}
		// XDC: canonical XDPoSChain applies the address denylist from the
		// genesis "denylistBlock" (params/config_forks.go:97 IsDenylist, used by
		// core/state_processor.go:170-180, the miner and the txpool). Honour it
		// over the per-chainID BlackListHFNumber, which for an unregistered chain
		// ID is the mainnet 38,383,838: before this, a private net with
		// denylistBlock set accepted, pooled and minted denylisted txs that the
		// canonical nodes reject. nil (every built-in network) keeps the default.
		if chainConfig.DenylistBlock != nil {
			common.BlackListHFNumber = chainConfig.DenylistBlock.Uint64()
			log.Info("XDC: genesis denylistBlock override applied", "block", common.BlackListHFNumber)
		}
		// Log all effective TIP values (#1528 G0.3). For registered chains these
		// are the constants set by CopyXDCConstants; for custom geneses they come
		// from the genesis ChainConfig fields via xdcTIPBlock.
		logXDCEffectiveTIPs(chainConfig)
	}
	engine, err := ethconfig.CreateConsensusEngine(chainConfig, chainDb)
	if err != nil {
		return nil, err
	}
	// Set networkID to chainID by default.
	networkID := config.NetworkId
	if networkID == 0 {
		networkID = chainConfig.ChainID.Uint64()
	}

	// Assemble the Ethereum object.
	eth := &Ethereum{
		config:          config,
		chainDb:         chainDb,
		accountManager:  stack.AccountManager(),
		engine:          engine,
		networkID:       networkID,
		gasPrice:        config.Miner.GasPrice,
		p2pServer:       stack.Server(),
		discmix:         enode.NewFairMix(discmixTimeout),
		shutdownTracker: shutdowncheck.NewShutdownTracker(chainDb),
		fmHeadEventCh:   make(chan core.ChainEvent, 10),
		fmBlockProcCh:   make(chan bool, 10),
	}
	bcVersion := rawdb.ReadDatabaseVersion(chainDb)
	var dbVer = "<nil>"
	if bcVersion != nil {
		dbVer = fmt.Sprintf("%d", *bcVersion)
	}
	log.Info("Initialising Ethereum protocol", "network", networkID, "dbversion", dbVer)

	// Create BlockChain object.
	if !config.SkipBcVersionCheck {
		if bcVersion != nil && *bcVersion > core.BlockChainVersion {
			return nil, fmt.Errorf("database version is v%d, Geth %s only supports v%d", *bcVersion, version.WithMeta, core.BlockChainVersion)
		} else if bcVersion == nil || *bcVersion < core.BlockChainVersion {
			if bcVersion != nil { // only print warning on upgrade, not on init
				log.Warn("Upgrade blockchain database version", "from", dbVer, "to", core.BlockChainVersion)
			}
			rawdb.WriteDatabaseVersion(chainDb, core.BlockChainVersion)
		}
	}
	histPolicy, err := config.HistoryMode.Resolve(genesisHash)
	if err != nil {
		return nil, err
	}
	var (
		options = &core.BlockChainConfig{
			TrieCleanLimit:    config.TrieCleanCache,
			NoPrefetch:        config.NoPrefetch,
			NoPrecompileCache: config.NoPrecompileCache,
			TrieDirtyLimit:    config.TrieDirtyCache,
			ArchiveMode:       config.NoPruning,
			// #62 / netv12: a node configured to produce blocks must never silently
			// enter no-state mode. Masking a missing parent state would import every
			// later block with EmptyRootHash, which permanently poisons the DB and
			// then gates the miner off at miner/xdpos_worker.go Gate 3.1 — a seated
			// masternode that mints nothing and times out its slot every round.
			// Gated on --mine ONLY. It deliberately does NOT also test
			// config.Miner.PendingFeeRecipient: that field is set by
			// --miner.etherbase on its own (cmd/utils/flags.go), so including it
			// classified any node that merely names a coinbase — a follower, an RPC
			// node, a proposed-but-not-yet-seated candidate — as a producer and
			// handed it the hard-import-failure path. A node that is not sealing
			// gains nothing from refusing to mask and loses the ability to finish a
			// resync, so widening this predicate is a pure availability regression
			// (gate finding F1 on PR #1363). Operators who want the strict behaviour
			// on a non-mining node should run --gcmode archive, which is excluded
			// from masking at the source.
			NoStateMaskingForbidden: config.Miner.Mining,
			TrieTimeLimit:           config.TrieTimeout,
			SnapshotLimit:           config.SnapshotCache,
			Preimages:               config.Preimages,
			StateHistory:            config.StateHistory,
			TrienodeHistory:         config.TrienodeHistory,
			NodeFullValueCheckpoint: config.NodeFullValueCheckpoint,
			BinTrieGroupDepth:       config.BinTrieGroupDepth,
			StateScheme:             scheme,
			HistoryPolicy:           histPolicy,
			TxLookupLimit:           int64(min(config.TransactionHistory, math.MaxInt64)),
			VmConfig: vm.Config{
				EnablePreimageRecording: config.EnablePreimageRecording,
			},
			// Enables file journaling for the trie database. The journal files will be stored
			// within the data directory. The corresponding paths will be either:
			// - DATADIR/triedb/merkle.journal
			// - DATADIR/triedb/verkle.journal
			TrieJournalDirectory: stack.ResolvePath("triedb"),
			SlowBlockThreshold:   config.SlowBlockThreshold,

			StatelessSelfValidation: config.StatelessSelfValidation,
			EnableWitnessStats:      config.EnableWitnessStats,
		}
	)
	if config.VMTrace != "" {
		traceConfig := json.RawMessage("{}")
		if config.VMTraceJsonConfig != "" {
			traceConfig = json.RawMessage(config.VMTraceJsonConfig)
		}
		t, err := tracers.LiveDirectory.New(config.VMTrace, traceConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create tracer %s: %v", config.VMTrace, err)
		}
		options.VmConfig.Tracer = t
	}
	// Override the chain config with provided settings.
	var overrides core.ChainOverrides
	if config.OverrideOsaka != nil {
		overrides.OverrideOsaka = config.OverrideOsaka
	}
	if config.OverrideAmsterdam != nil {
		overrides.OverrideAmsterdam = config.OverrideAmsterdam
	}
	if config.OverrideBPO1 != nil {
		overrides.OverrideBPO1 = config.OverrideBPO1
	}
	if config.OverrideBPO2 != nil {
		overrides.OverrideBPO2 = config.OverrideBPO2
	}
	if config.OverrideUBT != nil {
		overrides.OverrideUBT = config.OverrideUBT
	}
	options.Overrides = &overrides

	eth.blockchain, err = core.NewBlockChain(chainDb, config.Genesis, eth.engine, options)
	if err != nil {
		return nil, err
	}

	// XDC: attach XDPoS V1 + V2 consensus hooks (reward calculation, finality
	// marking) and construct the V2 engine instance. Without this, the V2
	// EngineV2Iface field on the XDPoS wrapper stays nil and V2 blocks pass
	// through the V1 light-verification fast path — no QC verification, no
	// epoch-switch reward distribution, no BFT finalisation.
	if xdposEngine, ok := eth.engine.(*XDPoS.XDPoS); ok {
		log.Info("Attaching XDPoS V1 + V2 consensus hooks")
		hooks.AttachConsensusV1Hooks(xdposEngine, eth.blockchain, chainConfig)
		// NOTE: AttachConsensusV2Hooks is called AFTER SetEngineV2 below so that
		// adaptor.EngineV2 is non-nil when the hook closures evaluate it.
		// Calling it here (before SetEngineV2) would skip the EngineV2 != nil
		// guard and leave HookPenalty and HookReward unwired on the V2 engine —
		// causing calcMasternodes to produce the wrong active set and silently
		// skipping epoch-switch reward distribution. (bug fix: refs #ISSUE)

		log.Info("XDPoS V2 wiring check (DEBUG)",
			"XDPoS_nil", chainConfig.XDPoS == nil,
			"V2_nil", chainConfig.XDPoS == nil || chainConfig.XDPoS.V2 == nil,
			"chainID", chainConfig.ChainID)
		if chainConfig.XDPoS != nil && chainConfig.XDPoS.V2 != nil {
			// A.97-M.A Task 1: allocate BFT participation channels before engine_v2.New
			// so the engine's goroutine-sends in UpdateParams/initial never block on nil.
			// Buffer 1: matches XDPoSChain worker usage; non-blocking producers tolerate
			// a briefly-stopped consumer (PR-B xdcWorker) without piling up goroutines.
			// Stored on eth.Ethereum (not the engine singleton) so the miner can start/stop
			// without channel lifetime issues. Do NOT close — engine is a singleton.
			eth.minePeriodCh = make(chan int, 1)
			eth.newRoundCh = make(chan types.Round, 1)

			v2Engine := engine_v2.New(chainConfig, chainDb, eth.minePeriodCh, eth.newRoundCh)
			// Mark V2-finalised blocks via the BlockChain so downstream
			// modules (filtermaps, RPC, gas oracle) see correct finalised tip.
			v2Engine.HookCommitBlock = func(header *types.Header) {
				eth.blockchain.SetFinalized(header)
				log.Debug("XDPoS V2: SetFinalized called", "number", header.Number, "hash", header.Hash())
			}
			xdposEngine.SetEngineV2(v2Engine)

			// AttachConsensusV2Hooks must run AFTER SetEngineV2 so adaptor.EngineV2
			// is non-nil inside the function. It wires both HookReward and HookPenalty
			// on the V2 engine via adaptor.EngineV2.SetHookReward / SetHookPenalty.
			// Canonical: XinFinOrg/XDPoSChain eth/hooks/engine_v2_hooks.go.
			hooks.AttachConsensusV2Hooks(xdposEngine, eth.blockchain, chainConfig)

			log.Info("XDPoS V2 engine wired for full header verification, finality, rewards, and penalty",
				"v2SwitchBlock", chainConfig.XDPoS.V2.SwitchBlock)
		}
	}

	// Initialize filtermaps log index.
	fmConfig := filtermaps.Config{
		History:        config.LogHistory,
		Disabled:       config.LogNoHistory,
		ExportFileName: config.LogExportCheckpoints,
		HashScheme:     scheme == rawdb.HashScheme,
	}
	chainView := eth.newChainView(eth.blockchain.CurrentBlock())
	historyCutoff, _ := eth.blockchain.HistoryPruningCutoff()
	var finalBlock uint64
	if fb := eth.blockchain.CurrentFinalBlock(); fb != nil {
		finalBlock = fb.Number.Uint64()
	}
	filterMaps, err := filtermaps.NewFilterMaps(chainDb, chainView, historyCutoff, finalBlock, filtermaps.DefaultParams, fmConfig)
	if err != nil {
		return nil, err
	}
	eth.filterMaps = filterMaps
	eth.closeFilterMaps = make(chan chan struct{})

	// TxPool
	if config.TxPool.Journal != "" {
		config.TxPool.Journal = stack.ResolvePath(config.TxPool.Journal)
	}
	legacyPool := legacypool.New(config.TxPool, eth.blockchain)
	// XDC: only current masternodes may send special txs (BlockSigners /
	// RandomizeSMC). Mirrors XDPoSChain eth/backend.go:430-440, including
	// treating every sender as a non-signer while the downloader is syncing.
	// Set before txpool.New starts the pool loops; the handler is read lazily.
	if xdposEngine, ok := eth.engine.(*XDPoS.XDPoS); ok {
		legacyPool.SetIsSigner(func(address common.Address) bool {
			if eth.handler == nil || eth.handler.downloader.Synchronising() {
				return false
			}
			return xdposEngine.IsAuthorisedAddress(eth.blockchain, eth.blockchain.CurrentHeader(), address)
		})
	}

	if config.BlobPool.Datadir != "" {
		config.BlobPool.Datadir = stack.ResolvePath(config.BlobPool.Datadir)
	}
	eth.blobTxPool = blobpool.New(config.BlobPool, eth.blockchain, legacyPool.HasPendingAuth)

	eth.txPool, err = txpool.New(config.TxPool.PriceLimit, eth.blockchain, []txpool.SubPool{legacyPool, eth.blobTxPool})
	if err != nil {
		return nil, err
	}
	// Only after txpool.New has run the pool's Init: the cache reads the pool's
	// lookup and store, which Init builds without holding the pool lock.
	eth.blobCache = blobpool.NewCache(eth.blobTxPool)

	if !config.TxPool.NoLocals {
		rejournal := config.TxPool.Rejournal
		if rejournal < time.Second {
			log.Warn("Sanitizing invalid txpool journal time", "provided", rejournal, "updated", time.Second)
			rejournal = time.Second
		}
		eth.localTxTracker = locals.New(config.TxPool.Journal, rejournal, eth.blockchain.Config(), eth.txPool)
		stack.RegisterLifecycle(eth.localTxTracker)
	}

	// Permit the downloader to use the trie cache allowance during fast sync
	cacheLimit := options.TrieCleanLimit + options.TrieDirtyLimit + options.SnapshotLimit
	if eth.handler, err = newHandler(&handlerConfig{
		NodeID:           eth.p2pServer.Self().ID(),
		Database:         chainDb,
		Chain:            eth.blockchain,
		TxPool:           eth.txPool,
		BlobPool:         eth.blobTxPool,
		Network:          networkID,
		Sync:             config.SyncMode,
		BloomCache:       uint64(cacheLimit),
		RequiredBlocks:   config.RequiredBlocks,
		SnapV2:           config.SnapV2,
		FetchProbability: config.BlobPool.FetchProbability,
	}); err != nil {
		return nil, err
	}

	// A.96 auto-pin from TrustedSyncCheckpoints is opt-in only (F-2026-19471):
	// the trust root is the binary, not a QC-verified chain epoch-switch header.
	// Canonical XDPoSChain has no compiled checkpoints (operator --fastsyncpivot
	// flags only). The default is dynamic pivot; supply --auto-checkpoint-pivot to
	// restore A.96. Operator-explicit --fastsyncpivot flags always take precedence
	// (FastSyncPivotNumber != 0 gate below). Must run BEFORE the A.6 wiring.
	if config.SyncMode == ethconfig.FastSync && config.FastSyncPivotNumber == 0 && config.AutoCheckpointPivot {
		ethconfig.AutoPinPivotFromCheckpoints(chainConfig, config)
	}

	// Operator-pinned Fast Sync trust anchor (refs #844 Phase A.6).
	// Wire the pivot values into the downloader so processFastSyncContent uses
	// them instead of the dynamic peer-head-derived pivot.
	if config.FastSyncPivotNumber != 0 {
		eth.handler.downloader.SetFastSyncPivot(
			config.FastSyncPivotNumber,
			config.FastSyncPivotHash,
			config.FastSyncPivotRoot,
		)
		// A.60.5 (refs #857): also expose the pivot as the trusted-checkpoint
		// anchor so the XDPoS V2 engine's inCheckpointCatchup gate fires and
		// strict round-based QC verification is relaxed for blocks within
		// 2*Epoch of the anchor. Without this, BeaconSync rejects every
		// header XDC peers serve with "failed to get epoch switch info for QC
		// verification" — the epoch-switch info lives in pre-pivot history
		// the node hasn't downloaded yet, and the V2 engine can't compute the
		// QC's signer set without it.
		eth.blockchain.SetFastSyncTrustedAnchor(
			config.FastSyncPivotNumber,
			config.FastSyncPivotHash,
		)
	} else if config.SyncMode == ethconfig.FastSync {
		// Clean fast-sync path: install the dynamic anchor callback so that when
		// the downloader selects a pivot from peer heads, verifyHeader relaxes QC
		// checks for the entire backfill range below it. Without this, BeaconSync
		// rejects every pre-pivot header with a gap-number mismatch or epoch-switch
		// error because the referenced epoch-switch blocks have not been downloaded
		// yet. The downloader authenticates the backfill range via parent-hash chain
		// from the pivot; this merely wires that trust signal to the consensus engine.
		// refs #1847 #1851 #1859
		bc := eth.blockchain
		eth.handler.downloader.SetFastSyncAnchorFn(func(n uint64, h common.Hash) {
			bc.SetFastSyncTrustedAnchor(n, h)
		})
	}

	eth.dropper = newDropper(eth.p2pServer.MaxDialedConns(), eth.p2pServer.MaxInboundConns())

	// A.97-M.A Task 2: construct and wire the BFT message dispatcher.
	// The Bfter connects inbound vote/timeout/syncInfo packets (currently no-ops
	// at handler_eth.go:90-98) to the V2 engine's verification + handling logic,
	// and forwards engine-emitted outbound BFT messages to the broadcast layer.
	// This is parity-restoring vs canonical XDPoSChain nodes, which always had
	// the Bfter wired. Non-mining sync nodes benefit too: inbound votes now drive
	// VoteHandler → QC formation → setNewRound, which is the correct behaviour.
	//
	// Construction is gated on the XDPoS V2 engine being available; non-XDPoS
	// or V1-only chains get a nil bfter and the no-op path in handler_eth.go.
	if xdposEngine, ok := eth.engine.(*XDPoS.XDPoS); ok {
		if v2, ok2 := xdposEngine.EngineV2.(*engine_v2.XDPoS_v2); ok2 && v2 != nil {
			h := eth.handler
			chainReader := eth.blockchain
			chainHeightFn := func() uint64 {
				cur := chainReader.CurrentBlock()
				if cur == nil {
					return 0
				}
				return cur.Number.Uint64()
			}
			b := bft.New(bft.BroadcastFns{
				Vote:     h.BroadcastVote,
				Timeout:  h.BroadcastTimeout,
				SyncInfo: h.BroadcastSyncInfo,
			}, chainReader, chainHeightFn)
			b.SetConsensusFns(v2)
			b.SetBroadcastCh(v2.BroadcastCh)
			if chainConfig.XDPoS != nil {
				b.SetEpoch(chainConfig.XDPoS.Epoch)
			}
			b.Start()
			h.SetBfter(b)
			log.Info("XDPoS V2 BFT dispatcher wired (A.97-M.A)", "epoch", chainConfig.XDPoS.Epoch)
		}
	}

	eth.miner = miner.New(eth, config.Miner, eth.engine)
	eth.miner.SetExtra(makeExtraData(config.Miner.ExtraData))
	eth.miner.SetPrioAddresses(config.TxPool.Locals)

	// XDC A.97-M.B Task 5+6: construct the XDPoS V2 production worker.
	// The worker is constructed here but NOT started — StartMining() starts it
	// only after Authorize() and sync checks pass.  Non-XDPoS chains (or XDPoS
	// without a V2 engine) leave xdcWorker nil.
	if xdposEngine, ok := eth.engine.(*XDPoS.XDPoS); ok {
		if v2, ok2 := xdposEngine.EngineV2.(*engine_v2.XDPoS_v2); ok2 && v2 != nil {
			eth.xdcWorker = miner.NewXdcWorker(
				xdposEngine,
				v2,
				eth.miner,
				eth.minePeriodCh,
				eth.newRoundCh,
				eth.handler,        // implements XdcBroadcaster (BroadcastBlock from handler_xdc.go)
				eth,                // implements XdcSynced (Synced() on eth.Ethereum)
				eth.accountManager, // for masternode signing tx (#922)
				eth.txPool,         // for masternode signing tx (#922)
			)
			log.Info("XDPoS V2 block-production worker constructed (A.97-M.B), awaiting StartMining")
		}
	}

	eth.APIBackend = &EthAPIBackend{stack.Config().ExtRPCEnabled(), stack.Config().AllowUnprotectedTxs, eth, nil}
	if eth.APIBackend.allowUnprotectedTxs {
		log.Info("Unprotected transactions allowed")
	}
	eth.APIBackend.gpo = gasprice.NewOracle(eth.APIBackend, config.GPO, config.Miner.GasPrice)

	// Start the RPC service
	eth.netRPCService = ethapi.NewNetAPI(eth.p2pServer, networkID)

	// Register the backend on the node
	stack.RegisterAPIs(eth.APIs())
	stack.RegisterProtocols(eth.Protocols())
	stack.RegisterLifecycle(eth)

	// Successful startup; push a marker and check previous unclean shutdowns.
	eth.shutdownTracker.MarkStartup()

	return eth, nil
}

func makeExtraData(extra []byte) []byte {
	if len(extra) == 0 {
		// create default extradata
		extra, _ = rlp.EncodeToBytes([]interface{}{
			uint(gethversion.Major<<16 | gethversion.Minor<<8 | gethversion.Patch),
			"geth",
			runtime.Version(),
			runtime.GOOS,
		})
	}
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		log.Warn("Miner extra data exceed limit", "extra", hexutil.Bytes(extra), "limit", params.MaximumExtraDataSize)
		extra = nil
	}
	return extra
}

// APIs return the collection of RPC services the ethereum package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *Ethereum) APIs() []rpc.API {
	apis := ethapi.GetAPIs(s.APIBackend)

	// Append all the local APIs.
	apis = append(apis, []rpc.API{
		{
			Namespace: "miner",
			Service:   NewMinerAPI(s),
		}, {
			Namespace: "eth",
			Service:   downloader.NewDownloaderAPI(s.handler.downloader, s.blockchain),
		}, {
			Namespace: "admin",
			Service:   NewAdminAPI(s),
		}, {
			Namespace: "debug",
			Service:   NewDebugAPI(s),
		}, {
			Namespace: "net",
			Service:   s.netRPCService,
		},
	}...)

	// XDC #814: upstream geth removed APIs() from the consensus.Engine interface,
	// so engine-specific namespaces are no longer auto-registered. XDPoS exposes
	// the XDPoS_* family (getMasternodesByNumber, networkInformation,
	// getEpochNumbersBetween, …) that block explorers, masternode dashboards,
	// and validator monitors depend on for v2.6.8 / v2.7.0 compatibility.
	// Wire it explicitly here.
	if xdposEng, ok := s.engine.(*XDPoS.XDPoS); ok {
		apis = append(apis, xdposEng.APIs(s.blockchain)...)
	}

	// A.97.2: expose xdc_getStateHealStatus under the "xdc" namespace so
	// operators can poll heal progress independently of the "eth" namespace.
	// The same BlockChainAPI instance handles both; GetStateHealStatus is
	// declared in internal/ethapi/api_xdc.go. Refs #894.
	apis = append(apis, rpc.API{
		Namespace: "xdc",
		Service:   ethapi.NewBlockChainAPI(s.APIBackend),
	})

	return apis
}

func (s *Ethereum) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *Ethereum) Miner() *miner.Miner { return s.miner }

// MinePeriodCh returns the channel on which the V2 engine publishes MinePeriod
// config updates (from UpdateParams + engine initial). PR-B xdcWorker reads this.
// May be nil if this is not an XDPoS V2 chain.
func (s *Ethereum) MinePeriodCh() chan int { return s.minePeriodCh }

// NewRoundCh returns the channel on which the V2 engine publishes new-round
// notifications (from setNewRound, non-blocking select). PR-B xdcWorker reads this.
// May be nil if this is not an XDPoS V2 chain.
func (s *Ethereum) NewRoundCh() chan types.Round { return s.newRoundCh }

func (s *Ethereum) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Ethereum) BlockChain() *core.BlockChain       { return s.blockchain }
func (s *Ethereum) TxPool() *txpool.TxPool             { return s.txPool }
func (s *Ethereum) BlobTxPool() *blobpool.BlobPool     { return s.blobTxPool }
func (s *Ethereum) BlobFetcher() *fetcher.BlobFetcher  { return s.handler.blobFetcher }
func (s *Ethereum) BlobCache() *blobpool.Cache         { return s.blobCache }
func (s *Ethereum) Engine() consensus.Engine           { return s.engine }
func (s *Ethereum) ChainDb() ethdb.Database            { return s.chainDb }
func (s *Ethereum) IsListening() bool                  { return true } // Always listening
func (s *Ethereum) Downloader() *downloader.Downloader { return s.handler.downloader }
func (s *Ethereum) Synced() bool                       { return s.handler.synced.Load() }
func (s *Ethereum) SetSynced()                         { s.handler.enableSyncedFeatures() }
func (s *Ethereum) ArchiveMode() bool                  { return s.config.NoPruning }
func (s *Ethereum) EngineMaxReorgDepth() uint64        { return s.config.EngineMaxReorgDepth }

// StartMining enables XDPoS V2 block production.
//
// Steps (A.97-M.B Task 6, design Q3 + Q5):
//  1. Resolve etherbase from cfg.Miner.PendingFeeRecipient (--miner.etherbase).
//  2. Find the unlocked keystore wallet for that address.
//  3. Build a 3-arg SignerFn wrapping wallet.SignData.
//  4. Call xdposEngine.Authorize(eb, signFn) — sets the signer on V1 and V2 engines.
//  5. Validate coinbase == signer (design Q3 — Prepare enforces this; we catch it early).
//  6. Start the xdcWorker loop.
//
// Gate: refuses if the node is still syncing (design Q5 gate 1).
// If syncing, logs a message and returns without error; the operator must
// restart after sync (design Q6 "restart required" note).
func (s *Ethereum) StartMining() error {
	xdposEngine, ok := s.engine.(*XDPoS.XDPoS)
	if !ok {
		return fmt.Errorf("StartMining: engine is not XDPoS (got %T)", s.engine)
	}
	if s.xdcWorker == nil {
		return fmt.Errorf("StartMining: XDPoS V2 worker not constructed (non-V2 chain?)")
	}

	// Step 1: resolve etherbase.
	eb := s.config.Miner.PendingFeeRecipient
	if eb == (common.Address{}) {
		return fmt.Errorf("StartMining: no etherbase set — use --miner.etherbase <address>")
	}

	// Step 2: find the unlocked keystore for the etherbase address.
	// We go directly to the KeyStore backend (not wallet.SignData) because
	// SignData re-hashes the payload with keccak256, but XDPoS V2's signSignature
	// already passes a pre-computed hash (TimeoutSigHash / VoteSigHash).
	// Using SignData would double-hash and produce a signature that ecrecover
	// cannot match — resulting in "Signer not in masternode list" on receipt.
	// XDPoSChain solved this via wallet.SignHash which calls ks.SignHash →
	// crypto.Sign(hash, key) directly; we replicate that here.
	var ks *keystore.KeyStore
	for _, b := range s.accountManager.Backends(keystore.KeyStoreType) {
		if kst, ok := b.(*keystore.KeyStore); ok {
			if accts := kst.Accounts(); len(accts) > 0 {
				for _, a := range accts {
					if a.Address == eb {
						ks = kst
						break
					}
				}
			}
		}
		if ks != nil {
			break
		}
	}
	if ks == nil {
		return fmt.Errorf("StartMining: etherbase %s not found in keystore (is the keystore loaded?)", eb.Hex())
	}

	// Step 3: build 3-arg SignerFn wrapping ks.SignHash (signs hash bytes directly).
	// XDPoS V2 signSignature passes the pre-computed hash — no mimeType needed.
	signFn := func(a accounts.Account, mimeType string, hash []byte) ([]byte, error) {
		return ks.SignHash(a, hash)
	}

	// Step 4: authorize the engine — sets signer on both V1 and V2 sub-engines.
	xdposEngine.Authorize(eb, signFn)

	// Step 5: validate coinbase == signer (fast-fail before the worker loop).
	s.lock.RLock()
	configured := eb
	s.lock.RUnlock()
	if configured != eb {
		return fmt.Errorf("StartMining: coinbase/etherbase mismatch (%s != %s)", configured.Hex(), eb.Hex())
	}
	log.Info("XDPoS2: StartMining authorized", "coinbase", eb)

	// XDPoS V2 masternodes declare themselves at-tip when they start mining.
	// Unlike PoS/merge nodes, XDPoS doesn't have a beacon-driven synced gate;
	// call SetSynced() so transaction processing and BFT participation are
	// enabled immediately (mirrors XDPoSChain's behaviour — no synced guard).
	s.SetSynced()
	// XdcBulkSyncMode management — A.99.3 + earlier no-force-false fix:
	// Only clear XdcBulkSyncMode when the xdcSyncer is at-tip or not yet started
	// (genesis devnet case). If the syncer is still in FULL_SYNC / TIP_CATCH, leave
	// it true so InsertChain uses fullVerify=false (tolerates the synthetic-epoch
	// active-masternodes-len anomaly). The syncer flips it false itself in
	// maybeTransitionToAtTip / fullSyncOnce once the local head catches up. Refs #894.
	if s.handler == nil || s.handler.xdcSync == nil ||
		xdcSnapPhase(s.handler.xdcSync.snapPhase.Load()) == phaseAtTip {
		core.XdcBulkSyncMode.Store(false)
		log.Info("XDPoS2: cleared XdcBulkSyncMode (at tip or no syncer)")
		// We are about to start producing blocks, so state validation must be on and
		// the miner must be able to open the real head state. If checkpointSyncNoState
		// is still latched, this either clears it (head state present) or logs a loud
		// ERROR telling the operator the DB must be resynced. Either way the silent
		// "seated masternode that never mints and never says why" state is gone.
		s.blockchain.ClearCheckpointSyncNoState()
	} else {
		log.Info("XDPoS2: xdcSyncer still syncing — keeping XdcBulkSyncMode=true until at-tip",
			"phase", xdcSnapPhase(s.handler.xdcSync.snapPhase.Load()))
	}

	// Step 6: start the worker.
	s.xdcWorker.Start(eb)
	return nil
}

// StopMining stops the XDPoS V2 block-production worker if it is running.
func (s *Ethereum) StopMining() {
	if s.xdcWorker != nil {
		s.xdcWorker.Stop()
	}
}

// Protocols returns all the currently configured network protocols to start.
func (s *Ethereum) Protocols() []p2p.Protocol {
	protos := eth.MakeProtocols((*ethHandler)(s.handler), s.networkID, s.discmix)
	if s.config.SnapshotCache > 0 {
		// SNAP/2 drops the trie node requests SNAP/1 healing needs, and peers
		// negotiate the highest shared version, so a node syncing via SNAP/1
		// must not advertise it. Every other node serves both versions.
		dl := s.handler.downloader
		advertiseV2 := dl.SnapSyncVersion() == snap.SNAP2 || dl.ConfigSyncMode() != ethconfig.SnapSync
		protos = append(protos, snap.MakeProtocols((*snapHandler)(s.handler), advertiseV2)...)
	}
	return protos
}

// Start implements node.Lifecycle, starting all internal goroutines needed by the
// Ethereum protocol implementation.
func (s *Ethereum) Start() error {
	if err := s.setupDiscovery(); err != nil {
		return err
	}

	// Regularly update shutdown marker
	s.shutdownTracker.Start()

	// Start the networking layer
	s.handler.Start(s.p2pServer.MaxPeers)

	// Start the connection manager with inclusion-based peer protection.
	s.dropper.Start(s.p2pServer, func() bool { return !s.Synced() }, s.handler.txTracker.GetAllPeerStats)

	// Subscribe to chain events for the filterMaps head updater.
	s.fmHeadSub = s.blockchain.SubscribeChainEvent(s.fmHeadEventCh)
	s.fmBlockProcSub = s.blockchain.SubscribeBlockProcessingEvent(s.fmBlockProcCh)

	// start log indexer
	s.filterMaps.Start()
	go s.updateFilterMapsHeads()
	return nil
}

func (s *Ethereum) newChainView(head *types.Header) *filtermaps.ChainView {
	if head == nil {
		return nil
	}
	return filtermaps.NewChainView(s.blockchain, head.Number.Uint64(), head.Hash())
}

func (s *Ethereum) updateFilterMapsHeads() {
	headEventCh := s.fmHeadEventCh
	blockProcCh := s.fmBlockProcCh
	defer func() {
		s.fmHeadSub.Unsubscribe()
		s.fmBlockProcSub.Unsubscribe()
		for {
			select {
			case <-headEventCh:
			case <-blockProcCh:
			default:
				return
			}
		}
	}()

	var head *types.Header
	setHead := func(newHead *types.Header) {
		if newHead == nil {
			return
		}
		if head == nil || newHead.Hash() != head.Hash() {
			head = newHead
			chainView := s.newChainView(head)
			if chainView == nil {
				return
			}
			historyCutoff, _ := s.blockchain.HistoryPruningCutoff()
			var finalBlock uint64
			if fb := s.blockchain.CurrentFinalBlock(); fb != nil {
				finalBlock = fb.Number.Uint64()
			}
			s.filterMaps.SetTarget(chainView, historyCutoff, finalBlock)
		}
	}
	setHead(s.blockchain.CurrentBlock())

	for {
		select {
		case ev := <-headEventCh:
			setHead(ev.Header)
		case blockProc := <-blockProcCh:
			s.filterMaps.SetBlockProcessing(blockProc)
		case <-time.After(time.Second * 10):
			setHead(s.blockchain.CurrentBlock())
		case ch := <-s.closeFilterMaps:
			close(ch)
			return
		}
	}
}

func (s *Ethereum) setupDiscovery() error {
	eth.StartENRUpdater(s.blockchain, s.p2pServer.LocalNode())

	// Add eth nodes from DNS.
	dnsclient := dnsdisc.NewClient(dnsdisc.Config{})
	if len(s.config.EthDiscoveryURLs) > 0 {
		iter, err := dnsclient.NewIterator(s.config.EthDiscoveryURLs...)
		if err != nil {
			return err
		}
		s.discmix.AddSource(iter)
	}

	// Add snap nodes from DNS.
	if len(s.config.SnapDiscoveryURLs) > 0 {
		iter, err := dnsclient.NewIterator(s.config.SnapDiscoveryURLs...)
		if err != nil {
			return err
		}
		s.discmix.AddSource(iter)
	}

	// Add DHT nodes from discv4.
	if s.p2pServer.DiscoveryV4() != nil {
		iter := s.p2pServer.DiscoveryV4().RandomNodes()
		resolverFunc := func(ctx context.Context, enr *enode.Node) *enode.Node {
			// XDC A.61: legacy XDPoSChain peers (the entire mainnet today)
			// do not implement EIP-868 ENRRequest. RequestENR returns nil
			// for them. If we drop nil, AsyncFilter discards every legacy
			// XDC peer and the dial pool starves (#857).
			// Keep the original node; let the eth handshake filter decide.
			nn, _ := s.p2pServer.DiscoveryV4().RequestENR(enr)
			if nn == nil {
				return enr
			}
			return nn
		}
		iter = enode.AsyncFilter(iter, resolverFunc, maxParallelENRRequests)
		iter = enode.Filter(iter, eth.NewNodeFilter(s.blockchain))
		iter = enode.NewBufferIter(iter, discoveryPrefetchBuffer)
		s.discmix.AddSource(iter)
	}

	// Add DHT nodes from discv5.
	if s.p2pServer.DiscoveryV5() != nil {
		filter := eth.NewNodeFilter(s.blockchain)
		iter := enode.Filter(s.p2pServer.DiscoveryV5().RandomNodes(), filter)
		iter = enode.NewBufferIter(iter, discoveryPrefetchBuffer)
		s.discmix.AddSource(iter)
	}

	return nil
}

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Ethereum protocol.
func (s *Ethereum) Stop() error {
	// Stop all the peer-related stuff first.
	s.discmix.Close()
	s.dropper.Stop()
	s.handler.txTracker.Stop()
	s.handler.Stop()

	// Then stop everything else.
	ch := make(chan struct{})
	s.closeFilterMaps <- ch
	<-ch
	s.filterMaps.Stop()
	s.blobCache.Stop()
	s.txPool.Close()
	s.blockchain.Stop()
	s.engine.Close()

	// Clean shutdown marker as the last thing before closing db
	s.shutdownTracker.Stop()

	s.chainDb.Close()

	return nil
}
