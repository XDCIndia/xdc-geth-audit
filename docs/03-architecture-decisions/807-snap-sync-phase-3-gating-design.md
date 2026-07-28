# ADR: Snap-Sync Gating + xdcSyncer Handoff (issue #807 Phase 3)

Status: PROPOSED · DESIGN ONLY · 2026-05-25

This ADR specifies the **code change** that enables `--syncmode snap`
to actually do snap-sync, gated on the Phase 1 pivot-selection algorithm
and validated by the Phase 2 archive replay. **Code does not ship until
Phase 2 reports zero false positives.**

## What gets touched (and what does not)

### Touched
- `eth/sync_xdc.go` — add snap-phase state machine; gate full-sync loop
- `eth/handler.go` — flip the WARN added in Phase 0 to a conditional path
- `consensus/XDPoS/engines/engine_v2/snap_pivot.go` — NEW file; pivot
  selection helper implementing ADR-807 (Phase 1) checks 1-6
- `eth/sync_xdc_test.go` — NEW; unit tests for the state machine
- `consensus/XDPoS/engines/engine_v2/snap_pivot_test.go` — NEW; unit
  tests against fixture headers from `tests/testdata/`

### NOT touched
- `eth/downloader/*.go` — zero lines changed (upstream-alignment principle)
- `eth/protocols/snap/*.go` — zero lines (snap subprotocol untouched)
- `consensus/XDPoS/engines/engine_v2/utils.go:160` — `verifyQC` is read
  as-is, not modified

The check by `git diff upstream/release/1.17 -- eth/downloader/` must
return zero lines.

## State machine for xdcSyncer

```
                  ┌──────────────────────────────────────┐
                  │                                      │
   (start, mode!=snap)              (start, mode==snap, pivot found)
                  │                                      │
                  ▼                                      ▼
            ┌──────────────┐                  ┌──────────────────┐
            │   FULL_SYNC  │                  │  SNAP_SELECTING  │
            │ (today's     │                  │  pivot heuristic  │
            │  behavior)   │                  │  runs over peers  │
            └──────┬───────┘                  └────────┬─────────┘
                   │                                   │
                   │              ┌────────────────────┤
                   │              │                    │
                   │       (pivot picked)        (no safe pivot
                   │              │             after MaxCandidates)
                   │              ▼                    │
                   │     ┌────────────────┐            │
                   │     │  SNAP_RUNNING  │            │
                   │     │  downloader.   │            │
                   │     │  SnapSyncer.   │            │
                   │     │  Sync(root)    │            │
                   │     └────────┬───────┘            │
                   │              │                    │
                   │      (snap completes      (snap errors:
                   │       successfully)         pivot stale,
                   │              │              peer dropped,
                   │              │              integrity failed)
                   │              ▼                    │
                   │     ┌────────────────┐            │
                   │     │  TIP_CATCH     │            │
                   │     │  xdcSyncer     │            │
                   │     │  resumes from  │            │
                   │     │  pivot+1       │            │
                   │     └────────┬───────┘            │
                   │              │                    │
                   └──────────────┴────────────────────┘
                                  │
                                  ▼
                          ┌──────────────┐
                          │    AT_TIP    │
                          │  (existing   │
                          │  syncOnce    │
                          │  loop)       │
                          └──────────────┘
```

Transitions:

| From | To | Trigger |
|---|---|---|
| start | FULL_SYNC | `ConfigSyncMode != SnapSync` (current default) |
| start | SNAP_SELECTING | `ConfigSyncMode == SnapSync && peer_count >= 1` |
| SNAP_SELECTING | SNAP_RUNNING | pivot found per ADR-807 Phase 1 |
| SNAP_SELECTING | FULL_SYNC | no safe pivot AND peer_head − local_head < 5000 (close enough to full-sync) |
| SNAP_SELECTING | SNAP_SELECTING | no safe pivot AND retry in 30s |
| SNAP_RUNNING | TIP_CATCH | `downloader.SnapSyncer.Progress().Done` |
| SNAP_RUNNING | FULL_SYNC | error from snap (pivot stale, peer dropped, integrity failed); log + restart from current local head |
| TIP_CATCH | AT_TIP | local head ≥ peer head − 3 |
| FULL_SYNC | AT_TIP | local head ≥ peer head − 3 |

## File-level changes

### `consensus/XDPoS/engines/engine_v2/snap_pivot.go` (NEW)

```go
package engine_v2

import (
    "github.com/ethereum/go-ethereum/consensus"
    "github.com/ethereum/go-ethereum/core/types"
)

const (
    SnapPivotMinDepth       = 12
    SnapPivotMaxCandidates  = 20
    SnapPivotHardforkBuffer = 10
)

// PivotResult is what the syncer needs to start snap-sync.
type PivotResult struct {
    Block  *types.Header
    Root   common.Hash
    Depth  uint64
}

// SelectSnapPivot returns a pivot-safe block per ADR-807 Phase 1 checks
// 1-6, walking down from peerHead in EpochLength steps. Returns nil if
// no candidate within MaxCandidates passes.
//
// Pure read-only over headers; never mutates engine state.
func (x *XDPoS_v2) SelectSnapPivot(
    chain consensus.ChainReader,
    peerHead uint64,
    fetchHeader func(uint64) *types.Header,  // peer round-trip
) *PivotResult {
    // [implementation per ADR-807 Phase 1 pseudocode]
}
```

### `eth/sync_xdc.go` (MODIFIED)

Add a `snapPhase` enum field on `xdcSyncer`:

```go
type xdcSnapPhase int
const (
    phaseFullSync xdcSnapPhase = iota
    phaseSnapSelecting
    phaseSnapRunning
    phaseTipCatch
    phaseAtTip
)

type xdcSyncer struct {
    // ... existing fields ...
    snapPhase atomic.Int32  // xdcSnapPhase; atomic for safe handler reads
    snapPivot *engine_v2.PivotResult
}
```

In `syncOnce()`:

```go
func (s *xdcSyncer) syncOnce() {
    switch xdcSnapPhase(s.snapPhase.Load()) {
    case phaseFullSync, phaseTipCatch, phaseAtTip:
        s.fullSyncOnce()  // existing logic, refactored
    case phaseSnapSelecting:
        s.snapSelectOnce()
    case phaseSnapRunning:
        // wait; downloader.SnapSyncer is running in its own goroutine
        return
    }
}
```

`snapSelectOnce()` invokes `engine.SelectSnapPivot()`. On success, calls
`s.handler.downloader.Synchronise(peer, peerHead, peerHash, ethconfig.SnapSync)`
directly — the downloader already has snap-sync ready to go; we just
trigger it. The downloader emits `downloader.DoneEvent` on completion
which we listen on to advance to `phaseTipCatch`.

### `eth/handler.go` (MODIFIED — flip the Phase 0 WARN)

Replace the unconditional WARN:

```go
if h.downloader.ConfigSyncMode() == ethconfig.SnapSync {
    log.Info("XDC: snap-sync enabled; pivot selection will engage once a " +
        "peer reports head ≥ local head + 5000")
}
h.xdcSync = newXDCSyncer(h)
h.xdcSync.start()
```

The state machine decides at runtime whether to actually engage snap or
fall back to full-sync.

## Failure modes + recovery

| Failure | Detection | Recovery |
|---|---|---|
| Pivot becomes stale during selection (peer head moves) | Check pivot depth still satisfies `peer_head - N >= MIN_DEPTH` before triggering Sync; if not, re-run selection | Re-enter SNAP_SELECTING |
| Peer drops during snap state download | downloader fires error event | Transition to FULL_SYNC; xdcSyncer resumes from current local head |
| Snap-delivered state-root mismatches block N's root | downloader verifies; emits integrity-fail | Transition to FULL_SYNC; **and** taint the offending peer via existing `taintPeer` |
| All peers refuse snap subprotocol | No SnapSyncer.Register fires within 60s | Transition to FULL_SYNC; log INFO with peer cap details |
| TIP_CATCH stalls on ErrUnknownAncestor | Already handled by `maybeRollbackOnUnknownAncestor` (commit `37a6dae58` + `ebc947e9c`) | Existing recovery applies; no change |

## Acceptance criteria for Phase 3

- [ ] Phase 2 reports ≥ 80% pivot-selection rate with zero false positives
- [ ] Unit tests cover all 4 state-machine transitions + 5 failure modes
- [ ] `git diff upstream/release/1.17 -- eth/downloader/` returns zero lines
- [ ] Fresh sync from genesis on Apothem with `--syncmode snap`:
  - enters SNAP_SELECTING within 30s
  - picks pivot within 90s
  - completes SNAP_RUNNING in ≤ 30 min on NVMe + 5 peers
  - completes TIP_CATCH in ≤ 30 min
  - total wall-clock ≤ 2 h
  - no `BAD BLOCK`, no `missing trie node`, no `ErrUnknownAncestor` cascade
- [ ] Live test on a 2-node testnet (one v1.17.0 full-mode peer + one
  v1.17.3 snap-sync test node) reproduces the wall-clock target

## Open questions for Phase 3 implementation

1. Does `downloader.Synchronise()` need any XDC-specific adapter, or does
   the existing snap implementation accept the pivot block hash unchanged?
   To verify: read `eth/downloader/downloader.go:281` (`d.SnapSyncer.Progress`)
   and trace what `Sync(root, cancel)` requires.

2. The TIP_CATCH phase must replay `pivot+1 .. peer_head` correctly,
   including consensus checks. xdcSyncer's `InsertChain` already does
   this in FULL_SYNC. Verify the transition leaves chain state
   consistent (head pointer, snapshot disk layer, masternode cache).

3. If the pivot block's epoch differs from local head's epoch, do we
   need to seed `recents` / masternode-rotation caches before TIP_CATCH?
   `core/blockchain.go:SetHead` already invalidates caches (#802 task 3);
   verify this also applies on the snap → full transition.

## Notes for upstream merge (M9)

When merging future upstream Geth tags:
- The xdcSyncer phase state machine is internal to `eth/sync_xdc.go`;
  upstream changes to `eth/downloader/` propagate cleanly because we
  invoke through public APIs only.
- The pivot-selection helper in `consensus/XDPoS/engines/engine_v2/`
  is XDC-only; no upstream conflict.
- `eth/handler.go` change is minimal (3-4 line difference); if upstream
  refactors the syncer-start sequence, the WARN line moves but the
  state machine entry point stays.

## References

- ADR-807 Phase 1 (`docs/03-architecture-decisions/807-snap-sync-phase-1-pivot-selection.md`)
- Phase 2 script (`scripts/snap-pivot-validate.sh`)
- `eth/sync_xdc.go` current implementation
- `eth/downloader/downloader.go` (read-only reference)
- Issue #807 — tracker
- Issue #806 — bench
- ADR 801 — V2 BFT 3-chain finalization fix (potential prerequisite)
