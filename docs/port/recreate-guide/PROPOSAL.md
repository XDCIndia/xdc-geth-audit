# `reset/integration-1.17.3` — Landing XDPoS on go-ethereum v1.17.3

**A clean re-baseline of XDC's XDPoS consensus onto pristine geth v1.17.3 — what the branch is, what it inherits, and what's left to land it. Bit-exact consensus throughout.**

> **Status:** DRAFT for review · **Date:** 2026-06-01 · **Prepared for:** XDC Core / XinFin engineering & leadership
> **Related:** [PR #827](https://github.com/XDCIndia/go-ethereum/pull/827) · [`README.md`](./README.md) (reproduction guide) · [`WHY-MODERN-GETH.md`](./WHY-MODERN-GETH.md) (engineering rationale)
> **Asks:** keep the consensus-parity harness as the mandatory release gate; resource the remaining validation (mainnet + RPC parity) to land the `reset/integration-1.17.3` baseline.

---

## Table of contents

1. [Executive summary](#1-executive-summary)
2. [Current pain points](#2-current-pain-points)
3. [Root cause](#3-root-cause)
4. [Proposed solution](#4-proposed-solution)
5. [Pain → remedy mapping](#5-pain--remedy-mapping)
6. [What does NOT change](#6-what-does-not-change)
7. [Why this is low-risk: proof points](#7-why-this-is-low-risk-proof-points)
8. [Approach & methodology](#8-approach--methodology)
9. [Phased plan & milestones](#9-phased-plan--milestones)
10. [Risks & mitigations](#10-risks--mitigations)
11. [Success criteria](#11-success-criteria)
12. [Effort & timeline](#12-effort--timeline)
13. [Recommendation & decision requested](#13-recommendation--decision-requested)
14. [Appendix](#14-appendix)

---

## 1. Executive summary

**Context.** `reset/integration-1.17.3` is a clean re-baseline of XDC's XDPoS consensus onto **pristine upstream geth v1.17.3** (commit `117e067f`). It was created (issue #740) to replace a long-lived pre-1.17 overlay that had accumulated state-divergence drift — replaying the XDPoS overlay onto a known-good upstream release rather than continuing to patch the old base.

**Why it matters.** Building on pristine v1.17.3 means the branch inherits modern geth **wholesale** — **PBSS** (bounded, self-pruning state), the **freezer**, **Pebble**, **snap sync**, and the **modern wire protocol** — instead of back-porting each feature. The diff is fresh and auditable against a known release, and the branch stays **within one minor of upstream**.

**Status.** A live node (**xdc01**, commit `02ebae0d`) runs the branch on **XDC mainnet, synced to head** — block ~103.3M, `syncing=false`, 24 peers, **zero consensus divergence** — on PBSS + Pebble + an active freezer. On disk: **303 GB total = 281 GB freezer + ~22 GB live PBSS state** (bounded). XDPoS consensus, block production, economics, and block hashes remain **bit-for-bit identical** to canonical XDC (harness-gated); the methodology and parity invariants are fully documented (PR #827). **Remaining work is validation — formal fork-boundary parity via the harness and RPC parity — then landing the baseline.**

---

## 2. What the v1.17.3 base resolves

*These are the costs of the long-lived pre-1.17 overlay (and of the legacy geth-1.8-era XDPoSChain client) that building on a modern base eliminates — included for contrast, not as a description of XDC's current multi-client fleet.*

| # | Pain point | Evidence | Who it hurts |
|---|---|---|---|
| **P1** | **Full nodes grow unbounded; periodic resync required** | ~800 GB and rising. Hash-scheme trie accumulates dead nodes; **no online pruning** (XDC has no snapshot layer → no `prune-state`). | Every node operator, masternodes, RPC providers — rising disk cost + recurring downtime |
| **P2** | **Initial sync takes days** | Fast-sync only (no snap). State is fetched **node-by-node by hash** — millions of latency-bound round-trips. | New masternode / RPC bring-up; disaster recovery; onboarding |
| **P3** | **Archive nodes ~2.5 TB and slow to query** | Hash-scheme archive keeps **every trie node ever written**; reads walk the trie. | Explorers, indexers, analytics, RPC providers — cost + latency |
| **P4** | **Fragile peering / aging networking** | `eth/62`–`63`, **no ForkID**, **no request IDs**; custom bridging is needed to talk to modern peers. | Reliability; engineering maintenance burden; interop |
| **P5** | **Maintenance drift — ~7 years behind upstream** | Base ≈ geth **1.8 (2018)**. The prior overlay accumulated state-divergence bugs, which is what **forced the #740 baseline reset**. | Security backports hard; can't adopt new EIPs; low developer mindshare; risk compounds over time |
| **P6** | **No modern data/history features** | No EIP-4844 blobs; no **era files / EIP-4444** history expiry. | Future scalability optionality; archive footprint |
| **P7** | **Slower execution internals** | Single-threaded 2018 code paths (no trie prefetch, no parallel state hashing, no Pebble). | Throughput headroom under load |

**Net effect:** higher infrastructure cost, slower and more fragile node operations, a shrinking pool of operators willing to run nodes, and an accelerating maintenance/security risk as the gap to upstream widens.

---

## 3. Root cause

> The XDC client is a **hard fork of go-ethereum ~1.8.x**. The entire modernization arc — state storage (snapshot → PBSS), sync (fast → snap), networking (`eth/64`–`70`), and database (LevelDB → Pebble) — happened **upstream** and was never adopted. XDC selectively backported the *toolchain and EVM rules* (Go 1.25, forks through Cancun, the `core/txpool` package, the `trie/triedb/hashdb` layout) but kept **2018-era state, sync, and networking** — including **no functional freezer** (`nofreezedb`) and an actively-patched **fast-sync** (no snap).

The fix is therefore not a feature project — it is a **re-platforming**: move XDPoS onto a base where these capabilities already exist, battle-tested, on Ethereum mainnet.

---

## 4. Proposed solution

Complete the migration of **XDPoS consensus onto upstream go-ethereum v1.17.3** (the `reset/integration-1.17.3` line), preserving **bit-exact consensus parity** with canonical XDC, and ship it as a tagged production release with an operator migration path.

This is **the engine-room swap, not a constitutional change**: XDC keeps its consensus, block production, rewards, and block hashes; it gains modern execution, storage, sync, and networking underneath.

---

## 5. Pain → remedy mapping

| Pain | Remedy (upstream capability) | First shipped | Expected outcome |
|---|---|---|---|
| **P1** unbounded full node, resync ritual | **PBSS** path-based state + **online pruning** | geth `v1.13.0` (default `v1.14.0`) | Full node **stops growing**; bounded steady-state; **no more resyncs** |
| **P2** days-long sync | **Flat snapshot** + **snap sync** | `v1.9.13` / `v1.10.0` | Fresh node to head in **hours** (fleet snapshot-serving permitting) |
| **P3** huge, slow archive | **PBSS path-scheme archive** + **era files** | `v1.13.0` / `v1.13.12` | Smaller, **flat (O(1)) reads**; slower growth |
| **P4** fragile peering | **`eth/66+` request IDs** + **ForkID** (+ `eth/70`) | `v1.9.0` / `v1.10.0` | Robust peering; **removes the custom no-RequestId bridge** |
| **P5** maintenance drift | **Within one minor of upstream** | ongoing | Security fixes & new EIPs land cleanly; developer mindshare; rot escaped |
| **P6** no modern data features | **blobpool / EIP-4844**, **era / EIP-4444** | `v1.12.1` / `v1.13.12` | Optionality for blobs + history offloading when XDC wants them |
| **P7** slow internals | **trie prefetch, parallel hashing, Pebble** | `v1.10`–`v1.11` | Throughput headroom |

*(Version attributions verified against upstream git history — see [`WHY-MODERN-GETH.md` §4](./WHY-MODERN-GETH.md#4-the-7-year-improvement-timeline-183--1173).)*

---

## 6. What does NOT change

This is the most important section for risk assessment. The migration is **consensus-neutral by construction**:

- ✅ **XDPoS consensus** — v1 (round-robin) and v2 (HotStuff-style BFT), QC/TC, epochs, masternode selection — **unchanged**.
- ✅ **Internal block production** — masternodes seal blocks via XDC's `miner`/engine path. We **do not** adopt geth's external-CL/Engine-API model.
- ✅ **Block & header RLP and hashes** — `Validators`/`Validator`/`Penalties` header fields, byte-exact encoding, genesis hashes — **unchanged**.
- ✅ **Economics** — fixed 12.5 gwei base fee (no burn), TRC21 fee routing, rewards (90/10), penalties — **unchanged**.
- ✅ **Chain identity** — chain IDs (50/51), genesis, fork schedule, system contracts (`0x88`/`0x89`/`0x90`) — **unchanged**.

For any block, the migrated node must produce an **identical post-state root, receipts root, and block hash** to canonical XDC. This is a hard, enforced constraint (§10, §11), not an aspiration.

---

## 7. Why this is low-risk: proof points

This is **not a greenfield bet** — the approach is already validated:

- **The branch exists and builds.** `reset/integration-1.17.3` carries the full XDC overlay (243 commits) on pristine geth v1.17.3.
- **Live mainnet sync verified.** The branch runs on **XDC mainnet on xdc01, synced to head** (block ~103.3M, `syncing=false`, 24 peers) with **zero `BAD BLOCK` / merkle-root mismatches** — measured directly on the node, not just Apothem.
- **Measured head-to-head** (both IPC, same chain head, vs the canonical legacy **XDPoSChain v2.6.8** node): 1.17.3 is **~3× smaller on disk** (303 GB vs 919 GB), keeps **~22 GB hot state** vs one growing 919 GB LevelDB, and serves warm RPC reads **~2–6× faster** — same XDPoS consensus. (See `EVIDENCE-BASE.md` for the full table.)
- **The hard parts are mapped.** The 58 surgical integration points, the consensus-parity invariants, and the version-agnostic re-port methodology are **fully documented** ([`README.md`](./README.md)).
- **The methodology is proven.** Reset-and-replay in logical bundles + a consensus-parity harness is already in use and produced the clean branch.

The remaining work is **completion and productionization**, not discovery.

---

## 8. Approach & methodology

1. **Pinned base.** Build on a frozen upstream tag (v1.17.3); catch up to newer upstream only after parity holds.
2. **Reset-and-replay in bundles.** The XDC overlay is replayed as logical bundles (config/genesis → V1 engine → header plumbing → bulk-sync → V2 engine → …), each built, tested, and merged independently, foundation-first.
3. **Consensus-parity harness as the gate.** Every consensus-touching change is validated to produce bit-exact state roots / receipts roots / block hashes vs canonical XDC, across normal blocks **and fork-boundary blocks** (V2 switch, EIP-1559 activation).
4. **Staged rollout behind the "inertness window."** Most ported changes activate only at specific fork heights, so a node can sync the early chain on the current binary and switch to the new binary before the relevant fork — enabling low-risk, incremental production validation.

---

## 9. Phased plan & milestones

| Phase | Milestones | Status | Exit criteria |
|---|---|---|---|
| **1 — Foundation & sync** | Chain recognition, engine, **block & state parity**, mainnet sync (M1–M6) | **Done** — xdc01 on **mainnet, synced to head** (~103.3M, zero divergence) | genesis→head, zero BAD BLOCK |
| **2 — Mainnet & RPC parity** | XDC **mainnet sync** (M6), **RPC parity** (M7) | In progress / next | Mainnet genesis→head parity; 100% RPC parity vs geth-XDC |
| **3 — Productionize & release** | Operator docs, **migration guide from geth-XDC**, staged rollout (canary → fresh-wipe validation → soak → mainnet), **tagged release** | Planned | External operator runs a node from docs; production soak clean |
| **4 — Ecosystem (optional)** | Masternode block production on new client (M8); **snapshot-serving fleet** to unlock snap sync network-wide | Optional / future | Mixed-client masternode network produces accepted blocks; snap sync available |

---

## 10. Risks & mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| **Consensus parity divergence** | **P0** | Parity harness on every consensus-touching change; bit-exact gates on fork boundaries; staged rollout behind the inertness window; treat any mismatch as stop-the-line |
| Snap sync needs the fleet to **serve snapshots** | Medium | Interim continuous full-sync driver already works; enable snap incrementally as the fleet upgrades (Phase 4) |
| **Operator migration friction** | Medium | Migration guide; parallel-run period; preserve data compatibility where feasible; clear rollback runbook |
| **Scope creep** into consensus "improvements" | Medium | Hard rule: **re-platform only, no consensus changes**; ADRs for any deviation; parity harness blocks accidental drift |
| Upstream **churn** during the effort | Low | Freeze on the pinned v1.17.3 base; catch up to newer upstream only after parity is green |

---

## 11. Success criteria

The migration is **done** when:

1. **Bit-exact consensus parity** — Apothem **and** mainnet sync genesis→head with post-state root + receipts root + block hash matching canonical XDC; **zero `BAD BLOCK`** records across the full range, including fork boundaries.
2. **Bounded full node** — with PBSS, the state portion stops growing; steady-state disk is bounded; **no resync required**.
3. **Fast sync** — a fresh node reaches head materially faster than today (target: hours via snap/continuous sync).
4. **100% RPC parity** — every documented XDC RPC method matches geth-XDC in shape and value (`XDPoS_*`, XDC `eth_*` extensions, block fields).
5. **Upstream proximity** — within one minor version of upstream geth, with parity still passing.
6. **Operability** — an external operator can follow the docs and run a syncing node; a clean migration path exists from the current client.

---

## 12. Effort & timeline

*Order-of-magnitude estimate, to be validated against parity-harness results and fleet readiness — not a commitment.*

- **Phase 2 (mainnet + RPC parity):** weeks — dominated by mainnet parity validation across fork boundaries and closing the documented RPC gaps (stale `gen_header_json.go`, namespace casing, `EventAPI`).
- **Phase 3 (productionize + release):** weeks — operator docs, migration guide, staged rollout + soak, tagged release.
- **To production v1.0:** on the order of **a quarter**, contingent on parity results and fleet upgrade cadence.
- **Phase 4 (optional):** separate workstream; masternode block production requires its own design review before implementation.

The dominant cost is **validation rigor** (parity), not net-new feature development — most of the build is complete.

---

## 13. Recommendation & decision requested

**Recommendation: land it.** The branch is built and de-risked — **xdc01 runs it on XDC mainnet, synced to head** (~103.3M, `syncing=false`, 24 peers, zero divergence) — and it changes nothing at the consensus layer. What remains is validation, not discovery.

**Asks:**

1. **Approve** taking `reset/integration-1.17.3` to a tagged production release.
2. **Adopt the consensus-parity harness as the mandatory release gate.**
3. **Resource Phases 2–3** (mainnet + RPC parity, then productionization & rollout).
4. **Defer Phase 4** (masternode production, snapshot-serving fleet) to a follow-on decision after Phase 3 soak.

---

## 14. Appendix

### Verified feature → first-upstream-release map
freezer + ForkID `v1.9.0` · snapshot `v1.9.13` · snap sync + offline pruner + `eth/66` `v1.10.0` (snap default `v1.10.4`) · Engine API `v1.10.3` · EIP-1559 `v1.10.4` · `eth/67` `v1.10.19` · Merge TTD `v1.10.22` · Pebble `v1.11.0` (default `v1.12.0`) + `eth/68` · subpool + blobpool code `v1.12.1` · **PBSS shipped `v1.13.0` (default `v1.14.0`)** · era files `v1.13.12` · `eth/69` `v1.16.0` · `eth/70` `v1.17.3`. *(Code-ship vs feature-ship reconciled against EF release blogs — see [`EVIDENCE-BASE.md`](./EVIDENCE-BASE.md).)*

### Companion documents
- [`README.md`](./README.md) — the version-agnostic reproduction guide (the *how*).
- [`WHY-MODERN-GETH.md`](./WHY-MODERN-GETH.md) — the engineering rationale & disk economics (the *why*).
- [`EVIDENCE-BASE.md`](./EVIDENCE-BASE.md) — externally-sourced, adversarially-verified citations (geth release blogs, EIPs, devp2p spec) for the technical claims in this proposal.
- [PR #827](https://github.com/XDCIndia/go-ethereum/pull/827) — the documentation set this proposal accompanies.

### Glossary
- **PBSS** — path-based state storage; trie nodes keyed by path with a bounded, self-pruning state-history window.
- **Snapshot** — flat account/storage acceleration layer enabling O(1) reads and snap sync.
- **Snap sync** — range-based state download + trie healing (vs. node-by-node fast sync).
- **Parity** — bit-exact post-state root / receipts root / block hash equality vs canonical XDC.
- **Inertness window** — the span before a fork's activation height during which ported changes are dormant, enabling staged rollout.

---

*DRAFT prepared 2026-06-01 for XDC Core review. Pain points and remedies are grounded in verified analysis of the local `XDPoSChain` (v2.7.0-beta) and `XDCIndia/go-ethereum` `reset/integration-1.17.3` checkouts. Can be reframed as an XDCIP or condensed into a one-page leadership memo on request.*
