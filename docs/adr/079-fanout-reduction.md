# ADR 079 — A.79: Reduce parallel fan-out from 4 to 3 peers

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-077 (A.77+A.78 parallel lookahead)

## Context

A.77/A.78 enable parallel lookahead pre-fetch from non-raced peers.
Lookahead fires only when `len(candidates) > xdcMaxPeerTriesPerSync`.

With the original `xdcMaxPeerTriesPerSync = 4`:
- 4 peers → 0 lookahead streams
- 5 peers → 1 stream
- 6 peers → 2 streams

In practice the server often settles at 4-5 peers, making lookahead rare.

## A.79 — Reduce fan-out from 4 to 3

`xdcMaxPeerTriesPerSync: 4 → 3`.

| Total peers | Fan-out | Lookahead before (K=4) | Lookahead after (K=3) |
|---|---|---|---|
| 3 | 3 | 0 | 0 |
| 4 | 3 | 0 | **1** |
| 5 | 3 | 1 | **2** |
| 6 | 3 | 2 | **3** |
| 7+ | 3 | 3 | **3** |

Lookahead is now achievable at 4 peers instead of 5.

## Trade-off analysis

Reducing fan-out from 4→3 means one fewer parallel query per range-0 round.
In the XDC mainnet peer fleet, each peer rate-limits at 128 blocks/request for
~60 seconds. The 4th fan-out query almost always returns `count=0` because
the peer was already queried recently. Dropping it costs nothing in practice
while freeing a slot for lookahead.

## Observed results (xdcscan, 2026-06-07)

Combined with the firewall fix (port 30590 opened for inbound P2P) which
raised connected peers from 2→6:

- Before A.79 + firewall fix: ~4 bl/s (4 peers, 0 lookahead streams)
- After A.79 + firewall fix: ~17 bl/s sustained, 33 bl/s burst

Import sequence at peak (27 seconds, 7 batches, 896 blocks):
```
20:48:02 imported → 103,365,327 (+128)
20:48:03 imported → 103,365,455 (+128)
20:48:11 imported → 103,365,583 (+128)
20:48:19 imported → 103,365,711 (+128)
20:48:22 imported → 103,365,839 (+128)
20:48:25 imported → 103,365,967 (+128)
20:48:29 imported → 103,366,095 (+128)
```

Typical log with A.79:
```
xdcSyncer: A.77 lookahead launched  streams=3  candidates=9   raced=3
xdcSyncer: imported batch           count=128  newHead=103,366,351
```

## Safety

- No correctness change — only affects which peers are queried for range-0
- InsertChain validates cross-batch continuity as before
