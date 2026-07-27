# The geth v1.17.3 Base — What `reset/integration-1.17.3` Inherits

**Seven years of upstream go-ethereum engineering (1.8.3 → 1.17.3), and what the `reset/integration-1.17.3` branch inherits by building on the pristine v1.17.3 base.**

> Companion to [`README.md`](./README.md) (the reproduction guide). This doc is the *why*; the README is the *how*.
>
> **Scope:** this catalogs the modern-geth capabilities the `reset/integration-1.17.3` branch inherits by building on pristine geth v1.17.3. Where the legacy geth-1.8-era **XDPoSChain** client appears in comparison tables, it is the older canonical client shown *for contrast* — not a characterization of XDC's current multi-client fleet (GP5, Erigon, Nethermind, Reth — see the [Architecture](https://xdc.network/network-architecture.html) overview).
>
> Verified against local checkouts: **XDPoSChain** working tree (`params/version.go` = `2.7.0-beta`) and **pristine geth v1.17.3** (`117e067f0f0bae1a17082321f224dedb6765b10f`). **Every feature→release attribution in §4 is verified against upstream go-ethereum git history** — each version is the first stable `v1.x.y` tag whose history contains the feature's introducing commit (commit SHAs cited inline).

---

## Table of contents

1. [TL;DR — the engineering case](#1-tldr--the-engineering-case)
2. [What each one actually is](#2-what-each-one-actually-is)
3. [Side-by-side: XDC 2.7.0 vs geth 1.17.3](#3-side-by-side-xdc-270-vs-geth-1173)
4. [The 7-year improvement timeline (1.8.3 → 1.17.3)](#4-the-7-year-improvement-timeline-183--1173)
5. [Area-by-area deep dive](#5-area-by-area-deep-dive)
6. [The disk economics (2.5 TB archive / 800 GB full)](#6-the-disk-economics-25-tb-archive--800-gb-full)
7. [What this means for the XDC port](#7-what-this-means-for-the-xdc-port)
8. [References](#8-references)

---

## 1. TL;DR — what the branch inherits

By building on pristine geth v1.17.3, the `reset/integration-1.17.3` branch inherits seven years of upstream engineering that the older XDC overlay — and the legacy geth-1.8-era XDPoSChain client — never had: modern state storage, sync, database, and wire protocol. This doc catalogs exactly what the v1.17.3 base provides, verified against upstream git history. (For reference, the legacy XDPoSChain is a hard fork of ~2018 geth `eth/63`, selectively modernized — Go toolchain, `core/txpool`, `trie/triedb/hashdb`, forks through Cancun — but still on 2018-era state/sync/networking.)

The payoff of hosting XDPoS on a 1.17.3-class base, ranked by operator impact:

1. **Path-based state storage (PBSS)** → full nodes stop growing and need no periodic resync. *(Biggest win — directly addresses the 800 GB-and-rising full node.)*
2. **Flat snapshot + snap sync** → from-scratch sync drops from days to hours.
3. **Pebble DB** → smoother I/O and compaction at TB scale.
4. **`eth/66+` request IDs + ForkID** → robust peering, and it removes the custom "no-RequestId" bridging the port currently needs.
5. **Era files / EIP-4444** → future history offloading to shrink both archive and full nodes.

The one place "newer" is *not* the goal: **block production**. Upstream geth removed internal mining at the Merge and now only *executes* what a consensus layer feeds it. XDPoS *is* XDC's consensus and produces blocks internally — so the port keeps XDC's model and merely re-hosts it on modern execution/storage.

---

## 2. What each one actually is

These aren't two versions of one thing — they're two different animals:

- **XDC 2.7.0 (XDPoSChain v2.7.0-beta)** — the **canonical XDC mainnet client**. A hard fork of go-ethereum ~1.8.x, **pre-Merge**, **hash-trie only**, **fast/full-sync only**, **LevelDB-era**, carrying the **full XDPoS consensus engine + internal masternode block production**. Selectively modernized: Go 1.25, `core/txpool` package, the `trie/triedb/hashdb` package layout, EVM forks defined through Cancun (block-gated). *(Live working tree audited: branch `fixfastsync-new`, `git describe` = `v2.4.6-beta1-2384`; latest release tag is v2.7.0.)*

- **Geth 1.17.3** — **current upstream Ethereum**. **Post-Merge PoS** (block production is external, via a consensus layer over the Engine API), **PBSS path-based state**, **snap sync**, **Pebble DB**, **EIP-4844 blobs**, timestamp-gated forks. Contains **zero** XDC-specific code.

So "XDC 2.7.0 vs geth 1.17.3" is really *"XDC's production node" vs "where upstream Ethereum infrastructure is today."* The gap between the two is exactly the chasm the [`reset/integration-1.17.3`](./README.md#3-the-methodology-that-worked-reset-and-replay-in-bundles) port crosses.

---

## 3. Side-by-side: XDC 2.7.0 vs geth 1.17.3

### Identity & lineage

| Dimension | XDC 2.7.0 (XDPoSChain) | Geth 1.17.3 |
|---|---|---|
| Module | `github.com/XinFinOrg/XDPoSChain` | `github.com/ethereum/go-ethereum` |
| Version string | `2.7.0-beta` | `1.17.3-stable` |
| Nature | Hard fork of go-ethereum (~1.8 era) | Upstream original |
| Networking lineage | ~2018 geth (`eth/62`, `eth/63`) | 2025 geth (`eth/69`, `eth/70`) |
| Go toolchain | `go 1.25` | `go 1.24.0` |
| Target network | XDC Mainnet (50) / Apothem (51) | Ethereum mainnet + testnets |

### Consensus & block production

| Dimension | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Consensus | **XDPoS** (v1 round-robin → v2 HotStuff-style BFT) | **PoS / The Merge** (validated, not produced, by the EL) |
| Block production | **Internal** — `miner/` + masternodes seal blocks | **External** — CL builds/finalizes via Engine API |
| Finality | BFT QC/TC, 3-chain commit (v2) | Casper-FFG via the beacon chain |
| Validator set | **108 masternodes**, on-chain via system contracts; epoch = 900 blocks | Beacon-chain validators (off the EL) |
| Block time | **~2 s** | ~12 s (CL slot) |
| Merge machinery (TTD) | **Absent** (pre-Merge by design) | Present (TTD, `mergeNetsplitBlock`) |

### EVM & execution semantics

| Dimension | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Header fields | **+ `Validators`, `Validator`, `Penalties`** (`gencodec:"required"`) | Standard (no XDPoS fields) |
| Fork coverage | Constantinople → … → **Shanghai, Cancun**, all **block-gated** | Through Cancun/Prague, **timestamp-gated** post-Merge |
| Fork activation | XDC bundles EIP-2929/3529/2565/3860 at **`Eip1559Block`**; **EIP-2028 off** | Standard Ethereum schedule |
| EIP-1559 base fee | **Fixed** (`12.5 gwei`, non-market) | **Dynamic** (market formula) |
| EIP-4844 blobs | **No** | **Yes** (blob txs, blob pool, KZG) |
| Special transactions | `0x89`/`0x92`/`0x93` zero-gas, minimal-log path | none |
| Big balances | `BigBalance` (>2²⁵⁶) overflow path | `uint256` only |

### State storage & database

| Dimension | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| State scheme | **Hash-based MPT** (`trie/triedb/hashdb`, no `pathdb`) | **PBSS path-based** (default) + hash scheme |
| Flat state snapshot | **None** (`core/state/snapshot` absent) | Present (snapshot/flat layer) |
| Ancients / freezer | **None** ❌ — runs `nofreezedb` (no cold/hot split) | Present + **era1 files** |
| DB backend | **LevelDB-era** (no `ethdb/pebble`) | **Pebble** (default) + LevelDB |
| State pruning | Limited (no online prune; resync to reclaim) | Path-scheme history window (~90k blocks), online pruning |

### Sync & networking

| Dimension | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Sync modes | **Fast + Full** (no snap) | **Snap** (default) + Full + **beacon sync** |
| eth wire versions | `eth/62`, `eth/63`, **`xdpos2`(100)** | `eth/69`, `eth/70` |
| ForkID (EIP-2124) | **No** | Yes (handshake fork filtering) |
| Request IDs (`eth/66`) | **No** (responses unmatched to requests) | Yes (multiplexed req/resp) |
| Discovery | XDC ping (packet **type 5**) | standard discv4 / discv5 |
| BFT gossip | Vote/Timeout/SyncInfo (`0xe0/e1/e2`) over wire | none |
| txpool | `core/txpool` package (no blob pool) | `core/txpool` **+ blobpool** |

### RPC & tooling

| Dimension | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Custom RPC | **`XDPoS_*`** namespace + `XDC`/`XDCx` modules | none |
| `eth_getBlock*` | includes `validators`/`validator`/`penalties` | standard fields only |
| Engine API | not used (no CL) | core to operation (CL ↔ EL) |
| Addressing | accepts **`xdc…`** address prefix | `0x…` only |

---

## 4. The 7-year improvement timeline (1.8.3 → 1.17.3)

Every version below is the **first stable go-ethereum release tag that shipped the feature**, verified against upstream git history: introducing commit → earliest `v1.x.y` tag containing it. (Dates ≤ v1.13 are real-world release dates; v1.16/v1.17 reflect this repo's history.)

| First release | Date | Landmark improvement (introducing commit) | In XDC 2.7.0? |
|---|---|---|---|
| `v1.8.x` | 2018 | The base: `eth/62`–`63`, fast-sync, LevelDB, hash-trie state | — (this *is* the base) |
| **`v1.9.0`** | 2019-07 | **Freezer / ancients** (immutable data out of the KV store); **ForkID** EIP-2124 / `eth/64` (`983f923`) | freezer ❌ · ForkID ❌ |
| **`v1.9.13`** | 2020-02 | **State snapshot** — the flat read-acceleration layer (`542df88`) | ❌ |
| **`v1.10.0`** | 2021-03 | **Snap sync** + `snap/1` (`017831d`); **offline state pruning** (`f566dd3`); **`eth/66` request IDs** (`6ec1561`) | ❌ |
| **`v1.10.3`** | 2021-04 | **Engine API** (catalyst) — the Merge groundwork (`f79cce5`) | ❌ |
| **`v1.10.4`** | 2021-05 | **EIP-1559** implementation (`94451c2`); London mainnet activation Aug 2021 (`v1.10.8`) | ✅ but **fixed** base fee |
| **`v1.10.19`** | 2022 | **`eth/67`** — drops `GetNodeData` (snap replaces it) | ❌ |
| **`v1.10.22`** | 2022-09 | **The Merge** — mainnet TTD, PoS; internal PoW mining retired | ❌ (XDPoS instead) |
| **`v1.11.0`** | 2023-02 | **Pebble** DB backend (`ed51b8c`); **`eth/68`** (`b0d4433`) | ❌ |
| **`v1.12.1`** | 2023-08 | **txpool subpool** refactor (`d40a255`); **blobpool** / EIP-4844 code (`1662228`); PBSS code merged & present in this tag (`7de748d`) | ❌ |
| **`v1.13.0`** | 2023-09 | **Path-based state storage (PBSS) officially shipped** — online pruning; default for new full nodes in **`v1.14.0`** | ❌ |
| **`v1.13.12`** | 2024-02 | **Era files** — history export, EIP-4444 path (`1f50aa7`) | ❌ |
| **`v1.16.0`** | 2025 | **`eth/69`** (`7e79254`) | ❌ |
| **`v1.17.3`** | 2026 (base) | **`eth/70`** (`965bd6b`); Verkle/UBT + block-access-list (EIP-7928) groundwork | ❌ |

> **Code-ship vs feature-ship (corrected against EF release blogs — see [`EVIDENCE-BASE.md`](./EVIDENCE-BASE.md)):** the path-based state-scheme PR (#25963) merged Aug 2023 and is *present in the `v1.12.1` tag*, but geth **officially shipped PBSS in `v1.13.0`** (2023-09) and made it the **default for new full nodes in `v1.14.0`**. Likewise the **blobpool** code landed in `v1.12.1` while EIP-4844 blobs *activated* on mainnet later (Dencun, 2024). The lesson: "code present in tag X" ≠ "feature shipped in X" ≠ "activated on mainnet in X" — cite **v1.13.0 / v1.14.0** for PBSS.

**The honest ledger:** XDC 2.7.0 modernized the *toolchain and EVM rules* (Go 1.25, forks→Cancun, `core/txpool` package, `trie/triedb/hashdb` layout) but kept **2018-era networking** (`eth/63`, no ForkID, no request IDs) and **2018-era state/sync** (hash-trie, no snapshot, no snap-sync, no PBSS, **no functional freezer** — `nofreezedb`, LevelDB) — and is currently *patching fast-sync* (the `fixfastsync-new` branch) rather than adopting snap.

---

## 5. Area-by-area deep dive

### 5.1 State storage — the headline (snapshot `v1.9.13` → PBSS shipped `v1.13.0`, default `v1.14.0`)

This single area explains most of the 2.5 TB / 800 GB numbers.

| | XDC 2.7.0 (geth ~1.8) | Geth 1.17.3 |
|---|---|---|
| Trie node keying | **Hash-based** (node keyed by its hash) | **Path-based (PBSS)** default; hash still available |
| Read path | Walk the MPT every access | **Flat snapshot** → O(1) account/storage reads |
| Dead-node handling | Accumulates; needs GC / offline prune | **Online pruning**, bounded state-history window (~90k blocks) |
| Full-node growth | **Unbounded** — state portion drifts up forever | **Bounded** — live state ≈ current state, stays ~flat |

**Why hash-scheme grows and PBSS doesn't:** every state write creates new trie nodes and orphans old ones. In the hash scheme those orphans pile up in the KV store unless you run offline pruning — which itself *requires the snapshot layer XDC doesn't have*. So XDC full nodes bloat over time and operators must periodically **resync**. PBSS keys nodes by path (one live node per path + a bounded history segment) and prunes history **online**, so the state portion stops growing. **This is the #1 reason to move XDC onto a 1.17.3 base.**

### 5.2 Chain-data layout — freezer (`v1.9.0`) & era files (`v1.13.12`)

| | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Hot KV vs cold data | **No freezer** ❌ — `nofreezedb`; blocks/receipts stay in LevelDB | Freezer + **era1 files** (exportable, compressible history; EIP-4444 path) |
| History expiry | No | Era files let nodes **offload / drop ancient history** entirely |

Correcting an earlier assumption (verified against the live codebase): XDPoSChain has **no functional freezer** — it runs geth's `nofreezedb` path (ancient reads return `errNotSupported`; `core/rawdb/freezer_table.go` is a 34-line stub; no freezer is ever opened). So **all blocks and receipts sit in LevelDB alongside state** — there is no cold/hot split at all, and no **era / EIP-4444** offload. Both the freezer (geth v1.9.0) and era files are modern-geth wins XDC still lacks.

### 5.3 Database engine — LevelDB → Pebble (added `v1.11.0`, default `v1.12.0`)

| | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Engine | **LevelDB** (Go port, single-threaded compaction) | **Pebble** (default; concurrent compaction, better write-amp at TB scale) |

At multi-hundred-GB sizes LevelDB compaction stalls hurt write throughput during sync and under load; Pebble (built by CockroachDB) was designed for exactly this regime.

### 5.4 Sync — fast → snap + healing (`snap/1` `v1.10.0`, default `v1.10.4`, fast-sync removed `v1.10.14`)

| | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Initial sync | **Fast sync** — fetch every state trie node by hash (`GetNodeData`), millions of tiny round-trips | **Snap sync** — fetch contiguous account/storage **ranges** from the snapshot, then **heal** the trie |
| Speed | Days; bottlenecked on latency & node-by-node retrieval | Hours; bandwidth-bound, far fewer round-trips |
| Pre-req | — | Needs the snapshot layer (which is why XDC can't do it today) |

This is exactly the friction the port hit: the XDC fleet serves no snapshot index, so `reset/integration-1.17.3` falls back to a custom continuous full-sync driver. A modern base + a snapshot-serving fleet would unlock snap sync.

### 5.5 Wire protocol — eth/63 → eth/70

| Capability | XDC 2.7.0 (`eth/62/63` + `xdpos2`) | Geth 1.17.3 — first shipped |
|---|---|---|
| **ForkID** (EIP-2124) | ❌ — can't pre-filter wrong-fork peers | ✅ `eth/64`, **`v1.9.0`** |
| Tx hash announcement | full-tx flooding | `eth/65`, **`v1.9.x`** |
| **Request IDs** | ❌ — responses can't be matched to requests | ✅ `eth/66`, **`v1.10.0`** |
| GetNodeData | present (fast-sync) | removed in `eth/67`, **`v1.10.19`** (snap replaces it) |
| Tx type+size announce | — | `eth/68`, **`v1.11.0`** → `eth/69` `v1.16.0` → `eth/70` `v1.17.3` |

The **missing request IDs** is the root of the gnarliest part of the port (the "no-RequestId bridge" + FIFO re-anchoring) — modern geth peers assume `eth/66+` semantics. And there is a **load-bearing link to storage**: `eth/67` *dropping* `GetNodeData` (EIP-4938) was the **prerequisite for PBSS** — a node that still serves raw trie nodes by hash cannot move to path-based keys. XDC 2.7.0 still serves `GetNodeData`, so the wire protocol must evolve *before* the storage scheme can. (Verified — see [`EVIDENCE-BASE.md`](./EVIDENCE-BASE.md) §8.)

### 5.6 Transaction pool & EVM (subpool + blobpool `v1.12.1`)

| | XDC 2.7.0 | Geth 1.17.3 |
|---|---|---|
| Txpool | `core/txpool` package ✅ (backported), legacy subpool only | **Subpool arch** + **blobpool** (EIP-4844) |
| Tx types | legacy + (some) typed | EIP-2718 typed: access-list, dynamic-fee, **blob (4844)**, **set-code (7702)** |
| Fee market | EIP-1559 header with **fixed 12.5 gwei** base fee | dynamic base fee + blob fee market |
| Precompiles/EIPs | XDC bundles 2929/3529/2565/3860 at `Eip1559Block`; 2028 off | standard schedule, KZG point-eval, BLS (Prague) |

### 5.7 Execution internals

Since 1.8, upstream added a **trie prefetcher** (warm the trie concurrently with EVM execution), **parallel state hashing** in `IntermediateRoot`, **code/state separation** (code dedup by hash), **stacktrie** for fast root computation during sync, structured **slog/log-v3**, richer metrics, and the **stateless/Verkle (UBT) + block-access-list (EIP-7928)** groundwork. XDC 2.7.0 runs the 2018 single-threaded paths.

### 5.8 Consensus integration — the one XDC keeps deliberately

Geth removed internal PoW mining at the Merge (`v1.10.22`) and now *executes* blocks an external consensus layer produces (Engine API, `v1.10.3`). **XDC must NOT adopt this** — XDPoS *is* XDC's consensus and produces blocks internally via `miner/` + masternodes. This is the single area where "newer" isn't the target; the port keeps XDC's block-production model and just hosts it on modern execution/storage.

---

## 6. The disk economics (2.5 TB archive / 800 GB full)

Where the bytes actually live, and how each improvement moves them:

```
                          XDPoSChain full node (~800 GB)          Geth-1.17.3-class full node
  ┌─ Blocks + receipts ─ in LevelDB (NO freezer · nofreezedb) ──  Ancients freezer (flat files; era offload)
  ├─ Live state (hash) ─ GROWS unbounded (no online prune)    ──  BOUNDED by PBSS (~current state)
  └─ DB engine         ─ LevelDB only                         ──  Pebble (better at TB scale)
```

- **Full node (~800 GB, rising):** most of the *growth* is dead trie nodes in the hash-scheme state that can't be pruned online (no snapshot → no `prune-state`). Operators resync periodically to reclaim it. **On PBSS the state portion stabilizes** — the node stops bloating and the ancients dominate (large but immutable). Net: a 1.17.3-class XDC full node would be **smaller and, more importantly, stop growing**, with no resync ritual.
- **Archive (~2.5 TB):** hash-scheme archive keeps *every* trie node ever written — inherently huge, and access is slow (trie walks). A **PBSS path-scheme archive + era files** stores history more compactly and reads it flat; archive stays large but grows slower and queries faster.
- **Sync time:** fast-sync (XDC today) is latency-bound and slow at these state sizes; **snap sync** would cut a from-scratch sync from days to hours — *if* the fleet serves snapshots.

**Measured (field reference).** On **xdc01** — this branch (`02ebae0d`) running XDC **mainnet**, geth 1.17.3-stable, PBSS full node, synced to head (~103.3M, 24 peers, `syncing=false`): **303 GB total = 281 GB freezer (immutable) + ~22 GB live PBSS state**. That **~22 GB live state is the PBSS thesis confirmed in production** — bounded by online pruning, while the freezer holds the compressible immutable bulk.

**Measured side-by-side** (both over IPC, same chain head ~103.31M): the canonical legacy **XDPoSChain v2.6.8** node holds **919 GB in one LevelDB** (no freezer, no online pruning) and serves warm RPC reads **~2–6× slower** (`getStorageAt` 1.87 ms vs 0.30 ms; `getCode` 4.83 ms vs 1.60 ms; `getBalance` 1.63 ms vs 0.53 ms). Same consensus, same head — the 1.17.3 base is **~3× smaller (303 GB vs 919 GB)** with bounded hot state. *(An earlier "~100× faster RPC" estimate was not supported once the legacy node was measured directly; the real, measured figure is ~2–6× for warm reads.)*

---

## 7. What this means for the XDC port

The `reset/integration-1.17.3` branch puts XDC's consensus and economic rules onto modern storage/sync — the pristine geth v1.17.3 base catalogued above — **without changing the rules themselves**.

That reframing explains why the *hard* parts of the port are exactly the modernized layers:

- **State storage** — hash→PBSS, the `BigBalance` RLP overflow path, byte-exact account leaves.
- **Sync** — fast→snap/beacon, the two-pivot model, bridging `xdpos2` requests onto `eth/70` peers that expect request IDs.
- **Pre-Merge → post-Merge networking** impedance mismatch (no ForkID, no request IDs, a continuous syncer instead of beacon idle).

…and why the **consensus engine itself copies over fairly cleanly** — it's self-contained and version-agnostic. The full step-by-step is in [`README.md`](./README.md); the consensus-parity invariants that must survive the move are in [README §8](./README.md#8-consensus-parity-invariants--what-must-be-bit-exact).

---

## 8. References

- [`README.md`](./README.md) — the reproduction guide (the *how*).
- [`EVIDENCE-BASE.md`](./EVIDENCE-BASE.md) — externally-sourced, adversarially-verified citations for every claim in this doc (the version numbers in §4 are cross-checked there).
- `docs/port/XDC_PORTING_PLAYBOOK.html` — the original 14-section playbook.
- `docs/RESET_BUNDLES.md`, `docs/port/MENTAL_MODEL.md` — methodology + the sync war story.
- Canonical clients (read-only, the spec): `XDPoSChain` (v2.7.0-beta), `XDCIndia/go-ethereum`.
- Upstream: <https://github.com/ethereum/go-ethereum> — feature→release attributions in §4 were derived from this repo's tag history (`git tag --contains <introducing-commit>`, earliest stable `v1.x.y`).

---

*Verified against local checkouts of XDPoSChain (v2.7.0-beta) and pristine geth v1.17.3 (`117e067f`). Feature→release attributions in §4 verified against upstream go-ethereum git history (introducing commit → earliest containing stable tag). Mainnet *activation* milestones (London, the Merge, Dencun) are network events distinct from the code-shipping release noted here.*
