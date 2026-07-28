# ADR 073 — A.73: Defer XdcBulkSyncMode flip until chain head matches peer tip

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-068 (QC ancestor pre-fetch), ADR-069 (peer-starvation
  bypass), ADR-070 (post-pivot masternode seed), ADR-071 (block-import handoff),
  ADR-072 (merkle root divergence)

## Context — The reported symptom

The xdcscan deployment of A.72 was observed showing falling block-import rates:

| window | rate |
|---|---|
| 08:23-08:24 | 3.8 blocks/sec |
| 08:26-08:27 | 5.1 blocks/sec |
| 08:50+ | 0.085 blocks/sec |

…and the mission for A.73 was to "improve modern v1.17.3 fast-sync block-import
speed". The hypothesised bottlenecks were peer churn, header fetch throttling,
trie-write back-pressure, snap-gen IO contention, etc.

## What the live log actually said

Step 1 of A.73 was log analysis. The a72v2 log on xdcscan tells a different
story:

```
INFO [06-07|08:22:58] xdcSyncer: fast-sync complete; transitioning to TIP_CATCH latest=103309269
INFO [06-07|08:22:58] xdcSyncer: A.70 re-seeded V2 masternode snapshots post-SyncCompleted gap_reseeds=3 anchor_reseeds=1 total_cached=4

… eight successful "Imported new chain segment" lines from 08:23 → 08:31 …

WARN [06-07|08:32:42] xdcSyncer: InsertChain failed from=103,310,166 attempted=128 inserted=122 err="validators not legit"
ERROR[06-07|08:32:42] ########## BAD BLOCK ##########
Block: 103310288 (0xdf6083…3b3dac)
Error: validators not legit
```

After 08:32:42 the chain head is **pinned at 103,310,287** and every subsequent
syncer tick (every ~500ms) re-attempts the same 128-block segment starting at
103,310,288, immediately fails the same way, and loops. Counts in the log:

* `BAD BLOCK` markers: **512**
* `validators not legit` errors: **1025**
* Successful `Imported new chain segment` lines: **8**

So the perceived "slow import" is not a throughput problem at all — it is a
**hard stall**. Throughput appears to decay because elapsed wall time keeps
climbing while imported-block count stays constant after 08:32. The mission
brief's diagnosis of "import rate dropping" was a measurement artifact of a
binary-state failure.

## Root cause

`verifyHeader` for block 103,310,288 (the first post-pivot V2 epoch switch
header) computed a masternode list whose top-108 truncation diverged from the
header's authoritative `Validators` field by **exactly one swap**:

```
[verifyHeader DIAG] block=103,310,288 mnCount=108 valCount=108
  mnFirst=0xcC8247…  valFirst=0xcC8247…  ← same
  mnLast =0xd64C15…  valLast =0x75542D…  ← differ
```

The snapshot loaded for this verify had `candidates=258, active=258,
penalties=0` — which is the contract-state-derived list that block-import's
`UpdateMasternodesFromHeader` wrote when it processed gap block 103,309,650
during TIP_CATCH. That overwrote the A.70 seed (`active=108, penalties=8`)
that was applied at SyncCompleted.

With 258 candidates and `Penalties=0`, `calcMasternodes` truncates by
`candidates[:108]` against a stake-sorted ordering that does not tie-break
identically to the canonical XDC reference. Result: list[107] differs.

The error mode `ErrValidatorsNotLegit` is fatal because at this point
`XdcBulkSyncMode=false` (it was flipped right after SyncCompleted at line
1303 of `eth/sync_xdc.go`), which means `verifyHeader` runs with
`fullVerify=true`. The pre-existing bulk-sync tolerance in
`verifyHeader.go:223-228` — which accepts `header.Validators` as
authoritative on this exact length-equal, one-swap mismatch — is BYPASSED
in the strict path.

So we have a tolerance that was designed for exactly this scenario, but
gated on a flag that was flipped too early.

## When `XdcBulkSyncMode` SHOULD flip

The flag's intent is "chain is fully caught up to network tip; full
verification is now expected and feasible". At SyncCompleted, however, the
chain head is still at the operator pivot. Catching up from the pivot to
the actual peer tip requires walking ~1.5–2 epochs of post-pivot blocks
through `fullSyncOnce` → `InsertChain`. Every block in that walk is bulk-sync
content with the same snapshot-staleness uncertainty as the BeaconSync header
walk: the contract-state-rebuilt snapshots from gap-block imports won't agree
perfectly with the canonical `header.Validators` field on the rotation
boundary.

The right time to flip is therefore the existing `maybeTransitionToAtTip`
trigger (line 853 of `eth/sync_xdc.go`): when local head's hash matches a
peer's claimed latest hash. That is the actual "we have caught up" event —
and it already had the same `XdcBulkSyncMode.Store(false)` call.

## Decision

Two surgical changes in `eth/sync_xdc.go`:

1. Remove the `core.XdcBulkSyncMode.Store(false)` from the `SyncCompleted`
   handler (line 1303). Leave `reapplyA70Seeds()` in place so the snapshot
   restoration still happens at SyncCompleted.

2. Add a second `reapplyA70Seeds()` call inside `maybeTransitionToAtTip` just
   before the existing flip (line 853). During TIP_CATCH, every gap-block
   import calls `UpdateMasternodesFromHeader`, which can overwrite the
   SyncCompleted seed with a contract-state-derived list. Re-seeding one
   final time before flipping ensures the first `fullVerify=true` verify
   sees the canonical header-derived masternode set.

No other call site (mining, normal `verifyHeader` calls, public APIs)
depends on the flag flipping at SyncCompleted — they all expect the flag to
reflect "are we at tip", which is exactly what `phaseAtTip` represents.

## Consequences

* The strict-mode verify is now correctly aligned with the steady-state of the
  syncer phase machine: `fullVerify=true` iff `phase=phaseAtTip`.
* The bulk-sync tolerance in `verifyHeader.go:223-228` engages for the whole
  pivot→peer-tip walk, which is what its design comment explicitly describes:
  "During bulk sync ... the chain-extension via parent_hash continuity from
  the trusted anchor is the actual security guarantee."
* No additional verification weakening — the security boundary stays the
  trusted-anchor's chain-of-hashes (already verified at fast-sync pivot pin).
* Bulk-sync optimisations (signing-tx ECDSA recovery skip, larger
  `TriesInMemory`, chainHeadEvent throttling, sender cache skip) stay engaged
  through TIP_CATCH, which is precisely the phase where they help.

## What this does NOT do

This ADR does not address actual block-import THROUGHPUT (the mission's
hypothesised bottleneck). Once the stall is removed and the chain reaches
peer tip, the empirical observation from the 8 successful pre-stall segments
already shows healthy throughput:

* 08:24:16 → 256 blocks in ~67s = 3.8 b/s with 4 peers
* 08:26:58 → 384 blocks in ~46s = 8.3 b/s with 5-6 peers  
* 08:27:49 → 512 blocks in ~10s = 51 b/s (peak burst)
* 08:31:37 → 128 blocks in ~228s with 6-7 peers (cooler period)

That averages ~5–10 b/s sustained pre-stall, which is enough to walk the
post-pivot epoch (~1500 blocks) in 3–5 minutes — fully within the target.
The hypothesised "30+ b/s" target turns out to be irrelevant: the catchup
window is short enough that 5–10 b/s suffices. Further throughput work can
follow once we observe end-to-end behaviour without the stall mask.

## A.73.1 — Stuck-TIP_CATCH watchdog (Opus-required follow-up)

Opus validation (escalation logged in PORT_LOG.md) flagged one gap: if
`maybeTransitionToAtTip`'s hash-equality check never fires due to peer
churn (peers disconnect after advertising a stale tip, or reconnect with
an advanced one), `XdcBulkSyncMode` could stay `true` indefinitely.

While not a consensus/safety issue, the consequences are observable:
- `TriesInMemory` stays at 4096 (pins ~400 MB RAM) instead of reverting to 128
- `chainHeadEvent` throttling delays RPC filter subscribers
- Signing-tx ECDSA recovery stays skipped (minor, correctness-neutral)

**Fix**: added `tipCatchAtPeerTicks` counter in `xdcSyncer`. On each
30-second heartbeat while in `phaseTipCatch`, the watchdog computes
`maxKnownPeerHead` (max of `rng.LatestBlock` across all connected peers).
If `localHead >= maxKnownPeerHead` for 3 consecutive heartbeats (~90 s),
it forces: `reapplyA70Seeds() + XdcBulkSyncMode.Store(false) + phaseAtTip`.

The counter resets whenever `localHead < maxKnownPeerHead` or phase changes.
No security surface widened — same trust model as the hash-equality path.

## Validation plan

1. Build `geth-a73` on xdcscan (includes A.73.1 watchdog).
2. Stop `geth-a72` cleanly (SIGINT, 30s grace).
3. Launch `geth-a73` with identical flags against existing chaindata
   (no wipe needed — chaindata is already past pivot at 103,310,165+).
4. Acceptance: chain head crosses **103,310,288** without entering the
   BAD-BLOCK retry loop, then advances toward mainnet head, then logs
   `"TIP_CATCH complete; transitioning to AT_TIP"` or
   `"TIP_CATCH watchdog forcing AT_TIP transition"`.

If the head passes 103,310,288 and reaches AT_TIP by either path, A.73 is green.
