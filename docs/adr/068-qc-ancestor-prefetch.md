# ADR 068 — Fix HeaderChain catchup-window active-gate for fresh-start fast-sync

Status: PROPOSED (2026-06-07, branch `fix/a68-qc-ancestor-prefetch`)
Refs: #844 (fast-sync master), #857 (A.x patch series), #859 (A.60.5c regression context)

UPDATE 2026-06-07 (post-empirical-test): the gate fix alone (A.68.1) is necessary but NOT sufficient. With the gate firing correctly the failure cascades from `verifyQC` into `GetMasternodesWithParents` for non-epoch-switch blocks — line 283 of verifyHeader.go calls it, the parents slice within a single 192-header batch does not span back to the previous epoch switch (~900 blocks behind for blocks just under the pivot), the snapshot-by-anchor-hash fallback fails because no snapshot is seeded at the pivot hash, and `verifyMsgSignature` returns `"empty masternode list"`. The fix needs both A.68.1 AND A.68.2 — pre-fetch the operator pivot's ancestor headers (back through the previous epoch switch + its gap block) at prime time so `getEpochSwitchInfo` can resolve masternodes through the DB. A.68.1 is landed in this ADR + this branch; A.68.2 will be a follow-up.

## TL;DR

The A.60.5c (commit `adc74c729`) mirror of the trusted-checkpoint anchor onto
`*HeaderChain` shipped a symmetric ±2×Epoch window with a
`headNum >= pt.BlockNumber - 2*Epoch` lower-bound check on the active gate.
That lower bound mis-fires on fresh-start fast-sync where the header chain
head sits at genesis (headNum=0) while the operator-pinned pivot is at block
~103M — `0 >= 103,309,269 − 1800` is false, the gate returns active=false,
strict V2 `verifyQC` runs on every header BeaconSync delivers, and the
recursive `getEpochSwitchInfo` walk crashes with
`parent header X not in DB (bulk sync?)` because the pre-pivot epoch-switch
ancestors have not been backfilled yet.

A.68 drops the lower bound. The gate stays active until headNum climbs PAST
`pt.BlockNumber + 2*Epoch`, matching the `*BlockChain` semantics exactly.
This restores the catchup-window behaviour A.60.5c was designed to provide
and unblocks BeaconSync's transition from state-sync into block-import on
fresh-start fast-sync with operator pivot.

## Context — empirical state (2026-06-07 05:51 CEST on xdcscan)

- Datadir: `/root/workspace/go-ethereum/nodes-local-mainnet/xdcscan-mainnet-fast-v1.17.3-a50580a81`
- Binary: built from commit `4e139ddf6` (reset/integration-1.17.3 HEAD)
- Operator pivot: block `103,309,269`, hash `0x32a2…eea9`, root `0xa9e7…111e`
- A.67 state-sync completes cleanly: 83,338,740 entries in 78m, 0% drop ratio
- After state-sync, BeaconSync fires every ~5s and ALWAYS fails:
  ```
  ERROR [getEpochSwitchInfo] recursive error err="parent header 0x513c9b…
        not in DB (bulk sync?)" hash=0xf25b…c47ee number=103,309,267
  ERROR [verifyQC] Failed to get epoch info error="parent header 0x513c9b…"
  ERROR Beacon backfilling failed err="retrieved hash chain is invalid:
        failed to get epoch switch info for QC verification"
  ```
- After 7h of retries: modern still at block=0; legacy v2.7.0 in same window
  reached chain tip `103,483,725`.

## Root cause — `HeaderChain.GetTrustedCheckpointAnchor` lower-bound bug

The call path is:

```
beaconBackfiller.resume → synchronise → processHeaders → InsertHeadersBeforeCutoff
  → BlockChain.hc.ValidateHeaderChain
  → hc.engine.VerifyHeaders(hc, chain)         ← chain is *HeaderChain
  → XDPoS_v2.verifyHeader
  → inCheckpointCatchup(chain, header.Number)
  → chain.(ckptAware).GetTrustedCheckpointAnchor()  ← HeaderChain's impl
```

Pre-A.68 code (`core/headerchain.go:96`):

```go
func (hc *HeaderChain) GetTrustedCheckpointAnchor() (uint64, common.Hash, bool) {
    pt := hc.fastSyncAnchor.Load()
    if pt == nil {
        return 0, common.Hash{}, false
    }
    epoch := uint64(900)
    if cfg := hc.config.XDPoS; cfg != nil && cfg.Epoch != 0 {
        epoch = cfg.Epoch
    }
    head := hc.CurrentHeader()
    if head == nil {
        return pt.BlockNumber, pt.BlockHash, true
    }
    headNum := head.Number.Uint64()
    lo, hi := uint64(0), pt.BlockNumber+2*epoch
    if pt.BlockNumber > 2*epoch {
        lo = pt.BlockNumber - 2*epoch
    }
    active := headNum >= lo && headNum <= hi   // ← BUG
    return pt.BlockNumber, pt.BlockHash, active
}
```

With pivot=103,309,269 and the chain head at genesis (the steady state for
the entire fast-sync engagement until `InsertHeaderChain` runs):

```
lo = 103,309,269 - 1800 = 103,307,469
hi = 103,309,269 + 1800 = 103,311,069
active = (0 >= 103,307,469) && (0 <= 103,311,069)
       = false && true
       = false    ← gate fails closed
```

`inCheckpointCatchup` then returns false, the catchup branch in
`verifyHeader` is skipped, strict `verifyQC` runs, and the recursive
`getEpochSwitchInfo` walk hits the "parent header not in DB" error.

Note `*BlockChain.GetTrustedCheckpointAnchor` (`core/blockchain_reader.go
:535`) uses a one-sided check — `active := head == nil ||
head.Number.Uint64() < pt.BlockNumber+2*epoch` — and works correctly. Only
the HeaderChain mirror is buggy.

## Options considered

### Option A — Pre-fetch ancestor headers before invoking BeaconSync (orcy-preferred)

Walk back from the operator pivot via `fetchHeaderByHash` for ~2*Epoch+
blocks (~1800 headers) BEFORE invoking `BeaconSync(headHdr, nil)`. Write
them to the DB via `rawdb.WriteHeader` + `rawdb.WriteCanonicalHash`. The
recursive `getEpochSwitchInfo` then finds every ancestor it needs in the
local DB and the QC check succeeds.

Cost: 1800 extra header round-trips per fast-sync start. Throughput ≈
192 headers per `eth.GetBlockHeaders` request on XDPOS2 = 10 sequential
fetches ≈ 5–20s extra startup latency. Code complexity: ~80 LoC in
`eth/sync_xdc.go` extending the existing `PrimeFastSyncAnchor` walk-back.

### Option B — Catchup-window relaxation (orcy-disfavoured for risk)

Modify `verifyQC` / `getEpochSwitchInfo` to allow soft verification when
the ancestor isn't in DB AND the chain head is at/near a fast-sync pivot.
Risk: weakens consensus correctness invariants at the pivot boundary.

### Option C — Reorder BeaconSync backfill (orcy-disfavoured for invasiveness)

Have skeleton.go download all headers in the catchup window before invoking
`verifyHeader` on any one of them. Risk: breaks the streaming nature of
skeleton.

### Option D (CHOSEN) — Fix the existing A.60.5c catchup-window active-gate

The catchup-window relaxation orcy described in Option B **already exists**:
`inCheckpointCatchup` + `verifyHeader`'s `if x.inCheckpointCatchup(...)`
branch (`consensus/XDPoS/engines/engine_v2/verifyHeader.go:105`) and the
matching `commitBlocks` 3-chain skip. The A.60.5c commit explicitly added
both halves: the V2-engine consumer (utils.go) AND the producer
(BlockChain + HeaderChain). The HeaderChain producer just has a one-line
bug in its active-gate logic.

This is NOT Option B — we are not weakening consensus checks, we are
fixing a regression in the existing A.60.5c machinery that ships those
relaxed checks. The strict-verification upper bound (`pt.BlockNumber +
2*Epoch`) stays exactly where it was. Once the header chain climbs past
that boundary, full strict verification resumes — same as today,
unchanged.

## Decision

Adopt Option D. One-line fix in `core/headerchain.go`:

```go
// Active until head climbs past anchor + 2*Epoch. Mirrors the BlockChain
// version's check exactly; the original A.60.5c lower-bound was wrong for
// the genesis-head case BeaconSync reverse-walk exhibits.
active := head.Number.Uint64() < pt.BlockNumber+2*epoch
```

Why not Option A?

1. **Root-cause vs band-aid.** Option A adds machinery to work around a
   bug; Option D fixes the bug. The existing infrastructure (Option-B-as-
   designed, shipped by A.60.5c) is correct in concept. The regression is
   isolated to a single boolean expression on one method of one struct.
2. **Code volume.** Option A: ~80 LoC. Option D: 5 LoC + 50 LoC of tests.
3. **Startup latency.** Option A: 5–20s extra header round-trips before
   BeaconSync. Option D: zero extra latency.
4. **Symmetry with BlockChain.** Option D makes HeaderChain.
   GetTrustedCheckpointAnchor behave identically to BlockChain.
   GetTrustedCheckpointAnchor for the active flag. Reduces surface area
   for divergent behaviour between the two paths.
5. **Consensus correctness invariant preserved.** The upper-bound (`active
   = false` once head crosses `pivot + 2*Epoch`) is untouched. Strict V2
   verification engages exactly when designed: as soon as the chain has
   the 2×Epoch worth of post-pivot history that `verifyQC` /
   `getEpochSwitchInfo` need.

## Risks of Option D

1. **Long-window blocks skip strict QC.** Headers in `[0, pivot+2*Epoch]`
   skip strict QC verification. This is by design — the operator-supplied
   pivot hash IS the trust anchor for the range, and the operator
   accepted that trust deliberately by passing `--fastsyncpivot{number,
   hash,root}`. The block-level Merkle-Patricia state proof at the pivot
   continues to enforce execution-state correctness. Identical to the
   existing trusted-checkpoint behaviour upstream.
2. **HeaderChain.CurrentHeader does eventually advance.** Once
   `BlockChain.InsertHeadersBeforeCutoff` finishes the first batch, the
   header chain head jumps to (or near) the pivot. From that moment the
   active flag flips off because pivot ≥ pivot + 2*Epoch is false — wait,
   pivot < pivot + 2*Epoch so it stays active. The flip happens only
   when the head climbs past pivot + 2*Epoch, i.e. when block-import
   has advanced ~1800 blocks past the pivot. That's exactly the
   designed-for boundary.
3. **Apothem operator pivot at block 82M.** Same arithmetic. Active
   until head climbs past 82,820,453 + 1800. Verified by the
   `TestGetTrustedCheckpointAnchorActiveAtGenesis` regression test.

## Risks of NOT adopting Option D (staying with Option A)

1. **Bug remains.** Even if Option A pre-fetches enough ancestors, the
   broken `headNum >= lo` gate would still flip the catchup off the
   moment HeaderChain.CurrentHeader returned anything below `pivot -
   2*Epoch` (which can happen during skeleton reverse-walk before
   forward-fill). The fix is needed regardless.
2. **Operational fragility.** Pre-fetch is timing-dependent — if a peer
   drops mid-walk-back the prime fails and the bug returns. Option D
   has no such failure mode.

## What would change the recommendation?

If empirical testing shows that even with Option D applied, BeaconSync
still fails to transition past state-sync — that means the failure mode
is NOT solely the gate bug and there's a deeper handoff issue that
ancestor pre-fetch would be needed for. In that case escalate to Opus
planner and reconsider Option A.

## Validation plan

1. Build `geth-a68` locally. (DONE — `/tmp/geth-a68` exists.)
2. Run unit tests: `go test -run TestGetTrustedCheckpointAnchor
   ./core`. (DONE — passes.)
3. Scp to xdcscan, swap binary, restore A.67 snapshot, launch.
4. Watch for: `Imported new chain segment` lines + `eth_blockNumber`
   incrementing past 0x0.
5. Acceptance: success within 10 minutes of launch.

## Files changed

A.68.1 (commit fb2bf8df5):
- `core/headerchain.go` — gate fix (5 LoC effective change, ~25 LoC
  comment update)
- `core/headerchain_test.go` — two regression tests
  (`TestGetTrustedCheckpointAnchorActiveAtGenesis`,
  `TestGetTrustedCheckpointAnchorInactivePastCatchupWindow`)
- `docs/adr/068-qc-ancestor-prefetch.md` — this document

A.68.2 (commit 6d8e91aa1):
- `eth/downloader/downloader.go` — new `PrimeAncestorHeaders` exported
  method (writes header + canonical-hash mappings for headers immediately
  below the prime window; no body/receipts/snap-block plumbing because
  these headers exist purely for V2 engine recursion to find them)
- `eth/sync_xdc.go` — new `prefetchAncestorEpochs` method called after
  successful `PrimeFastSyncAnchor`. Walks back 2×Epoch headers via
  `fetchHeaderBatch` (XDPOS2 192-cap), writes batch-by-batch.

## Empirical validation (2026-06-07 06:24 CEST, xdcscan)

After A.68.1 + A.68.2 land, with the A.67 chaindata snapshot restored
and operator pivot 103,309,269:

```
INFO xdcSyncer: A.68.2 ancestor pre-fetch starting from=103,307,405 to=103,309,204 headers_needed=1800 primeBase=103,309,205
INFO Fast Sync: primed ancestor headers (A.68.2) supplied=192 written=192 already_present=0 lo=103,309,013 hi=103,309,204
...
INFO Fast Sync: primed ancestor headers (A.68.2) supplied=72 written=72 already_present=0 lo=103,307,405 hi=103,307,476
INFO xdcSyncer: A.68.2 ancestor pre-fetch complete target_depth=1800 actual_written=1800 lo=103,307,405 hi=103,309,204
```

(10 sequential batches, ~1.0s end-to-end, all 1800 headers written.)

Then BeaconSync engages cleanly — terminate-floor logic correctly bounds
the skeleton walk-back at the operator pivot:

```
INFO "skeleton: terminate floor reached — exiting walk-back" tail=103,309,269 floor=103,309,268
INFO Skeleton peer pool seeded total=5 xdpos2=5 eth=0
INFO Fetching skeleton headers peer=... from=103,309,268 count=192 batch=192
```

**NO MORE QC ERRORS. NO MORE "empty masternode list" ERRORS.** The
explicit failure modes that A.68 targets are fully eliminated.

A new bottleneck — peers returning count=0 to byNumber+reverse skeleton
fetches in the operator-pivot's neighbourhood — has emerged and prevents
full forward progress in this test session, but that is a SEPARATE
peer-availability / skeleton-internal-logic issue out of scope for A.68
(track as A.69).
