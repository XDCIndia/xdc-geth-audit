# XDC Snap-Sync — Mental Model

Captures the *two-pivot* model that misled Phase 14 (2026-05-26 incident).

## The two pivots

Snap-sync against the XDC fleet involves **two distinct pivot blocks** that
play different roles. Conflating them caused a 4-hour debug session and
shipped a wrong-direction fix.

| Pivot | Selected by | Code path | Used for |
|-------|-------------|-----------|----------|
| **xdcSyncer pivot** | `eth/sync_xdc.go::fetchPeerHeadHeader` | `peer_head - snapPivotBackoff` (1024) | QC-trusted ancestor for finality + masternode verification (`final` arg to `BeaconSync`) |
| **Downloader pivot** | `eth/downloader/downloader.go::syncWithPeer` | `latest.Number - fsMinFullBlocks` (64) | State-trie download target — this block's `stateRoot` is what `snap.Syncer` asks peers to serve |

Both pivots exist simultaneously. They are **not interchangeable**. The
state download targets the downloader pivot's root, not the xdcSyncer
pivot's root.

## Why the distinction matters for peer retention

Path-scheme nodes retain ~90,000 blocks of state-history. If the
downloader pivot lands more than ~90k blocks below peer head, peers
have legitimately pruned that state and will reject `GetAccountRange`
for it. This is not a peer fault, it's by design.

- ✓ Downloader pivot at `peer_head - 64` → 64 blocks behind, state present.
- ✗ Downloader pivot at `peer_head - 100_000` → 100k blocks behind, state pruned.

`fsMinFullBlocks` controls the downloader's offset from `head`. It does
NOT pin any externally chosen pivot — raising it pushes the downloader's
state-download target *deeper into history*, not closer to xdcSyncer's
selection.

## How they connect via BeaconSync

```
xdcSyncer (sync_xdc.go)             downloader (downloader.go)
─────────────────────────           ────────────────────────────
fetchPeerHeadHeader()
  ├── peer_head fetched
  └── pivot = peer_head - 1024
                  │
                  ▼
BeaconSync(head, final)
  head  = peer_head (NOT xdcSyncer pivot)
  final = pivot.Block (xdcSyncer pivot)
                  │
                  ▼
                                    syncWithPeer()
                                      latest = skeleton.Bounds() = head
                                      d.pivotHeader = skeleton.Header(latest - 64)
                                      ──────────────────────────────────────
                                      processSnapSyncContent()
                                        sync = d.syncState(d.pivotHeader.Root)
                                        snap.Syncer.Sync(this root)
                                          GetAccountRange(root, …) → peers
```

`head` controls the downloader pivot via `latest - fsMinFullBlocks`.
`final` is the QC-trusted point — used by skeleton for finality logic,
not by state download. Pass these two arguments **independently**, even
when their values look similar.

## The Phase 14 misdiagnosis (don't repeat)

**Hypothesis** (Phase 14): "If we raise `fsMinFullBlocks` to 100_000,
the downloader will land on the xdcSyncer-selected pivot, pinning state
download to a known-good block."

**Reality**: `fsMinFullBlocks` is the downloader's own offset from
`head`. Raising it shifted the downloader pivot 100k blocks *backward*
in history. Block 82,233,280 (= 82,333,280 − 100_000) has stateRoot
`62b864..9550cf`, which no path-scheme peer retains. All peers
correctly rejected. The error message ("Peer rejected account range")
read like a peer-fleet readiness issue but was self-inflicted.

**Reproduce check**: query a healthy peer for the block at
`requested_root_height`. If `peer_head - block_number > 90_000`, the
problem is in our pivot offset, not the peer.

## Phase 14.2 design

- Keep `fsMinFullBlocks = 64` (upstream default).
- Pass `peer_head` (the actual chain tip from a snap-capable peer) as
  `BeaconSync(head, ...)` so the downloader pivot lands at
  `peer_head - 64` → well inside retention.
- Pass `xdcSyncer.pivot.Block` as `final` for QC verification.
- Per-peer header fetch capped at 3 attempts × 5s timeout → max 15s
  before falling back to FULL_SYNC if fleet is stalled.

## Phase 14.3 invalidation

When the downloader pivot changes across restarts, the persisted
`SnapshotSyncStatus` may hold tasks/counters from a previous run at a
different root. Tasks themselves are root-agnostic (hash ranges), but a
previous run that completed under a wrong root persists `tasks = nil`,
which makes the next attempt skip account sync entirely and proceed
directly to the heal phase against a root with no account data.

Mitigation: before invoking `BeaconSync`, compare the new downloader
pivot number against the persisted `rawdb.ReadLastPivotNumber`. If they
differ, overwrite `SnapshotSyncStatus` with garbage so
`snap.Syncer.loadSyncStatus` falls through to a fresh task init.

## The rule

> `fsMinFullBlocks` controls the *downloader's* pivot offset from head.
> It is **not** a knob for pinning any other pivot.
> The downloader pivot is **always** `head − fsMinFullBlocks` —
> the only way to relocate it is to relocate `head`.
