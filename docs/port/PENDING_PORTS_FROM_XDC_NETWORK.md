# Pending Ports from xdc-network → fix/pathdb-rlp-import

**Audit date:** 2026-05-24
**Methodology:** `git diff 54a91b3a..xdc-network -- <file>` for every Go file
modified on xdc-network but not on the current branch (49 files). Classified
each as DONE / NOT-XDC / PENDING-CRITICAL / PENDING-NICE-TO-HAVE.

**Top-line finding:**
- 4 NOT-XDC (FastSync-related, dropped by ADR #58)
- 11 PENDING-CRITICAL
- 34 PENDING-NICE-TO-HAVE

Sync currently runs past 12M on Apothem on `fix/pathdb-rlp-import`, but this
masks issues: **V2 blocks are being accepted via the V1 light-verification
fast path** because `eth/backend.go` never constructs `engine_v2.New(...)`,
so `XDPoS.EngineV2 == nil` and all V2 code paths fall through to V1. This
will look like a green sync but does not execute V2 consensus (no QC checks,
no masternode rotation, no penalty/reward at epoch switches, no BFT
finality). A consensus parity harness will surface this; basic sync-to-head
will not.

---

## PENDING-CRITICAL (11 files, suggested order)

### 1. `params/protocol_params.go`
Add `XDCBaseFee = 12500000000` (12.5 gwei) constant block. Dependency for #2.
Trivial — 8 lines.

### 2. `consensus/misc/eip1559/eip1559.go`
Port the entire `VerifyEIP1559Header` + `CalcBaseFee` rewrite:
- Replace `IsLondon(...)` with `IsEIP1559(...)` throughout
- In `CalcBaseFee`, branch on `config.XDPoS != nil` → return `XDCBaseFee`
- Drop gas-limit elasticity-multiplier at the fork
- Add checkpoint-sync edge-case relaxations

**Why:** Without this, every post-EIP-1559 block fails base-fee verification.

### 3. `core/vm/contracts.go`
Add `PrecompiledContractsXDCBerlin` map (MODEXP without EIP-2565) and route
XDC chains to it for Berlin..pre-EIP1559 blocks.

**Why:** MODEXP gas-used will differ from canonical for every block
containing MODEXP in that range → state-root divergence. Issue #710/#716.

### 4. `core/txpool/validation.go`
In `ValidateTransaction`, branch on `opts.Config.IsXDC()` and pass
`isEIP2028=false` to `IntrinsicGas`. 6 lines.

**Why:** XDC uses 68 gas/non-zero-byte (not 16 per EIP-2028) → every tx with
calldata diverges on intrinsic-gas calculation.

### 5. `core/txpool/legacypool/legacypool.go`
Add 18-line blacklist guard at top of `validateTx` using `common.IsInBlacklist`
(already present at HEAD) and `BlackListHFNumber`.

**Why:** Canonical XDC mempool rejects tx where sender/recipient is on the
blacklist past the hardfork. Currently we accept them.

### 6. `eth/backend.go`  ← **HIGHEST LEVERAGE**
Wire ~250 lines of XDC integration glue:
- `common.CopyXDCConstants(chainID)`
- `XDPoS.SetNetworkConstants(chainID)`
- `engine_v2.New(...)` instantiation
- `xdposEngine.SetEngineV2(v2Engine)`
- `v2Engine.HookCommitBlock = ...`
- `v2Engine.HookPenalty = ...` (60-line closure)
- `v2Engine.SetHookReward(...)`
- `hooks.AttachConsensusV1Hooks(...)`
- `hooks.AttachConsensusV2Hooks(...)`
- `SyncFromBlock` validation + pass-through to BlockChain
- `XdcAgent` instantiation (if/when masternode work begins)

**Why:** `eth/hooks/*.go` and `SetEngineV2` exist at HEAD as dead code. V2
engine instance is never constructed. This is the single biggest gap.

### 7. `eth/handler_eth.go`
Add to `ethHandler.Handle`'s switch:
- `*eth.VotePacket`, `*eth.TimeoutPacket`, `*eth.SyncInfoPacket` → call
  `handleInboundVote/Timeout/SyncInfo` (define them, use `getOrInitBft()`)
- `*eth.NewBlockHashesPacket`, `*eth.NewBlockPacket` → trigger sync /
  InsertChain instead of being dropped

**Why:** At HEAD, these messages decode but fall through to
`unexpected eth packet type`. V2 BFT engine never receives a single
vote/timeout/syncinfo from peers.

### 8. `eth/peer.go` + `eth/peerset.go`
Pair these:
- `peer.go`: add `knownVotes/knownTimeouts/knownSyncInfos` LRU caches +
  `Mark*`/`Known*` methods + `Difficulty`/`Head`/`Number` fields on
  `ethPeerInfo`
- `peerset.go`: `PeersWithoutVote/Timeout/SyncInfo` helpers, wire LRU caches
  in `registerPeer`

**Why:** Required to prevent V2 broadcast storms once handler_eth dispatch
is wired (#7). Issue #272.

### 9. `common/bytes.go`
Add `hasXdcPrefix` and the `xdc`/`XDC` branch in `FromHex`. Without this,
any JSON-RPC call passing an address as `xdc...` (XDC display format) silently
produces zero bytes.

### 10. `internal/web3ext/web3ext.go`
Single-line `"xdpos": XDPoSJs,` registration. Required for
`web3.xdpos.*` RPC methods to work from the geth console.

### 11. `core/blockchain_reader.go`
Add `SetCheckpointSyncNoState`, `GetTrustedCheckpointAnchor`,
`checkpointSyncNoState` atomic.

**Why:** V2 engine (`engine_v2.go:1243`, `utils.go:143`) asserts the
`GetTrustedCheckpointAnchor` interface — currently fails at HEAD, so V2
anchor-relaxation never fires. Important for checkpoint-sync past V2 switch.

---

## PENDING-NICE-TO-HAVE (34 files, grouped)

### Checkpoint-sync polish (5 files, skip unless `--syncfromblock`)
- `core/headerchain.go` — checkpoint-anchor break in `Reorg`'s parent walk
- `core/rawdb/chain_freezer.go` — skip freezer loop for checkpoint nodes
- `core/rawdb/chain_iterator.go` — log-level downgrade
- `core/rawdb/database.go` — allow ancient DB without block 1
- `core/txindexer.go` — `syncFromBlock` field

### Sync robustness — recommended for production (3 files)
- `eth/downloader/fetchers_concurrent.go` — 60s stall detect + peer-drop
- `eth/downloader/queue.go` — `errStaleDelivery` rework (NOT the FastSync parts)
- `p2p/dial.go` — staticDial backoff + lastErr tracking

All three fix #638 (peer-drop loops). Sync runs without them but is more
fragile under bad peers.

### Perf tuning (3 files, all trivial)
- `ethdb/batch.go` — `IdealBatchSize` 100KB → 2MB
- `ethdb/pebble/pebble.go` — Pebble L0CompactionThreshold tuning
- `eth/downloader/queue.go` cache-size parts only

### Drop-in peer compat with old XDPoSChain v2.6.x (3 files)
- `eth/protocols/eth/discovery.go` — accept peers without ENR ForkID
- `eth/protocols/eth/broadcast.go` — nil-guard on `p.txpool`
- `p2p/rlpx/rlpx.go` — restore pre-EIP-8 RLPx auth decode

Skip unless mainnet still has v2.6.x peers.

### Masternode-only — defer to M8 (4 files)
- `miner/miner.go` — Agent interface scaffolding
- `miner/xdc_agent.go` — 353-line XDC sealing agent
- `eth/downloader/downloader_xdc.go` — legacy 1330-line XDPOS2 downloader
  (replaced by `eth/sync_xdc.go` 272 lines)
- `eth/downloader/downloader_checkpoint.go` — XDC checkpoint discovery

Per CLAUDE.md, M8 requires Opus planner escalation before implementing.

### Operator UX (4 files, optional)
- `build/ci.go` — `-buildvcs=false` build flag
- `cmd/geth/{chaincmd,config,main,misccmd}.go` — `clientIdentifier()`,
  `dump-checkpoint` subcommand, `SyncFromBlock` flag, `/health` endpoint
- `node/config.go` — cross-client chaindata dir compat (geth ↔ XDC binary)

### Snap-sync polish (1 file)
- `eth/protocols/snap/sync.go` — `maxMissingNodeAttempts` for unrecoverable
  pruned-state nodes. Only matters in snap-sync mode.

### Recovery utilities — pair-port if/when needed (3 files)
- `triedb/database.go` — `DiskLayerRoot()`
- `triedb/pathdb/database.go` — backing implementation
- `p2p/util.go` — `expHeap.remove(item)` helper (required by `p2p/dial.go`)

### p2p extras (2 files)
- `p2p/server.go` — `firstPeerlessTime` + `staticDialedConn` MaxPeers bypass
- `p2p/discover/v4_udp.go` — one diagnostic Debug log

### Cosmetic / log noise (3 files, skip)
- `core/txpool/{txpool,blobpool/blobpool}.go` — log-level downgrades
- `ethstats/ethstats.go` — `Consensus` field in nodeInfo

---

## NOT-XDC (4 files, no port needed)
- `crypto/ecies/ecies.go` — `params.BlockSize` → constant `1` (upstream cleanup)
- `eth/catalyst/api.go` — FastSync mode check (dropped per ADR #58)
- `eth/downloader/syncmode.go` — FastSync constant (dropped per ADR #58)
- `eth/ethconfig/syncmode.go` — FastSync constant (dropped per ADR #58)

---

## Recommended next batch

The four-PR sequence with highest leverage and lowest risk:

**Batch A — EVM consensus parity (independent, can land first)**
1. `params/protocol_params.go` + `consensus/misc/eip1559/eip1559.go` (XDCBaseFee)
2. `core/vm/contracts.go` (XDCBerlin precompile)
3. `core/txpool/validation.go` (EIP-2028 disable)

**Batch B — V2 engine activation (the big one)**
4. `eth/backend.go` (engine_v2.New + hook wiring) + paired
   `core/blockchain_reader.go` (GetTrustedCheckpointAnchor)

**Batch C — V2 wire dispatch + broadcast safety**
5. `eth/handler_eth.go` (Vote/Timeout/SyncInfo dispatch) +
   `eth/peer.go` + `eth/peerset.go` (knownVote LRUs)

**Batch D — RPC + CLI niceties**
6. `common/bytes.go` (FromHex xdc prefix)
7. `internal/web3ext/web3ext.go` (xdpos JS module)
8. `core/txpool/legacypool/legacypool.go` (blacklist guard)

Each batch should be verified against the parity harness on a fresh sync.
