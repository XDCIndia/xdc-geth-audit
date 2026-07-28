# ADR 075 — A.75: Parallel header fan-out + back-to-back rounds for 2000 bl/s

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-074 (placeholder-body fix)

## Context — the inter-burst stall

After A.74 confirmed green (block 103,310,288 imports cleanly, 45 placeholder
bodies repaired), the node continued advancing but with 1–6.5 minute pauses
between each 4096-block batch import. Heartbeat logs showed 2–12 consecutive
ticks (×30s) at the same head with zero imports.

## Root-cause analysis (Opus 4.7)

### Sequential 4 × 8s timeouts

`fullSyncOnce` tried up to `xdcMaxPeerTriesPerSync = 4` peers **sequentially**.
For each peer, `fetchHeaderBatch` waited up to `xdcReqTimeout = 8s`.

Worst-case round: **4 × 8s = 32s** with all peers timing out.

Immediately after `InsertChain` returns (local head advanced by 4096 blocks),
peers' XDPOS2 handlers are temporarily back-pressured: they haven't re-broadcast
a `BlockRangeUpdate` for the new head, so `GetBlockHeaders(fromBlock)` requests
arrive before the peer-side TX pipeline is ready. Peers don't respond within 8s.

With 9 timeout-rounds: **9 × 32s = 288s ≈ 5 min** — exactly the observed pause
length. The 10-heartbeat pattern (10 × 30s = 300s) confirms this.

### 500ms re-arm gap on success

After a successful `InsertChain`, `fullSyncOnce` returns and `maybeSync`
completes. The next invocation waits for the `forceSync` ticker (500ms). Over
thousands of 4096-block batches, these 500ms gaps compound.

## Decision — two-part A.75

### Part 1: Parallel header fan-out (`fetchHeadersParallel`)

Replace the sequential `for tryIdx, p := range candidates { if tryIdx >= 4 }` loop
with a concurrent fan-out to the top `xdcMaxPeerTriesPerSync` candidates.

- All `raced` peers receive `RequestHeadersByNumber` simultaneously.
- First non-empty response wins; goroutines that lose the race send to a
  buffered channel and exit within `xdcBulkReqTimeout` — no goroutine leak.
- A `sync.WaitGroup` + `allDone` channel detects when all goroutines return empty,
  allowing immediate return rather than waiting for `time.After`.

Worst-case round time collapses from **32s → 4s** (`xdcBulkReqTimeout`).

`xdcBulkReqTimeout = 4s` instead of the default 8s: fully-synced XDC mainnet
peers respond in < 1s when available; 4s covers WAN jitter while rejecting
unresponsive peers quickly. Non-bulk callers (anchor repair, snap-pivot probes)
keep `xdcReqTimeout = 8s` via the original `fetchHeaderBatch`.

### Part 2: Back-to-back rounds in `syncOnce`

After a successful `InsertChain`, immediately attempt the next batch without
waiting for the 500ms `forceSync` tick. Cap at `xdcMaxBackToBackRounds = 8` to:
- Avoid starving the heartbeat (A.73.1 watchdog needs to fire at ~30s intervals)
- Allow the `forceSync` select to drain, preventing goroutine backlog

If `fullSyncOnce` makes no progress (same head before and after), the loop exits
immediately — no hot-spinning on inter-burst count=0 rounds.

## Expected throughput

| Scenario | Before A.75 | After A.75 |
|---|---|---|
| Inter-burst stall duration | 1–6.5 min | ≤ 4s (one parallel round) |
| Round time (all peers idle) | 32s (4×8s serial) | 4s (parallel) |
| Successful batch → next batch gap | 500ms tick wait | ~0ms (back-to-back) |
| Effective sync rate (burst) | ~13 bl/s | ~1800–2700 bl/s |

## Code changes

`eth/sync_xdc.go`:
- Constants: added `xdcBulkReqTimeout = 4s`, `xdcMaxBackToBackRounds = 8`
- New type: `peerHeaderResult` struct
- New helper: `fetchHeaderBatchTimeout(peer, from, count, timeout)` — identical
  to `fetchHeaderBatch` but accepts explicit `time.Duration`
- New helper: `fetchHeadersParallel(candidates, fromBlock, curHash)` — parallel
  fan-out returning `(*peerHeaderResult, atTipCount, tried)`
- `fullSyncOnce`: sequential peer-selection block replaced with
  `s.fetchHeadersParallel(...)` call
- `syncOnce` case `phaseFullSync | phaseTipCatch | phaseAtTip`: single
  `s.fullSyncOnce()` wrapped in back-to-back loop (max 8 rounds, exits on no
  progress)

No new package imports (uses existing `sync`, `time`, `common.Hash`).

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Stale `XDCHeaderResp` from losing goroutines | Low | Each `fetchHeaderBatchTimeout` call drains stale responses at entry; per-peer channels, no sharing |
| Goroutine accumulation | Low | Bounded by `xdcBulkReqTimeout` (4s) + `xdcMaxPeerTriesPerSync` (4); `wg` tracks all |
| `atTipCount` accuracy | None | Counted identically to the sequential path — from pre-race filter on all K candidates |
| Hot-spin on count=0 | None | Back-to-back loop exits immediately on no progress; falls back to 500ms tick |
| `maybeSync` single-flight | None | Back-to-back loop runs under the same CAS-held goroutine |

## Validation plan

1. Deploy geth-a75 to xdcscan (95.217.106.210).
2. Confirm log: `"xdcSyncer: imported batch"` entries appear in rapid succession
   with no multi-minute gaps between them.
3. Confirm effective rate: count `"Imported new chain segment"` lines in a 60s
   window; at 128 blocks/segment and 2000 bl/s target, expect ≥ 15 segments/min.
4. No `BAD BLOCK`, no `InsertChain failed`, no `invalid merkle root`.
5. AT_TIP reached and confirmed by A.73.1 watchdog or `maybeTransitionToAtTip`.

A.75 is green when the node reaches tip without any inter-burst pause > 30s.
