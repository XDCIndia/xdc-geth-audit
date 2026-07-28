# ADR 077 — A.77 + A.78: Parallel lookahead pre-fetch from fresh peers

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-075 (parallel fan-out + back-to-back rounds), ADR-076 (progressive backoff)

## Context — throughput ceiling after A.75 + A.76

After A.75 + A.76, each `fullSyncOnce` call imports one 128-block batch from
the winning peer, then backs off or retries. The back-to-back loop can fire
immediately after `InsertChain` but each next iteration still requires a full
round-trip (header fetch + body fetch + InsertChain) before advancing.

With N peers and a ~60s rate-limit window per peer, the theoretical ceiling is:
- N peers × 128 blocks / 60s ≈ N × 2.1 bl/s
- At 6 peers: ~12.8 bl/s, observed ~5 bl/s (peers staggered, not perfectly
  spread)

## A.77 — Parallel lookahead (initial design)

After `fetchHeadersParallel` returns the winning range-0 batch, immediately
launch goroutines to pre-fetch ranges 1, 2, 3 from OTHER candidates, concurrent
with range-0 body fetch + `InsertChain`. After range-0 completes, collect and
import the pre-fetched lookahead ranges in order.

```
Timeline (before A.77):
  [fan-out]->[bodies-0]->[IC-0]->[fan-out]->[bodies-1]->[IC-1]
                                                                (sequential)

Timeline (after A.77):
  [fan-out]->[bodies-0+lh(1,2,3)]->[IC-0]->[IC-1]->[IC-2]->[IC-3]
             ↑concurrent goroutines↑                          (pipelined)
```

### A.77 design flaw discovered post-deployment

A.77 launched lookahead goroutines for candidates[1..3] — peers that had just
been queried in the `fetchHeadersParallel` fan-out. Those peers returned
`count=0` (rate-limited) for range-0, and immediately after receiving a second
request from the lookahead goroutine, they again returned `count=0`.

No `"xdcSyncer: A.77 lookahead batch"` log lines appeared, confirming that
the lookahead goroutines consistently received empty responses and exited early.

## A.78 — Fix lookahead to use non-raced (fresh) peers

Root cause: the set of peers queried in the parallel fan-out (`racedIDs`) are
rate-limited immediately after the fan-out. The lookahead must use only peers
that were NOT in `racedIDs`.

### Changes

**`fetchHeadersParallel` return signature extended:**

```go
func (s *xdcSyncer) fetchHeadersParallel(...) (*peerHeaderResult, int, int, map[string]bool)
```

The new 4th return value (`racedIDs`) is the set of peer IDs that received
`GetBlockHeaders` requests this round. Empty if no peers were raced (all
at-tip).

**Lookahead launch loop updated:**

```go
// skip the range-0 winner AND any peer already raced this round
if c.ID() == peer.ID() || racedIDs[c.ID()] {
    continue
}
```

This ensures lookahead goroutines only target peers with staggered rate-limit
windows — peers that were not queried this round and can serve ranges 1, 2, 3.

### Effective lookahead peers

| Total peers | Fan-out (top-4) | Fresh for lookahead | Lookahead goroutines |
|---|---|---|---|
| 4 | 4 | 0 | 0 (no-op, safe) |
| 5 | 4 | 1 | 1 |
| 6 | 4 | 2 | 2 |
| 7+ | 4 | 3+ | 3 (capped by xdcLookaheadStreams) |

With 7+ connected peers, all 3 lookahead slots fire, allowing up to
4 × 128 = 512 blocks per `fullSyncOnce` call instead of 128.

## Expected throughput improvement

With 7 peers and lookahead fully firing:

| Metric | A.75+A.76 | A.77+A.78 |
|---|---|---|
| Blocks per `fullSyncOnce` | 128 | up to 512 |
| Effective rate at 6 peers | ~5 bl/s | ~8–12 bl/s |
| Effective rate at 8+ peers | ~8 bl/s | ~15–25 bl/s |

The improvement materialises only when `len(candidates) > xdcMaxPeerTriesPerSync`,
i.e., when more than 4 non-tainted peers are connected.

## Safety

- `lookaheadChs` is empty when no fresh peers exist → A.77 block is a no-op
- `InsertChain` validates cross-batch parent hashes, rejecting any lookahead
  batch with a gap vs range-0
- `s.quitCh` respected in all collect loops
- `xdcBulkReqTimeout` (4s) caps lookahead wait per range

## Validation

Look for `"xdcSyncer: A.77 lookahead batch"` log lines and verify 3+ rapid
consecutive imports (< 5s apart) when peer count ≥ 7.
