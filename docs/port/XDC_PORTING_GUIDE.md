# XDC Porting Guide — Reconstructing the XDC overlay on a fresh geth release

This document records every XDC-specific change applied on top of pristine
go-ethereum **v1.17.3** (commit `117e067f0f0bae1a17082321f224dedb6765b10f`,
release tag `v1.17.3`) to produce the head of branch
`reset/integration-1.17.3`. The scope is **262 files changed, +69,413/-412
lines, 243 commits** between `117e067f0` and `8337f48ec`.

The intended audience is an engineer (human or AI) tasked with applying the
same overlay to a future upstream tag — e.g. `v1.18.x`, `v2.0`. Every claim
below is grounded in the actual files present in the tree; line numbers and
quoted snippets were re-read at the time of writing.

---

## 1. Executive summary

XDC Network is a delegated-PoS chain forked from Ethereum. It uses two
consensus generations:

- **XDPoS V1** — Clique-style round-robin leader rotation over a 108-node
  masternode set. Epoch length 900 blocks. Used from genesis to a
  per-network `V2.SwitchBlock`.
- **XDPoS V2** — HotStuff-derived BFT with QuorumCerts, votes, timeouts.
  Switches in mid-chain (mainnet block `80,370,000`, Apothem block
  `56,828,700`). Reward distribution, masternode rotation, and finality
  flow change at the switch.

This port adds:

1. The XDPoS engine (V1 + V2) under `consensus/XDPoS/...`.
2. XDC-specific chain configs and 80+ trusted-sync checkpoints in
   `params/config.go`.
3. Embedded genesis JSON with pre-deployed validator/blocksigner/randomize
   smart contracts in `core/xdc_genesis/` and `genesis/`.
4. A custom XDPOS/100 wire protocol that coexists with upstream ETH/69 +
   ETH/70 on the same `eth` capability.
5. A pre-merge sync loop (`eth/sync_xdc.go`, ~1240 lines) because the
   v1.17.3 BeaconSync is post-merge and stops idle without a CL.
6. An RPC namespace `XDPoS_*` (plus extensions to `eth_*` block fields).
7. About 30 `XDC:` bypass-log call-sites in `core/*` and `eth/*` that
   document where the XDC state model legitimately diverges from upstream
   Ethereum validation rules.
8. Solidity-source-derived Go bindings for the XDPoS system contracts
   (`contracts/blocksigner/`, `contracts/validator/`, `contracts/randomize/`,
   `contracts/multisigwallet/`, `contracts/trc21issuer/`, `contracts/XDCx/`).

The branch HEAD `8337f48ec` is **not** at full consensus parity with
canonical XDPoSChain v2.6.8. See `docs/port/CONSENSUS_PARITY.md` and
`docs/port/STATUS.md` for milestone status. The port is M3-blocked on
peer-fleet snapshot indexing; the consensus-rule mismatches are tracked
inline as `XDC:` comments and as ADRs under `docs/adr/`.

---

## 2. Prerequisites & skills required

To execute this port from scratch you need:

- **Go**, including modern generics (geth v1.17.x uses `lru.Cache[K,V]`,
  `rlp.RawList[T]`, etc.).
- **go-ethereum internals**: `core.BlockChain`, `core/state.StateDB`,
  `eth/downloader`, `eth/protocols/eth`, `eth/protocols/snap`, the
  `consensus.Engine` interface (now also `consensus.CacheInvalidator`).
- **Ethereum consensus model**: header layout, RLP encoding, trie /
  pathdb, receipt-root / state-root semantics, EIP-2929/3529/3860 gas
  rules, EIP-1559 base-fee semantics, EIP-2565 MODEXP pricing.
- **XDPoS BFT specification**: rounds, QuorumCerts (QC), TimeoutCerts
  (TC), epoch-switch headers, the "M1 (signers)" and "M2 (validators)"
  double-signing scheme used in V1, and the masternode set computation
  via the Validator (0x88), BlockSigner (0x89), and Randomize (0x90)
  contracts.
- **Cryptography**: secp256k1 ECDSA `crypto.Sign` / `crypto.Ecrecover`.
  BLS is **not** used in this port — XDPoS V2 uses k-of-N ECDSA
  signatures over the QC payload, not aggregate signatures.
- **Solidity** for reading/regenerating the XDPoS system-contract
  bindings under `contracts/`.
- **Git rebase + merge workflows**: the original port used a
  bundle-merge mechanic (B0..B11) preserving 677 SHAs while presenting
  ~12 reviewable units. See `docs/PORT_LOG.md`.

Familiarity with the project structure under
`/Users/anilchinchawale/github/XDCNetwork/deVTest/` is helpful: this
repo (`go-ethereum`) is the XDC-overlay-on-v1.17.3-geth branch.
Canonical XDPoS behaviour is referenced from sibling
`../XDPoSChain` (also pulled locally).

---

## 3. High-level architecture diagram

```
                       node startup (cmd/geth)
                              │
                              ▼
   ┌──────────────────────────────────────────────────────────┐
   │ cmd/utils/flags.go::SetXDCNetworkConfig                  │
   │   --xdcmainnet  / --apothem flag                         │
   │     → cfg.NetworkId = 50 / 51                            │
   │     → cfg.Genesis = core.DefaultXDC{Mainnet,Apothem}     │
   │                                                          │
   │ params.XDC{Mainnet,Apothem}ChainConfig (params/config.go)│
   │   ChainID 50 / 51                                        │
   │   XDPoS = { Period: 2, Epoch: 900, Gap: 450, Reward:5000}│
   │   XDPoS.V2 = { SwitchBlock, AllConfigs, CurrentConfig }  │
   │   TrustedSyncCheckpoints = [...]                         │
   └──────────────────────────────────────────────────────────┘
                              │
                              ▼
   ┌──────────────────────────────────────────────────────────┐
   │ core/genesis.go::SetupGenesisBlock / LoadChainConfig     │
   │   • patchMissingForkBlocks (mainnet/apothem)             │
   │   • patch missing V2 / TrustedSyncCheckpoints            │
   │   • write genesis state from xdc_genesis/{mainnet,       │
   │     apothem}.json — pre-deploys 0x88/0x89/0x90 bytecode  │
   └──────────────────────────────────────────────────────────┘
                              │
                              ▼
   ┌──────────────────────────────────────────────────────────┐
   │ eth/backend.go::New                                      │
   │   engine := XDPoS.New(chainConfig.XDPoS, chainDb)        │
   │   hooks.AttachConsensusV1Hooks(engine, blockchain, cfg)  │
   │   hooks.AttachConsensusV2Hooks(engine, blockchain, cfg)  │
   │   v2 := engine_v2.New(chainConfig, db, nil, nil)         │
   │   engine.SetEngineV2(v2)                                 │
   │   v2.SetHookReward(engine.HookReward)                    │
   │   common.CopyXDCConstants(chainConfig.ChainID.Uint64())  │
   └──────────────────────────────────────────────────────────┘
                              │
            block import path  │  RPC path     P2P path
              ┌────────────────┼────────────────┐
              ▼                ▼                ▼
   ┌─────────────────┐ ┌─────────────────┐ ┌───────────────────┐
   │ core/           │ │ internal/ethapi/│ │ eth/protocols/eth/│
   │  block_         │ │  api_xdc.go     │ │  protocol.go      │
   │  validator.go   │ │  (xdc_* RPCs,   │ │  XDPOS2 = 100     │
   │  • checkpoint   │ │   RPCMarshal-   │ │  VoteMsg 0xe0     │
   │    sync bypass  │ │   HeaderXDC)    │ │  TimeoutMsg 0xe1  │
   │                 │ │                 │ │  SyncInfoMsg 0xe2 │
   │ core/           │ │ consensus/      │ │  StatusPacket62   │
   │  state_         │ │  XDPoS/         │ │                   │
   │  processor.go   │ │  api*.go        │ │ eth/handler_xdc.go│
   │  • TIPSigning   │ │  (XDPoS_* RPCs) │ │  Broadcast{Vote,  │
   │    dispatch     │ │                 │ │   Timeout,        │
   │  • ApplyEmpty   │ │                 │ │   SyncInfo}       │
   │    Transaction  │ │                 │ │                   │
   │  • TRC21 owner  │ │                 │ │ eth/sync_xdc.go   │
   │    routing      │ │                 │ │  • xdcSyncer loop │
   │  • parentState  │ │                 │ │  • snap phase FSM │
   │    snapshot     │ │                 │ │  • fork-tainted   │
   │                 │ │                 │ │    peer pool      │
   │ consensus/XDPoS │ │                 │ │  • rollback on    │
   │  /xdpos.go      │ │                 │ │    UnknownAncestor│
   │  Author/Verify/ │ │                 │ │                   │
   │  Prepare/       │ │                 │ │ eth/bft/handler.go│
   │  Finalize/Seal  │ │                 │ │  V2 BFT message   │
   │  → routes V1/V2 │ │                 │ │  dispatcher       │
   │  by IsV2Block   │ │                 │ │                   │
   └─────────────────┘ └─────────────────┘ └───────────────────┘
```

---

## 4. Repository archaeology

### 4.1 Fork base

- **Upstream geth tag**: v1.17.3
- **Commit SHA**: `117e067f0f0bae1a17082321f224dedb6765b10f`
- **Branch HEAD at time of writing**: `8337f48ec` on `reset/integration-1.17.3`
- **Commits in overlay**: 243
- **Lines added/deleted**: +69,413 / -412

The overlay was produced by an explicit baseline reset: the original
xdc-network branch carried ~677 commits on top of geth pre-1.17.0
(commit `54a91b3a`). Issue #740 reset that overlay onto v1.17.3 in
bundles `B0`..`B11`; see `docs/PORT_LOG.md` for the bundle merge
ordering decision recorded as `[PLANNER-ADVICE 2026-05-23]`.

### 4.2 Major themes by date (selected)

| Commit | Theme |
|---|---|
| `4aeb4ab7e` | feat(engine): Register XDPoS engine and add XDC genesis handling |
| `def2a50dd` | feat(params): Add XDC mainnet and Apothem chain configurations |
| `a2adfac0e` | feat: Add XDPoS consensus engine for XDC Network |
| `c25528c75` | merge: B0 — foundation (XDPoS engine + chain config + contracts + genesis) |
| `b787b7099` | B10: partial port of eth/ |
| `c40a66f70` | B5: bulk XDC overlay onto v1.17.3 (deferred F-bucket) |
| `74f7f44f9` | B3: add XDPOS2/100 wire protocol per Opus advice |
| `7daa274b8` | xdc: wire XDPoS V2 engine + accept V2 BFT messages at handler |
| `880456794` | core/vm: XDC EVM instruction set markers for Berlin..Shanghai |
| `b0644ebd9` | eth/sync_xdc: panic recover so node survives reward overflow |
| `e8ddde738` | consensus/XDPoS, eth/backend: expose XDPoS_* RPC namespace (#814) |
| `08d651913` | core/vm: gate StaticCall zero-value touch on Istanbul for XDC (#812) |
| `1506e0a67` | core/vm: opBlobBaseFee — handle nil BlobBaseFee (XDC mainnet stall fix) |
| `b9c9ab4c7..111967600` | #807 Phase 3..14: snap-sync state machine + stall fallback |

### 4.3 How to reproduce the baseline reset against a future tag

The mental model is:

```bash
# starting state: branch reset/integration-1.17.3 head = 8337f48ec
# new upstream tag: vX.Y.Z

git remote add upstream https://github.com/ethereum/go-ethereum.git
git fetch upstream --tags
git checkout -b reset/integration-vX.Y.Z vX.Y.Z

# replay the 243 XDC-overlay commits onto the new base:
git rebase --onto reset/integration-vX.Y.Z 117e067f0 reset/integration-1.17.3
```

In practice you will not get a clean rebase. The conflict surface is
concentrated in:

- `consensus/consensus.go` — engine interface; XDC adds `CacheInvalidator`.
- `core/blockchain.go` — many `XDC:` markers around `SetHead`,
  `checkpointSyncNoState`, V2-switch trie commit.
- `core/state_processor.go` — `Process()` parentState capture, TIPSigning
  dispatch, TIPXDCXReceiver dispatch, `coinbaseOwner` resolution.
- `core/state_transition.go` — TRC21 owner routing, special-tx fee bypass,
  XDC blacklist enforcement, EIP-3529 deferral, EIP-2028 disable.
- `core/types/block.go` — three new always-encoded header fields
  (`Validators`, `Validator`, `Penalties`) and `HashNoValidator`.
- `core/types/state_account.go` — `BigBalance` overflow path.
- `eth/protocols/eth/protocol.go` — `XDPOS2 = 100`, `VoteMsg`/`TimeoutMsg`/
  `SyncInfoMsg`, `StatusPacket62` (no ForkID).
- `eth/handler.go`, `eth/handler_eth.go` — XDPOS2 dispatch + xdcSyncer trigger.
- `params/config.go` — entire XDC mainnet/Apothem chain configs +
  `Rules{IsXDC, IsEIP1559, IsEIP3860}` extensions.

The bundle order used in the original port (per Opus planner advice in
`docs/PORT_LOG.md`) was:

```
B0 (engine + config + contracts + genesis) →
B1 (Phase A continuation, drop cmd/XDC) →
B3 (XDPOS2/100 wire protocol) →
B4 (P2P discovery v4 + XDPOS2 RLP) →
B5 (bulk XDC overlay) →
B7 (core/blockchain*) →
B2 (V2 engine wiring) →
B6 (snap-sync hooks) →
B8 (state_processor + transition + evm) →
B9 (downloader) →
B10 (eth/handlers) →
B11 (RPC + final polish)
```

The rationale: substrate first (additive, low-collision), V2 engine
second (depends on the substrate). Land collision-heavy plumbing where
v1.17.3 is still vanilla, then layer V2.

---

## 5. Subsystem-by-subsystem changes

### 5.a Chain configuration & params

#### Why

XDC mainnet and Apothem each have their own chain ID, fork schedule,
masternode bootstrap list, and XDPoS-specific V2 round configuration.
Several "EIPs" that upstream couples to a single fork block are split
across two on XDC (e.g. EIP-2929/3529/3860 land at `Eip1559Block`,
not Berlin/London/Shanghai).

#### What was added/modified

| File | Δ lines | Purpose |
|---|---:|---|
| `params/config.go` | +2022 | `XDCMainnetChainConfig`, `XDCApothemChainConfig`, `XDPoSConfig`, `V2`, `V2Config`, `MainnetV2Configs`, `ApothemV2Configs`, `TrustedSyncCheckpoint`, `Rules{IsXDC,IsEIP1559,IsEIP3860}`, `IsXDC()`, `XDCBlocksPerYear()`, `patchMissingForkBlocks()`, `~17 IsTIP*` predicates |
| `params/bootnodes.go` | +85 | `XDCMainnetBootnodes` (28 entries), `XDCApothemBootnodes` (small fleet) |
| `params/protocol_params.go` | +8 | `XDCBaseFee = 12_500_000_000` (constant 12.5 gwei post-EIP1559) |
| `params/xdc_features.go` | +55 (new) | `XDCFeatures{V2SwitchBlock, V2FirstEpochPenaltySkip}` + `IsXDPoSV2()` predicate |
| `common/types.go` | +many | TIP fork-block globals (`TIPSigning`, `TIPXDCX...`, `TIPTRC21Fee`, `BlackListHFNumber`), `blacklist[Address]`, `CopyXDCConstants(chainID)` switch |
| `common/constants.go` | +24 (new) | `MaxMasternodes=18`, `LimitPenaltyEpoch=4`, `BlocksPerYear=15_768_000`, `RangeReturnSigner=150`, etc. |
| `common/xdc_contracts.go` | +60 (new) | System-contract addresses: `BlockSignersBinary=0x...0089`, `MasternodeVotingSMCBinary=0x...0088`, `RandomizeSMCBinary=0x...0090`, `FoudationAddrBinary=0x...0068`, `XDCXAddrBinary=0x...0091`, etc. |

#### How the patterns work

`XDCMainnetChainConfig` in `params/config.go:200` sets `ChainID=50`, fork
blocks aligned with XDPoSChain v2.6.8 mainnet constants, and an
`XDPoSConfig{Period:2, Epoch:900, Reward:5000, RewardCheckpoint:900,
Gap:450, FoudationWalletAddr:0x92a289...}`. The V2 block is `80,370,000`
on mainnet, `56,828,700` on Apothem (see `params/config.go:1183`).

`TrustedSyncCheckpoints` is a per-network array of pre-verified
`{Number, Hash, Root, Masternodes, Penalties}` quadruples. V2-era
anchors (post-V2-switch) carry the embedded masternode list as well,
because V2 headers do not carry it in `header.Validators`; the
checkpoint list is the "Path B" pre-seed used by
`InsertHeadersBeforeCutoff`. Mainnet has ~30 entries from block
`50,000,000` to `101,999,250`; Apothem has ~15 from `15M` to recent.

`MainnetV2Configs` (params/config.go:2168) and `ApothemV2Configs`
(params/config.go:2230) map a `switchRound` to a `V2Config` describing
masternode count, timeout periods, mine period, certificate threshold,
and exponential-timeout config. Configs activate at their round and
remain effective until superseded.

`Rules{IsXDC, IsEIP1559, IsEIP3860}` (params/config.go:3244) carry
XDC-aware EVM rule flags. The block at which EIP-2929/3529/3860 are
enabled is deferred to `Eip1559Block` on XDC, not Berlin/London/Shanghai;
the predicate is computed in `Rules()` at params/config.go:3273:

```go
isEIP2929 := c.IsBerlin(num) && !isVerkle
if c.IsXDC() {
    isEIP2929 = c.IsEIP1559(num) && !isVerkle
}
...
if c.IsXDC() {
    isMergeRule = c.IsLondon(num)
    isShanghai  = c.IsLondon(num)
    isCancun    = c.IsCancunBlock(num)
    isEIP3860   = c.IsEIP1559(num)
}
```

`IsXDC()` is the single discriminator. It is true iff `c.XDPoS != nil`
(see params/config.go:2665). This unified predicate replaces ~11
historical hard-coded `chainID == 50 || chainID == 51` sites.

The TIP fork-block globals in `common/types.go:97-122` are
package-level `*big.Int` defaults set to the mainnet values; calling
`common.CopyXDCConstants(chainID)` (common/types.go:194) mutates them
to Apothem/devnet values at engine wiring time. **This is a load-bearing
global-mutation pattern**: any test that touches these constants must
call `CopyXDCConstants` with the right chain ID first, and global
state-pollution between tests is possible.

#### When applying to a future geth

- `Rules` struct: upstream may add new flags (Verkle, Amsterdam,
  Pectra). Re-thread `IsXDC` / `IsEIP1559` / `IsEIP3860` so they are
  not overwritten by upstream-fork-time predicates.
- `ChainConfig.CheckConfigForkOrder`: upstream may add new ordering
  constraints; XDC config sets `ConstantinopleBlock == PetersburgBlock
  == IstanbulBlock == BerlinBlock == LondonBlock` on mainnet
  (`76_321_000`) which trips some checks. Keep these tied together.
- `Eip1559Block` is XDC-specific (not standard geth); preserve it.
- `XDPoS.V2.BuildConfigIndex()` is called from the engine `New()` after
  AllConfigs is populated — see params/config.go:2069. If you change
  config initialisation order, re-call it.

---

### 5.b Genesis

#### Why

XDC chains require three system contracts pre-deployed at addresses
`0x88` (Validator/MasternodeVoting), `0x89` (BlockSigners), `0x90`
(Randomize). The mainnet genesis state contains the initial 108
masternode set encoded into validator-contract storage. The genesis
header's `extraData` carries an `extraVanity (32 bytes) || signers ||
extraSeal (65 bytes)` block, the V1 signer encoding.

#### What was added/modified

| File | Δ lines | Purpose |
|---|---:|---|
| `core/genesis_xdc.go` | +106 (new) | `//go:embed xdc_genesis/mainnet.json`, `//go:embed xdc_genesis/apothem.json`; `DefaultXDCMainnetGenesisBlock()`, `DefaultXDCApothemGenesisBlock()`, `xdcGenesisAlloc()`, `IsXDCNetwork()` |
| `core/xdc_genesis/mainnet.json` | +112 | Full mainnet genesis: chainId, fork blocks, XDPoS config, extraData with signers, alloc with 0x88/0x89/0x90 bytecode + initial storage |
| `core/xdc_genesis/apothem.json` | +142 | Apothem genesis (chainId 51) |
| `core/genesis.go` | many | `SetupGenesisBlock`/`LoadChainConfig` patch missing V2 config, TrustedSyncCheckpoints, fork blocks for chain IDs 50 / 51 |
| `genesis/genesis.go` | +14 (new) | `//go:embed mainnet.json`, etc. — alternate genesis package used by some scripts |
| `genesis/{xdc_apothem,xdc_mainnet,xdc_mainnet_fixed,xdc_mainnet_official}.json` | mixed | Variants kept for migration / script use |
| `apothem-genesis.json`, `genesis-apothem.json`, `mainnet-genesis.json`, `mainnet-genesis-fixed.json` | extras | Top-level copies used by `scripts/init_genesis.sh` and Docker images |

#### How the patterns work

`core/genesis_xdc.go:30` embeds the genesis JSON at compile time:

```go
//go:embed xdc_genesis/mainnet.json
var xdcMainnetGenesisJSON []byte

//go:embed xdc_genesis/apothem.json
var xdcApothemGenesisJSON []byte
```

`DefaultXDCMainnetGenesisBlock()` (core/genesis_xdc.go:49) returns
`json.Unmarshal(xdcMainnetGenesisJSON, &Genesis{})`. The verified
mainnet genesis hash is
`0x4a9d748bd78a8d0385b67788c2435dcdb914f98a96250b68863a1f8b7642d6b1`
(see `params/config.go:39`); Apothem is
`0xbdea512b4f12ff1135ec92c00dc047ffb93890c2ea1aa0eefe9b013d80640075`.

The JSON `alloc` block at `0x...0088` is the validator contract
bytecode + storage holding the initial masternode list. At `0x...0089`
the BlockSigner contract bytecode; at `0x...0090` the Randomize
contract bytecode. The `code` and `storage` keys are the
`GenesisAccount.Code` and `GenesisAccount.Storage` fields; balance is
zero.

**Critical**: `core/genesis.go` was modified to patch missing fields
that the JSON does not carry. The JSON omits later fork blocks and the
V2 config, so `LoadChainConfig` (genesis.go:470) and `SetupGenesisBlock`
(genesis.go:380) re-inject them from `params.XDC{Mainnet,Apothem}ChainConfig`
(see genesis.go:399, 412, 432, 483, 551, 575). Each patch emits an
`XDC:` info log. Without these patches the runtime forkID hash would
differ from canonical XDC and wire handshake against production peers
would fail.

`patchMissingForkBlocks` (genesis.go:597) is the helper that copies
`ConstantinopleBlock`, `PetersburgBlock`, `IstanbulBlock`, `BerlinBlock`,
`LondonBlock`, `Eip1559Block`, `CancunBlock` from the defaults if nil.

#### When applying to a future geth

- `core.Genesis` struct: upstream may add fields (e.g. `RequestsHash`,
  `BlobScheduleConfig`). Re-marshall the JSON if these are required at
  genesis time.
- `GenesisAccount.Storage` keys must remain right-padded 32-byte hex
  strings; the JSON was produced from canonical XDPoSChain v2.6.8
  storage layout.
- The genesis hash is a function of every field; any change to fork
  blocks, alloc balances, or storage keys breaks consensus with
  existing chaindata. Test against `params.XDCMainnetGenesisHash` and
  `params.XDCApothemGenesisHash` after porting.

---

### 5.c Consensus engine

#### Why

XDC has two consensus generations:

- **V1**: Clique-derived. 18 (testnet) or 108 (mainnet) masternodes
  rotate. Each block requires creator signature (M1 in `header.Validator`)
  and at epoch-checkpoint blocks an additional validator signature (M2 in
  `header.Validators`). Reward distribution at every checkpoint
  (`block.Number % 900 == 0`).
- **V2**: HotStuff BFT. Round-based proposer rotation. A block carries
  a `QuorumCert` proving 2/3 votes on its parent. Reward distribution at
  epoch-switch blocks (mid-epoch in V2 due to the V2-era anchor
  convention, `Number % 900 == 450`). Penalty list pruning per epoch.

#### What was added/modified

| File | Lines | Purpose |
|---|---:|---|
| `consensus/XDPoS/xdpos.go` | 2049 | Engine wrapper. Routes V1 vs V2 by `IsV2Block(header)`. Implements `consensus.Engine`: `Author`, `VerifyHeader(s)`, `Prepare`, `Finalize`, `FinalizeAndAssemble`, `Seal`, `CalcDifficulty`, `APIs`, `Close`. |
| `consensus/XDPoS/engines/engine_v2/engine.go` | 2361 | Full V2 implementation: `New`, `Initial`, `Prepare`, `Finalize`, `Seal`, `verifyHeader`, `calcMasternodes`, `IsEpochSwitch`, `getEpochSwitchInfo`, `findEpochSwitchAtOrBefore`, `InvalidateCaches`, `SelectSnapPivot`. |
| `consensus/XDPoS/engines/engine_v2/verifyHeader.go` | 313 | V2 header validation logic aligned with XDPoSChain v2.6.8. Validates QC, round, signer membership, gas-limit 1/1024 bound. |
| `consensus/XDPoS/engines/engine_v2/vote.go` | 373 | Vote handling: `VerifyVoteMessage`, `VoteHandler`, `sendVote`, vote-pool threshold checks. |
| `consensus/XDPoS/engines/engine_v2/timeout.go` | 440 | Timeout handling for liveness when proposer fails. |
| `consensus/XDPoS/engines/engine_v2/forensics.go` | 568 | Equivocation / double-signing detection. |
| `consensus/XDPoS/engines/engine_v2/epochSwitch.go` | 89 | Helper for epoch-switch block identification. |
| `consensus/XDPoS/engines/engine_v2/snapshot.go` | 156 | V2 snapshot store of masternodes + penalties keyed by epoch-switch block. |
| `consensus/XDPoS/engines/engine_v2/mining.go` | 80 | Sealing-side V2 helpers. |
| `consensus/XDPoS/engines/engine_v2/snap_pivot.go` | 213 | `SelectSnapPivot` — ADR-807 Phase 1 pivot selection for snap-sync. |
| `consensus/XDPoS/engines/engine_v1/engine.go` | 981 | V1 (Clique-style) implementation. `New`, `verifyHeader`, `verifyCascadingFields`, `verifySeal`, `snapshot`, `apply`. |
| `consensus/XDPoS/engines/engine_v1/utils.go` | misc | V1 helpers. |
| `consensus/XDPoS/engines/common.go` | 14 | `ExtraVanity = 32`, `ExtraSeal = 65`, `SignerFn` type. |
| `consensus/XDPoS/snapshot.go` | 272 | V1 snapshot type. |
| `consensus/XDPoS/reward.go` | 500 | `GetRewardForCheckpoint`, `HookReward` implementation: walks back by parent hash, computes per-signer reward by counting signing-txs across the rCheckpoint window. |
| `consensus/XDPoS/penalty.go` | 215 | Penalty calculation. |
| `consensus/XDPoS/api.go`, `api_extended.go`, `xdpos_api.go` | ~16k | RPC namespace methods. |
| `consensus/XDPoS/genesis_validate.go` | misc | Genesis-time validation. |
| `consensus/XDPoS/contracts.go`, `contracts_test.go` | misc | Engine-side helpers for system-contract calls. |
| `consensus/XDPoS/constants.go`, `network_constants.go` | ~16k | `defaultEpochLength=900`, `defaultPeriod=2`, `defaultGap=450`, network-specific `NetworkConstants` map. |
| `consensus/XDPoS/utils/{constants,errors,pool,types,utils}.go` | misc | Shared utilities used by both engines. |
| `consensus/XDPoS/events.go`, `api_events.go` | misc | Event types for V2 BFT messages. |
| `consensus/consensus.go` | +24 | Adds `CacheInvalidator` optional interface (notify engine on `BlockChain.SetHead`). |

#### How the patterns work

`consensus/XDPoS/xdpos.go` is the wrapper that the rest of geth holds. Its
`New` (xdpos.go:423) takes `*params.XDPoSConfig` and an `ethdb.Database`,
returning `*XDPoS`. Its constructor does **not** create the V2 engine; that
is done in `eth/backend.go:315` and attached via `SetEngineV2(v2)`.

Block routing via `IsV2Block` (xdpos.go:460):

```go
func (c *XDPoS) IsV2Block(header *types.Header) bool {
    if c.config == nil || c.config.V2 == nil || c.config.V2.SwitchBlock == nil {
        return false
    }
    return header.Number.Cmp(c.config.V2.SwitchBlock) >= 0
}
```

`VerifyHeader` (xdpos.go:477) dispatches:

- pre-V2: `verifyHeaderLightV1` → `verifyCascadingFields` → `verifySeal`
  (V1 light-verify path, used by header-only chain segments)
- V2: `verifyHeaderV2` → delegates to `c.engineV2.VerifyHeaderWithParents`

`Prepare` (xdpos.go:1001), `Finalize` (xdpos.go:1114), `Seal` (xdpos.go:1304)
all dispatch the same way by `IsV2Block(header)`.

The V2 engine's `verifyHeader` (engine_v2/verifyHeader.go:23) performs:

1. `len(header.Validator) == 65` — single ECDSA signature.
2. `header.Time <= now` (only when `fullVerify`).
3. `parent.Number == number-1`, parent hash check, gas-limit 1/1024 bound.
4. `getExtraFields` to decode the V2 ExtraFields (round, QC, signers).
5. QC validation (signature count ≥ certThreshold, all from the
   masternode set at that round).
6. Signer-membership check.

The reward path (`consensus/XDPoS/reward.go:66 GetRewardForCheckpoint`)
counts signing transactions by walking backwards from `header.Number` by
parent hash for `(rCheckpoint * 2) - 1` blocks, using `DB-read` not cache
to guarantee deterministic counts (reward.go:94). It then divides the
inflation-adjusted reward across signers proportional to their signing
count.

`HookReward` (set by `hooks.AttachConsensusV2Hooks` in
`eth/hooks/engine_v2_hooks.go:35`) is invoked during V2 `Finalize`. It:

- Skips if `header.Number == V2.SwitchBlock + 1` (per v2.6.8).
- Computes `chainReward = Reward * 1e18` and applies `util.RewardInflation`
  which halves at `BlocksPerYear*2` and quarters at `BlocksPerYear*5`,
  bypassed if `IsTIPNoHalvingMNReward` is active.
- Walks signers from the BlockSigner contract storage at 0x89.
- Splits the reward by `RewardMasterPercent` / `RewardVoterPercent` /
  `RewardFoundationPercent` (see `consensus/XDPoS/constants.go`).
- Credits the resulting balances directly via `statedb.AddBalance`.

The wrapper-level `parentState` capture happens in `core/state_processor.go:82`:

```go
parentState := statedb.Copy()
...
if xdposEngine, ok := p.chain.Engine().(interface{ SetParentState(*state.StateDB) }); ok {
    xdposEngine.SetParentState(parentState)
}
p.chain.Engine().Finalize(p.chain, header, tracingStateDB, block.Body())
```

This is **load-bearing for consensus parity**: reward calculation must
read balances from pre-tx state, not post-tx. Without it every
checkpoint block's state root would diverge.

#### When applying to a future geth

- `consensus.Engine` interface in `consensus/consensus.go`. Upstream
  v1.17.x removed `APIs() []rpc.API` from the interface; the XDC port
  re-adds it via explicit registration in `eth/backend.go:475-477`. If
  upstream re-adds it, drop the explicit registration.
- The engine V2 holds singleton-flavored caches keyed by hash
  (`x.snapshots`, `x.verifiedHeaders`, `x.epochSwitches`, etc.). If
  `BlockChain.SetHead` is invoked, the cache must be invalidated. The
  port adds a `consensus.CacheInvalidator` interface (consensus.go:107)
  and `XDPoS_v2.InvalidateCaches(newHead uint64)` (engine_v2/engine.go:2351).
  `core/blockchain.go:778` calls it.
- `types.Body` API: upstream v1.17.3 introduced
  `Finalize(chain, header, statedb, body)` (where body holds txs,
  uncles, withdrawals). The V1 engine kept the older multi-arg
  signature internally. The wrapper `Finalize` (xdpos.go:1114) bridges
  by constructing the args from `body`. If `types.Body` changes
  again, update the wrapper signature first.
- `state.StateDB`: upstream v1.17.x deprecated `AddBalance(addr,
  *big.Int)` in favour of `AddBalance(addr, *uint256.Int, tracing.BalanceChangeReason)`.
  The reward code in `consensus/XDPoS/reward.go` uses
  `uint256.NewInt(0).SetFromBig(amount)` and the new signature. If
  upstream tweaks the tracing enum, re-thread reasons.

---

### 5.d Block validation & types

#### Why

XDC headers carry three additional fields that are always RLP-encoded:
`Validators` (M2 signers at epoch-switch blocks), `Validator` (M1
signer per block, 65-byte ECDSA), and `Penalties` (penalty list for
the current epoch). Geth's upstream header RLP must be extended; this
changes the genesis hash and every block hash to be XDC-specific.

#### What was added/modified

| File | Δ | Purpose |
|---|---:|---|
| `core/types/block.go` | +60 ~ | `Header.{Validators,Validator,Penalties}`, `headerMarshaling`, `HashNoNonce()`, `HashNoValidator()`. |
| `core/types/gen_header_rlp.go` | regenerated | RLP encode/decode for the new header layout. |
| `core/types/gen_header_json.go` | regenerated | JSON marshaling for the new fields. |
| `core/types/header_rlp_compat_test.go` | new | Round-trip RLP test pinning XDC header shape. |
| `core/types/gen_account_rlp.go` | hand-written | BigBalance-aware StateAccount RLP encoder/decoder (replaces rlpgen). |
| `core/types/state_account.go` | rewritten | `BigBalance *big.Int` overflow field, `FullAccount`, `SlimAccountRLP`, `DecodeSlimAccount`. |
| `core/types/transaction.go` | +13 | `IsSpecialTx(to)` predicate for BlockSigners/Randomize destinations. |
| `core/types/consensus_v2.go` | +282 (new) | V2 BFT message types: `Round`, `BlockInfo`, `Vote`, `Timeout`, `SyncInfo`, `QuorumCert`, `TimeoutCert`, `ExtraFields_v2`, `EpochSwitchInfo`, `VoteForSign`, `TimeoutForSign`, `ForensicsInfo`. |
| `core/types/vote_pool.go` | +97 (new) | Vote-pool primitives. |
| `core/types/order_transaction.go` | +246 (new) | XDCx order-transaction types (used by trading engine integration). |
| `core/block_validator.go` | +60~ | Checkpoint-sync `ValidateBody` bypass, genesis-sync early-block bypass, signing-tx caching for reward calc. |
| `core/blockchain.go` | many | Checkpoint-sync state-pruning bypass, V2-switch trie commit, SetHead cache invalidation. |
| `core/blockchain_xdc.go` | +84 (new) | `HookReward`, `HookPenalty`, `GetCurrentEpoch`, `IsEpochSwitch` stubs that engine hooks override. |
| `core/blockchain_reader.go` | +misc | `GetHeaderByCheckpoint` (XDC trusted-checkpoint anchor lookup). |
| `core/blockchain_insert.go` | misc | XDC-specific insert handling. |
| `core/events_xdc.go` | new | XDC-specific subscription event types. |
| `core/rawdb/accessors_xdc.go` | +many | Masternode / epoch / penalty / vote / QC accessors with their own key prefixes (`masternode-`, `epoch-`, etc.). |
| `core/rawdb/accessors_sync.go` | new | Skeleton sync accessors (used by xdcSyncer). |
| `core/rawdb/schema.go` | mod | New key prefixes for XDC data. |
| `core/rawdb/accessors_chain.go` | mod | Misc accessor adjustments. |

#### How the patterns work

`Header` in `core/types/block.go:81`:

```go
type Header struct {
    ParentHash  common.Hash    `json:"parentHash"`
    UncleHash   common.Hash    `json:"sha3Uncles"`
    Coinbase    common.Address `json:"miner"`
    Root        common.Hash    `json:"stateRoot"`
    TxHash      common.Hash    `json:"transactionsRoot"`
    ReceiptHash common.Hash    `json:"receiptsRoot"`
    Bloom       Bloom          `json:"logsBloom"`
    Difficulty  *big.Int       `json:"difficulty"       gencodec:"required"`
    Number      *big.Int       `json:"number"           gencodec:"required"`
    GasLimit    uint64         `json:"gasLimit"         gencodec:"required"`
    GasUsed     uint64         `json:"gasUsed"          gencodec:"required"`
    Time        uint64         `json:"timestamp"        gencodec:"required"`
    Extra       []byte         `json:"extraData"        gencodec:"required"`
    MixDigest   common.Hash    `json:"mixHash"`
    Nonce       BlockNonce     `json:"nonce"`

    // XDPoS fields for validator consensus (always encoded, not optional)
    Validators []byte `json:"validators"`
    Validator  []byte `json:"validator"`
    Penalties  []byte `json:"penalties"`

    BaseFee *big.Int `json:"baseFeePerGas" rlp:"optional"`
    ...
}
```

The three XDC fields are placed **before** the EIP-1559+ optional
fields, so they participate in every block's RLP encoding and hash.
This is what makes the genesis hash XDC-specific.

`HashNoValidator()` (block.go:161) recomputes the hash with the
`Validator` field zeroed; used by V1 double-validation (M2 verification
signs the header with `Validator` excluded).

`StateAccount.BigBalance` in `core/types/state_account.go:46`:

```go
type StateAccount struct {
    Nonce    uint64
    Balance  *uint256.Int
    Root     common.Hash
    CodeHash []byte
    BigBalance *big.Int `rlp:"-"`
}
```

`BigBalance` is **not** RLP-encoded directly; instead `SlimAccountRLP`
(state_account.go:98) checks `if account.BigBalance != nil` and
serializes via the `slimAccountBigBal` struct using `big.Int` instead
of `uint256.Int`. Decode (`FullAccount`, state_account.go:146) tries
the big-int decoding path first and only falls back to `uint256` if
the value fits.

This exists because XDC genesis accounts hold `2^256-1` balance and
rewards can push real balance past that; upstream geth's strict
`uint256.Int` would either truncate or panic.

Checkpoint-sync bypass in `core/block_validator.go:130`:

```go
if !parentIsTrustedCheckpoint && !v.bc.HasBlockAndState(parentHash, parentNum) {
    checkpointNoState := v.bc.checkpointSyncNoState.Load()
    if checkpointNoState {
        log.Debug("ValidateBody: checkpointSyncNoState=true, skipping state check", ...)
    } else if !v.bc.HasBlock(parentHash, parentNum) {
        return consensus.ErrUnknownAncestor
    } else {
        if v.config.XDPoS != nil && parentNum < 10000 {
            log.Debug("ValidateBody: genesis sync early block, allowing missing parent state", ...)
        } else {
            return consensus.ErrPrunedAncestor
        }
    }
}
```

The trusted-checkpoint parent-bypass is a load-bearing
checkpoint-sync optimisation: if your local tip is the trusted
checkpoint, the pre-checkpoint state is intentionally absent.

#### When applying to a future geth

- `Header` field order is part of RLP encoding. Place XDC fields in the
  same slot or the genesis hash changes. Do not let upstream auto-rebase
  reorder them.
- If upstream changes `Body` to include `requests` (Prague EIP-7685) or
  any new top-level field, regenerate `gen_header_rlp.go` and pin a
  round-trip test (`header_rlp_compat_test.go`).
- The hand-written `gen_account_rlp.go` is intentionally not produced
  by `rlpgen`. Do not run `go generate` over `state_account.go` — it
  will overwrite the BigBalance path.

---

### 5.e State, precompiles, and the EVM jump table

#### Why

XDC defers several EIPs from the upstream blocks geth ties them to:

- **EIP-2929** (cold/warm access lists): geth at Berlin → XDC at `Eip1559Block`.
- **EIP-3529** (refund cap reduction): geth at London → XDC at `Eip1559Block`.
- **EIP-3860** (initcode word cost): geth at Shanghai → XDC at `Eip1559Block`.
- **EIP-2565** (cheaper MODEXP): geth at Berlin → XDC at `Eip1559Block`.
- **EIP-2028** (calldata gas reduction): unconditionally disabled on XDC.

These deferrals match canonical XDPoSChain v2.6.8 and are
consensus-load-bearing: every transaction with calldata, refunds,
initcode, or cold-storage access produces different gas accounting
under the upstream schedule than under canonical.

#### What was added/modified

| File | Purpose |
|---|---|
| `core/vm/jump_table.go` | `newXDCBerlinInstructionSet`, `newXDCLondonInstructionSet`, `newXDCMergeInstructionSet`, `newXDCShanghaiInstructionSet` — marker-only instruction sets used by XDC chains pre-Eip1559Block. |
| `core/vm/contracts.go` | `PrecompiledContractsXDCBerlin` — same as `PrecompiledContractsBerlin` but `bigModExp{eip2565: false}`. Selected by `activePrecompiledContracts` when `rules.IsXDC && !rules.IsEIP1559`. |
| `core/vm/evm.go` | `XDC:` comment around StaticCall zero-value-touch (PR #724/#812 — gates on Istanbul for XDC). |
| `core/vm/eips.go` | misc enable/disable wiring. |
| `core/state_transition.go` | EIP-2028 disable (txpool/validation.go:129 mirror), EIP-3529 deferral, TRC21 owner fee routing, special-tx fee bypass, BigBalance handling. |
| `core/state/state_object.go` | BigBalance journal-entry path on AddBalance / SetBalance. |
| `core/state/journal.go` | `balanceChange` extended to record BigBalance overflow. |
| `core/state/reader.go` | Per-reader `Account()` panic recover (PR #793) wrapping `multiStateReader`. |
| `core/state/snapshot/disklayer.go` | `XDC: use DecodeSlimAccount so balances > uint256 ... are decoded into SlimAccount.BigBalance` (disklayer.go:94). |
| `core/state/snapshot/difflayer.go` | BigBalance-aware decode mirror of disklayer (difflayer.go:226). |
| `core/state/statedb.go` | misc. |
| `core/state/statedb_utils.go` | Direct system-contract storage readers: `GetCandidateOwner`, etc. — invoked by `core.getCoinbaseOwner`. |
| `core/state_processor.go` | TIPSigning self-destruct of 0x89, TIPSigning dispatch to `ApplySigningTransaction`, TIPXDCXReceiver dispatch to `ApplyEmptyTransaction`, `coinbaseOwner` resolution. |
| `core/state_processor_xdc.go` | Helper `XDCStateProcessor` (mostly stubs; the real dispatch lives in state_processor.go). |
| `trie/secure_trie.go` | `GetAccount` panic recover (PR #795) wrapping rlp.DecodeBytes for old historical accounts with non-uint256 balance encoding. |
| `triedb/pathdb/reader.go` | `DecodeSlimAccount` migration in `Reader.Account` (PR #796). |

#### How the patterns work

`core/vm/jump_table.go` — XDC instruction-set markers:

```go
func newXDCBerlinInstructionSet() JumpTable {
    return validate(newIstanbulInstructionSet())
}
func newXDCLondonInstructionSet() JumpTable {
    instructionSet := newXDCBerlinInstructionSet()
    enable3198(&instructionSet) // BASEFEE
    return validate(instructionSet)
}
func newXDCMergeInstructionSet() JumpTable {
    instructionSet := newXDCLondonInstructionSet()
    instructionSet[PREVRANDAO] = &operation{ execute: opRandom, ... }
    return validate(instructionSet)
}
func newXDCShanghaiInstructionSet() JumpTable {
    instructionSet := newXDCMergeInstructionSet()
    enable3855(&instructionSet) // PUSH0
    return validate(instructionSet)
}
```

The dispatch in `core/vm/jump_table.go` (look for `case rules.IsXDC...`) maps
the chain rules to one of these tables when `IsEIP1559` is false; when
`IsEIP1559` is true it falls through to the standard `shanghaiInstructionSet`.

`core/vm/contracts.go:108-128` introduces `PrecompiledContractsXDCBerlin`
which is `PrecompiledContractsBerlin` minus EIP-2565, selected at
`activePrecompiledContracts` (contracts.go:246):

```go
case rules.IsBerlin:
    if rules.IsXDC && !rules.IsEIP1559 {
        return PrecompiledContractsXDCBerlin
    }
    return PrecompiledContractsBerlin
```

`core/state_transition.go` — TRC21 owner routing. At `state_transition.go:715`:

```go
// XDC: pre-EIP1559 blocks can have IsLondon=true with BaseFee==nil
// because canonical defers fee-market activation to Eip1559Block.
```

The owner-of-tx-fee is computed once per block by
`core.getCoinbaseOwner` (state_processor.go:65) calling
`state.GetCandidateOwner(statedb, beneficiary)` which reads the validator
contract storage at 0x88. This owner address is threaded through
`ApplyTransactionWithEVM` (state_processor.go:267) via the variadic
`owner ...common.Address` parameter, then into `ApplyMessage` and
finally inside `state_transition.go` where the fee is credited to the
owner address instead of the coinbase when `IsTIPTRC21Fee` is active.

The fee-bypass path: when `tx.To() == BlockSignersBinary && IsTIPSigning`,
`Process()` (state_processor.go:162) routes to
`ApplySigningTransaction` (state_processor.go:499) which bumps the
nonce, emits a single bare log, and produces a zero-gas receipt — no
EVM. This matches canonical exactly. The 0x89 contract is
self-destructed at the TIPSigning fork (state_processor.go:118-123),
and the IntermediateRoot before SelfDestruct is required for pathdb
correctness:

```go
if common.TIPSigning.Cmp(block.Number()) == 0 {
    _ = statedb.IntermediateRoot(false)
    statedb.SelfDestruct(common.BlockSignersBinary)
    statedb.Finalise(true)
    _ = statedb.IntermediateRoot(true)
}
```

#### When applying to a future geth

- `Rules` is the only knob: as long as `Rules.IsXDC` and `Rules.IsEIP1559`
  flow correctly into `core/vm/jump_table.go` and `core/vm/contracts.go`,
  the XDC instruction sets will be selected.
- If upstream renames `enable3198`, `enable3855`, `enableXXXX` helpers, the
  XDC marker constructors above must follow.
- The `tracing.NonceChangeUnspecified` / `tracing.BalanceChangeReason`
  enums may grow; preserve the value chosen here so traces remain stable.
- `PrecompiledContractsXDCBerlin` only differs from upstream by
  `eip2565: false`. If upstream adds new precompiles to Berlin (KZG, etc.)
  they need to be added here too — this is a "branched copy" that drifts.

---

### 5.f Sync layer

#### Why

Upstream v1.17.3 dropped the pre-merge `chainSyncer`. Its sync model
is now `BeaconSync(head, final)` driven by a consensus-layer
`forkchoiceUpdated`. XDC has no consensus layer; the BFT consensus is
self-contained inside the masternode set. After the first BeaconSync
round the downloader idles.

The fix is a dedicated pre-merge sync loop (`xdcSyncer`) that picks
the best XDPOS2-capable peer every tick, fetches a 4096-block batch
of headers + bodies starting from local head, and inserts via
`blockchain.InsertChain`. Plus a state machine that gates `--syncmode
snap` engagement against the XDC peer fleet's snap readiness.

#### What was added/modified

| File | Lines | Purpose |
|---|---:|---|
| `eth/sync_xdc.go` | 1241 | `xdcSyncer` — pre-merge sync loop, multi-peer retry, fork-tainted peer pool, self-healing rollback on `ErrUnknownAncestor` / `ErrNotItsTurn`, snap-sync phase state machine (FULL_SYNC / SNAP_SELECTING / SNAP_RUNNING / TIP_CATCH / AT_TIP), panic recovery, stall detection with FULL_SYNC fallback. |
| `eth/sync_xdc_phase_test.go` | 150 | Unit tests for the snap-phase state machine. |
| `eth/handler.go` | many | Skip useless-peer disconnect during SNAP_RUNNING; xdcSyncer auto-trigger when behind. |
| `eth/handler_eth.go` | many | XDPOS2 message dispatch into the engine V2 BFT handler; NewBlockHashes / NewBlock legacy bridge. |
| `eth/handler_xdc.go` | 100 | Broadcast{Vote,Timeout,SyncInfo} V2 BFT broadcast helpers. |
| `eth/bft/handler.go` | new | BFT message channel dispatch into engine V2. |
| `eth/protocols/eth/peer.go` | +175 | Dedicated XDPOS2 legacy-response dispatch (request IDs are stripped on the wire); `LastXDCHeaderReqID`, `LastXDCBodyReqID` accessors; FIFO queue for in-flight XDPOS2 header requests. |
| `eth/protocols/eth/protocol.go` | +84 | `XDPOS2 = 100`, `protocolLengths[XDPOS2] = 227`, `VoteMsg/TimeoutMsg/SyncInfoMsg`, `StatusPacket62` (no ForkID, TD-as-block-number), `NewBlockHashesPacket`, `NewBlockPacket` (re-added). |
| `eth/protocols/eth/handlers.go` | many | XDPOS2 GetBlockHeaders / BlockHeaders / GetBlockBodies / BlockBodies legacy-shape decode; stub handlers for NewBlockHashes / NewBlock; truncate header batch to requested count; FIFO dispatch; re-anchor responses. |
| `eth/protocols/eth/handler.go` | many | XDPOS2 capability advertisement; protocol filter on chain. |
| `eth/protocols/eth/handshake.go` | many | XDPOS2 handshake with `StatusPacket62`. |
| `eth/protocols/snap/xdc_snap_verify.go` | 104 (new) | XDC-specific snap proof verification. |
| `eth/protocols/snap/xdc_snap_verify_test.go` | 94 (new) | tests. |
| `eth/downloader/beaconsync.go` | mod | `XDC: Also skip when the cutoff hash matches a TrustedSyncCheckpoint`; cutoff-hash zero check. |
| `eth/downloader/downloader.go` | small | Drop XDC Legacy methods (B9 follow-up); `XDC:` markers. |
| `eth/downloader/skeleton.go` | small | Skeleton DIAG ports + revert (Phase 12). |
| `eth/util/util.go` | 63 (new) | `RewardInflation` halving-bypass helper. |
| `eth/api_backend.go` | small | XDC API backend hooks. |
| `eth/backend.go` | many | Attach V1 + V2 hooks, instantiate engine V2, attach V2 HookReward, wire `setFinalized` hook, register `XDPoS_*` namespace. |

#### How the patterns work

The XDPOS2 wire protocol (`eth/protocols/eth/protocol.go:35`):

```go
const (
    ETH69  = 69
    ETH70  = 70
    XDPOS2 = 100 // XDC consensus protocol (refs #740)
)
var ProtocolVersions = []uint{XDPOS2, ETH70, ETH69}
var protocolLengths = map[uint]uint64{ETH69: 18, ETH70: 18, XDPOS2: 227}
```

XDPOS2 is advertised first so XDC peers prefer it; stock geth peers
fall back to ETH/70 or ETH/69. The protocol length of 227 reserves
space for `VoteMsg = 0xe0`, `TimeoutMsg = 0xe1`, `SyncInfoMsg = 0xe2`,
and the legacy `NewBlockHashesMsg = 0x01` / `NewBlockMsg = 0x07` (which
upstream dropped post-merge).

The handshake uses two distinct packet shapes:

- **ETH/69, ETH/70**: `StatusPacket{ProtocolVersion, NetworkID, Genesis, ForkID, EarliestBlock, LatestBlock, LatestBlockHash}`.
- **XDPOS2**: `StatusPacket62{ProtocolVersion, NetworkID, TD, Head, Genesis}`. No ForkID. TD is a placeholder (block number).

See protocol.go:115. The handshake code (handshake.go) branches by
negotiated version.

`xdcSyncer` (eth/sync_xdc.go:135):

```go
type xdcSyncer struct {
    handler         *handler
    syncing         atomic.Bool
    quitCh          chan struct{}
    stuckFrom       uint64 // fromBlock that's been failing
    stuckFailures   int    // consecutive InsertChain failures at stuckFrom
    stuckRolledBack uint64 // cumulative rollback in current stuck-period
    forkTaintedMu   sync.Mutex
    forkTainted     map[string]time.Time
    snapPhase       atomic.Int32
    snapPivotMu     sync.Mutex
    snapPivot       *engine_v2.PivotResult
    snapBeaconStarted atomic.Bool
    snapBeaconStartedAt atomic.Int64
}
```

Constants:

- `xdcForceSyncCycle = 500ms` — tick cadence.
- `xdcBatchSize = 4096` — headers/bodies per round.
- `xdcReqTimeout = 8s`.
- `xdcMaxPeerTriesPerSync = 4`.
- `xdcUnknownAncestorThreshold = 8` consecutive failures before rollback.
- `xdcRollbackWindowInit = 128`, `xdcRollbackWindowMax = 8192`.
- `xdcForkTaintTTL = 60s`.

The snap-sync FSM (sync_xdc.go:109) is `phaseFullSync` →
`phaseSnapSelecting` (try to fetch a pivot header from a peer per
ADR-807 Phase 1) → `phaseSnapRunning` (BeaconSync running, watching
for stall) → `phaseTipCatch` (snap done, catching final blocks) →
`phaseAtTip`. Transitions are gated behind a double opt-in: both
`--syncmode snap` and `XDC_EXPERIMENTAL_SNAPSYNC=1`. Without the env
var, SNAP_RUNNING logs a warning and the syncer falls back to FULL_SYNC.

The `eth/handler_xdc.go` broadcast helpers (eth/handler_xdc.go:20):

```go
func (h *handler) BroadcastVote(vote *types.Vote) {
    payload, _ := rlp.EncodeToBytes(vote)
    ...
    pkt := &eth.VotePacket{Vote: payload}
    for _, p := range peers {
        p.Peer.Send(eth.VoteMsg, pkt)
    }
}
```

`BroadcastTimeout`, `BroadcastSyncInfo` follow the same shape. These
are called from `engine_v2.broadcastToBftChannel` via a channel
indirection (`eth/bft/handler.go`).

Peer-level XDPOS2 dispatch (eth/protocols/eth/peer.go:82) handles the
absence of request-ID wrapping on XDPOS2 — geth's modern dispatcher
keys responses by request ID, but XDPOS2 wire shape is the legacy
eth/63 format that doesn't carry one. The port adds `lastXDCHeaderReqID`
and `lastXDCBodyReqID` accessors so the syncer can correlate
out-of-band.

#### When applying to a future geth

- The hardest part of this subsystem is the FIFO matching of XDPOS2
  legacy responses. If upstream further refactors `eth/protocols/eth/dispatcher`,
  the bypass paths in `peer.go` and `handlers.go` must be re-derived.
- `BeaconSync(head, final)` signature: the two-pivot model
  (`docs/port/MENTAL_MODEL.md`) depends on `head` driving the
  downloader's pivot offset. If upstream changes `BeaconSync` to take
  one block, the XDC sync engagement (sync_xdc.go ~`syncOnce`) needs
  to re-derive how to engage snap.
- `consensus.CacheInvalidator` is invoked from `BlockChain.SetHead`. If
  upstream re-renames `SetHead`, re-route the hook.
- `downloader.SnapSyncer.Progress().AccountSynced` is read by Phase
  14.4 stall detection. If the field name changes, update the
  progress probe in sync_xdc.go.

---

### 5.g RPC / API

#### Why

XDC tooling (block explorers, masternode dashboards, validator monitors,
the official XDPay wallet) depends on a `XDPoS_*` JSON-RPC namespace
that returns masternode information, epoch metadata, V2 block info,
reward records, and signing finality. Several `eth_*` methods also have
XDC-specific fields in their response bodies (e.g. `eth_getBlockByNumber`
returns the masternode set and signing count).

#### What was added/modified

| File | Lines | Purpose |
|---|---:|---|
| `consensus/XDPoS/api.go` | ~5700 | `clique`-style API: `GetSnapshot`, `GetSnapshotAtHash`, `GetSigners`, `GetSignersAtHash`, `GetMasternodes`, `Proposals`, `Propose`, `Discard`, `GetCandidates`. |
| `consensus/XDPoS/api_extended.go` | ~10k | V2 / extension API: `GetV2BlockByNumber`, `GetV2BlockByHash`, `GetMasternodesByNumber`, `GetBlockInfoByEpochNum`, `GetEpochNumbersBetween`, `GetMissedRoundsInEpochByBlockNum`, `GetLatestPoolStatus`, `NetworkInformation`, `GetRewardByAccount`. |
| `consensus/XDPoS/api_events.go` | misc | Event API. |
| `consensus/XDPoS/api_test.go` | misc | Tests. |
| `consensus/XDPoS/xdpos_api.go` | ~4600 | Engine-side API registration. |
| `internal/ethapi/api_xdc.go` | 582 (new) | XDC extensions to BlockChainAPI: `GetBlockSignersByHash`, `GetBlockSignersByNumber`, `GetBlockFinalityByHash`, `GetBlockFinalityByNumber`, `GetMasternodes`, `GetCandidateStatus`, `GetCandidates`, `RPCMarshalHeaderXDC` (adds Validators/Validator/Penalties fields to `eth_getBlockByX` output), `GetRewardByHash`, `GetTransactionAndReceiptProof`, `GetAccountInfo`, `GetCheckpointFromEpoch`, `GetStakerROI`, `GetStakerROIMasternode`. |
| `internal/ethapi/api_xdc_test.go` | 207 | tests. |
| `internal/ethapi/backend.go` | +5 | Backend interface extensions to support the XDC RPC methods. |
| `internal/web3ext/web3ext_xdc.go` | 385 (new) | JavaScript `web3.xdpos.*` method bindings. |
| `internal/web3ext/web3ext.go` | +1 | Register `"xdpos": XDPoSJs` in `Modules`. |
| `les/api_backend_xdc.go` | 164 (new) | LES variant of the API backend (likely vestigial in v1.17.3 since LES was removed; carried for compatibility). |
| `eth/backend.go` (APIs() func) | +9 | Explicit `apis = append(apis, xdposEng.APIs(s.blockchain)...)` — required because upstream removed `APIs()` from `consensus.Engine` interface (see #814). |

#### How the patterns work

`eth/backend.go:469-477` is the load-bearing wiring:

```go
// XDC #814: upstream geth removed APIs() from the consensus.Engine interface,
// so engine-specific namespaces are no longer auto-registered. XDPoS exposes
// the XDPoS_* family (getMasternodesByNumber, networkInformation,
// getEpochNumbersBetween, …) that block explorers, masternode dashboards,
// and validator monitors depend on for v2.6.8 / v2.7.0 compatibility.
// Wire it explicitly here.
if xdposEng, ok := s.engine.(*XDPoS.XDPoS); ok {
    apis = append(apis, xdposEng.APIs(s.blockchain)...)
}
```

`consensus/XDPoS/xdpos.go:1467 APIs()` returns the `rpc.API` slice with
namespace `"XDPoS"`. The methods on `consensus/XDPoS/api.go` and
`consensus/XDPoS/api_extended.go` end up exposed as
`XDPoS_getSnapshot`, `XDPoS_getMasternodes`,
`XDPoS_getMasternodesByNumber`, `XDPoS_networkInformation`, etc.

`internal/ethapi/api_xdc.go:345 RPCMarshalHeaderXDC` is the function
that adds XDC-specific fields to `eth_getBlockByX` responses. It wraps
the standard `RPCMarshalHeader` output and appends `validators`,
`validator`, `penalties`, `signers`, `finality`.

`internal/web3ext/web3ext_xdc.go` is a long JS string that registers
`web3.xdpos.getSnapshot`, `web3.xdpos.getMasternodes`, etc., so attached
geth consoles can call them as `xdpos.getMasternodes(blockNumber)`.

#### When applying to a future geth

- `rpc.API` shape: stable. Method receivers must be exported and
  parameters must use `*rpc.BlockNumber` for block-number args.
- If upstream further changes `BlockChainAPI` to use generics or change
  field shape, the XDC extensions need to be re-rooted on the new
  receiver type.
- `internal/ethapi/backend.go` adds `EngineXDPoS() interface{}` (or
  similar — see the +5 lines diff); preserve this so api_xdc.go can
  reach the engine.

---

### 5.h Contracts

#### Why

XDC has on-chain governance, voting, and reward-tracking contracts:

- `0x...0088` — MasternodeVotingSMC (also acts as the Validator
  contract — naming is overloaded).
- `0x...0089` — BlockSigner contract (emits Sign events recording
  per-block masternode signatures).
- `0x...0090` — Randomize contract (commit-reveal scheme that drives
  masternode rotation order per epoch).
- `0x...0091` — XDCX trading-engine contract.
- `0x...0092` — TradingStateAddr.
- `0x...0093` — XDCXLending engine.
- `0x...0094` — XDCXLendingFinalizedTrade.

Plus the TRC21 standard (XRC-21 fee-token) and a multisig wallet.

#### What was added

| File | Lines | Purpose |
|---|---:|---|
| `contracts/blocksigner/contract/blocksigner.go` | gen | abigen-generated Go bindings for BlockSigner.sol. |
| `contracts/blocksigner/block_signer.go` | 248 | Higher-level wrapper. |
| `contracts/blocksigner/blocksigner.go` | 65 | Convenience constructor. |
| `contracts/validator/contract/validator.go` | gen | bindings for Validator.sol. |
| `contracts/validator/validator.go` | 87 | Wrapper. |
| `contracts/validator/xdc_validator.go` | 500 | Live-call helpers used by the engine for masternode lookup. |
| `contracts/randomize/contract/randomize.go` | gen | bindings. |
| `contracts/randomize/randomize.go` | 56 | Wrapper. |
| `contracts/multisigwallet/contract/multisigwallet.go` | gen | bindings. |
| `contracts/multisigwallet/multisigwallet.go` | 57 | Wrapper. |
| `contracts/trc21issuer/contract/TRC21.go`, `TRC21Issuer.go` | gen | TRC21 issuer bindings. |
| `contracts/trc21issuer/trc21.go`, `trc21issuer.go` | 41, 41 | Wrappers. |
| `contracts/XDCx/contract/*.go` | gen | XDCX trading + lending bindings. |
| `contracts/XDCx/*.go` | misc | Wrappers + simulation harness. |
| `contracts/utils.go`, `utils_test.go` | 378, 212 | Shared helpers (msg encoding, ABI decoding). |
| `accounts/abi/bind/xdc_bind.go` | small | ABI binding tweak for XDC-prefix address (xdc...) decode. |

The Solidity source for these contracts lives in
`../XDPoSChain/contracts/` (the canonical reference). The
go-ethereum-overlay carries only the generated Go bindings + Go-side
wrappers. To regenerate the bindings from a fresh contract source,
run `abigen --sol Validator.sol --pkg contract --out contract/validator.go`
(uses the standard go-ethereum `abigen`).

#### When applying to a future geth

- The Go bindings depend on the `accounts/abi` API. If upstream
  refactors abigen output (e.g. adds context-aware callers), regenerate.
- The contract bytecode pre-deployed at genesis is **not** regenerated
  from source — it is verbatim from canonical XDPoSChain mainnet.
  Recompiling Solidity at a newer compiler version will produce
  different bytecode and break the genesis hash.

---

### 5.i CLI & node configuration

#### Why

`./build/bin/geth --apothem` and `--xdcmainnet` are the user-facing
flags that select the embedded XDC genesis + bootnodes. There also
needs to be careful handling of `--syncmode snap` (which is partially
implemented on this port — see Phase 14.4) and discovery (which uses
XDC's custom Ping packet type, not standard v4 ENRRequest).

#### What was added/modified

| File | Purpose |
|---|---|
| `cmd/utils/flags.go` | `XDCMainnetFlag`, `XDCApothemFlag`, network detection in `SetNetworkID`, bootnodes selection, snap-sync `--syncmode` is mapped through xdcSyncer FSM. |
| `cmd/clearxdpos/main.go` | Utility to wipe XDPoS-specific data from chaindata. |
| `cmd/extract-checkpoint-state/main.go` | Utility to dump the state at a checkpoint block. |
| `cmd/xdc-replay/main.go` | Replay tool. |
| `cmd/puppeth/{puppeth,wizard,wizard_genesis,wizard_intro}.go` | Wizard adjustments for XDC chains. |
| `node/config_xdc.go` | 225 (new). XDC-specific node config defaults. |
| `node/health.go` | 84 (new). Liveness probe endpoint. |
| `eth/ethconfig/config.go` | misc XDC defaults. |
| `eth/ethconfig/xdpos_config_test.go` | tests. |
| `p2p/discover/v4wire/v4wire.go` | XDC ping type 5 (`UseXDCPing = true` toggles `Ping.Kind()` between PingPacket=1 and ENRRequestPacket=5). |
| `core/xdc_sync_opts.go` | `XdcBulkSyncMode atomic.Bool`, `XdcBulkTriesInMemory=4096`, `XdcChainHeadEventInterval=512`, `XdcCheckpointInterval=900`, `XdcMaxPeersDuringSync=200`. |
| `accounts/accounts.go`, `accounts/external/backend.go` | XDC-prefix address handling. |
| `common/bytes.go`, `common/sort/slice.go`, `common/countdown/countdown.go` | XDC helpers. |
| `build/ci_xdc.go` | CI build script. |
| `.github/workflows/xdc-{build,test}.yml` | CI workflows. |
| `Dockerfile*`, `docker/`, `docker-compose.yml` | Docker images for bootnode / testnet / mainnet nodes. |

`cmd/utils/flags.go:168-175`:

```go
XDCMainnetFlag = &cli.BoolFlag{
    Name:     "xdcmainnet",
    Usage:    "XDC mainnet (network ID 50): XDPoS BFT consensus network",
}
XDCApothemFlag = &cli.BoolFlag{
    Name:     "apothem",
    Usage:    "XDC Apothem testnet (network ID 51): XDPoS BFT consensus test network",
}
```

`cmd/utils/flags.go:1972-1981` is where `--xdcmainnet` / `--apothem`
load the embedded genesis:

```go
case ctx.Bool(XDCMainnetFlag.Name):
    cfg.NetworkId = 50
    cfg.Genesis = core.DefaultXDCMainnetGenesisBlock()
case ctx.Bool(XDCApothemFlag.Name):
    cfg.NetworkId = 51
    cfg.Genesis = core.DefaultXDCApothemGenesisBlock()
```

`p2p/discover/v4wire/v4wire.go` — XDC ping wire-shape:

```go
const (
    PingPacket = iota + 1
    PongPacket
    FindnodePacket
    NeighborsPacket
    ENRRequestPacket  // = 5, XDC uses this slot for PingXDC
    ENRResponsePacket // = 6
)

// XDC uses packet type 5 for ping instead of type 1.
var UseXDCPing = true

func (req *Ping) Kind() byte {
    if UseXDCPing {
        return ENRRequestPacket
    }
    return PingPacket
}
```

This is **wire-level compatibility** with the XDC fleet, which since
the geth pre-1.8 days has used packet type 5 for discovery. ENRRequest
is shadowed; nodes wanting to ask for ENR records over discovery v4
will not be able to on XDC. ENR over discovery v5 (`discv5`) is
unaffected.

#### When applying to a future geth

- `cli` flag library API may shift; the `cli.BoolFlag` literal struct
  is the v3 shape. If upstream upgrades to a different CLI library,
  re-thread the flag definition.
- `node.Config` may absorb fields that `node/config_xdc.go` overrides.
  Re-merge.
- `p2p/discover/v4wire`: if upstream renames packet-type constants or
  adds a new validation that ENRRequest must be a real ENR request,
  the XDC ping toggle needs to be re-rooted.

---

### 5.j P2P fleet integration

#### Why

The XDC fleet (xdc01..xdc05, xdc167, xdc168 etc.) is a fixed set of
deployment hosts running geth-XDC. The `scripts/local-sync-test*.sh`
harness orchestrates multi-node sync tests against this fleet for CI
and operations.

#### What was added (deployment tools — brief)

| File | Purpose |
|---|---|
| `scripts/local-sync-test.sh` | 1234-line multi-node sync test harness with snapshot restore, FLEET_ENODES pinning, ethstats integration. |
| `scripts/run-xdc-mainnet.sh`, `run_xdc_node.sh` | Per-node startup scripts. |
| `scripts/init_genesis.sh` | Genesis bootstrap. |
| `scripts/snap-pivot-validate.sh` | ADR-807 Phase 2 validation script. |
| `scripts/snapshot-daily.sh` | Daily snapshot dump. |
| `scripts/sync-bench.sh` | Sync throughput measurement (loophole L7). |
| `scripts/archive-integrity-probe.sh` | Archive-node integrity probe (loophole L3). |
| `scripts/validate-canonical-parity.sh` | 497-line canonical-parity validator. |
| `scripts/xdc-parity-check.sh`, `xdc_sync_test.sh`, `xdc_test.sh` | misc test runners. |
| `prometheus.yml` | Prometheus scrape config for fleet metrics. |
| `test-xdc-mainnet.sh` | Quick mainnet smoke test. |
| `TESTING.md`, `docs/TESTING.md` | Test playbook. |

These are deployment tools, not consensus. They are listed here for
completeness — a future port-engineer can ignore them initially and
focus on consensus correctness.

---

### 5.k Documentation present in tree

`docs/port/`:

- `STATUS.md` — live human-readable status. As of 2026-05-26 the
  Phase 14 series is code-complete and M3 is blocked on peer-fleet
  snapshot indexing.
- `PORT_LOG.md` (under docs/) — running diary including the
  `[PLANNER-ADVICE 2026-05-23]` Opus planner output that defined the
  bundle order.
- `HANDOFF.md` — entry-point document for engineers picking up #740.
- `MENTAL_MODEL.md` — the two-pivot mental model for snap-sync (read this
  before touching `eth/sync_xdc.go`).
- `ADOPTION_STATUS_FINAL.md` — file-by-file `XDC:` marker coverage table
  (xdc-network vs reset/integration-1.17.3) as of 2026-05-23.
- `BUG_HUNT_PLAN.md` — issue #798 bug-hunt notes.
- `DOWNLOADER_XDC_PORT_ATTEMPT.md` — honest write-up of an unsuccessful
  downloader_xdc.go port attempt.
- `MISSING_FROM_XDC_NETWORK.md`, `PENDING_PORTS_FROM_XDC_NETWORK.md` —
  forward gap analysis.
- `REMAINING_WORK.md` — backlog.
- `SYNC_RUNNING_STATUS.md` — sync-engaged status (#783).
- `V117_FILE_AUDIT.md`, `V117_UPGRADE_LOOPHOLES.md` — audit of files
  introduced in v1.17.3 and whether they could affect state divergence.
- `XDC_SYNC_BREAKTHROUGH.md` — PR #774 unblocking snap-sync engagement.
- `XDPOS2_DATAPLANE_PORT.md` — design note for the XDPOS2 data-plane port.
- `XDC_*.html` — interactive HTML status / diff / playbook dashboards.

`docs/adr/`:

- `001-genesis-config-merge.md` — accepted PR #757. Patch missing XDC
  chain-config fields at SetupGenesisBlock.
- `002-syncmode-aliasing.md` — accepted PR #757. Drop `fast` and
  `fastpath` sync modes from local-sync-test.

`docs/03-architecture-decisions/`:

- `801-v2-bft-finalization-fix.md` — V2 BFT finalization fix.
- `807-snap-sync-phase-1-pivot-selection.md` — ADR-807 Phase 1.
- `807-snap-sync-phase-3-gating-design.md` — ADR-807 Phase 3.

`docs/B*-deferred/`:

- `B7-deferred/blockchain.go.conflicts.txt` — deferred merge conflicts.
- `B9-deferred/{downloader,fetchers_concurrent,queue}.go.conflicts.txt`
- `B10-deferred/{backend,handler}.go.conflicts.txt`

Other:

- `docs/PORT_LOG.md` — bundle merge log.
- `docs/RESET_BUNDLES.md`, `RESET_CONFLICT_LOG.md`, `RESET_B5_DEFERRED.md` —
  bundle catalog and conflict log.
- `docs/SYNC.md`, `docs/TESTING.md`, `docs/XDCx.md`, `docs/XDCxLending.md`,
  `docs/XDPoS.md` — subsystem reference docs.
- `docs/runbooks/v1173-rollback.md` — rollback runbook.

---

## 6. Hard fork timeline

### 6.1 XDC Mainnet (chainId 50)

| Fork | Block | What activates |
|---|---:|---|
| Homestead | 1 | EIP-2/3/7 (CREATE/CALL/DELEGATECALL gas, signature `s` rule, etc.) |
| EIP-150 | 2 | Gas cost increases for IO-heavy ops |
| EIP-155 | 3 | Replay protection (chain ID in signature) |
| EIP-158 | 3 | State clearing (empty-account rule) |
| Byzantium | 4 | RETURNDATA*, STATICCALL, REVERT, etc. |
| TIPSigning | 3,000,000 | BlockSigner contract @ 0x89 self-destructed; signing txs dispatched to `ApplySigningTransaction` (zero-gas, bare log). |
| TIPRandomize | 3,464,000 | Randomize contract activation (M2 randomization). |
| TIPIncreaseMasternodes | 5,000,000 | Masternode count limit increase. |
| TIPXDCX / TIPXDCXLending / TIPXDCXCancellationFee / TIPNoHalvingMNReward / TIPTRC21Fee | 38,383,838 | XDCx trading + lending activate; reward halving disabled; TRC21 fees route to coinbase owner; effective Istanbul activation (`IsIstanbul()` returns true via `TIPXDCXCancellationFee` fallback). |
| Berlin / Constantinople / Petersburg / Istanbul / London / TIPMerge / TIPShanghai | 76,321,000 | All bundled at this block. XDC instruction sets newXDC{Berlin,London,Merge,Shanghai} drive opcode availability. |
| BlockNumberGas50x / TIPV2SwitchBlock / TIPXDCXMinerDisable | 80,370,000 | **V2 BFT switch.** XDCx trading-engine miner-side disabled. |
| TIPXDCXReceiverDisable | 80,370,900 | XDCx receiver-side dispatch disabled (now normal EVM execution). |
| Eip1559Block | 98,800,200 | EIP-2929 / 3529 / 3860 / 2565 activate. EIP-1559 dynamic-fee tx-type accepted. Base fee constant at 12.5 gwei (`XDCBaseFee`). |
| CancunBlock | 98,802,000 | Cancun opcodes (BLOBHASH, MCOPY, TLOAD/TSTORE). |

### 6.2 Apothem testnet (chainId 51)

| Fork | Block | What activates |
|---|---:|---|
| Homestead..Byzantium | 1..4 | Same as mainnet |
| TIPSigning | 3,000,000 | Same |
| TIPRandomize | 3,464,000 | Same |
| TIPXDCX / ... / TIPTRC21Fee | 23,779,191 | Apothem-specific equivalent of mainnet's 38,383,838 |
| BlockNumberGas50x | 56,828,700 | (testnet anchor) |
| TIPV2SwitchBlock | 56,828,700 | **V2 BFT switch.** |
| Berlin..London / TIPMerge / TIPShanghai | 61,290,000 | Bundled fork on Apothem |
| TIPXDCXMinerDisable | 61,290,000 | XDCx miner-side disable |
| TIPXDCXReceiverDisable | 66,825,000 | XDCx receiver-side disable |
| Eip1559Block | 71,550,000 | EIP-2929 / 3529 / 3860 / 2565 activate. |
| CancunBlock | 71,551,800 | Cancun opcodes. |

Source: `common/types.go:194 CopyXDCConstants` and `params/config.go`
chain configs. The "everything-bundled-at-Berlin" pattern is canonical
XDPoSChain behaviour: it telescopes Ethereum's Berlin → London → Merge
→ Shanghai sequence into a single fork.

---

## 7. Bypass log family ("XDC:" prefix)

Approximately 30 inline `XDC:` log call-sites mark places where XDC's
state model legitimately diverges from upstream Ethereum validation.
These exist because:

- XDC has state mutations that upstream's stricter validation refuses
  (e.g. account balance > 2^256-1).
- XDC has checkpoint-sync modes where parent state is intentionally absent.
- XDC has fork-specific tx dispatches that bypass the EVM (TIPSigning,
  TIPXDCXReceiver).
- XDC's V2-switch block requires a forced trie commit.

The full list, grouped by file:

### 7.1 `core/genesis.go`

- Line 395, 503: patch missing `TrustedSyncCheckpoints`.
- Line 409, 518: patch missing V2 config.
- Line 432: patch missing fork blocks (calls `patchMissingForkBlocks`).
- Line 600/604/608/612/616/620/624: log when each individual fork
  block is patched.

### 7.2 `core/blockchain.go`

- Line 778: `XDC: notify consensus engine to purge any in-memory caches keyed by ...`
  (calls `engine.(consensus.CacheInvalidator).InvalidateCaches(newHead)`).
- Line 1788-1796: `XDC: Also force-commit at V2 switch block — this is a critical consensus boundary`.
  At `V2.SwitchBlock`, the trie is force-committed; failure logs error and
  panics on bootstrap.
- Additional sites in checkpoint-sync handling.

### 7.3 `core/block_validator.go`

- Line 131: `XDC: During checkpoint sync without state, the parent state won't exist ...`.
- Line 141: `XDC: during genesis sync with legacy peers (no snap sync), parent state may not be available ...`.
- Line 174: `XDC: During checkpoint sync, parent state may be missing which causes gas execution to differ from canonical`.

### 7.4 `core/blockchain_reader.go`

- Line 522: `XDC: minimum-viable port from xdc-network. The V2 engine's anchor ...`.

### 7.5 `core/state_processor.go`

- Line 78: `XDC: capture pre-tx state for the XDPoS engine's reward calculation`.
- Line 102: `XDC: at the TIPSigning fork block ... canonical XDPoSChain wipes the BlockSigners contract`.
- Line 142: `XDC: resolve TRC21 fee recipient once per block`.
- Line 155: `XDC: signing transactions at the TIPSigning fork are routed to ApplySigningTransaction`.
- Line 172: `XDC: between TIPXDCX (Apothem 23,779,191) and TIPXDCXReceiverDisable ... canonical XDPoSChain routes them to ApplyEmptyTransaction`.
- Line 215: `XDC: Set parentState on the engine for reward calculation`.

### 7.6 `core/state_transition.go`

- Line 428: `XDC: if the sender holds a BigBalance overflow (balance > 2^256-1), the ...`.
- Line 500: `XDC: skip the maxFeePerGas-vs-baseFee check for special txs`.
- Line 591: `XDC: disable EIP-2028 transaction data pricing to match XDPoSChain v2.6.8`.
- Line 715: `XDC: pre-EIP1559 blocks can have IsLondon=true with BaseFee==nil`.
- Line 827: `XDC: defer EIP-3529 (refund-cap reduction from 1/2 to 1/5) from London to ...`.

### 7.7 `core/evm.go`

- Line 147: `XDC: when the sender holds a BigBalance overflow ...`.

### 7.8 `core/txpool/validation.go`, `core/txpool/legacypool/legacypool.go`

- validation.go:129: `XDC: disable EIP-2028 (lower per-non-zero-byte calldata gas) for XDC`.
- legacypool.go:578: `XDC: reject txs whose sender or recipient is on the XDC blacklist`.

### 7.9 `core/state/*`

- `core/state/reader.go:112`: `XDC: preserve overflow balance from snapshot`.
- `core/state/reader.go:358`: `XDC: wrap each reader call with a recover so a panic from an overflowing` (PR #793).
- `core/state/snapshot/disklayer.go:94`: `XDC: use DecodeSlimAccount so balances > uint256 (BigBalance overflow)`.
- `core/state/snapshot/difflayer.go:226`: `XDC: BigBalance-aware decode`.

### 7.10 `trie/secure_trie.go`, `triedb/pathdb/reader.go`

- `trie/secure_trie.go:118-119`: `XDC: wrap rlp.DecodeBytes in recover. Stored data on disk for some historical XDC accounts may encode balance > uint256 via big.Int RLP`.
- `triedb/pathdb/reader.go:136`, `287`: `XDC: use DecodeSlimAccount which handles BigBalance overflow`.

### 7.11 `eth/*`

- `eth/backend.go:185`: `XDC: initialise chain-ID-dependent XDC constants (blacklist HF, masternode ...)`.
- `eth/backend.go:300`: `XDC: attach XDPoS V1 + V2 consensus hooks`.
- `eth/handler.go:142,383,444,449`: `XDC: pre-merge auto-sync trigger`, snap-running peer-disconnect skip.
- `eth/sync_xdc.go:189,823`: bulk-sync enable, XDPOS2 TD-as-block-number semantics.
- `eth/downloader/beaconsync.go:289`: `XDC: Also skip when the cutoff hash matches a TrustedSyncCheckpoint`.
- `eth/protocols/eth/peer.go:82,102`: XDPOS2 legacy-response queueing.

### 7.12 The "load-bearing" rule

Every `XDC:` log line is **load-bearing for consensus parity**. Removing
or "improving" any of them will produce a state-root divergence at the
first block where the relevant condition fires.

If you find a `XDC:` log that seems redundant, file an issue and check
parity before deleting. The canonical parity log lives at
`docs/port/CONSENSUS_PARITY.md` (currently not maintained in this
branch — see STATUS.md).

---

## 8. Step-by-step recreation procedure

This section is concrete: a developer with a fresh `vX.Y.Z` upstream
tag should be able to follow these steps and reach a buildable,
recognisably-XDC binary.

### 8.1 Prerequisites

- Go 1.23+ (geth v1.17.3 minimum).
- `git`, `gh` (GitHub CLI).
- ~20 GB free disk for chaindata + builds.
- Local clones of `XDPoSChain` (canonical XDC reference) and `XinFin-Node`
  (operator deployment) — read-only references.

### 8.2 Setting up the repo

```bash
cd /workspace
git clone https://github.com/ethereum/go-ethereum.git go-ethereum
cd go-ethereum
git checkout vX.Y.Z   # the new target upstream tag

# Add this repo as a remote so we can cherry-pick:
git remote add xdc-overlay <path-or-url-to-this-repo>
git fetch xdc-overlay
```

### 8.3 Bundle 0 — engine + config + contracts + genesis (no upstream conflicts expected)

```bash
# Create the integration branch:
git checkout -b reset/integration-vX.Y.Z

# Copy purely-new XDC files (no merge possible):
mkdir -p consensus/XDPoS
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    consensus/XDPoS/ \
    contracts/blocksigner/ contracts/validator/ contracts/randomize/ \
    contracts/multisigwallet/ contracts/trc21issuer/ contracts/XDCx/ \
    contracts/utils.go contracts/utils_test.go \
    core/xdc_genesis/ \
    core/genesis_xdc.go core/blockchain_xdc.go core/events_xdc.go \
    core/state_processor_xdc.go core/state_processor_xdc_test.go \
    core/xdc_sync_opts.go core/rawdb/accessors_xdc.go core/rawdb/accessors_sync.go \
    common/xdc_contracts.go common/constants.go common/countdown/ \
    common/sort/ common/bytes.go \
    params/bootnodes.go params/xdc_features.go \
    accounts/abi/bind/xdc_bind.go \
    eth/hooks/ eth/util/ \
    eth/sync_xdc.go eth/sync_xdc_phase_test.go eth/handler_xdc.go \
    eth/bft/ \
    eth/protocols/snap/xdc_snap_verify.go eth/protocols/snap/xdc_snap_verify_test.go \
    internal/ethapi/api_xdc.go internal/ethapi/api_xdc_test.go \
    internal/web3ext/web3ext_xdc.go \
    les/api_backend_xdc.go \
    node/config_xdc.go node/health.go \
    genesis/ apothem-genesis.json genesis-apothem.json \
    mainnet-genesis.json mainnet-genesis-fixed.json \
    XDCx/ XDCxlending/ \
    cmd/clearxdpos/ cmd/extract-checkpoint-state/ cmd/xdc-replay/ \
    scripts/ \
    docs/port/ docs/adr/ docs/03-architecture-decisions/ docs/PORT_LOG.md \
    docs/runbooks/ docs/RESET_BUNDLES.md docs/RESET_CONFLICT_LOG.md \
    docs/SYNC.md docs/TESTING.md docs/XDCx.md docs/XDCxLending.md docs/XDPoS.md \
    docs/GENESIS_COMPATIBILITY_FIX.md docs/RESET_B5_DEFERRED.md \
    Dockerfile* docker/ docker-compose.yml prometheus.yml \
    test-xdc-mainnet.sh TESTING.md \
    .github/workflows/xdc-build.yml .github/workflows/xdc-test.yml \
    build/ci_xdc.go

git commit -m "B0: import XDC overlay files (no merge required)"
```

### 8.4 Bundle 1 — params/config.go merge

`params/config.go` is the largest single file change. Merge it
carefully:

```bash
# 3-way merge of params/config.go:
git show xdc-overlay/reset/integration-1.17.3:params/config.go > /tmp/xdc_config.go
git show xdc-overlay/reset/integration-1.17.3:117e067f0 -- params/config.go > /tmp/base_config.go
# Re-apply XDC additions on top of the new upstream config.go. Look for:
#   - XDCMainnetChainConfig, XDCApothemChainConfig (~params/config.go:200, ~params/config.go:1158)
#   - XDPoSConfig, V2, V2Config (~params/config.go:2047-2156)
#   - MainnetV2Configs, ApothemV2Configs (~params/config.go:2168, ~params/config.go:2230)
#   - TrustedSyncCheckpoint (~params/config.go:2030)
#   - Rules{IsXDC, IsEIP1559, IsEIP3860} (~params/config.go:3244)
#   - IsXDC(), XDCBlocksPerYear(), IsTIP* predicates (~params/config.go:2665+)
#   - patchMissingForkBlocks could stay in core/genesis.go (not config.go)
git add params/config.go && git commit -m "B1: re-apply XDC chain configs onto upstream params/config.go"

# Then common/types.go (TIP fork blocks + blacklist):
# Apply the same pattern.
```

### 8.5 Bundle 2 — header struct + RLP

`core/types/block.go` adds three header fields. This breaks every
RLP round-trip test in `core/types/*_test.go`:

```bash
# Apply:
#   - Add Validators, Validator, Penalties []byte fields BEFORE BaseFee
#   - Add HashNoNonce() and HashNoValidator()
#   - Add hexutil.Bytes formatters in headerMarshaling
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    core/types/block.go core/types/gen_header_rlp.go core/types/gen_header_json.go \
    core/types/header_rlp_compat_test.go \
    core/types/gen_account_rlp.go core/types/state_account.go \
    core/types/transaction.go core/types/consensus_v2.go \
    core/types/vote_pool.go core/types/order_transaction.go
git commit -m "B2: header layout + state account BigBalance + V2 BFT types"

# Build-check:
go build ./core/types/... && go test ./core/types/...
```

### 8.6 Bundle 3 — XDPOS2 wire protocol

```bash
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    eth/protocols/eth/protocol.go eth/protocols/eth/handler.go \
    eth/protocols/eth/handlers.go eth/protocols/eth/handshake.go \
    eth/protocols/eth/peer.go
git commit -m "B3: XDPOS2/100 wire protocol"

# Build-check:
go build ./eth/protocols/eth/...
```

### 8.7 Bundle 4 — P2P discovery

```bash
git checkout xdc-overlay/reset/integration-1.17.3 -- p2p/discover/v4wire/v4wire.go
git commit -m "B4: XDC discovery v4 ping type 5 (ENRRequest slot)"
```

### 8.8 Bundle 5 — state + EVM

Restoring the EVM XDC marker tables, precompile branching, and state
overlays:

```bash
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    core/vm/jump_table.go core/vm/contracts.go core/vm/evm.go core/vm/eips.go \
    core/state_transition.go \
    core/state/journal.go core/state/state_object.go core/state/statedb.go \
    core/state/statedb_utils.go core/state/reader.go core/state/state_reader.go \
    core/state/snapshot/disklayer.go core/state/snapshot/difflayer.go \
    trie/secure_trie.go triedb/pathdb/reader.go \
    consensus/consensus.go \
    consensus/misc/eip1559/eip1559.go consensus/misc/eip1559/eip1559_xdc_test.go
git commit -m "B5: XDC EVM + state overlay (BigBalance, EIP deferrals, CacheInvalidator)"
```

### 8.9 Bundle 6 — txpool + tx validation

```bash
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    core/txpool/validation.go core/txpool/legacypool/legacypool.go
git commit -m "B6: txpool — EIP-2028 disable + XDC blacklist enforcement"
```

### 8.10 Bundle 7 — core/blockchain* + state_processor

These are the highest-conflict files. Use the strategy from
`docs/PORT_LOG.md`: upstream skeleton + re-apply XDC additions marked
with `XDC:` comments.

```bash
# This is the most conflict-prone step. Take the upstream file and apply each XDC:
# marker manually:
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    core/blockchain.go core/blockchain_insert.go core/blockchain_reader.go \
    core/block_validator.go core/state_processor.go core/genesis.go \
    core/evm.go core/rawdb/schema.go core/rawdb/accessors_chain.go

# 3-way merge: for each XDC: marker in the overlay file, find the
# corresponding upstream call-site and re-insert. The merge is by hand.
# Pay extra attention to:
#   - core/blockchain.go:778       (CacheInvalidator on SetHead)
#   - core/blockchain.go:1788-1796 (V2-switch trie force-commit)
#   - core/state_processor.go:78   (parentState snapshot)
#   - core/state_processor.go:102  (TIPSigning self-destruct of 0x89)
#   - core/state_processor.go:162  (TIPSigning dispatch)
#   - core/state_processor.go:180  (TIPXDCXReceiver dispatch)
#   - core/state_processor.go:215  (SetParentState on engine)
#   - core/state_processor.go:499  (ApplySigningTransaction)
#   - core/state_processor.go:555  (ApplyEmptyTransaction)
#   - core/genesis.go:395+         (patchMissing* family)
#   - core/block_validator.go:130+ (checkpoint-sync bypass)
git commit -m "B7: core/blockchain + state_processor + block_validator XDC overlays"
```

### 8.11 Bundle 8 — downloader + sync_xdc + eth/backend + eth/handler

```bash
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    eth/downloader/downloader.go eth/downloader/beaconsync.go eth/downloader/skeleton.go \
    eth/handler.go eth/handler_eth.go \
    eth/api_backend.go eth/backend.go \
    eth/ethconfig/config.go eth/ethconfig/xdpos_config_test.go \
    internal/ethapi/backend.go internal/web3ext/web3ext.go \
    cmd/utils/flags.go cmd/puppeth/
git commit -m "B8: eth/* + cmd/utils/flags.go XDC overlay"
```

### 8.12 Bundle 9 — params/protocol_params.go + accounts

```bash
git checkout xdc-overlay/reset/integration-1.17.3 -- \
    params/protocol_params.go \
    accounts/accounts.go accounts/external/backend.go
git commit -m "B9: misc params + accounts XDC overlay"
```

### 8.13 Build + initial validation

```bash
make all                              # produces ./build/bin/geth
./build/bin/geth --apothem init genesis/xdc_apothem.json
./build/bin/geth --apothem --datadir /tmp/apothem-test \
                 --syncmode full --http --port 30303
# Watch logs for "XDC: patched missing V2 config in genesis spec for apothem"
# and for the apothem genesis hash
# 0xbdea512b4f12ff1135ec92c00dc047ffb93890c2ea1aa0eefe9b013d80640075.
```

If the binary boots, the port has its skeleton in place. Next steps
are correctness, not connectivity.

### 8.14 Consensus parity validation

The parity harness lives at... not in `tools/parity-harness/` (the
directory does not exist in this tree — that path is aspirational per
the CLAUDE.md operating manual). What does exist:

- `scripts/validate-canonical-parity.sh` — 497-line script that
  iterates a block range and compares state root / receipts root /
  block hash against a canonical RPC endpoint (e.g. `rpc.xdcrpc.com`).
- `scripts/xdc-parity-check.sh` — wrapper to verify against XDC v2.6.8.
- `consensus/XDPoS/state_root_vectors_test.go` — embedded test vectors.
- `consensus/XDPoS/cross_client_vectors_test.go` — cross-client RLP test vectors.

Run sequence:

```bash
go test ./consensus/XDPoS/...
./scripts/validate-canonical-parity.sh --network=apothem --from=23779000 --to=23780000
```

A green run means state-roots match for the requested block window.

### 8.15 Sync engagement against the fleet

`scripts/local-sync-test.sh` deploys against the XDC peer fleet. The
fleet enodes are pinned via `FLEET_ENODES`. See
`docs/port/SYNC_RUNNING_STATUS.md` for current node configuration.

---

## 9. Pitfalls & "don't do this"

### 9.1 Consensus correctness is sacred

Never "improve" XDPoS rules during port work. This is the prime
directive in `CLAUDE.md` (the project operating manual). Specifically:

- Do not modify reward calculation, masternode selection, or epoch
  boundaries.
- Do not change RLP encoding of any block / header / receipt field.
- Do not force Erigon (or upstream) defaults onto XDC-customised paths.
- Do not skip validation steps because "the upstream already does it".
- Treat hash mismatch as P0 — stop everything until root-caused.

### 9.2 The "XDC:" markers are load-bearing

Removing or "cleaning up" any `XDC:` log line will likely produce a
state-root divergence at a future block. The markers identify exactly
where canonical XDPoSChain v2.6.8 behaviour differs from upstream
Ethereum validation. Every one of them has historical justification
(usually traceable to a specific PR # in the commit log).

### 9.3 V1 → V2 hardfork boundary is high-risk

At mainnet block `80,370,000`:

- The trie must be force-committed (`core/blockchain.go:1788`).
- V1 snapshot logic stops applying; V2 snapshot store activates.
- `header.Validators` (V1 M2 signers) becomes empty; the validator
  list lives in the validator-contract storage and gets read via
  `engine_v2.calcMasternodes`.
- Reward distribution moves from `Number % 900 == 0` to a more nuanced
  schedule keyed by epoch-switch detection (`engine_v2.IsEpochSwitch`).
- The first V2 block (V2.SwitchBlock + 1) skips reward distribution
  entirely (see `eth/hooks/engine_v2_hooks.go:53`).

The TrustedSyncCheckpoint at `80,370,000` (mainnet) is "Path B": its
`Masternodes` field is populated so that `InsertHeadersBeforeCutoff`
can pre-seed the V2 snapshot store without needing contract state.
Without that pre-seed, the engine fails verification of the very next
block.

### 9.4 BigBalance is a 4-file invariant

The `BigBalance` overflow path lives in:

- `core/types/state_account.go` (StateAccount, SlimAccount)
- `core/state/state_object.go` (AddBalance / SetBalance journal)
- `core/state/snapshot/{disklayer,difflayer}.go` (snapshot decode)
- `core/state/reader.go` (panic recover, BigBalance preserve)
- `trie/secure_trie.go` (GetAccount panic recover)
- `triedb/pathdb/reader.go` (Account decode)

If you touch any one of these, audit the others. The historical
divergence cases (#793/#794/#795/#796) all traced to one half of the
pair being updated without the other.

### 9.5 Snap-sync semantics are subtle

Read `docs/port/MENTAL_MODEL.md` before touching `eth/sync_xdc.go`.
The two-pivot model (xdcSyncer pivot vs downloader pivot) is the
single most common source of incorrect bug-fixes. Specifically:

- `fsMinFullBlocks` controls the **downloader's** pivot offset from
  head. It is **not** a knob for pinning any other pivot.
- The downloader pivot is **always** `head - fsMinFullBlocks` — the
  only way to relocate it is to relocate `head`.
- Path-scheme nodes retain ~90,000 blocks of state-history; if the
  downloader pivot falls below `peer_head - 90,000` the state is
  legitimately pruned and peers will correctly reject `GetAccountRange`
  for that root.

### 9.6 XDC peer fleet does not currently serve snap

As of the docs at `STATUS.md`, the XDC peer fleet (xdc01..xdc05) has
not generated a snap-protocol snapshot index. The `--syncmode snap`
path will engage but ultimately fall back to FULL_SYNC via Phase 14.4
stall detection. This is **expected behaviour** until task #96 is
done; don't waste a day diagnosing "snap-sync broken" when the
unblock is upstream of the binary.

### 9.7 The blacklist is hardcoded and chain-specific

`common/types.go:126` holds a 57-address blacklist used at all blocks
≥ `BlackListHFNumber` (mainnet: 38,383,838; Apothem: 23,779,191).
Both sender and recipient are checked. The set was sourced from
XDPoSChain v2.6.8 `constants.mainnet.go`. **Do not** remove entries.

### 9.8 The `common.TIP*` globals are mutated at runtime

`common.CopyXDCConstants(chainID)` mutates package-level `*big.Int`
globals. This is called once during engine construction in
`eth/backend.go:185`. **Never call it twice with different chain IDs
in the same process** — tests that exercise both mainnet and Apothem
in one binary will alias the latter on top of the former. The
canonical-parity script handles this by running per-chain processes.

### 9.9 The `Foudation` typo

`common/xdc_contracts.go:32`: `FoudationAddrBinary` (missing 'n') is
intentional. The historical misspelling propagated through the
canonical XDPoSChain codebase, exported APIs depend on it, and a fix
would touch ~20 sites. The typo is preserved.

---

## 10. Reference appendix

### 10.1 Key file paths (~50 files, one-line descriptions)

| Path | Description |
|---|---|
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/xdpos.go` | XDPoS engine wrapper (2049 lines). |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v2/engine.go` | V2 BFT engine (2361 lines). |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v2/verifyHeader.go` | V2 header verification. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v2/vote.go` | V2 vote handling. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v2/timeout.go` | V2 timeout handling. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v2/forensics.go` | V2 equivocation detection. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v2/snap_pivot.go` | ADR-807 snap-sync pivot selection. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/engines/engine_v1/engine.go` | V1 (Clique-style) engine (981 lines). |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/reward.go` | Reward calculation per-checkpoint. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/snapshot.go` | V1 snapshot type. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/api.go` | XDPoS_* RPC namespace (clique-style). |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/api_extended.go` | XDPoS_* extended RPC. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/XDPoS/network_constants.go` | Per-network constants map. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/consensus/consensus.go` | `consensus.Engine` interface + `CacheInvalidator`. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/params/config.go` | Chain configs (XDCMainnet, XDCApothem), V2 configs, Rules, IsXDC predicate. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/params/bootnodes.go` | XDCMainnetBootnodes, XDCApothemBootnodes. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/params/protocol_params.go` | XDCBaseFee constant (12.5 gwei). |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/params/xdc_features.go` | XDCFeatures struct. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/common/types.go` | TIP fork-block globals, blacklist, CopyXDCConstants. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/common/constants.go` | MaxMasternodes, BlocksPerYear, etc. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/common/xdc_contracts.go` | System contract address constants. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/genesis.go` | SetupGenesisBlock with XDC patching. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/genesis_xdc.go` | Embed mainnet.json + apothem.json. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/xdc_genesis/mainnet.json` | Embedded mainnet genesis JSON. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/xdc_genesis/apothem.json` | Embedded Apothem genesis JSON. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/blockchain.go` | BlockChain with checkpointSyncNoState + V2 force-commit. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/blockchain_xdc.go` | XDC blockchain hooks. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/block_validator.go` | Checkpoint-sync bypasses. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/state_processor.go` | Process(): parentState snapshot, TIPSigning dispatch, TIPXDCXReceiver dispatch, ApplySigningTransaction, ApplyEmptyTransaction. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/state_transition.go` | TRC21 owner fee routing, EIP-2028 disable, EIP-3529 deferral. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/types/block.go` | Header fields + HashNoValidator. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/types/state_account.go` | StateAccount + BigBalance. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/types/consensus_v2.go` | V2 BFT message types. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/types/transaction.go` | IsSpecialTx predicate. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/vm/jump_table.go` | newXDC{Berlin,London,Merge,Shanghai}InstructionSet. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/vm/contracts.go` | PrecompiledContractsXDCBerlin. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/xdc_sync_opts.go` | XdcBulkSyncMode atomic + constants. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/rawdb/accessors_xdc.go` | XDC key-prefix accessors. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/state/reader.go` | BigBalance preserve + panic recover. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/state/snapshot/disklayer.go` | DecodeSlimAccount for BigBalance. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/core/state/snapshot/difflayer.go` | DecodeSlimAccount mirror. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/trie/secure_trie.go` | GetAccount panic recover. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/triedb/pathdb/reader.go` | pathdb DecodeSlimAccount. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/backend.go` | Engine + hooks + XDPoS_* API registration. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/handler.go` | xdcSyncer trigger; SNAP_RUNNING peer-disconnect skip. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/handler_eth.go` | XDPOS2 message dispatch. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/handler_xdc.go` | BroadcastVote/Timeout/SyncInfo. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/sync_xdc.go` | xdcSyncer pre-merge sync loop. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/sync_xdc_phase_test.go` | snap-phase FSM tests. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/bft/handler.go` | V2 BFT message channel. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/eth/protocol.go` | XDPOS2 = 100, VoteMsg/TimeoutMsg/SyncInfoMsg, StatusPacket62. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/eth/peer.go` | XDPOS2 legacy-response dispatch + FIFO. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/eth/handlers.go` | XDPOS2 GetBlockHeaders/Bodies legacy decode. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/eth/handshake.go` | XDPOS2 + ETH/69/70 handshake. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/snap/xdc_snap_verify.go` | XDC-specific snap proof verification. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/downloader/beaconsync.go` | TrustedSyncCheckpoint cutoff handling. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/hooks/hooks.go` | AttachConsensusV1Hooks. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/hooks/engine_v2_hooks.go` | AttachConsensusV2Hooks. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/util/util.go` | RewardInflation halving-bypass. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/internal/ethapi/api_xdc.go` | XDC extensions to BlockChainAPI. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/internal/web3ext/web3ext_xdc.go` | web3.xdpos.* JS bindings. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/p2p/discover/v4wire/v4wire.go` | XDC ping = packet type 5. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/cmd/utils/flags.go` | --xdcmainnet / --apothem flags. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/node/config_xdc.go` | XDC node defaults. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/contracts/blocksigner/contract/blocksigner.go` | BlockSigner.sol bindings. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/contracts/validator/contract/validator.go` | Validator.sol bindings. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/contracts/randomize/contract/randomize.go` | Randomize.sol bindings. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/contracts/validator/xdc_validator.go` | Validator live-call helpers. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/scripts/local-sync-test.sh` | Multi-node fleet sync harness. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/scripts/validate-canonical-parity.sh` | State-root parity validator. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/port/STATUS.md` | Live status. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/port/MENTAL_MODEL.md` | Two-pivot snap-sync mental model. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/PORT_LOG.md` | Running diary including PLANNER-ADVICE entries. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/adr/001-genesis-config-merge.md` | ADR for genesis config patching. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/adr/002-syncmode-aliasing.md` | ADR for syncmode aliasing. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/03-architecture-decisions/801-v2-bft-finalization-fix.md` | V2 BFT finalization. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/03-architecture-decisions/807-snap-sync-phase-1-pivot-selection.md` | ADR-807 Phase 1. |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/docs/03-architecture-decisions/807-snap-sync-phase-3-gating-design.md` | ADR-807 Phase 3. |

### 10.2 Network IDs, chain IDs, port assignments

| Network | NetworkID | ChainID | Genesis hash |
|---|---:|---:|---|
| Mainnet | 50 | 50 | `0x4a9d748bd78a8d0385b67788c2435dcdb914f98a96250b68863a1f8b7642d6b1` |
| Apothem | 51 | 51 | `0xbdea512b4f12ff1135ec92c00dc047ffb93890c2ea1aa0eefe9b013d80640075` |
| Devnet (XDPoSChain main) | 551 | 551 | (devnet variable) |
| Legacy devnet | 5551 | 5551 | (V2 switch at block 2700) |
| Private dev chain | 99999 | 99999 | (cmd/puppeth generated) |

Default ports (geth defaults unchanged): P2P 30303, HTTP 8545, WS 8546.

### 10.3 System contract addresses

| Address | Contract |
|---|---|
| `0x0000000000000000000000000000000000000088` | MasternodeVotingSMCBinary (Validator contract — naming overloaded) |
| `0x0000000000000000000000000000000000000089` | BlockSignersBinary (self-destructed at TIPSigning) |
| `0x0000000000000000000000000000000000000090` | RandomizeSMCBinary |
| `0x0000000000000000000000000000000000000068` | FoudationAddrBinary [sic — typo preserved] |
| `0x0000000000000000000000000000000000000099` | TeamAddrBinary |
| `0x0000000000000000000000000000000000000091` | XDCXAddrBinary |
| `0x0000000000000000000000000000000000000092` | TradingStateAddrBinary |
| `0x0000000000000000000000000000000000000093` | XDCXLendingAddressBinary |
| `0x0000000000000000000000000000000000000094` | XDCXLendingFinalizedTradeAddressBinary |
| `0x0000000000000000000000000000000000000011` | LendingLockAddressBinary |
| `0x000000000000000000000000000000000000009a` | MintedRecordAddressBinary |
| `0x0000000000000000000000000000000000000001` | XDCNativeAddressBinary |

### 10.4 Bootnode list location

- `params/bootnodes.go:85` — `XDCMainnetBootnodes` (28 entries, canonical XinFinOrg fleet).
- `params/bootnodes.go:126` — `XDCApothemBootnodes` (small fleet).
- `scripts/local-sync-test.sh:FLEET_ENODES=...` — operational fleet enodes (xdc01..xdc05, refreshed `8337f48ec`).

### 10.5 External references — PR / issue numbers cited inline

Issue / PR numbers that appear repeatedly in code comments:

- **#740** — Reset onto v1.17.3 (master tracker).
- **#742** — B5-B10 cumulative XDC overlay PR.
- **#651, #682** — bypass-log family origins (state-divergence absorption).
- **#694** — system-contract-address split (`common/xdc_contracts.go`).
- **#710, #716** — `Rules.IsXDC`, `Rules.IsEIP1559` derivation.
- **#724** — StaticCall Istanbul gate (re-applied in #812 for mainnet #34,059,322).
- **#757** — Genesis merge fork-blocks + V2 patching (ADR-001).
- **#763, #768, #770, #772, #774, #776** — XDPOS2 wire-shape ports.
- **#788, #789, #790, #791, #792** — sync_xdc continuous loop + panic recovery + 4096 batch.
- **#793, #794, #795, #796** — BigBalance overflow path quartet.
- **#797** — pathdb RLP import follow-up.
- **#798, #799** — issue #798 bug-hunt (orchestrator).
- **#801, #802** — self-healing rollback + fork-tainted peer pool.
- **#807 Phase 1..14** — snap-sync state machine (Phase 14.4: stall detection + FULL_SYNC fallback).
- **#812** — StaticCall Istanbul gate restore (mainnet #34,059,322 stall).
- **#814** — XDPoS_* RPC namespace explicit registration after upstream removed `APIs()` from `consensus.Engine`.
- **#822, #824, #825, #826** — bootnode + fleet pinning + static-node export fixes.

XDPoS V2 specification reference: `../XDPoSChain` (canonical Go
implementation) and `../XinFin-Node` (operator deployment patterns)
in the working tree. The XDPoS BFT whitepaper is referenced in
`docs/XDPoS.md` and the original XDPoSChain README.

---

## End of guide

This guide intentionally stops short of being a "tutorial" — it is a
reference against which an engineer can verify each step they take
while applying the overlay to a fresh upstream tag. The single
authoritative source for any individual claim is the code itself;
this guide cites that code with file paths and line numbers so the
engineer can re-read it.

When this guide goes out of date — and it will, the next time
upstream geth ships and the next reset is run — the right way to
update it is **section-by-section, re-grounded in the actual code at
that time**. Do not paraphrase prior versions of this document;
re-read the code.

— Generated from branch `reset/integration-1.17.3` at commit
`8337f48ec`, against fork base `117e067f0` (geth v1.17.3).
