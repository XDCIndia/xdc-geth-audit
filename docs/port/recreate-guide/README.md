# Recreating XDC Consensus on Any go-ethereum Version

**The definitive, version-agnostic reproduction guide for layering XDC Network (XDPoS) consensus on top of upstream go-ethereum.**

> Reviewed range: pristine geth **v1.17.3** (`117e067f0f0bae1a17082321f224dedb6765b10f`) → `origin/reset/integration-1.17.3` HEAD (`e9898e81`, 2026-05-30).
> Scope of the overlay: **262 files changed — 204 new, 58 modified, +69,413 / −412 lines.**
>
> This document tells you *how the port was done*, *why each change exists*, and *how to redo it on any future geth version* — in enough detail that a developer **or an AI agent** can execute it from scratch. Every claim below is grounded in the actual diff and cites `file:line` on the XDC branch.

---

## Table of contents

1. [What this is, and how to read it](#1-what-this-is-and-how-to-read-it)
2. [The mental model: XDC as an overlay](#2-the-mental-model-xdc-as-an-overlay)
3. [The methodology that worked: Reset-and-Replay in bundles](#3-the-methodology-that-worked-reset-and-replay-in-bundles)
4. [Prerequisites — knowledge & skills required](#4-prerequisites--knowledge--skills-required)
5. [The four marker conventions (find XDC code in any tree)](#5-the-four-marker-conventions-find-xdc-code-in-any-tree)
6. [The reproduction process — phase by phase](#6-the-reproduction-process--phase-by-phase)
7. [Subsystem reference (the six seams)](#7-subsystem-reference-the-six-seams)
8. [Consensus-parity invariants — what MUST be bit-exact](#8-consensus-parity-invariants--what-must-be-bit-exact)
9. [The fork-activation timeline (the "inertness window")](#9-the-fork-activation-timeline-the-inertness-window)
10. [Validation & the parity harness](#10-validation--the-parity-harness)
11. [Gotchas, traps & war stories](#11-gotchas-traps--war-stories)
12. [Known gaps in the current port](#12-known-gaps-in-the-current-port)
13. [The re-port checklist (copy-paste)](#13-the-re-port-checklist-copy-paste)
14. [For an AI agent executing this port](#14-for-an-ai-agent-executing-this-port)
15. [References & tooling](#15-references--tooling)

---

## 1. What this is, and how to read it

XDC Network is an EVM chain that runs **XDPoS** (XinFin Delegated Proof-of-Stake) consensus instead of Ethereum's PoW/PoS. The canonical XDC client (`XDPoSChain`, and its geth-based sibling at `XDCIndia/go-ethereum`) is a *fork* of go-ethereum. Keeping that fork close to modern geth means periodically re-applying the XDC changes on top of a newer geth base.

This guide is the distilled "how to re-apply" knowledge. It is organised so you can read it three ways:

- **Top to bottom** — to understand the whole shape of the port.
- **Section 7 as a reference** — when you're elbow-deep in a specific subsystem.
- **Section 13 as a checklist** — when you're actually doing the work.

**Two sources of truth you will consult constantly:**

| You want to know… | Look in… |
| --- | --- |
| "How does **XDC** do X?" (the behaviour you must reproduce) | `XDPoSChain/` and `XDCIndia/go-ethereum` (the canonical clients) |
| "How does **geth** do Y now?" (the base you're porting onto) | the target upstream geth tag |

The golden rule of this whole exercise: **the XDC behaviour is the specification; the new geth is just the host.** When they disagree, XDC wins, and you document why.

---

## 2. The mental model: XDC as an overlay

The single most important realisation, confirmed by the file breakdown (**204 new files vs 58 modified**, only 412 deleted lines):

> **XDC is overwhelmingly *additive*. ~78% of the change is new files that upstream never touches. The hard 22% is a small set of *surgical seams* cut into core geth files.**

So the port decomposes cleanly into two kinds of work:

### A. Self-contained packages — copy verbatim
These are pure-XDC packages that import only stable geth surfaces (`consensus`, `core/types`, `core/state`, `params`, `common`, `ethdb`, `rlp`, `crypto`, `log`). They move between geth versions with little or no change:

- `consensus/XDPoS/**` — the entire consensus engine (43 files, ~12k LOC; `xdpos.go` 2049 lines, `engines/engine_v2/engine.go` 2361 lines, `engines/engine_v1/engine.go` 981 lines).
- `contracts/**` — generated bindings for the system contracts (validator `0x88`, blocksigner `0x89`, randomize `0x90`, XDCx DEX, TRC21 issuer, multisig).
- `eth/hooks/**` — the reward/penalty/validator closures.
- `eth/bft/**`, `eth/handler_xdc.go`, `eth/sync_xdc.go` — XDC's sync driver and BFT plumbing.
- `core/xdc_genesis/*.json` + `core/genesis_xdc.go` — embedded genesis specs.
- `params/xdc_features.go`, `node/config_xdc.go`, `node/health.go`, `common/xdc_contracts.go`, `core/xdc_sync_opts.go`.

### B. Surgical seams — re-cut by hand
These are the **58 modified geth files** where XDC code is woven into upstream logic. These are where all the difficulty lives, because *the surrounding upstream code drifts between versions*. The seam is stable in *intent*; its exact location moves.

The rest of this guide is mostly about category B.

```
         ┌──────────────────────────────────────────────────────────┐
         │                  UPSTREAM GO-ETHEREUM                      │
         │  (the host — drifts every release)                        │
         │                                                            │
         │   params/   core/   eth/   internal/   p2p/   cmd/         │
         │      ▲        ▲       ▲         ▲        ▲      ▲           │
         │      │        │       │         │        │      │  58       │
         │   ───┼────────┼───────┼─────────┼────────┼──────┼── surgical│
         │      │        │       │         │        │      │  seams    │
         └──────┼────────┼───────┼─────────┼────────┼──────┼──────────┘
                │        │       │         │        │      │
         ┌──────┴────────┴───────┴─────────┴────────┴──────┴──────────┐
         │              204 PURE-XDC FILES (copy verbatim)            │
         │  consensus/XDPoS · contracts · eth/hooks · eth/bft ·       │
         │  core/xdc_genesis · eth/sync_xdc.go · params/xdc_features  │
         └────────────────────────────────────────────────────────────┘
```

---

## 3. The methodology that worked: Reset-and-Replay in bundles

This is *the process* the team actually used — and the process you should repeat. It is documented in `docs/RESET_BUNDLES.md`, `docs/PORT_LOG.md`, and `docs/port/HANDOFF.md`.

### 3.1 The situation

The `xdc-network` branch carried **677 commits** of XDC overlay on top of a *pre-1.17.0* geth base (commit `54a91b3a`). That base had rotted — state-divergence bugs accumulated, and the overlay was too far behind upstream to merge cleanly. The decision (issue #740): **reset onto pristine geth v1.17.3 and replay the overlay as logical bundles.**

### 3.2 Why not a linear rebase?

Rebasing 677 commits one-by-one across a major-version gap is a nightmare: every commit conflicts, and you can't tell a real semantic conflict from a context-line shift. Instead, the overlay was **grouped into ~11 logical bundles** (B0–B11), each cherry-picked onto a clean baseline branch, built, tested, and merged as a single `--no-ff` merge commit. Original commit SHAs stay reachable via `git log --all`.

### 3.3 The bundle catalogue

| ID | Title | Key paths | Conflict risk |
| --- | --- | --- | --- |
| **B0** | Chain config + genesis + validator/blocksigner contracts | `params/`, `core/genesis*.go`, `contracts/` | low |
| **B1** | XDPoS **V1** engine | `consensus/XDPoS/engines/engine_v1/` | low |
| **B2** | XDPoS **V2** engine + epoch/masternode wiring | `consensus/XDPoS/engines/engine_v2/`, `utils/` | medium |
| **B3** | TIPSigning + BlockSigners precompiles + signer state | `core/vm/`, signer state | low |
| **B4** | Header/finality plumbing + `XDC:` marker family | `core/blockchain.go`, `core/state_processor.go`, `core/types/`, `eth/handler.go` | **HIGH** |
| **B5** | P1 bulk-sync + checkpoint trie commit | `eth/downloader/`, `core/blockchain.go`, `core/state/` | **HIGH** |
| **B6** | Snap-sync XDC adaptations (path scheme, pivot stability) | `eth/downloader/`, `core/rawdb/`, `triedb/pathdb/` | medium |
| **B7** | Bypass cleanup / state-divergence guards | `core/blockchain.go`, `core/state_processor.go` | **HIGH** |
| **B8** | sync-test scripts + canonical parity validator | `scripts/` | none |
| **B9** | Recent fix PRs + one-off bug fixes | mixed | low–medium |
| **B10** | Docs / port log / ADRs | `docs/` | none |
| **B11** | Build, CI, devops leftovers | various | low |

### 3.4 The crucial ordering insight

The bundles were **not** merged in numerical order. The actual order (after escalating the decision to an architectural planner) was:

```
B0 → B1 → B3 → B4 → B5 → B7 → B2 → B6 → B8 → B9 → B10 → B11
```

**Why move B2 (the V2 engine) to 7th?** Because V2 consensus depends on header plumbing (B4), P1 bulk-sync (B5), and the bypass cleanup (B7). If you land V2 first, its tests fail *for plumbing reasons* and you can't tell whether V2 itself was ported correctly. **Land the foundation, then the thing that sits on it.** This generalises: *sequence bundles so that when something breaks, only one layer could be responsible.*

### 3.5 The conflict-resolution policy

Every bundle was governed by one rule:

> **On any conflict where the `XDC:` marker is present on either side, XDC behaviour wins. Document each such resolution as one line in `docs/RESET_CONFLICT_LOG.md`: file path + before/after + a one-line "why".**

For "pure refactor" conflicts (upstream moved code with no semantic change), take the upstream skeleton and re-apply the XDC additions on top — don't take the XDC version verbatim, or you lose the upstream improvement.

### 3.6 The per-bundle gate

```bash
git checkout -b bundle/Bn reset/baseline-<ver>
git cherry-pick <bundle commits>          # resolve conflicts per the policy above
go build ./... && go vet ./... && go test ./<touched-packages>/...

# For F-bucket-collision bundles (B4, B5, B7) ONLY — run the full sync gate:
scripts/local-sync-test.sh wipe-stateful && scripts/local-sync-test.sh start all
# Then grep the logs — these strings MUST return zero hits:
#   "BAD BLOCK" | "invalid merkle root" | "state-root bypass" | "forced trie commit"

git checkout reset/baseline-<ver>
git merge --no-ff bundle/Bn -m "merge: <title> — absorbs XDC PRs #x,#y,#z"
```

The high-risk bundles (B4/B5/B7 — the ones touching `core/blockchain.go` and `eth/downloader/`) get the **full sync test**, not just unit tests. Everything else needs only build + vet + touched-package tests.

---

## 4. Prerequisites — knowledge & skills required

Before you start, you (human or agent) need working command of:

### Domain knowledge
- **EVM execution model** — gas accounting, intrinsic gas, refunds, precompiles, the fork/EIP system (Berlin/London/Shanghai/Cancun and the EIPs each bundles).
- **RLP encoding** — how block headers, transactions, and state accounts serialise; *why field order and presence change the hash*.
- **State trie / world state** — `IntermediateRoot`, `Finalise`, account leaves, how a single dirty-empty account can delete a leaf and shift the root.
- **geth architecture** — `consensus.Engine` interface, `core.StateProcessor`, `core.BlockChain`, the `eth` package wiring (`eth/backend.go`), the downloader/skeleton/beaconsync sync pipeline, the devp2p `eth` wire protocol.
- **XDPoS consensus** — V1 (round-robin, snapshot-based) vs V2 (HotStuff-style BFT with QC/TC, rounds, epochs); masternode selection; epoch-boundary rewards; penalties; the system contracts at `0x88/0x89/0x90`.

### Tooling skills
- **`git`** — cherry-pick, `--no-ff` merges, `merge-base`, `rev-list`, range diffs, worktrees.
- **`rlpgen` / `gencodec`** (`go:generate`) — regenerating `gen_header_rlp.go` / `gen_header_json.go` / `gen_account_rlp.go` after changing a struct. **This is the single most important mechanical skill** — get it wrong and every block hash is wrong.
- **`go build` / `go vet` / `go test`**, the race detector (`-race`), `GOMAXPROCS=1` for ruling out concurrency bugs.
- **`abigen`** — regenerating `contracts/**` bindings from Solidity if the system contracts change.
- A **parity oracle** — a synced canonical XDC node (or block/state dumps from one) to diff against.

### If you are an AI agent
See [§14](#14-for-an-ai-agent-executing-this-port). In short: use parallel sub-agents to analyse subsystems, **adversarially verify every consensus-critical claim against the actual code**, and escalate irreversible architectural calls rather than guessing.

---

## 5. The four marker conventions (find XDC code in any tree)

XDC code is discoverable. When you land on an unfamiliar XDC fork and need to know "what did they change here?", these four conventions surface it:

1. **The `XDC:` / `xdpos:` log & comment prefix.** Grep `XDC:` and `xdpos:` — nearly every surgical edit is tagged.
   ```bash
   grep -rIn "XDC:" --include=*.go | grep -v _test.go
   ```
2. **The `IsXDC()` predicate.** `IsXDC()` is `c.XDPoS != nil` (`params/config.go`). Every conditional that forks behaviour for XDC chains gates on it (or on `config.XDPoS != nil`). Grep it to find every behavioural fork:
   ```bash
   grep -rIn "IsXDC()\|XDPoS != nil\|config.XDPoS" --include=*.go
   ```
3. **`xdc_*` filenames.** New files added beside upstream files carry an `xdc_` or `_xdc` infix: `genesis_xdc.go`, `sync_xdc.go`, `handler_xdc.go`, `config_xdc.go`, `xdc_sync_opts.go`, `api_xdc.go`. List them:
   ```bash
   git ls-files | grep -iE "xdc|xdpos"
   ```
4. **The diff against the fork base.** The ground truth. If you know the geth tag the fork was built on:
   ```bash
   git diff <geth-base-tag>..HEAD --stat
   git diff <geth-base-tag>..HEAD --name-status | grep '^M'   # the surgical seams
   ```

The "find the smoking gun" workflow when parity breaks at block N: `git diff <base>..HEAD -- <suspect file>` on the file in the failing code path, then grep that diff for `IsXDC`/`XDC:` to isolate the XDC-specific branch.

---

## 6. The reproduction process — phase by phase

### Phase 0 — Establish the baseline and the oracle

1. **Pin the geth base.** Check out the exact upstream tag you're porting onto. Tag it (`v<ver>-pristine`) and confirm it builds clean on your toolchain. This is your `reset/baseline-<ver>` branch.
2. **Enumerate the overlay.** Diff the current XDC branch against its *old* geth base to get the full list of XDC commits/files. This is your work-list.
3. **Stand up a parity oracle.** You need something to compare against: either a synced canonical XDC node exposing RPC, or block + pre-state dumps for a range of blocks (including fork-boundary blocks). Without this you are flying blind — *do not skip it.*
4. **Create the conflict log.** An empty `docs/RESET_CONFLICT_LOG.md` ready for one line per resolved conflict.

### Phase 1 — Copy the self-contained packages verbatim

Bring across category-A packages (see [§2](#2-the-mental-model-xdc-as-an-overlay)) unchanged. They won't *compile* yet (they reference seams that don't exist), but getting the files in place first lets the compiler tell you exactly which seams are missing.

Order: `consensus/XDPoS/**`, `contracts/**`, `eth/hooks/**`, `core/xdc_genesis/**`, `params/xdc_features.go`, `common/xdc_contracts.go`, `core/xdc_sync_opts.go`, `node/config_xdc.go`, `node/health.go`.

### Phase 2 — Cut the surgical seams, in dependency order

This is the bundle order from [§3.4](#34-the-crucial-ordering-insight), expressed as subsystems. Do them in this sequence — each depends on the previous:

1. **`params/` + `common/`** — chain configs, the `ChainConfig` struct fields, `XDPoSConfig`/`V2`, `XDCBaseFee`, the TIP constants. (Nothing downstream works until `XDPoS != nil` is reachable.) → [§7.1](#71-chain-config-params--genesis)
2. **`core/types/`** — the three header fields + RLP regeneration + `StateAccount.BigBalance`. (Consensus-critical; everything hashes through here.) → [§7.2](#72-core-types--rlp-consensus-critical)
3. **`core/genesis.go`** — the SetupGenesis patch logic + genesis header field init. *Assert the genesis hashes immediately.* → [§7.1](#71-chain-config-params--genesis)
4. **`core/vm/` + `core/state_transition.go` + `core/state_processor.go` + `core/evm.go` + `eip1559`** — the execution-layer seams. → [§7.4](#74-evm-precompiles-gas--state-transition)
5. **`eth/ethconfig` + `eth/backend.go`** — engine construction and the V2 wiring sequence. → [§7.3](#73-consensus-engine--wiring)
6. **`eth/protocols/eth/` + `eth/handler*.go` + `eth/sync_xdc.go` + `p2p/discover/v4wire`** — the wire protocol and sync driver. → [§7.5](#75-eth-wire-protocol-p2p--sync)
7. **`internal/ethapi/` + `internal/web3ext/` + `eth/api_backend.go`** — the RPC surface. → [§7.6](#76-rpc--api-surface)
8. **`cmd/utils/flags.go` + `params/bootnodes.go`** — CLI flags and bootnodes (non-consensus, do last).

### Phase 3 — Build & wire

`go build ./...` then `go vet ./...`. The compiler walks you through every still-missing seam. Resolve them against the canonical client, logging conflicts.

### Phase 4 — Validate

The three tiers from [§10](#10-validation--the-parity-harness). **Do not declare done until the parity harness is green across the fork boundaries**, not just genesis.

---

## 7. Subsystem reference (the six seams)

Each subsystem below is documented as: **what XDC changes · the upstream seam · the re-port recipe · fragility.** All `file:line` references are on `reset/integration-1.17.3`.

### 7.1 Chain config, params & genesis

**What XDC adds**
- **Two chain configs** in `params/config.go`:
  - `XDCMainnetChainConfig` — ChainID **50**. Homestead=1, EIP150=2, EIP155=3, EIP158=3, Byzantium=4; Constantinople=Petersburg=Istanbul=Berlin=London=**76321000**; `Eip1559Block`=**98800200**, `CancunBlock`=**98802000**. XDPoS: `Period`=2, `Epoch`=900, `Reward`=5000, `RewardCheckpoint`=900, `Gap`=450, `FoudationWalletAddr`=`0x92a289fe…ac65`. V2: `SwitchBlock`=**80370000**, `SwitchEpoch`=89300.
  - `XDCApothemChainConfig` — ChainID **51**. Same pre-Byzantium; Constantinople…London=**61290000**; `Eip1559Block`=**71550000**, `CancunBlock`=**71551800**. `FoudationWalletAddr`=`0x746249…cd5c`. V2: `SwitchBlock`=**56828700**, `SwitchEpoch`=63143.
- **New structs**: `XDPoSConfig{Period, Epoch, Reward, RewardCheckpoint, Gap, FoudationWalletAddr, V2}`, `V2{SwitchBlock, CurrentConfig (json:"config"), AllConfigs, SwitchEpoch, …}`, `V2Config`, `ExpTimeoutConfig`, `TrustedSyncCheckpoint{Number, Hash, Root, Masternodes, Penalties}`.
- **`ChainConfig` field additions**: `Eip1559Block`, `CancunBlock`, `XDPoS *XDPoSConfig`, `XDCFeatures`, `TrustedSyncCheckpoints`.
- **Genesis hash constants**: Mainnet `0x4a9d748b…d6b1`, Apothem `0xbdea512b…0075`.
- **Fork-predicate overrides**: `IsIstanbul` = `TIPXDCXCancellationFee OR IstanbulBlock` (activates Istanbul early via the TIP constant); `IsEIP1559` = forks on `Eip1559Block`, **decoupled from `IsLondon`**; `IsCancunBlock` (block-number Cancun gate); `IsXDC()` = `XDPoS != nil`.
- **`params/protocol_params.go`**: `XDCBaseFee = 12500000000` (12.5 gwei).
- **`params/xdc_features.go`** (NEW): `IsXDPoSV2`, `IsXDPoSV2SwitchBlock`, `SkipV2FirstEpochPenalty`.
- **Genesis** (`core/genesis_xdc.go` + `core/xdc_genesis/*.json`): `//go:embed`-ed minimal genesis specs; `DefaultXDC{Mainnet,Apothem}GenesisBlock()`. Addresses use the **`xdc` hex prefix**. `extraData` encodes the genesis masternode set (V1 layout: 32-byte vanity + N×20-byte signers + 65-byte seal).

**The upstream seam**
- The new structs/configs sit beside `MainnetChainConfig` etc. The `Is*` methods *override* the stock fork predicates one-for-one.
- **The critical workaround (ADR-001):** `chainConfigOrDefault` returns `g.Config` verbatim when a genesis is supplied (always, for `--apothem`/`--xdcmainnet`) — it never maps the XDC genesis hash to the *rich* `params.XDC*ChainConfig`. So the rich config (V2, late forks, checkpoints) must be **merged back in post-hoc** inside `SetupGenesisBlock` and `LoadChainConfig`, via `patchMissingForkBlocks()`. Without this, `CheckConfigForkOrder` fails and — worse — the **forkID hash diverges from canonical, so production peers reject the wire handshake.**
- Genesis header gets `Validators/Validator/Penalties` set to **empty slices** (`[]byte{}`, not nil) in `toBlockWithRoot` — required for the genesis hash to be correct.

**Re-port recipe**
1. Port the structs verbatim. **JSON tags are load-bearing**: `foudationWalletAddr` (yes, misspelled — match canonical), `config` (for `V2.CurrentConfig`), `certificateThreshold`. A mismatched tag silently drops the field on unmarshal.
2. Copy every numeric value (chain IDs, fork blocks, XDPoS fields, V2Configs maps) **exactly** from `XDPoSChain/params/config.go`. These are consensus-critical.
3. Re-implement the `Is*` overrides. **First check whether upstream renamed or deleted the predicate** — modern geth is actively removing pre-Merge block forks. If `IsIstanbul`/`IsPetersburg` are gone, the early-Istanbul behaviour needs a new home (likely the EVM rules selection).
4. Port `patchMissingForkBlocks` into both `SetupGenesisBlock` and `LoadChainConfig`. The insertion *point* moves every time upstream refactors genesis setup — re-locate it (it goes immediately after the effective config is resolved).
5. Copy `core/xdc_genesis/*.json` **byte-for-byte** (do not reformat). Then **assert** `DefaultXDCMainnetGenesisBlock().ToBlock().Hash() == 0x4a9d748b…` and Apothem `== 0xbdea512b…` before doing anything else.

**Fragility: HIGH** — this is the most fragile file in the port. Upstream is deleting block-fork plumbing; the `Is*` override seam and the `Constantinople=…=Berlin` collapse depend on `CheckConfigForkOrder` and the EVM still consulting these predicates. The `V2` struct's embedded `sync.RWMutex` also makes `ChainConfig` non-copyable by value (any upstream `cfg := *someConfig` trips `go vet`).

### 7.2 Core types & RLP (consensus-critical)

**What XDC adds** — three byte-slice fields on `Header`, inserted **after `Nonce`, before `BaseFee`** (`core/types/block.go:85-87`, verified):
```go
Validators []byte `json:"validators"`   // next epoch's masternode set (epoch-boundary blocks only)
Validator  []byte `json:"validator"`    // creator's seal over HashNoValidator()
Penalties  []byte `json:"penalties"`    // masternodes penalised this epoch
```
They carry **no `rlp` tag** → they are *always encoded* (empty = `0x80`). Verified RLP order in `gen_header_rlp.go:39-44`:
```
w.WriteBytes(obj.Nonce[:])    // index 15
w.WriteBytes(obj.Validators)  // index 16  ← XDC
w.WriteBytes(obj.Validator)   // index 17  ← XDC
w.WriteBytes(obj.Penalties)   // index 18  ← XDC
_tmp1 := obj.BaseFee != nil   // index 19+ optional cascade (unchanged)
```
Real XDC headers serialise as an **18-element list** (indices 0–17; the optional tail stays nil). Also: `HashNoValidator()` (an 18-element hash with the `Validator` slot forced empty, for seal verification), `Header.Time` kept as `uint64` (RLP-identical to XDPoSChain's `*big.Int` for all real values — proven by `header_rlp_compat_test.go`), and the two newest upstream optionals (`BlockAccessListHash` EIP-7928, `SlotNumber` EIP-7843) **stripped from the generated encoder**.

Plus **`StateAccount.BigBalance *big.Int rlp:"-"`** (`state_account.go`): XDC genesis accounts hold `2^256-1` and rewards push beyond uint256. The hand-written `EncodeRLP`/`DecodeRLP` encodes via `WriteBigInt` only when `BigBalance != nil`; for any balance ≤ 2^256-1 it is **byte-identical to upstream**. Mirrored in `SlimAccount` for snap-sync.

**The upstream seam** — the rlpgen-generated `Header.EncodeRLP` and the account-trie leaf encoder. XDC's fields slot between `Nonce` and the optional cascade; the account overflow path is invisible for normal balances.

**Re-port recipe**
1. Insert the three fields after `Nonce`, before `BaseFee`, **in that order**, no `rlp` tag. Add matching `hexutil.Bytes` entries to `headerMarshaling`.
2. **Regenerate** via the two `go:generate` directives in `block.go`:
   ```bash
   cd core/types
   go run github.com/fjl/gencodec -type Header -field-override headerMarshaling -out gen_header_json.go
   go run ../../rlp/rlpgen -type Header -out gen_header_rlp.go
   ```
   Then verify rlpgen emitted the three fields as plain `w.WriteBytes(...)` between `Nonce` and the `obj.BaseFee != nil` cascade.
3. **Curate the optional tail explicitly.** Strip from the generated encoder any geth optional field *newer than XDC's fork point* that you can't guarantee stays nil (the port stripped `BlockAccessListHash` and `SlotNumber`). Keep `BaseFee` and the Cancun fields as nil-by-default stubs.
4. For `StateAccount`: disable rlpgen, hand-write `EncodeRLP`/`DecodeRLP` with the three-way branch (BigBalance / nil / uint256), and mirror in `SlimAccountRLP`/`FullAccount`.

**Fragility: HIGH** — the single most consensus-sensitive change. Wrong field order, an accidental `rlp:"optional"` tag, or a stray serialised optional → *every header hash is wrong from genesis*.

### 7.3 Consensus engine & wiring

**What XDC adds** — `consensus/XDPoS/` is a **V1 wrapper engine that delegates to V2**. `xdpos.go` implements `consensus.Engine`; every method dispatches by version via `IsV2Block(header)` = `header.Number >= config.V2.SwitchBlock`. `New()` **panics if `SwitchBlock % Epoch != 0`** (V2 switch must be epoch-aligned). `engines/engine_v2/` is a full HotStuff-style BFT engine (QC/TC, rounds, round-based epochs, 3-chain commit finality). `engine_v2.New()` is a **process-global singleton** (`engineOnce.Do`) to avoid OOM at the switch block.

The **reward hook lives outside the engine package** — in `eth/hooks/engine_v2_hooks.go` — because the V2 reward path needs `*core.BlockChain` + `core/rawdb` to walk signing transactions, dependencies the engine deliberately avoids to stay import-cycle-free.

**The upstream seam — and the exact wiring sequence (this is where V2 silently dies if you get it wrong):**

1. `eth/ethconfig/config.go` `CreateConsensusEngine` — **first check**, before the `TerminalTotalDifficulty == nil` guard:
   ```go
   if config.XDPoS != nil { return XDPoS.New(config.XDPoS, db), nil }
   ```
2. `eth/backend.go` `New()`, **before** `CreateConsensusEngine`: `common.CopyXDCConstants(chainID)` + `XDPoS.SetNetworkConstants(chainID)`.
3. `eth/backend.go` `New()`, **after** the blockchain exists — type-assert `*XDPoS.XDPoS`, then call **in this exact order**:
   ```go
   hooks.AttachConsensusV1Hooks(xdposEngine, blockchain, chainConfig)
   hooks.AttachConsensusV2Hooks(xdposEngine, blockchain, chainConfig)   // sets wrapper.HookReward
   v2 := engine_v2.New(chainConfig, chainDb, nil, nil)                  // nil chans = verify mode
   v2.HookCommitBlock = func(h *types.Header) { blockchain.SetFinalized(h) }
   xdposEngine.SetEngineV2(v2)                                          // ← flips wrapper to V2 dispatch
   v2.SetHookReward(func(...) { return xdposEngine.HookReward(...) })   // ← the ONLY effective reward wiring
   ```
4. `core/state_processor.go` — after the pre-tx state copy: `if e, ok := chain.Engine().(interface{ SetParentState(*state.StateDB) }); ok { e.SetParentState(statedb.Copy()) }`. Rewards read **pre-transaction** balances; omit this and every epoch-boundary state root diverges.
5. `eth/backend.go` `APIs()` — re-register the engine RPC namespace manually (upstream **removed `APIs()` from `consensus.Engine`**, #814): `apis = append(apis, xdposEng.APIs(s.blockchain)...)`.

**Re-port recipe** — copy `consensus/XDPoS/**` and `eth/hooks/**` verbatim; ensure `core.XdcBulkSyncMode` (an `atomic.Bool` in `core/xdc_sync_opts.go`) exists. Then re-cut the five wiring points above. The engine's method signatures are pinned to geth's `consensus.Engine` interface — if upstream changes `Finalize`/`Seal`/`FinalizeAndAssemble` signatures, the wrapper must track them.

**Fragility: HIGH** for the wiring (six ordered calls in `eth.New`, a function upstream refactors constantly); MED for the engine source itself.

> **The single most common reason V2 silently doesn't activate:** the reward-hook ordering. `AttachConsensusV2Hooks` runs *before* `SetEngineV2`, so its internal `EngineV2.SetHookReward` is dead (EngineV2 still nil at that moment). The **only** effective wiring of the V2 engine's reward callback is the explicit `v2.SetHookReward(...)` call *after* `SetEngineV2`. Because `EngineV2.Finalize` checks the *V2 engine's* `HookReward` field — not the wrapper's — omitting or misordering that one line means V2 epoch-switch rewards never fire. Zero compile errors, zero verification errors — just a state-root divergence at the first V2 epoch boundary.

### 7.4 EVM, precompiles, gas & state transition

The organising principle: **XDC activates a non-standard EIP bundle at `Eip1559Block`, not at Berlin/London/Shanghai.** Everything here exists to reproduce XDPoSChain's `newEip1559InstructionSet`, which enables EIP-2929 + EIP-3529 + EIP-2565 + EIP-3860 *all together* at `Eip1559Block`.

| Change | What | Seam | Fragility |
| --- | --- | --- | --- |
| **EVM rules decoupling** | Four "marker" instruction sets (`xdcBerlin/London/Merge/Shanghai`) gate opcode pricing by `IsEIP1559`, not the upstream fork. The `isXDC && IsEIP1559` case is inserted **above** `IsShanghai` in `NewEVM` so XDC jumps to the Shanghai table (2929+3529+3860) at `Eip1559Block`. | `NewEVM` instruction-set switch; `ChainConfig.Rules()` | **HIGH** — `jump_table.go`/`eips.go` reorganised almost every upstream release |
| **MODEXP deferral** | `PrecompiledContractsXDCBerlin` with `bigModExp{eip2565:false}` charges old (expensive) MODEXP gas for Berlin..pre-EIP1559. | `activePrecompiledContracts`, `case rules.IsBerlin` | MED |
| **EIP-2028 OFF (permanent)** | `IntrinsicGas` always charges **68 gas / non-zero data byte**, never 16. XDPoSChain never implemented 2028. | `IntrinsicGas` call in `execute()` | HIGH |
| **EIP-3529 deferral** | Refund cap stays 1/2 (not 1/5) until `Eip1559Block` (`calcRefund`: `IsLondon`→`IsEIP1559`). | `calcRefund()` | HIGH |
| **Fixed base fee** | `CalcBaseFee` returns constant `XDCBaseFee` (12.5 gwei) for any XDC chain — never market-driven. `VerifyEIP1559Header`: pre-`Eip1559Block` headers must carry no BaseFee. | `eip1559.CalcBaseFee` / `VerifyEIP1559Header` | MED |
| **No base-fee burn / TRC21 routing** | Past `TIPTRC21Fee` with a non-zero owner, the **full `gasUsed*gasPrice`** is credited to the token owner, not the coinbase, with no base-fee deduction. XDC burns nothing. | fee-credit block at end of `execute()`; `owner ...common.Address` variadic threaded through `ApplyMessage` | HIGH |
| **Special transactions** | Txs to BlockSigners `0x89` → `ApplySigningTransaction` (zero gas, **minimal log `{Address, BlockNumber}`**, nonce bump, no EVM). Txs to trading `0x92`/lending `0x93` → `ApplyEmptyTransaction`. Bypass the `GasFeeCap < BaseFee` check in `preCheck`. | tx loop in `StateProcessor.Process` (before `TransactionToMessage`) | HIGH |
| **TIPSigning self-destruct** | At `block.Number == TIPSigning`: `IntermediateRoot(false)` → `SelfDestruct(0x89)` → `Finalise(true)` → `IntermediateRoot(true)`. The leading `IntermediateRoot(false)` is required for pathdb correctness. | `StateProcessor.Process`, after DAO handling | HIGH |
| **`parentState` reward seam** | `statedb.Copy()` snapshotted at top of `Process`, pushed to engine via `SetParentState` before `Finalize`. | `StateProcessor.Process` | HIGH |
| **PREVRANDAO** | For XDC chains, `random = Keccak256Hash(header.Number.Bytes())` (XDPoS has no difficulty/mixdigest). | `NewEVMBlockContext` | LOW-MED |
| **StaticCall touch gating** | The zero-value account touch at the top of `StaticCall` is gated `!IsXDC() || IsIstanbul(num)` — else a STATICCALL to `ecrecover` deletes leaf `0x01` and diverges the root by one leaf. | first statement of `EVM.StaticCall` | MED (easily lost on reset) |

**System contracts** (`contracts/**`): the engine reads masternode/signer/reward data through **hardcoded storage-slot layouts** (`core/state/statedb_utils.go` — `slotValidatorMapping`, `slotBlockSignerMapping`), *not* via EVM `eth_call`. The generated Go bindings are mostly for RPC/tooling. **The storage-slot constants are the consensus-critical data dependency** — a wrong slot silently corrupts reward distribution.

**Re-port recipe** — search `execute()` for the `IntrinsicGas(` call, the coinbase fee-credit `if/else`, `calcRefund`, and `preCheck`'s fee-cap check; re-cut each. Re-derive the four marker tables from whatever the new geth calls `newIstanbulInstructionSet`/`enable3198`/`enable3855`/`opRandom`. Port `ApplySigningTransaction`/`ApplyEmptyTransaction` verbatim — **the minimal-log shape is consensus-critical** (a "richer" log breaks the bloom and receipts root).

### 7.5 eth wire protocol, P2P & sync

**What XDC adds**
- **Protocol**: keeps `ProtocolName = "eth"`, adds **version `XDPOS2 = 100`** offered *first* in `ProtocolVersions` (two XDC nodes negotiate `eth/100`; stock geth falls back to 69/70). `protocolLengths[XDPOS2] = 227`. Re-introduces post-merge-removed `NewBlockHashesMsg = 0x01` / `NewBlockMsg = 0x07`, adds BFT codes `VoteMsg=0xe0`, `TimeoutMsg=0xe1`, `SyncInfoMsg=0xe2`.
- **Handshake**: XDPOS2 uses a pre-merge `StatusPacket62` (ProtocolVersion, NetworkID, TD, Head, Genesis) with **NO ForkID**. Validates only NetworkID + ProtocolVersion + Genesis. The peer's `TD` is stashed as a **peer-ranking metric only** — *it is not a block number* (≈4 difficulty units/block on Apothem).
- **Discovery**: `p2p/discover/v4wire` — `UseXDCPing = true` → pings go out as **packet type 5** (XDC mainnet nodes only understand pingXDC). Without this you find zero peers.
- **Sync driver** (`eth/sync_xdc.go`, ~1240 lines, NEW): v1.17.3's `BeaconSync` is post-merge and idles after one batch waiting for a CL message XDC never sends. `xdcSyncer` drives sync continuously — a 500ms ticker, picks best peer by the TD-rank, fetches 4096 headers + bodies, `InsertChain`. **Caught-up test is hash equality, not TD comparison.** Includes self-healing fork rollback (`SetHead(head - window)` with exponential backoff on repeated `ErrUnknownAncestor`). `XdcBulkSyncMode atomic.Bool` (`core/xdc_sync_opts.go`) gates fast-paths during bulk sync.
- **BFT dispatch**: for sync nodes, Vote/Timeout/SyncInfo packets are **accept-and-ignore** (`handler_eth.go` returns nil — *never drop the peer*). Full BFT (`eth/bft/`) is only needed for masternode block production (M8).
- **No-RequestId bridge**: XDPOS2 (eth/63-era) requests carry no RequestId, so geth's response dispatcher can't route them. Solved with bypass channels (`XDCHeaderResp`/`XDCBodyResp`) for the xdcSyncer plus a FIFO re-anchoring bridge for the downloader.

**Re-port recipe** — re-add the `XDPOS2=100` const (first in `ProtocolVersions`, `protocolLengths[100]=227`), the `StatusPacket62` handshake path (no ForkID), the `xdpos2` handler map, `UseXDCPing`, and the xdcSyncer wiring in `eth/handler.go` `Start()`/`Stop()`. The downloader/skeleton internals churn heavily upstream — re-derive the no-RequestId bridge against the new tracker API; keep the re-anchor-by-hash logic verbatim (it handles fleet quirks, not geth behaviour).

**Fragility: HIGH** for the no-RequestId bridge and the two-pivot snap machine (both sit on the most volatile upstream code); MED for the xdcSyncer wiring; MED but mechanically simple for v4wire and the protocol const additions.

### 7.6 RPC / API surface

**What XDC adds**
- **`XDPoS` namespace** (PascalCase — JSON-RPC is case-sensitive; tooling calls `XDPoS_getMasternodesByNumber`), via `consensus/XDPoS/api.go` + `api_extended.go`: `getSnapshot`, `getSigners`, `getMasternodes`, `getMasternodesByNumber`, `getCandidates`, `proposals`, `getV2BlockByNumber`, `getBlockInfoByEpochNum`, `getEpochNumbersBetween`, `networkInformation`, `getRewardByAccount`, …
- **`eth_*` extensions** (`internal/ethapi/api_xdc.go`): `getBlockSignersByNumber`, `getBlockFinalityByNumber`, `getCandidates`, `getRewardByHash`, `getTransactionAndReceiptProof`, `getAccountInfo`, `getStakerROI`, …
- **`Backend` interface** (`internal/ethapi/backend.go`) gains `GetRewardByHash(...)` and `BlockChain() *core.BlockChain`; implemented in `eth/api_backend.go`.

**The upstream seam** — because upstream **removed `APIs()` from the `consensus.Engine` interface** (#814), engine namespaces aren't auto-registered. The port re-registers manually in `Ethereum.APIs()` via a type assertion (see [§7.3](#73-consensus-engine--wiring) step 5).

**Re-port recipe** — register the namespace manually in `Ethereum.APIs()`; extend the `Backend` interface and implement in `api_backend.go` (this forces *every* `Backend` implementor to add the two methods, or the build breaks); regenerate `gen_header_json.go` so `eth_getBlockByNumber` actually emits the `validators`/`validator`/`penalties` fields.

**Fragility: HIGH** — the `Backend` interface extension is the most likely re-port build break; the manual `APIs()` registration lives in a frequently-refactored function. See [§12](#12-known-gaps-in-the-current-port) for current RPC defects.

---

## 8. Consensus-parity invariants — what MUST be bit-exact

If you change *nothing else correctly*, these are the things that, if wrong, fork the chain. They are ordered by how early/often they bite.

1. **Genesis hash.** Mainnet `0x4a9d748b…d6b1`, Apothem `0xbdea512b…0075`. Driven by the embedded JSON (extraData masternode encoding, alloc, timestamp, gasLimit) + the empty-slice init of the three header fields. *Assert this first.*
2. **Header RLP layout.** `Validators`, `Validator`, `Penalties` as `[]byte`, always-encoded, at indices 16/17/18, between `Nonce` and `BaseFee`. No `rlp:"optional"`. No stray serialised upstream optional.
3. **StateAccount RLP.** Balances ≤ 2^256-1 encode byte-identically to upstream; only true >256-bit balances take the `BigBalance` path.
4. **The EIP bundle at `Eip1559Block`.** EIP-2929, 3529, 2565, 3860 all defer to `Eip1559Block`; EIP-2028 permanently off (68 gas/non-zero byte). `IsEIP1559 != IsLondon`.
5. **Special-tx handling.** `0x89`/`0x92`/`0x93` txs: zero gas, minimal `{Address, BlockNumber}` log, exact bloom.
6. **TIPSigning self-destruct sequence** of `0x89` (with the pathdb-correct `IntermediateRoot(false)` first).
7. **`parentState` for rewards.** Reward calc reads pre-transaction balances.
8. **TRC21 fee routing & no base-fee burn.** Full fee to the candidate owner past `TIPTRC21Fee`.
9. **Fixed base fee = 12500000000.**
10. **TIP fork-block constants & reward percentages** (`common/types.go` / `CopyXDCConstants`): 90% owner / 10% foundation; the TIP block numbers per chain.
11. **StaticCall zero-value touch gated to `IsIstanbul`.**
12. **PREVRANDAO = `Keccak256(blockNumber)`.**

Treat any mismatch in these as **P0 — stop everything and root-cause it before continuing.**

---

## 9. The fork-activation timeline (the "inertness window")

A subtle but powerful operational insight from `docs/port/HANDOFF.md`: **most of what you port is inert until the chain reaches the activation height.** This lets you sync a node on an old binary through the early chain, then swap in the new binary before the relevant fork.

```
Apothem:
GENESIS ─── 23.78M ──────── 56.83M ──────── 61.29M ──────── 71.55M ─────── HEAD
              │                │                │                │
          TIPSigning      V2 Switch      Berlin/late forks   EIP-1559
          (signer SD)     (BFT engine)   (61,290,000)       XDCBaseFee
                                                            MODEXP/2929/3529/3860
```

Implications for the re-port:
- A bug in the V2 engine wiring **cannot** manifest before the V2 switch block — so an early-chain sync that's green tells you *nothing* about V2 correctness.
- Your parity harness **must** cover the fork-boundary blocks (the switch block ± a few epochs), not just genesis-onward.
- You can stage deployment: validate the inert early range first, then gate the fork ranges behind the parity harness.

---

## 10. Validation & the parity harness

Three tiers, escalating in cost and confidence (from `docs/port/XDC_PORTING_PLAYBOOK.html` §8):

**Tier 1 — build + vet + tests** (every change)
```bash
go build ./...
go vet ./...
go test -short -count=1 ./consensus/XDPoS/... ./core/... ./params/ \
  ./consensus/misc/eip1559/ ./core/vm/ ./core/txpool/... ./internal/web3ext/
```
The engine ships its own parity vectors — `consensus/XDPoS/cross_client_vectors_test.go`, `state_root_vectors_test.go`, `header_rlp_compat_test.go`, and `testvectors/*.json`. **Run these — they catch RLP/hash drift directly.**

**Tier 2 — local sync-test cluster** (`scripts/local-sync-test.sh`)
```bash
scripts/local-sync-test.sh wipe-stateful && scripts/local-sync-test.sh start all
```
A multi-node local cluster that exercises real block import + sync. Grep the logs — these must return **zero hits**: `BAD BLOCK`, `invalid merkle root`, `state-root bypass`, `forced trie commit`.

**Tier 3 — canonical parity (the real gate)**
For block N with its pre-state, the ported binary MUST produce an **identical post-state root, receipts root, and block hash** to canonical XDC. Run it across:
- A genesis-to-head sync against the production fleet, asserting head hash == public XDC explorer.
- The fork-boundary ranges specifically (V2 switch, EIP-1559 activation).
- `scripts/validate-canonical-parity.sh` / `scripts/xdc-parity-check.sh`.

**When parity fails:** isolate the first divergent block, dump pre-state + post-state for it on both binaries, diff the state roots leaf-by-leaf, and `git diff <base>..HEAD` the file in that code path. The bug is almost always a missing or misplaced surgical seam from [§7](#7-subsystem-reference-the-six-seams). Use `GOMAXPROCS=1` and `-race` to rule out a concurrency artefact in `IntermediateRoot`.

---

## 11. Gotchas, traps & war stories

These are the lessons that cost the team hours — written down so they cost you minutes.

- **`IsEIP1559 != IsLondon`.** This decoupling pervades five files. Any code gating *pricing/fee* logic on `IsLondon` for XDC is a bug; only marker-opcode availability uses `IsLondon`.
- **The reward-hook ordering** (see [§7.3](#73-consensus-engine--wiring)). The most expensive invisible bug in the port: V2 rewards silently never fire, diverging the state root at the first V2 epoch boundary, with no compile or verification error.
- **"TD is not a block number."** The XDPOS2 handshake's `TD` field is ≈4 difficulty-units/block and is used *only* for peer ranking. Any code that subtracts it from local head to compute "blocks behind" produces garbage. The caught-up check uses **hash equality**.
- **The two-pivot / `fsMinFullBlocks` misdiagnosis** (the documented 4-hour debug, `docs/port/MENTAL_MODEL.md`). The downloader's state pivot is *always* `head − fsMinFullBlocks (=64)` — it is **not** a knob for pinning some externally chosen pivot. Pass the peer's **true tip** as `BeaconSync(head, …)` and the QC-trusted ancestor (`peer_tip − 1024`) as `final`. Raising `fsMinFullBlocks` pushes the pivot *backward into history* (toward pruned state), it does not move it toward your chosen pivot.
- **Minimal logs for special txs.** The natural instinct is to emit the "real" Sign event — that breaks the bloom and receipts root. Canonical emits only `{Address, BlockNumber}`.
- **Lost-on-reset regressions.** The StaticCall touch gating and the EVM marker tables were *silently dropped* in a prior reset and had to be restored. A clean upstream merge re-introduces upstream defaults and re-breaks parity — gate these with the parity harness, not eyeballing.
- **`xdc` address prefix & misspelled JSON tag.** `foudationWalletAddr` is misspelled *on purpose* (match canonical). Addresses in genesis use the `xdc` hex prefix — the `common` unmarshaler must honour it or the JSON won't parse.
- **`engine_v2.New` is a singleton.** Calling it twice (e.g. in tests or a multi-chain setup) returns the *first* config. 
- **`ChainConfig` is non-copyable by value** (the `V2` struct embeds a `sync.RWMutex`). `cfg := *someConfig` trips `go vet`.

---

## 12. Known gaps in the current port

Surfaced by the subsystem analysis (verified against the branch). Document these so a re-porter doesn't mistake them for working features:

- **`gen_header_json.go` is stale.** It was regenerated against pristine v1.17.3 (correctly dropping `BlockAccessListHash`/`SlotNumber`) but **not** re-run after the three XDC fields were added to `headerMarshaling`. Because it defines a real `MarshalJSON`, it wins over struct tags — so **`eth_getBlockByNumber` does not currently emit `validators`/`validator`/`penalties`**. Fix: re-run `go generate ./core/types/`. *(This is the highest-impact RPC parity defect.)*
- **RPC namespace casing collision.** `XDPoS.APIs()` serves `XDPoS_*` (PascalCase) but `web3ext.go` registers the console module `xdpos` calling `xdpos_*` (lowercase) — they don't match at runtime.
- **`EventAPI` defined but never registered.** `xdpos_subscribe` (newQC/newTC/roundChange/newEpoch) is dead code — `NewEventAPI` is never added to any `rpc.API` list.
- **`xdc_*` console namespace is dead.** `XDCJs`/`XDCxJs`/`XDCLendingJs` are defined in `web3ext_xdc.go` but never added to the `Modules` map, and there's no Go service backing `xdc_*` RPC.
- **`CacheInvalidator.InvalidateCaches`** — interface + call site exist, but no engine implements it yet (forward-declared hardening for reorgs).
- **`NetworkInformation` hardcodes chainID 50** (wrong on Apothem); **`getLatestPoolStatus` is a stub** returning 0/0.
- **Deferred to M8 (masternode block production):** the full BFT dispatcher wiring, `miner/xdc_agent.go`, broadcast LRU caches. A pure sync node doesn't need these.

---

## 13. The re-port checklist (copy-paste)

```
PHASE 0 — BASELINE & ORACLE
[ ] Check out target geth tag; tag as v<ver>-pristine; confirm clean build
[ ] Enumerate XDC overlay vs old base: git diff <old-base>..xdc-HEAD --name-status
[ ] Stand up a parity oracle (synced canonical XDC node or block/state dumps)
[ ] Create empty docs/RESET_CONFLICT_LOG.md

PHASE 1 — COPY VERBATIM (category A)
[ ] consensus/XDPoS/**            [ ] contracts/**            [ ] eth/hooks/**
[ ] core/xdc_genesis/** + genesis_xdc.go
[ ] params/xdc_features.go        [ ] common/xdc_contracts.go [ ] core/xdc_sync_opts.go
[ ] eth/sync_xdc.go, eth/bft/**, eth/handler_xdc.go
[ ] node/config_xdc.go, node/health.go

PHASE 2 — SURGICAL SEAMS (in this order)
[ ] params/config.go: structs, 2 chain configs, genesis-hash consts, Is* overrides
[ ] params/protocol_params.go: XDCBaseFee = 12500000000
[ ] common/types.go: TIP constants, CopyXDCConstants, xdc-prefix, reward %
[ ] core/types/block.go: 3 header fields after Nonce; regenerate gen_header_{rlp,json}.go
[ ] core/types/state_account.go: BigBalance + hand-written RLP; mirror SlimAccount
[ ] core/genesis.go: patchMissingForkBlocks in SetupGenesis + LoadChainConfig; header field init
[ ]   >>> ASSERT genesis hashes == 0x4a9d748b… / 0xbdea512b… <<<
[ ] core/vm/{evm,jump_table,eips,contracts}.go: marker tables, MODEXP defer, opcode gating
[ ] core/evm.go: PREVRANDAO=Keccak256(num); CanTransfer BigBalance
[ ] core/state_transition.go: EIP-2028 off, 3529 defer, special-tx fee-cap bypass, TRC21 routing
[ ] core/state_processor.go: parentState snapshot+SetParentState, TIPSigning SD, Apply{Signing,Empty}Tx
[ ] consensus/misc/eip1559/eip1559.go: fixed XDCBaseFee, header verify tolerances
[ ] eth/ethconfig/config.go: CreateConsensusEngine XDPoS branch (first)
[ ] eth/backend.go: CopyXDCConstants before engine; the 6-call V2 wiring sequence; APIs() registration
[ ] eth/protocols/eth/{protocol,handshake,handler,handlers,peer}.go: XDPOS2=100, StatusPacket62, xdpos2 map, no-RequestId bridge
[ ] eth/handler.go + handler_eth.go: xdcSyncer start/stop; BFT accept-and-ignore
[ ] p2p/discover/v4wire/v4wire.go: UseXDCPing (ping type 5)
[ ] internal/ethapi/{backend,api_xdc}.go + eth/api_backend.go: Backend iface + eth_* methods
[ ] internal/web3ext/web3ext.go: console module registration
[ ] cmd/utils/flags.go + params/bootnodes.go: --apothem/--xdcmainnet + bootnodes

PHASE 3 — BUILD & WIRE
[ ] go build ./... ; go vet ./...  (resolve each missing seam; log conflicts)

PHASE 4 — VALIDATE
[ ] go test ./consensus/XDPoS/... (parity vectors)  + touched packages
[ ] scripts/local-sync-test.sh wipe-stateful && start all ; grep logs = zero bad hits
[ ] Canonical parity across genesis→head AND fork boundaries (V2 switch, EIP-1559)
[ ] Assert head hash == public XDC explorer
```

---

## 14. For an AI agent executing this port

This guide is written so an autonomous agent (Claude or otherwise) can execute the port. Specific guidance:

- **Decompose by subsystem, fan out, verify.** The six subsystems in [§7](#7-subsystem-reference-the-six-seams) are independent enough to analyse in parallel sub-agents, but **every consensus-critical claim must be adversarially verified against the actual code** before you act on it. (This guide was itself built that way; the agents found real bugs, e.g. the stale `gen_header_json.go`.) Cite `file:line`.
- **The spec is the canonical client, not your priors.** When in doubt about a value (a fork block, a reward %, a gas constant), read it out of `XDPoSChain`/`XDCIndia/go-ethereum` — never infer it.
- **Consensus parity is sacred.** Do not "improve", "optimise", or "clean up" any consensus path. Do not change RLP encoding. Do not force upstream defaults onto an XDC-customised path. A hash mismatch is P0.
- **Escalate irreversible architectural calls.** The original team escalated to a planning model for: the catch-up strategy (big-bang vs incremental vs freeze), any module rewrite, the first consensus mismatch (root cause may be architectural), the bundle ordering, and any decision that changes block-hash output. Do the same — these are scalpel decisions, not routine fixes.
- **Use the markers** ([§5](#5-the-four-marker-conventions-find-xdc-code-in-any-tree)) to locate XDC code, and the **fork-activation timeline** ([§9](#9-the-fork-activation-timeline-the-inertness-window)) to know what a given test range can and can't prove.
- **Maintain a running log.** Append every decision and every conflict resolution to a port log + conflict log. The next agent (or human) needs the trail.
- **A reusable skill exists for this.** See `.claude/skills/port-xdc-onto-geth/SKILL.md` in this repo — it encodes this methodology as an invokable skill.

---

## 15. References & tooling

### Sibling docs (in this repo)
| Doc | What it gives you |
| --- | --- |
| [`PROPOSAL.md`](./PROPOSAL.md) | **Status & landing plan** for `reset/integration-1.17.3` — what the branch is, what it inherits from geth v1.17.3, bit-exact consensus parity, the phased validation, and the asks |
| [`WHY-MODERN-GETH.md`](./WHY-MODERN-GETH.md) | The **rationale** companion — what the `reset/integration-1.17.3` branch inherits from the geth v1.17.3 base (the 1.8.3 → 1.17.3 capability timeline) and the disk/sync economics, verified against upstream |
| [`EVIDENCE-BASE.md`](./EVIDENCE-BASE.md) | **Externally-sourced, adversarially-verified citations** (geth release blogs, EIPs, devp2p spec) for the rationale's claims — 25/25 verified, with the code-ship-vs-feature-ship reconciliation (e.g. PBSS shipped v1.13.0) |
| `docs/port/XDC_PORTING_PLAYBOOK.html` | The original 14-section playbook (this guide consolidates + verifies it) |
| `docs/port/XDC_CONSENSUS_CHANGES.html` | Interactive map of XDC changes vs upstream + fork timeline |
| `docs/port/XDC_DEEP_DIFF.html` | Deep file-by-file diff explorer |
| `docs/RESET_BUNDLES.md` | The bundle catalogue + merge order + conflict policy |
| `docs/port/MENTAL_MODEL.md` | The two-pivot snap-sync model (the 4-hour war story) |
| `docs/port/HANDOFF.md` | The inertness-window deployment strategy |
| `docs/adr/001-genesis-config-merge.md` | The SetupGenesis patch rationale (ADR-001) |
| `docs/XDPoS.md` | XDPoS consensus overview (V1/V2, masternodes, rewards, penalties) |

### Scripts
| Script | Purpose |
| --- | --- |
| `scripts/local-sync-test.sh` | Multi-node local sync cluster (Tier 2 gate) |
| `scripts/validate-canonical-parity.sh`, `scripts/xdc-parity-check.sh` | Canonical parity (Tier 3 gate) |
| `scripts/run-xdc-mainnet.sh`, `scripts/run_xdc_node.sh` | Run a node against mainnet/apothem |

### Canonical reference clients (read-only — the spec)
- `XDPoSChain` — the reference XDC consensus implementation.
- `XDCIndia/go-ethereum` (this fork's origin) — the geth-based canonical client.

### External
- XDC docs: https://docs.xinfin.org
- go-ethereum: https://github.com/ethereum/go-ethereum

---

*Generated from a verified analysis of the `117e067f…e9898e81` range on `origin/reset/integration-1.17.3`. Every file:line reference is on that branch. When upstream geth moves, the seams move with it — but the intent, the invariants, and the order in this guide do not.*
