# ADR 076 — A.76: Progressive backoff for sustained empty-header rounds

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-075 (parallel fan-out + back-to-back rounds)

## Context — rate-limit amplification during inter-burst stalls

After A.75, the node fires 3–4 consecutive back-to-back batches (128 blocks
each, ~33–56s apart), then stalls for 5–8 minutes before the next burst. The
stall occurs because all connected peers rate-limit header requests after
serving a burst of batches.

### The amplification problem

When all `fetchHeadersParallel` candidates return empty headers quickly (<100ms
per peer, because they refuse without waiting for the 4s `xdcBulkReqTimeout`),
the back-to-back loop exits immediately (no progress), and the 500ms
`forceSync` tick fires the next `maybeSync` 500ms later. The result:

- Each cycle: ~100ms fan-out + 500ms tick wait = ~600ms per cycle
- 8-minute stall = 800 cycles × 4–7 peers = 3,200–5,600 header requests

Continuously hammering rate-limited peers extends the rate-limit window and
prevents recovery, making the stall longer than necessary.

## Decision — progressive backoff in `syncOnce`

Track `emptyRounds atomic.Int64` on `xdcSyncer`. Increment on each no-progress
round; reset to 0 on any successful import. In the back-to-back loop:

| emptyRounds | Additional sleep | Effective cycle time |
|---|---|---|
| 1–10 | 0s | ~600ms (unchanged from A.75) |
| 11–60 | 2s | ~2.6s |
| 61+ | 8s | ~8.6s |

The sleep runs inside the back-to-back goroutine (holding the CAS), preventing
concurrent `maybeSync` invocations from creating parallel request storms.

### Request reduction during 8-minute stall

| Phase | Cycles | Requests (×4 peers) |
|---|---|---|
| Before A.76 | 800 | 3,200 |
| After A.76 | 10 + 96 + 38 ≈ 144 | 576 |

**5.5× reduction** in header requests during rate-limit windows, allowing
peers to recover sooner and reducing average stall duration.

### Why not change `xdcForceSyncCycle`?

The 500ms tick also drives other phase transitions (snap-select, fast-sync
checks). Changing the global tick cadence would affect all phases. The
per-field counter isolates the backoff to the full-sync path only.

### Why not block on the CAS indefinitely?

The 500ms tick + CAS pattern ensures the heartbeat (30s) can fire and observe
`syncing=false` between stalls, keeping the watchdog (A.73.1) alive.
The backoff sleep only extends the hold, not indefinitely — max 8s per round.

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Backoff delays recovery when one peer becomes ready | Low | emptyRounds resets on first progress; back-to-back loop fires immediately on next import |
| Long sleep blocks quit signal | None | `select { case <-time.After(wait): case <-s.quitCh: }` — respects shutdown |
| Counter wrong across phase transitions | Low | emptyRounds is global to syncer; if phase changes (FULL→TIP_CATCH), the counter may still be elevated — acceptable since TIP_CATCH also calls fullSyncOnce |

## Validation

Stall duration should shorten from 5–8 minutes to ≤ 3 minutes as peers
recover from rate-limiting with fewer concurrent requests hitting them.
Effective sync rate should increase (less time wasted in stalls).
