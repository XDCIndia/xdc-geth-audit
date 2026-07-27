# `eth/downloader/downloader_xdc.go` port attempt — 2026-05-23

Status: ABORTED after cascading-dependency wall hit.
Attempted scope: drop in xdc-network's 1330-line file with mechanical
adaptations (mux→feed, FastSync→SnapSync, stubs for missing methods).

## What worked

Mechanical replacements applied via `sed`:

- `d.mux.Post(StartEvent{})` → `d.feed.Send(SyncEvent{Type: SyncStarted, Mode: mode})`
- `d.mux.Post(FailedEvent{err})` → `d.feed.Send(SyncEvent{Type: SyncFailed, ...})`
- `d.mux.Post(DoneEvent{latest})` → `d.feed.Send(SyncEvent{Type: SyncCompleted, ...})`
- `ethconfig.FastSync` → `ethconfig.SnapSync` (fast-sync removed in v1.17.3)
- `d.blockchain.SetCheckpointPrunePoint(...)` → `xdcSetCheckpointPrunePoint(d.blockchain, ...)` (typed-interface stub)
- `d.blockchain.SetCheckpointSyncNoState(...)` → `xdcSetCheckpointSyncNoState(...)` (same)

## What blocked the port

After the mechanical pass, the build surfaces a cascading dep chain that
each requires its own multi-file port:

### Missing methods on the `BlockChain` interface

```go
d.blockchain.Config()       // chain config getter — needs to be on the interface
                            // AND implemented on *core.BlockChain
d.blockchain.SetCheckpointPrunePoint(num uint64, hash common.Hash)
d.blockchain.SetCheckpointSyncNoState(v bool)
```

The latter two are XDC-specific writers used by the checkpoint-aware sync
path. They live on xdc-network's `*core.BlockChain` but were not ported in
the B6/B7 bundles.

### Missing methods on `eth.Peer`

```go
p.peer.RequestHeadersByHashLegacy(...)   // bare-RLP outbound, no RequestId
p.peer.RequestHeadersByNumberLegacy(...) // same
```

These are different methods than the existing `RequestHeadersByHash/Number`.
PR #768 added XDPOS2-branching INSIDE the existing methods, but
downloader_xdc.go expects these as separate methods with different
signatures (no sink channel, synchronous return). Each call site assumes
a callback-style API delivered via `DeliverHeadersXDC`.

### Missing globals in `core` package

```go
core.XdcBulkSyncMode.Store(true)   // atomic.Bool for bulk-sync flag
```

Referenced in `synchroniseXDC` as a defer-cleanup pattern. The flag itself
exists in xdc-network's `core/state_db.go` (or similar) for ECDSA
signature-recovery skipping — a hot-path optimisation during sync. Not
ported.

### Missing `Downloader` fields/methods

```go
d.nextRequestID()                  // atomic counter
d.insertCheckpointAnchor(p)        // XDC checkpoint anchor insertion
```

These can be stubbed locally (added in this attempt), but the real
implementations live in `downloader_xdc.go` (which we're trying to port)
and `core/blockchain.go`'s XDC additions (not yet ported).

### Body delivery type mismatch

xdc-network's `DeliverBodiesXDC(peerId, txs, uncles, reqId)` uses
`[][]*types.Transaction` and `[][]*types.Header` — the v1.17.3 body type
is `eth.BlockBody` with `Transactions`, `Uncles`, `Withdrawals`. Field
shapes diverged; the delivery hand-off won't compile without re-shaping.

## The transitive scope

Honestly porting `downloader_xdc.go` requires:

| Component | Lines (approx) | Type of work |
|---|---|---|
| `eth/downloader/downloader_xdc.go` | 1330 | port + adapt |
| `core/blockchain.go` SetCheckpoint* methods | ~60 | port |
| `core/state_db.go` XdcBulkSyncMode | ~40 | port |
| `eth/protocols/eth/peer.go` Request*Legacy methods | ~80 | new |
| `eth/downloader/types.go` BlockChain interface additions | ~10 | new |
| Body-delivery type shape adaptation | ~50 | adapt |
| **Total** | **~1570 lines** | **multi-file, multi-package** |

This is the genuine paired-bundle (PR #749 scope) territory and matches
CLAUDE.md's escalation criteria (#2: rewrite existing module, #5: block
production design, #6: any block-hash-impacting decision).

## What works now (post-PR #770)

All the surgical wire-protocol ports are landed:

- ✓ Handshake: PR #757 (forkID merge), PR #758 (StatusPacket62)
- ✓ Peer-tip tracking: PR #764 (populate lastRange)
- ✓ Sync trigger: PR #766/767 (xdcSyncer Option A scaffolding)
- ✓ XDPOS2 inbound headers: PR #763 (handleGetBlockHeaders legacy)
- ✓ XDPOS2 outbound + response: PR #768 (peer.RequestHeaders bare + handleBlockHeaders legacy + real-header fetch)
- ✓ Tracker non-fatal on legacy size: PR #769
- ✓ XDPOS2 bodies bidirectional: PR #770 (handleGetBlockBodies / handleBlockBodies legacy)
- ✓ TIPSigning Process dispatch: PR #761

The wire-level peering is functional against the production XDC fleet.
The remaining blocker is sync-trigger semantics that BeaconDevSync
(v1.17.3's only entry point) can't provide — it expects a forkchoiceUpdated
lineage that XDC doesn't have a consensus-layer to drive.

## Recommended next session

1. Opus design review of the paired bundle approach: do we lift xdc-network's
   `downloader_xdc.go` wholesale (port-and-adapt) or rewrite it for v1.17.3's
   downloader architecture (more invasive but cleaner)?
2. Land the prerequisite core/blockchain.go XDC methods first (smaller PR,
   no cascading deps).
3. Then port `downloader_xdc.go` against the now-complete BlockChain interface.
4. Wire xdcSyncer (PR #766/767) to call `SynchroniseXDC` instead of
   `BeaconDevSync`, completing the loop.

Time estimate: 2-3 focused sessions with Opus design escalation.
