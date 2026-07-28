# XDC Node Sync Modes — full / fast / snap / archive

A practical guide to the four ways an XDC node can acquire chain + state, how
they differ, and which to run for your use case.

This fork exposes the choice through two flags (see `cmd/utils/flags.go`):

- `--syncmode {full|fast|snap}` — how chain **state** is obtained.
- `--gcmode {full|archive}` — whether historical **state** is pruned or kept.

Both default to `--state.scheme path` (PBSS) on a fresh datadir.

> **TL;DR** — Don't sync from genesis over P2P. Restore a published snapshot
> (`curl -fsSL https://xdc.network/install.sh | bash`) and you're near tip in
> minutes. Run **full** for a validator, snapshot-restored **fast** for a
> general RPC node, **archive** for an explorer/tracing, and **snap** only if
> you're testing the experimental path.

---

## The four node types → flags

| Node type | Flags |
|---|---|
| **full**    | `--syncmode full` (default `--gcmode full`, pruned) |
| **fast**    | `--syncmode fast` |
| **snap**    | `--syncmode snap` **and** env `XDC_EXPERIMENTAL_SNAPSYNC=1` (experimental) |
| **archive** | `--syncmode full --gcmode archive` |

`--gcmode archive` sets `NoPruning=true` — the node keeps the full historical
state trie for every block.

---

## Comparison

| Dimension | **full** | **fast** | **snap** | **archive** |
|---|---|---|---|---|
| **How state is obtained** | Re-execute *every* block from genesis | Download state-at-pivot via **NodeData** (classic geth fast, XDPOS2 — #844) then execute forward | Download state-at-pivot via **snap/1** account/storage ranges + heal (#807), then execute forward | Re-execute every block and **keep all of it** |
| **What's downloaded** | All headers + bodies + receipts; computes state locally | Headers + bodies + receipts + pivot state trie | Headers + pivot state (account/storage ranges + bytecodes) + bodies forward | Same as full |
| **Historical state** (balance / `debug_trace*` at an old block) | ❌ recent only (~last 128 blocks of trie) | ❌ recent only (~90k blocks, PBSS state-history) | ❌ recent only (~90k blocks, PBSS) | ✅ **every block, forever** |
| **Genesis→tip time** (apothem, from scratch over P2P) | **Slowest** — execution-bound (days) | Hours (state download + backfill) | ~35 min state download once engaged (measured on apothem) | Slowest (= full, never prunes) |
| **Disk** (approx restored size, apothem) | moderate | **~25 GB** (thinnest) | ~25–40 GB | **~129 GB+** (mainnet substantially larger) |
| **Can be a validator** | ✅ has complete tip state | ⚠️ only after snap-**heal** at tip (#890 / A.95) | ⚠️ same — heal first | ✅ |
| **Serves snap to peers** | ✅ if PBSS + `--snapshot` on | ⚠️ thin / depends | ✅ if PBSS + `--snapshot` on | ✅ |
| **Maturity on XDC** | ✅ rock-solid (default) | ✅ production (A.96 / A.97, PR #958) | 🧪 **experimental** — validated on apothem (PR #959), double-opt-in, not yet mainnet/default | ✅ rock-solid |
| **Best for** | Safe general node / validator | Fast RPC / observer node | (future) fastest fresh sync | Explorers, tracing, audits, analytics |

*Disk figures are approximate restored sizes observed on apothem snapshots; mainnet is roughly 1.5–2×. Exact on-disk size depends on state scheme and pruning.*

---

## Per-mode detail (XDC-specific)

### full — `--syncmode full`
The safe default. `xdcSyncer` downloads and **executes/validates every block**
from genesis, so it independently verifies the entire chain. Correct but slow
to reach tip on a long chain — throughput is execution-bound (see
`docs/.../SYNC_THROUGHPUT_DIAGNOSIS`). Prunes old state (keeps a recent window),
so it cannot answer historical-state queries.

### fast — `--syncmode fast`
The XDC-restored **NodeData** fast-sync (#844, milestones A.1–A.97). It picks a
pivot — auto-pinned from baked-in `TrustedSyncCheckpoints` (A.96) or a dynamic
peer-head-derived pivot (A.97) — downloads the state trie at that pivot via the
NodeData protocol, and executes only blocks after the pivot. This is what the
published fast snapshots and the apothem-fast node use.

- **Gotcha:** `--snapshot=false` is **silently overridden** in fast mode (#155).
- A fast node is **not validator-ready** until its tip state is healed (see
  `XDC_SNAP_HEAL=1`, #890).

### snap — `--syncmode snap` + `XDC_EXPERIMENTAL_SNAPSYNC=1`
The upstream **snap/1** engine (`GetAccountRange` / `GetStorageRanges` /
`GetByteCodes` + state healing). It is **double-opt-in gated**: it engages only
when *both* `--syncmode snap` and `XDC_EXPERIMENTAL_SNAPSYNC=1` are set —
without the env var it logs a WARN and falls back to FULL_SYNC, so default
operators are never affected.

Validated end-to-end on apothem (PR #959): a fresh node downloaded the full
state (37.66 GB, 324,696 accounts, 155.8M storage slots), committed the pivot,
reached AT_TIP, and produced block hashes **bit-identical** to a canonical
validator. Still **experimental** — one apothem run, not yet mainnet/default.
Two hardening follow-ups are open: #837 (gap-block V2 state for post-pivot epoch
crossings) and #801 (a finality-safe pivot instead of trusting head-64).

### archive — `--syncmode full --gcmode archive`
Executes every block (like full) but **never prunes state** — it retains the
full state trie for every historical block. Required for `debug_traceTransaction`
and `eth_getBalance`/`eth_call` at arbitrary historical blocks. Largest disk
footprint and grows forever.

---

## How to run each

The supported fast path for **any** mode is to restore a published snapshot
(near-tip in minutes) instead of syncing from genesis over P2P:

```bash
# One command: downloads the matching geth binary + latest snapshot, starts a node
curl -fsSL https://xdc.network/install.sh | bash
```

To run a specific mode manually (apothem shown; use `--xdcmainnet` for mainnet):

```bash
# full (default; good for a validator once at tip)
geth --apothem --syncmode full --state.scheme path

# fast (thin RPC/observer node)
geth --apothem --syncmode fast --state.scheme path

# snap (EXPERIMENTAL — both opt-ins required)
XDC_EXPERIMENTAL_SNAPSYNC=1 geth --apothem --syncmode snap --state.scheme path

# archive (explorer / tracing — large disk)
geth --apothem --syncmode full --gcmode archive --state.scheme path

# heal a fast/snap node's tip state so it can validate (#890)
XDC_SNAP_HEAL=1 geth --apothem --syncmode fast --state.scheme path
```

---

## Decision guide

| Your goal | Run |
|---|---|
| General-purpose RPC node, fast to stand up | snapshot-restored **fast** (installer default) |
| Validator / masternode | **full** (or fast/snap **+ `XDC_SNAP_HEAL=1`** to heal tip state) |
| Block explorer, tracing, historical analytics | **archive** (`--gcmode archive`) |
| Maximum trust (verify all history yourself) | **full** or **archive** from genesis |
| Testing the experimental fastest fresh-sync | **snap** (`+ XDC_EXPERIMENTAL_SNAPSYNC=1`) |

---

## References

- #807 — snap-sync tracker; #810 / #836 — snap-sync acceptance criteria
- #894 — dynamic-pivot fast-sync; #958 — fast snapshot tooling (`one.sh` / installer)
- #959 — snap skeleton terminate-floor + anchor prime (makes snap reach state download)
- #890 / A.95 — validator-eligible fast-sync (state heal at tip)
- #155 — `--snapshot=false` silently overridden in fast mode
