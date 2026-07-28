# Evidence Base — Cited Sources for the geth v1.17.3 Baseline

**Externally-sourced, adversarially-verified evidence supporting [`PROPOSAL.md`](./PROPOSAL.md) and [`WHY-MODERN-GETH.md`](./WHY-MODERN-GETH.md) — what the `reset/integration-1.17.3` branch inherits from the v1.17.3 base, with citations.**

> **Method.** Produced by a deep-research pass: 6 search angles → 24 sources fetched → 107 candidate claims extracted → **top 25 verified by 3-vote adversarial check** (a claim needed ≥2/3 *refutes* to be killed). Result: **25/25 confirmed, 0 killed**. Nearly every finding rests on **primary sources** — official EIPs, the `ethereum/devp2p` spec, Ethereum Foundation release blogs, geth release notes — cross-checked in several cases against the geth v1.17.3 source tree on disk.
>
> This complements the earlier **codebase-grounded** analysis (which used `git tag --contains` on the local repo). Where the two differ, the distinction is *"first release containing the code"* (git) vs *"release where geth officially shipped the feature"* (EF blog) — see the correction below.

---

## ⚠️ Correction to the earlier docs (PBSS version)

My earlier `WHY-MODERN-GETH.md`/`PROPOSAL.md` stated **PBSS "first shipped in v1.12.1, not 1.13.0."** The deep-research evidence (EF release blog + v1.11.0 notes, 3-0 verified) shows that framing was **misleading**. The accurate picture:

| | Version | Evidence |
|---|---|---|
| PBSS deferred (prep PRs only) | **v1.11.0** (2023-02) | v1.11.0 release notes: deferred "so we can get back to a steady release schedule" |
| Code merged (PR #25963) — *present in this tag* | in **v1.12.1** (2023-08-10) | local `git tag --contains 7de748d3f` |
| **PBSS officially shipped** (announced, usable) | **v1.13.0** (2023-09-12) | EF blog *"geth v1.13.0"* |
| **PBSS default for new full nodes** | **v1.14.0** (2024) | EF blog / geth docs |

**Lesson (methodological):** "earliest tag containing the introducing commit" ≈ "first release with that *code*," but for a large multi-PR feature announced later it is **not** the same as "first release where the feature shipped/was usable/became default." For PBSS specifically, cite **v1.13.0 (shipped) / v1.14.0 (default)**. The other git-derived versions in §4 of `WHY-MODERN-GETH.md` (freezer v1.9.0, snap v1.10.0, pebble v1.11.0, etc.) are corroborated below.

---

## 🔬 Live codebase audit (XDPoSChain working tree)

Re-verified directly against the `XDPoSChain` checkout — working tree on branch **`fixfastsync-new`**, `git describe` = **`v2.4.6-beta1-2384-ge4686012d`** (latest release tag v2.7.0). Corrections / refinements vs the earlier write-up:

| Claim | Earlier write-up | **Verified now** | Evidence (file) |
|---|---|---|---|
| Freezer / ancients | "present ✅ (backported)" | **None** — `nofreezedb`; `Ancient*` → `errNotSupported`; `freezer_table.go` is a 34-line stub; no freezer ever opened | `core/rawdb/database.go`, `freezer_table.go` |
| Trie database | "no `trie/triedb`" | **`trie/triedb/hashdb`** present (modern package split) but **no `pathdb`** → still hash-scheme, no PBSS | `trie/triedb/hashdb/database.go` |
| Go toolchain | `1.23` | **`1.25`** | `go.mod` |
| Sync direction | (static) | **Actively patching fast-sync** — `fixfastsync-new` branch (multi-header pivots, strict pivot-flag validation); snap still not adopted | recent `git log` |
| Merge | "pre-Merge" | **`MergeBlock`** block-marker in config (still **no** TTD / no PoS machinery) | `params/config.go` |

Re-confirmed unchanged on the live tree: `eth/62` + `eth/63` + `xdpos2(100)` and **no ForkID**; **no snapshot**, **no Pebble**, **no snap sync** (FastSync / FullSync / LightSync); **LevelDB only**; forks block-gated through Cancun; header `Validators` / `Validator` / `Penalties` (gencodec `required`); fixed base fee `InitialBaseFee = 12500000000` (12.5 gwei).

> **Net effect on the disk story:** with *neither* a freezer *nor* PBSS, the legacy node has **no cold/hot split and no online pruning** — every block, receipt, and dead trie node accumulates in one growing LevelDB. The migration case is therefore *stronger*, not weaker, than the earlier draft implied.

### 📟 Field measurement — `reset/integration-1.17.3` on XDC mainnet (node `xdc01`)

Measured **directly on a live node** running this branch (`02ebae0d`, an ancestor of `reset/integration-1.17.3`) via SSH — `geth 1.17.3-stable · --state.scheme=path (PBSS) · --syncmode full · Pebble · freezer active`, XDC mainnet (chainId 50):

| Metric | Measured | Note |
|---|---|---|
| Sync state | **synced to head** — block **103,313,922**, `eth.syncing = false` | head ts 2026-06-02 19:30 UTC |
| Peers | **24** | healthy mainnet peering |
| Total on disk | **303 GB** | full datadir |
| Freezer / ancients | **281 GB** (274 GB chain + 6.8 GB state-history) | immutable, compressible |
| **Live PBSS state (Pebble)** | **~22 GB** | **bounded by online pruning** — the PBSS thesis confirmed in production |
| Host disk | 880 GB NVMe, 667 GB used (80%) | — |

**Significance:** a modern (1.17.3 / PBSS / full-sync) XDC mainnet node at head is **303 GB with only ~22 GB of mutable state**, and confirms the **freezer is active** on the 1.17.3 line (vs the legacy XDPoSChain `nofreezedb`).

### 📟 Field measurement — legacy XDPoSChain v2.6.8 (canonical client) vs 1.17.3

Both nodes measured **directly and live** at the same chain head (~103.31M, `syncing=false`): the canonical legacy client (`XDC/v2.6.8-stable`, Docker `xinfinorg/xdposchain:v2.6.8`, `--gcmode full --syncmode full`, 16 peers) and `xdc01` (this branch). Apples-to-apples — both RPC timings over **IPC**.

| Storage | Legacy v2.6.8 (hash + LevelDB) | 1.17.3 PBSS (xdc01) | Leap |
|---|---|---|---|
| Total chaindata | **919 GB** (one LevelDB) | **303 GB** | **~3× smaller** |
| Freezer / cold-hot split | **none** (`nofreezedb`) — all hot | 281 GB ancients | ✓ |
| Hot / mutable state | ~919 GB & growing (no online prune) | **~22 GB** (online-pruned) | bounded |

| RPC latency — IPC, avg ms/call (n=30) | Legacy v2.6.8 | 1.17.3 PBSS | Speed-up |
|---|---|---|---|
| `eth_blockNumber` | 0.567 | 0.267 | ~2.1× |
| `eth_getBalance` | 1.633 | 0.533 | ~3.1× |
| `eth_getBlock(latest)` | 2.767 | 1.233 | ~2.2× |
| `eth_getCode` | 4.833 | 1.600 | ~3.0× |
| `eth_getStorageAt` | 1.867 | 0.300 | **~6.2×** |

| Compute & networking | Legacy v2.6.8 | 1.17.3 PBSS | Note |
|---|---|---|---|
| **CPU (steady)** | **~1022 %** (~10 cores) | **~23 %** (~0.2 core/node) | **~40× less CPU** |
| **RAM (RSS)** | **~3.0 GB** | **~5.0 GB/node** | Gen-2 trades RAM for speed (PBSS/Pebble caches) |
| Peers | 20 | 33 | both healthy |
| Mempool (txpool) | 6 pending / 4 queued | 0 / 0 | — |
| Host load avg | 12.6 (1 node) | 8.9 (2 nodes on host) | per-node far lighter on Gen-2 |
| Block gas limit | 420 M | 420 M | same network |

**Net (measured, both sides):** same XDPoS consensus, same chain head — the 1.17.3 base is **~3× smaller on disk**, keeps a **bounded ~22 GB hot state** vs one ever-growing 919 GB LevelDB, serves warm RPC reads **~2–6× faster** (`getStorageAt`, a direct slot read, 6×), and runs on **~40× less CPU** (~10 cores → ~0.2 core) — at the cost of **~2× more RAM** (~5 GB vs ~3 GB). *Caveats: warm-cache single reads (not cold/archive or load tests); the legacy node had restarted ~17 min before the CPU snapshot (catch-up inflates it — an earlier weeks-uptime reading showed ~837%, so ~8–10 cores is steady); both RPC timings now IPC-to-IPC. An earlier "~100× faster RPC" estimate was **not** supported once the legacy node was measured directly — corrected here.*

---

## Verified findings (all primary-sourced unless noted)

### 1. Freezer / ancients — geth v1.9.0 (Jul 2019) · vote 3-0
Splits the DB: blocks & receipts **older than ~90,000 blocks** behind head ("3 epochs") move out of LevelDB into an **append-only flat-binary "freezer"**, explicitly so cold data can live on **cheaper/durable HDD** while recent state stays on SSD. Concrete benchmark: a fresh fast-sync at block 7.77M placed **79 GB in the freezer vs 60 GB in LevelDB**.
*Sources: [geth databases doc](https://geth.ethereum.org/docs/fundamentals/databases), [EF blog v1.9.0](https://blog.ethereum.org/2019/07/10/geth-v1-9-0), [PR #17814](https://github.com/ethereum/go-ethereum/pull/17814).*
→ **Correction (live codebase audit, below):** XDPoSChain does **NOT** have a functional freezer — it runs geth's `nofreezedb` path (`Ancient*` return `errNotSupported`; `freezer_table.go` is a 34-line error-var stub; no freezer is ever opened). All blocks/receipts live in LevelDB. The freezer is a modern-geth win XDC still lacks.

### 2. Path-based state storage (PBSS) — shipped v1.13.0, default v1.14.0 · vote 3-0
Replaces the hash-scheme MPT. Implements **online pruning by default** ("no more guerilla offline pruning"). **Offline pruning applies ONLY to the legacy hash scheme** and "will be deprecated." Verified in v1.17.3 source: `snapshot.go` ("only supported in hash mode"), `flags.go` (`--state.scheme hash|path`).
*Sources: [v1.11.0 notes](https://github.com/ethereum/go-ethereum/releases/tag/v1.11.0), [EF blog v1.13.0](https://blog.ethereum.org/2023/09/12/geth-v1-13-0), [geth dbpruning doc](https://geth.ethereum.org/docs/fundamentals/dbpruning).*
→ XDC 2.7.0 (hash-only) **predates all state-scheme evolution** — this is the single biggest gain from migrating.

### 3. Hash-scheme nodes grow unbounded; PBSS bounds them — geth docs · vote 2-1 (dissent was a currency quibble)
A snap-synced **hash-scheme** node needs **>650 GB** and **grows ~14 GB/week** with default cache; offline pruning resets it back to ~650 GB. **PBSS prunes online**, avoiding the prune-and-reset cycle.
**CAVEAT (currency):** these doc figures have drifted upward — independent **2026 sources cite ~1.2 TiB and ~7–8 GiB/week** for a fresh hash full node. Use 650 GB / 14 GB-week to illustrate the *mechanism*, not today's absolute best-case.
*Sources: [geth dbpruning doc](https://geth.ethereum.org/docs/fundamentals/dbpruning), [EF blog v1.13.0](https://blog.ethereum.org/2023/09/12/geth-v1-13-0), [7BlockLabs 2026 disk sizes](https://www.7blocklabs.com/blog/ethereum-archive-node-disk-size-2026-vs-erigon-archive-node-disk-size-2026-vs-geth-full-node-disk-size-2026).*
→ This is the **mechanism** behind XDC's ~800 GB-and-rising full node and its resync ritual.

### 4. Snap sync — introduced v1.10.0, default v1.10.4, fast-sync removed v1.10.14 · vote 3-0
Downloads state-trie **leaves with range proofs** and **regenerates the trie locally** (vs fetching every intermediate node), plus a **state-healing** phase. Healing "has to outpace the growth of the blockchain, otherwise the node will never catch up." geth made snap the default and **removed fast sync entirely**, demonstrating architectural superiority.
*Sources: [geth sync-modes doc](https://geth.ethereum.org/docs/fundamentals/sync-modes), [EF blog v1.10.0](https://blog.ethereum.org/2021/03/03/geth-v1-10-0), [devp2p snap spec](https://github.com/ethereum/devp2p/blob/master/caps/snap.md).*
→ XDC 2.7.0 uses **fast sync** (node-by-node state download) — slow, and the reason a fresh node takes days.

### 5. eth wire protocol chain (eth/63 → eth/70), each tied to an EIP · vote 3-0
`eth/64` (Nov 2019, EIP-2124/2364 ForkID) · `eth/65` (Jan 2020, EIP-2464) · `eth/66` (Apr 2021, EIP-2481 request-ids) · `eth/67` (Mar 2022, EIP-4938 drop GetNodeData) · `eth/68` (Oct 2022, EIP-5793) · `eth/69` (Apr 2025, EIP-7642) · `eth/70` (Jun 2025, EIP-7975, **still Draft**).
*Sources: [devp2p eth spec](https://github.com/ethereum/devp2p/blob/master/caps/eth.md) + the individual EIPs.*
→ XDC 2.7.0 sits at **eth/62–63**, predating every one of these.

### 6. eth/64 ForkID (EIP-2124/2364) — fixes fragile peering · vote 3-0
Adds `forkid:[forkHash:[4]byte, forkNext:uint64]` to the Status handshake; `forkHash` = **CRC32 of genesis + all passed fork-block numbers**. Incompatible nodes "disconnect before expensive block exchanges and validations," and ForkID-based **discovery filtering rejects incompatible peers before dialing**. Verified bit-for-bit in `core/forkid/forkid.go`.
*Sources: [EIP-2124](https://eips.ethereum.org/EIPS/eip-2124), [EIP-2364](https://eips.ethereum.org/EIPS/eip-2364), [PR #31592](https://github.com/ethereum/go-ethereum/pull/31592).*
→ XDC 2.7.0 has **no ForkID** — it cannot cheaply reject cross-fork/cross-chain peers (the "fragile peering" pain point).

### 7. eth/66 request IDs (EIP-2481) · vote 3-0
Adds a 64-bit `request_id` to every request/response pair so clients **match responses to requests directly**, instead of fragile response-content matching.
*Sources: [devp2p eth spec](https://github.com/ethereum/devp2p/blob/master/caps/eth.md), [EIP-2481](https://eips.ethereum.org/EIPS/eip-2481).*
→ XDC 2.7.0 predates request-ids — **the exact gap** that forces the port's custom "no-RequestId bridge."

### 8. eth/67 dropping GetNodeData (EIP-4938) was the **prerequisite for PBSS** · vote 3-0
Removed `GetNodeData`/`NodeData` (the eth/63 mechanism for serving raw trie nodes by hash). EIP-4938 verbatim: *"Avoiding the need to store such a mapping in the database is the main motivation for removing this request type."* The v1.13.0 blog confirms the causal chain: with path-based keys "Geth becomes incapable of serving the old fast sync."
*Sources: [EIP-4938](https://eips.ethereum.org/EIPS/eip-4938), [EF blog v1.13.0](https://blog.ethereum.org/2023/09/12/geth-v1-13-0).*
→ **The load-bearing link:** you cannot adopt PBSS while still serving `GetNodeData`. XDC 2.7.0 still serves it → **locked into hash-keyed storage**. Migrating the *wire protocol* is a prerequisite for migrating *storage*.

### 9. eth/69 (EIP-7642) — history-expiry-aware peering · vote 3-0
Drops the now-meaningless `td` from Status; adds `earliestBlock/latestBlock/latestBlockHash` + a `BlockRangeUpdate` (0x11) message so peers advertise the **historical range they can serve**. Motivated by the history-expiry decision that clients **may drop pre-merge history after May 1, 2025** (EIP-4444 direction).
*Sources: [EIP-7642](https://eips.ethereum.org/EIPS/eip-7642), [EF partial-history-expiry post](https://blog.ethereum.org/2025/07/08/partial-history-exp).*

### 10. The Merge (Sep 15 2022) + Engine API; internal PoW sealing removed v1.14.0 · vote 3-0
PoW→PoS; mining no longer possible on mainnet. EL↔CL now talk over a **JWT-authenticated Engine API** (port 8551). geth **v1.14.0 (Apr 2024) removed Ethash/Clique sealing** entirely (`--mine` panics).
*Sources: [EF Merge announcement](https://blog.ethereum.org/2022/08/24/mainnet-merge-announcement), [execution-apis engine auth](https://github.com/ethereum/execution-apis/blob/main/src/engine/authentication.md), [geth #28671](https://github.com/ethereum/go-ethereum/issues/28671).*
→ **Relevance to XDC:** XDPoS is its *own* consensus engine, so XDC does **not** adopt the Engine-API split — but it inherits the dead-PoW-code cleanup. **Open architectural question** (below): does XDPoS sit as an in-process `consensus.Engine` (the Clique pattern, which still exists) or need an external driver? Per the project's operating rules this warrants planner escalation.

### 11. Pebble DB — added v1.11.0 (opt-in), default v1.12.0 · vote 3-0
geth added Pebble to replace/augment LevelDB, citing LevelDB as "a one-person project where the maintainer has signaled that the project is not a priority." Opt-in via `--db.engine=pebble` in v1.11.0; default in v1.12.0. (v1.11.0 also fixed a snap-sync stale-pivot stall, PR #26453.)
*Sources: [v1.11.0 notes](https://github.com/ethereum/go-ethereum/releases/tag/v1.11.0), [PR #26517](https://github.com/ethereum/go-ethereum/pull/26517).*
→ XDC 2.7.0 is **LevelDB-only**.

---

## Reconciliation: git-verified vs officially-shipped versions

For three features, "code present in tag" (my git method) and "officially shipped/default" (EF blog) differ. Cite the right one for the right purpose:

| Feature | Code in tag (git `--contains`) | Officially shipped / default (EF) | Use in proposal |
|---|---|---|---|
| **PBSS** | v1.12.1 (2023-08-10) | **shipped v1.13.0; default v1.14.0** | **v1.13.0 / v1.14.0** |
| **Snap sync** | v1.10.0 | **default v1.10.4**; fast-sync removed v1.10.14 | "introduced v1.10.0, default v1.10.4" |
| **Pebble** | v1.11.0 | **default v1.12.0** | "added v1.11.0, default v1.12.0" |

Freezer (v1.9.0), ForkID (v1.9.x / eth/64 Nov 2019), eth/66 (v1.10.0), eth/67 (v1.10.19), eth/68 (v1.11.0), Merge/PoW-removal (v1.14.0): git and external sources agree.

---

## Scope gaps — NOT independently verified by this run

The deep-research run honestly flagged that several items in the question did **not** appear in its 25 confirmed claims, so treat these as **codebase-derived (mine) but not externally re-verified here**:

- **XDC-specific chaindata sizes** (~2.5 TB archive / ~800 GB full): **no external claim verified these.** They are **operator-provided figures** (from the request) and should be confirmed against a primary XDC source (XinFin-Node docs or a measured chaindata snapshot) before being quoted as authoritative. XDC.dev operator posts exist but were not in the verified set.
- **Ethereum archive (~multi-TB) and full/snap (~1–1.5 TB)**: only the hash-scheme **>650 GB** figure was verified, not archive sizes.
- **EIP-1559 (London) and EIP-2718 (typed txs) geth versions**: not externally verified this run (my git data: EIP-1559 code v1.10.4).
- **EIP-4844 blobs + blobpool geth version**: not externally verified this run (my git data: blobpool code v1.12.1; activated mainnet Dencun, 2024).
- **EIP-4444 / era1 file format** as a storage feature: only the *wire-protocol coupling* via eth/69 was verified, not the on-disk era format (my git data: `internal/era` first in v1.13.12).
- **Beacon sync** as a distinct mode: not separately verified (only snap + post-merge pivot).

These don't weaken the proposal — they mark which numbers are externally-cited vs internally-derived.

---

## Open questions surfaced

1. **Citable XDC chaindata sizes.** Source the ~2.5 TB / ~800 GB figures from a primary XDC reference before quoting them as authoritative.
2. **Exact geth releases for EIP-1559 / EIP-2718 / EIP-4844 / era files** — confirm against EF blogs (my git data is in §4 of `WHY-MODERN-GETH.md`).
3. **EIP-4444 era1 on-disk format** vs the eth/69 wire coupling — confirm the storage-feature release.
4. **XDPoS on a modern geth core:** in-process `consensus.Engine` (Clique pattern, still present) vs an external Engine-API-style driver? The evidence informs but does not resolve this; per the project operating rules it is a **block-production/consensus-model decision warranting planner escalation** before implementation (it is also explicitly out of scope for the sync-node port — see [`PROPOSAL.md` §9 Phase 4](./PROPOSAL.md#9-phased-plan--milestones)).

---

## Full source list

**Primary (official EIPs / specs / EF blogs / geth docs & releases):**
- geth docs — [databases](https://geth.ethereum.org/docs/fundamentals/databases) · [dbpruning](https://geth.ethereum.org/docs/fundamentals/dbpruning) · [sync-modes](https://geth.ethereum.org/docs/fundamentals/sync-modes) · [downloadera](https://geth.ethereum.org/docs/fundamentals/downloadera)
- EF blogs — [v1.9.0](https://blog.ethereum.org/2019/07/10/geth-v1-9-0) · [v1.10.0](https://blog.ethereum.org/2021/03/03/geth-v1-10-0) · [v1.13.0](https://blog.ethereum.org/2023/09/12/geth-v1-13-0) · [Merge announcement](https://blog.ethereum.org/2022/08/24/mainnet-merge-announcement) · [partial history expiry](https://blog.ethereum.org/2025/07/08/partial-history-exp)
- geth releases — [v1.11.0 notes](https://github.com/ethereum/go-ethereum/releases/tag/v1.11.0) · [all releases](https://github.com/ethereum/go-ethereum/releases) · PRs [#17814](https://github.com/ethereum/go-ethereum/pull/17814), [#26517](https://github.com/ethereum/go-ethereum/pull/26517), [#31592](https://github.com/ethereum/go-ethereum/pull/31592), issue [#28671](https://github.com/ethereum/go-ethereum/issues/28671)
- specs/EIPs — [devp2p eth](https://github.com/ethereum/devp2p/blob/master/caps/eth.md) · [devp2p snap](https://github.com/ethereum/devp2p/blob/master/caps/snap.md) · [EIP-2124](https://eips.ethereum.org/EIPS/eip-2124) · [2364](https://eips.ethereum.org/EIPS/eip-2364) · [2481](https://eips.ethereum.org/EIPS/eip-2481) · [4938](https://eips.ethereum.org/EIPS/eip-4938) · [7642](https://eips.ethereum.org/EIPS/eip-7642) · [4844](https://eips.ethereum.org/EIPS/eip-4844) · [execution-apis engine auth](https://github.com/ethereum/execution-apis/blob/main/src/engine/authentication.md)

**Operator data (blog/forum — lower quality, use with care):**
- [7BlockLabs — 2026 archive/full disk sizes](https://www.7blocklabs.com/blog/ethereum-archive-node-disk-size-2026-vs-erigon-archive-node-disk-size-2026-vs-geth-full-node-disk-size-2026)
- [XDC.dev — speed up XDC node sync with snapshots](https://www.xdc.dev/rushabh_parmar/how-to-speed-up-xdc-network-node-sync-using-snapshots-2237) · [backing up XDC geth chaindata](https://www.xdc.dev/s4njk4n/backing-up-xdc-geth-client-chain-database-58oh) · [hardened XDC full node 2026](https://www.xdc.dev/s4njk4n/hardened-xdc-full-node-2026-notes-to-self-3g2d)
- [Yorick Downe — node hardware/disk guide](https://gist.github.com/yorickdowne/3323759b4cbf2022e191ab058a4276b2)

---

*Deep-research run `wf_8d47bd87-8ad` · 6 angles · 24 sources · 107 claims → 25 verified (25 confirmed, 0 killed). Findings here are the verified subset; codebase-derived facts not re-verified externally are marked in "Scope gaps."*
