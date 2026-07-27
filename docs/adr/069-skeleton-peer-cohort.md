# ADR 069 — Peer-starvation bypass for fast-sync NodeData + skeleton drop paths

Status: PROPOSED (2026-06-07, branch `fix/a69-skeleton-peer-cohort`)
Refs: #844 (fast-sync master), #857 (A.x patch series), #859 (block-import
unblocker context), PR #863 (A.68 QC ancestor pre-fetch — merged into
`reset/integration-1.17.3`)

## TL;DR

A.68 unblocked QC verification of pre-pivot ancestor headers delivered by
BeaconSync's skeleton walk-back. With that landed, modern v1.17.3 reaches
the state-sync phase cleanly — but the block-import path further down still
stalls on tiny peer cohorts:

- The A.40 sticky-PBSS blacklist (`nodeDataPbssLikely`) marks any peer that
  serves 10 empty NodeData responses inside a 120s window as PBSS-only and
  permanently drops it from `NodeDataIdlePeers`. With one connected XDPOS2
  peer this is fatal: the peer hits 10 empties for any transient reason
  (peer briefly at chain tip, peer's trie window getting pruned just as we
  ask for a hash inside it, peer busy with another request) → blacklisted →
  no idle peers left → state-sync stalls indefinitely.
- The XDC fix #131 empty-response drop in `skeleton.executeTask` symmetrically
  drops a peer after 5 consecutive empty header responses. With a single
  peer this strands the skeleton in the same way: header walk-back can no
  longer make progress.
- The A.37 "legacy" gate in `faststatesync.assignTasks` skips any peer that
  failed to advertise an XDCCaps trailer in handshake (or advertised a zero
  CommitPrefix). The wire-shape fix (A.37 era) made the trailer optional,
  so legacy peers normally fall through the safe-default path — but the
  gate is still active and is one more way the lone peer can get filtered
  out of dispatch on the next handshake rev.

A.69 introduces a peer-starvation floor (default 2 peers) below which each
of these three "drop / blacklist" mechanisms is bypassed. The counters are
still bumped + logged for visibility; only the punitive action is
suppressed. Above the floor — i.e. on a healthy peer fleet — the original
hardening behaviour is preserved unchanged.

## Context — empirical state (2026-06-07 06:34 CEST on xdcscan)

- Datadir: `/root/workspace/go-ethereum/nodes-local-mainnet/xdcscan-mainnet-fast-v1.17.3-a50580a81`
- Snapshot restored from `xdcscan-a67-state-T3p-1780804229.tar.zst` (8.0 GB)
- Binary built from `reset/integration-1.17.3` HEAD post PR #863 merge
- Operator pivot: block 103,309,269, hash `0x32a2…d9eea9`
- Chain tip at observation: ~103,484,500 (175 K blocks ahead of pivot)

Post-A.68 deploy + snapshot restore + restart observations:

- A.68.2 pre-fetch completed cleanly: 1,800 ancestor headers in 0.4 s
  (10 batches × 192 headers, covering 103,307,405..103,309,204).
- BeaconSync engaged at head=103,309,269 (operator-pinned).
- "Retrieved pivot header from local number=103,309,205" — `syncToHead`
  fell into the local-DB fallback for `head - fsMinFullBlocks` because the
  skeleton subchain only spans 103,309,268..103,309,269. The
  PrimeFastSyncAnchor 65-header window made the local fallback succeed.
- 0 QC errors (vs hundreds before A.68 — confirms A.68 fixed that layer).
- peers=1 throughout the test window (most public XDC mainnet peers
  failed to dial despite A.61 ENR fallback + A.62 60-entry bootnode list).
- 0 "Imported new chain" lines.
- `eth_blockNumber` stays at `0x0` after >30 min.

`eth_blockNumber` is computed from `blockchain.CurrentBlock().Number`,
which only advances past genesis when `commitFastSyncPivotBlock` fires
inside `processFastSyncContent`. That commit gates on `<-sync.done`, which
in turn needs state-sync to drain the trie under the operator-pinned root.
State-sync stalls because the single peer's empty-response counter ticks
up to the 10-empty PBSS threshold and `NodeDataIdlePeers` returns `[]`
forever after.

## Root cause — three peer-drop paths assume a healthy fleet

### 1. `peerSet.NodeDataIdlePeers` (A.40 sticky-PBSS blacklist)

```go
// existing
idle := func(p *peerConnection) bool {
    if p.nodeDataPbssLikely.Load() {
        return false  // sticky-skip — never returns true again this sync
    }
    ...
}
```

`nodeDataPbssLikely` is set once `nodeDataEmpties` crosses 10 inside a 120 s
sliding window. A genuinely-PBSS peer trips this within a couple of
batches. A correctly-HBSS peer with a transient stall (tip handoff,
window prune, request collision) trips it just as easily — and with one
peer in the fleet, that's the entire NodeData source gone.

### 2. `skeleton.executeTask` empty-response drop (XDC fix #131)

```go
const maxEmptyResponses = 5
s.emptyResponseCount[peer.id]++
if count >= maxEmptyResponses {
    s.drop(peer.id)   // ← peer goes away permanently this cycle
}
```

Symmetric to #1 but for header requests. With one idle peer in
`s.idles`, dropping it leaves `assignTasks` with no candidates and the
skeleton makes no further requests until a NEW peer joins. New peers are
exactly what we don't have.

### 3. `faststatesync.assignTasks` A.37 legacy gate

```go
for _, p := range peers {
    if p.remoteLegacy.Load() {
        continue   // ← strips legacy peers from NodeData dispatch
    }
    ...
}
```

`remoteLegacy` is currently always `false` for XDPOS2 peers because
`remoteCaps` is nil (no XDCCaps in handshake). The gate is dormant today,
but its existence means a future handshake rev that re-introduces the
trailer will silently re-activate the gate — and on a one-peer cohort
that's another stall vector.

## Decision

Introduce a single shared concept — the **peer-starvation floor** — and
apply it as a uniform bypass against all three drop / blacklist paths.

| Site | Constant | Default | Bypass behaviour |
|------|----------|---------|------------------|
| `peerSet.NodeDataIdlePeers` | `nodeDataPbssBypassPeerFloor` | 2 | Ignore `nodeDataPbssLikely` when `len(peers) <= floor` |
| `skeleton.executeTask` | `skeletonEmptyDropPeerFloor` | 2 | Do not drop on empty-response threshold when `len(s.idles) <= floor`; reset counter so the peer gets a fresh budget |
| `faststatesync.assignTasks` | `nodeDataPbssBypassPeerFloor` | 2 | Don't skip `remoteLegacy` peers when `s.d.peers.Len() <= floor` |

The A.37 proactive PBSS gate (`remoteSchemePbss`) is **deliberately not
bypassed** because it reflects an explicit handshake claim by the peer
("I run PBSS, I cannot serve NodeData"). Bypassing that would burn time
on a guaranteed-fail peer and leave the operator with a quiet stall
instead of "no eligible peers".

The counters themselves are still updated and the bypass is logged at
INFO once per crossing so operators can correlate stalls with the bypass
being active.

## Options considered

### Option A — Peer-cohort ensure-min-peers loop in xdcSyncer

Periodically re-resolve and re-dial bootnodes when peers < threshold.
Rejected as the primary fix because:

- It papers over the dial problem (most mainnet peers genuinely won't
  attach to v1.17.3 — that's the territory A.61/A.62 already tried) rather
  than recovering from it once attached.
- It requires wiring `p2pServer` through `handler` → `xdcSyncer`, which
  introduces a non-trivial dependency edge that has to survive future
  refactors. The risk/reward at this stage of the port doesn't justify it.
- It does nothing for the case where exactly one peer attaches and serves
  honest empties — the failure mode we observed today.

A future ADR may add it as a complementary improvement once the cohort
is reliably > 2; tracked as an A.69-follow item.

### Option B — Lower the A.40 threshold globally (e.g. 100 empties)

Rejected because it weakens hardening against actual PBSS peers without
addressing the fundamental single-peer-cliff problem: any threshold > 0
trips eventually when there's only one peer to talk to.

### Option C — Forward-skeleton variant

The brief raised this as a possibility for peers that don't honour
byNumber-reverse requests. Empirically not applicable here: A.68.2
demonstrated that the same XDC peers DO honour byNumber requests
(1,800-header pre-fetch landed in 0.4 s). The reverse-walk works; the
issue is downstream.

### Option D — Force-commit pivot from operator-supplied state when
state-sync is stuck

Rejected as too dangerous for an autonomous fix. Committing the pivot
without the full trie underneath would corrupt
`blockchain.CurrentBlock()` and downstream EVM execution against
unresolved state nodes would fail in unpredictable ways. A safer
"snapshot ingest" path exists in the operator-flow design but is out of
scope for A.69.

## Implementation

Three sites; one shared concept. ~25 lines of substantive change.

```go
// eth/downloader/peer.go
const nodeDataPbssBypassPeerFloor = 2

func (ps *peerSet) NodeDataIdlePeers() ([]*peerConnection, int) {
    ps.lock.RLock()
    totalPeers := len(ps.peers)
    ps.lock.RUnlock()
    peerStarved := totalPeers <= nodeDataPbssBypassPeerFloor
    idle := func(p *peerConnection) bool {
        if p.nodeDataPbssLikely.Load() && !peerStarved {
            return false
        }
        if p.remoteSchemePbss.Load() {
            return false  // proactive: still respect explicit handshake claim
        }
        return p.inflightCount() < maxParallelInflightSlots
    }
    ...
}
```

```go
// eth/downloader/skeleton.go
const skeletonEmptyDropPeerFloor = 2

// inside executeTask, len(headers) == 0 branch:
totalIdles := len(s.idles)
peerStarved := totalIdles <= skeletonEmptyDropPeerFloor
if count >= maxEmptyResponses && !peerStarved {
    s.drop(peer.id)   // original drop path on healthy fleet
} else {
    if count >= maxEmptyResponses && peerStarved {
        s.emptyResponseCount[peer.id] = 0  // reset budget — keep retrying
        log.Warn("Skeleton: keeping peer despite empties (peer-starved)", ...)
    }
    s.scheduleRevertRequest(req)
}
```

```go
// eth/downloader/faststatesync.go
// inside assignTasks:
peerStarved := s.d.peers.Len() <= nodeDataPbssBypassPeerFloor
for _, p := range peers {
    if p.remoteLegacy.Load() && !peerStarved {
        continue
    }
    ...
}
```

## Why this preserves A.68

A.68.1 changed `HeaderChain.GetTrustedCheckpointAnchor`'s active-gate
lower-bound check; A.68.2 added `prefetchAncestorEpochs` to seed pre-pivot
headers before BeaconSync runs. Neither code path is touched here. The
diff stays inside the downloader's peer-eligibility helpers and the
skeleton's per-cycle response handler — both downstream of A.68's
HeaderChain + xdcSyncer changes. Regression risk on the fixed QC stall
is nil.

## Validation plan

Iteration loop on xdcscan (95.217.106.210:12141):

1. Push `fix/a69-skeleton-peer-cohort` to GitHub.
2. On server:
   ```bash
   cd /root/workspace/go-ethereum
   git fetch origin
   git checkout fix/a69-skeleton-peer-cohort
   go build -o build/bin/geth-a69 ./cmd/geth
   ```
3. Stop current geth (PID ~2086214, port 8590).
4. Wipe + restore snapshot:
   ```bash
   cd nodes-local-mainnet/xdcscan-mainnet-fast-v1.17.3-a50580a81
   rm -rf XDC
   tar --use-compress-program="zstd -d -T0" -xf \
       /root/snapshots/xdcscan-a67-state-T3p-1780804229.tar.zst
   ```
5. Launch geth-a69 with same flags as A.68 final.
6. Watch log for:
   - "Imported new state entries" — confirms state-sync is making progress
     despite the (previously fatal) empty-response runs.
   - "Skeleton: keeping peer despite empties (peer-starved)" — confirms
     the bypass is active on this fleet size.
   - `eth_blockNumber > 0x0` after the pivot commits.
   - "Imported new chain" lines after pivot commit (TipCatch phase begins).

### Success criteria

- `eth_blockNumber` rises from `0x0` to `0x6276955` (= 103,309,269, the
  operator pivot) within 30 min — confirms pivot committed.
- Subsequently advances toward the live chain tip (~103,484,500) at a
  rate proportional to the fullSyncOnce batch size.

### Failure criteria

- `eth_blockNumber` stays at `0x0` after 30 min → look for new failure
  variant (e.g. body fetch returning 0, receipts not matching), open a
  follow-up A.70.

## Risks

- **R1 — Legitimate PBSS peers stay in the dispatch pool when peer-starved.**
  Mitigation: the A.37 proactive `remoteSchemePbss` gate is NOT bypassed
  (handshake-asserted). The only window for an actual-PBSS peer to slip
  through is the heuristic A.40 gate (10 consecutive observed empties) —
  and those peers will simply return more empties on every subsequent
  request, costing roundtrip time but not progress. Net: bypass turns a
  fatal stall into a slow recovery on a degenerate fleet, which is the
  desired tradeoff.
- **R2 — Bypass on a healthy fleet hides real PBSS peers.** Mitigation:
  bypass only fires when `len(peers) <= 2`. Once dial resolution improves
  the gate behaves identically to today.
- **R3 — Skeleton keeps a peer that genuinely has no headers in our range.**
  Mitigation: empties cost roundtrip time, not consensus correctness. The
  walk-back will still terminate at `terminateFloor` (A.14 operator-pinned
  anchor) regardless of how many empty cycles precede it.

## Test plan

- Existing `TestSkeletonAcceptsXDPOS2Peers` continues to pass (peer-cohort
  bypass does not change skeleton's protocol-version handling).
- Existing `TestParallelInflightNoOverlap` continues to pass (slot
  semantics untouched).
- Full `eth/downloader/` test suite: PASSING locally (4.5 s).
- `eth/` test suite: PASSING locally (4.4 s).
- Empirical: validation harness above on xdcscan after deploy.
