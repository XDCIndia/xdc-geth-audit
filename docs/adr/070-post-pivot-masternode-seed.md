# ADR 070 — Seed V2 masternode snapshots from pre-fetched epoch-switch headers

Status: PROPOSED (2026-06-07, branch `fix/a70-post-pivot-masternode-seed`)
Refs: #844 (fast-sync master), #859 (A.x patch series), follow-up to ADR 068 (A.68 QC ancestor prefetch) and ADR 069 (A.69 peer-starvation bypass).

## TL;DR

After A.68 (PR #863) unblocks the QC verification path by pre-fetching 1,800
ancestor headers below the operator pivot, and A.69 (PR #864) keeps peers
attached through the post-handoff peer-starvation window, modern v1.17.3
reaches the operator pivot 103,309,269 and starts attempting to import
post-pivot blocks. Empirically (xdcscan 2026-06-07 06:38 CEST) **every**
post-pivot block fails consensus with `BAD BLOCK Error: validators not legit`
and the node stalls at pivot+121 (block 103,309,390) with 1,100+ rejected
inserts in a retry storm (~90/min).

Root cause: the V2 engine's masternode-snapshot DB is empty after fast-sync
state restore. When `xdcSyncer.fullSyncOnce` resumes after `SyncCompleted`
flips `core.XdcBulkSyncMode` to false, `InsertChain` invokes
`VerifyHeaderWithParents(..., fullVerify=true)`. For V2 epoch-switch blocks
in the catchup window (round-based epochs can land anywhere between the
block-aligned checkpoint 103,309,200 and the next round-aligned switch), the
strict comparison at `engines/engine_v2/verifyHeader.go:198`
`CompareSignersLists(masterNodes, validatorsAddress)` fails because
`getSnapshot(chain, blockNum, false, parents)` cannot find a snapshot at the
gap-block hash (no `storeSnapshot` ever ran for this fast-sync session).

A.70 closes the gap by piggy-backing on the 1,800-header prefetch that
A.68.2 already performs: scan those headers for V2 epoch-switch headers,
extract masternodes from `header.Validators` (the canonical active set
authored by the issuing epoch), compute the corresponding gap-block number
(`epochCheckpoint - Gap`), look up the gap header in the local DB, and
persist a snapshot keyed at the gap header's hash via the existing
`UpdateMasternodesWithPenalties` path. The next `getSnapshot` call for any
block in the catchup window hits the DB and returns the seeded masternode
list — `CompareSignersLists` succeeds and `InsertChain` advances.

We additionally seed a snapshot at the operator pivot's own hash so the
trusted-checkpoint catchup fallback at `engine.go:1251`
(`GetMasternodesWithParents` → `loadSnapshot(x.db, anchorHash)`) becomes
functional. Pre-A.70 that path always failed because no snapshot was ever
stored at the anchor hash.

## Context — empirical state (2026-06-07 06:38 CEST on xdcscan)

- Datadir: `/root/workspace/go-ethereum/nodes-local-mainnet/xdcscan-mainnet-fast-v1.17.3-a50580a81`
- Binary: built from A.68 + A.69 (commit `f345c5d60`)
- Operator pivot: block `103,309,269`, hash `0x32a2…eea9`, root `0xa9e7…111e`
- A.68 prefetch: 1,800 ancestor headers written (103,307,405..103,309,204).
- A.68.1 catchup gate: active.
- A.69 peer-starvation bypass: active.
- State-sync complete; BeaconSync engages, pivot reached.

Post-pivot, every InsertChain call fails:

```
########## BAD BLOCK #########
Block: 103309391 (0x517ef2…a58dd9)
Error: validators not legit
##############################
xdcSyncer: InsertChain failed from=103,309,391 attempted=128 inserted=0
            err="validators not legit"
```

Plus `[verifyHeader] validatorsAddress i=0..107` spam (108 validators per
failed block), 1,101+ BAD BLOCKs, 2.3M `Failed` + 2.3M `Skipping` lines.
Head sticks at pivot+121.

## Root cause — empty V2 snapshot store + fullVerify=true post-SyncCompleted

The V2 engine resolves masternodes via `getSnapshot(chain, blockNum, ...)`
keyed at the gap header's hash (`gapNum = blockNum - blockNum%Epoch - Gap`).
For block 103,309,391, gapNum = `103,309,200 - 450 = 103,308,750` — the gap
header IS present in the DB (it sits inside A.68.2's 1,800-header window),
but `loadSnapshot(x.db, gapHeader.Hash())` returns `not found` because no
code path ever stored a snapshot at that hash for this fast-sync session.

Fallback chain in `getSnapshot`:

1. Cache miss (line 800).
2. DB lookup miss (line 818).
3. Seed-from-epoch-switch path (line 863+). For the
   `!isGapInput && parents != nil` case (single-block InsertChain has
   non-nil but empty parents from the BlockChain side), the switch at
   line 920-927 returns an error and defers to `verifyHeader`'s fallback.

`verifyHeader.go` line 153 `calcMasternodes(...)` errors → bulk-sync
fallback at line 167 calls `GetMasternodesWithParents`. Inside:

4. DB path `getEpochSwitchInfo` (line 1187): fails with
   `parent header X not in DB` once the recursion walks past 103,307,405
   (the bottom of A.68.2's prefetch window — the chain has no headers
   below this point).
5. Parents-fallback `getEpochSwitchInfoWithParents`: same failure, parents
   slice is empty.
6. Deep fallback (line 1200-1238): no V1-era epoch-switch within parents
   range, no V2 round-0 in parents.
7. Trusted-checkpoint catchup fallback (line 1245-1257): correctly enters
   the `inCheckpointCatchup` window, looks up `loadSnapshot(x.db, anchorHash)`
   — **fails** because no snapshot was ever persisted at the anchor hash.

All fallbacks fail. `verifyHeader` returns `ErrValidatorsNotLegit` (line 245
in the fullVerify branch). `InsertChain` rejects. Repeat ad infinitum.

The condition that triggers this:
- `core.XdcBulkSyncMode` was flipped to `false` at `SyncCompleted` (eth/sync_xdc.go:1246).
- `verifyHeaderV2` (xdpos.go:738) computes `fullVerify := !XdcBulkSyncMode.Load() = true`.
- The "trust header.Validators on rotation mismatch" bypass at verifyHeader.go:223
  only fires when `!fullVerify`.

## Options considered

**Option A — Auto-seed snapshots at pre-fetched epoch-switch headers (CHOSEN)**

After A.68.2 writes the 1,800 ancestor headers, walk them low→high looking
for V2 epoch-switch headers (`engine.IsEpochSwitch(header) == true`). For
each found, compute the gap-block number for the epoch it OPENS (block-aligned:
`gapNum = E.Number - E.Number%Epoch - Gap`), fetch the gap header from the
local DB (it's in the prefetched window by construction — the prefetch
covers `pivot - 2*Epoch + 1 .. pivot - 64`), and persist a snapshot via
`engine.UpdateMasternodesWithPenalties(chain, gapHeader, ms, penalties)`.
Masternodes come from `header.Validators` of the epoch-switch header
(canonical active set for that epoch). Penalties come from `header.Penalties`.

Additionally, seed a snapshot at the operator pivot's own hash (extracted
masternodes are inherited from the immediately-prior epoch-switch header in
the prefetch window) so `GetMasternodesWithParents`' trusted-checkpoint
catchup fallback succeeds for blocks where the gap-key path doesn't fire.

Pros:
- Natural extension of A.68.2 — the headers are already in memory.
- No protocol changes, no operator burden, no new CLI flags.
- Snapshot is built from the EPOCH-SWITCH HEADER's own Validators field
  — the canonical source the engine itself uses elsewhere
  (`GetMasternodesFromEpochSwitchHeader`, engine.go:1128).
- Idempotent: `storeSnapshot` is unconditional put; repeated A.70 runs are
  safe.
- Zero impact when there's no operator pivot (the prefetch doesn't run).

Cons:
- Adds ~10ms of work to syncer startup (one IsEpochSwitch call per header,
  ~2 snapshot writes per call).
- Relies on the prefetch window containing at least one epoch-switch
  header; for a 2*Epoch=1800-header window covering `pivot - 1800 .. pivot - 64`,
  this is guaranteed (epochs are ≤ 900 blocks).

**Option B — CLI-provided masternodes (`--fastsyncpivotmasternodes`)**

Operator queries a synced node for `XDPoS_getMasternodesByNumber` and
passes the comma-separated list. Pro: explicit, auditable. Con: another
flag operators must remember; redundant with data already in
`header.Validators`.

**Option C — Trust `header.Validators` during the catchup window**

When `inCheckpointCatchup(chain, blockNum) == true`, skip
`CompareSignersLists` and accept `header.Validators` as authoritative.
Pro: tiny diff. Con: weakens consensus correctness — a malicious peer
could in principle feed a header with bogus Validators that the operator
pivot's trust anchor wouldn't catch in a chain-extension attack at
exactly the right point in the catchup window. The verify branch at line
223 already takes this trade-off for bulk-sync; extending it to fullVerify
inside the catchup window is defensible but feels like wallpapering over
the real gap (an empty snapshot store) rather than fixing it. Defer.

**Option D — Fetch headers all the way to the next epoch boundary**

Extend A.68.2's pre-fetch FORWARD: download headers
`pivot .. pivot + Epoch` so the validator has the complete current-epoch
context. Pro: comprehensive. Con: BeaconSync is supposed to be doing this
walk-forward anyway — duplicating it in the syncer is brittle and adds
peer load. The forward headers will arrive via BeaconSync regardless;
the missing piece is just the SNAPSHOT, which can be derived from
already-available data (Option A).

## Decision

Option A. Document the seed semantics in code (file comment + per-call log
line) so future operators can grep the chain logs to confirm seeding
fired. ADR 070 records the rationale.

## Validation

- Native build on xdcscan (95.217.106.210:30590).
- Snapshot: `/root/snapshots/xdcscan-a67-state-T3p-1780804229.tar.zst` (8.0GB).
- Restore + restart with A.70 binary.
- Success: `eth_blockNumber` climbs past 103,309,390 (current stall) and
  approaches chain tip (~103,484,500) within 30 minutes.
- Failure: still "validators not legit" or a different new consensus error.

## Hard constraints satisfied

- DO NOT break A.68 (QC stall fix) or A.69 (peer-starvation bypass): A.70
  only adds work AFTER A.68.2 has already succeeded; the entire seed step
  runs inside the existing `prefetchAncestorEpochs` path with no edits to
  the QC verification or peer-starvation code paths.
- Match upstream geth code style: idiomatic Go, log.Warn/Info, single
  best-effort helper with early returns on failure.
- Commit prefix: `consensus/xdpos:` (engine-level seed export) and
  `eth/sync_xdc:` (caller-side seed invocation).
- Reference issue: #844 #859.
- ADR mandatory: this file.
- PR against `reset/integration-1.17.3`.
