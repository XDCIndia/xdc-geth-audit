# Opus Planner Brief — Sprint 3 design after Sprint 2 wire-mux blocker

## Context
- erigon-xdc port goal: M5 = full mainnet fast-sync from genesis to head
- xdcscan PoC on go-ethereum branch `feat/a11-fast-sync-peer-gate@9f592e60c`
- Sprint 2 (A.50 + A.53 + A.55) deployed; binary healthy; **0 nodes/min observed**

## Sprint 2 mechanics confirmed working
- A.50 (skeleton xdpos2 peer-set bridge): `Skeleton peer pool seeded total=3 xdpos2=3 eth=0`
- A.53 (parallel NodeData inflight, 16 slots/peer): `Fast state peer pool gated peers=3 hbss=3`
- A.55 (chunked commit / checkpoint resume): code path never reached
- Operator-pinned pivot 103,408,927 (head-128 fresh from rpc.xinfin.network) accepted by syncer

## Sprint 2 blocker observed
Skeleton-header phase **never completes** because XDPoSChain peers silently drop
`amount=512 reverse=true` range queries near pivot 103M:
```
Fetching skeleton headers from=103,408,926 count=512 reverse=true
   → XDPOS2 BlockHeaders received count=0
   → 1m0s Header request timed out, dropping peer
   → Restarting sync cycle reason="chain gapped, head: 99740698, newHead: 103408927"
   → infinite loop
```

Same peers DO answer:
- single-hash queries (`amount=1`) — anchor priming succeeded, 65 headers retrieved
- numbered queries past their last-fully-served range — partial responses, count=0 still possible

A.14c (PID 3876607, stale 79M pivot, 9h53m up) achieves ~35 nodes/min processed=49,922 because
its pivot is in a range peers still serve range queries for. Fresh head-128 pivots are unservable.

## Options for Sprint 3

**S3.A — One-header-at-a-time skeleton fetcher**
Replace `amount=512 reverse=true` calls in skeleton.go with a fan-out of N single-header
hash queries (the proven-working request type). Hash-by-hash walks back from `tip-1` per
peer until each fills its assigned skeleton segment.
- Pros: uses request shape we know works; high parallelism per peer; no protocol change
- Cons: 512x more requests per skeleton fill; bandwidth multiplied by inflight queue overhead;
  needs new bookkeeping for partial fills

**S3.B — Direct NodeData-by-hash without skeleton**
Skip skeleton entirely. Use operator pivot's stateRoot directly. BeaconSync-style cold-start
with state-sync as the *first* downloader stage. Headers backfill afterwards using the same
one-at-a-time pattern.
- Pros: state sync starts immediately; A.53 slot table immediately useful
- Cons: violates downloader invariants (header chain must precede state); receipts/bodies
  unbacked until headers come; may break consensus parity gate

**S3.C — Multiplex one-header queries onto the same xdpos2 stream with pipelining**
Keep skeleton's `count=512` API surface, but in the wire layer fan it out as 512 concurrent
`count=1` requests over the same connection, reassembling the response array client-side.
A pure protocol-adapter, leaves downloader/skeleton untouched.
- Pros: no skeleton.go changes; no consensus-parity risk
- Cons: subtle req_id mapping; peer rate-limits may throttle 512-burst; latency may exceed 1m timeout

**S3.D — Drop fast-sync entirely; use snap-sync (Erigon native)**
This isn't an erigon-xdc question (we're on go-ethereum here), but the migration to erigon is the
parent goal. Could fast-sync on go-ethereum be abandoned and effort redirected to erigon's
snap-sync where Erigon already handles the wire-multiplex problem?
- Pros: aligned with M5/M6 erigon-xdc roadmap
- Cons: large pivot; loses go-ethereum's reverse-deploy testing capability

## Question for the planner
Which option (S3.A / S3.B / S3.C / S3.D) minimizes risk-to-deliver while preserving consensus
parity gates? Top 3 risks of recommended option. Top 3 risks of NOT taking it. What would
change the recommendation?
