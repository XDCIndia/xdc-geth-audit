# ADR 074 — A.74: Fix placeholder-body gap in PrimeFastSyncAnchor anchor window

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-072 (pre-fetch bodies), ADR-073 (bulk-sync defer)

## Context — The reported symptom

After A.73 fixed the `validators not legit` stall, geth-a73 on xdcscan
advanced past the pivot (103,309,269) but stalled again at block 103,310,288
with a different error:

```
ERROR ########## BAD BLOCK ##########
Block: 103310288
Error: invalid merkle root (remote: 0x1234… local: 0xabcd…)
```

The `[V2-SIG-MISS]` diagnostic code deployed in geth-a74 (diagnostic) revealed:

```
WARN [V2-SIG-MISS] filterBlock=103309215 hashKnown=true addrCount=0
WARN [V2-SIG-MISS] filterBlock=103309230 hashKnown=true addrCount=0
WARN [V2-SIG-MISS] filterBlock=103309245 hashKnown=true addrCount=0
WARN [V2-SIG-MISS] filterBlock=103309260 hashKnown=true addrCount=0
```

4 of the 60 filter blocks in the reward window had no signing tx data.
`totalSigner = 6045` instead of canonical `6047`, causing wrong per-signer
rewards and a stateRoot divergence.

## Root cause analysis

### Step 1 — Wrong txCount in rawdb

`rawdb.ReadBlock(db, hash, 103309261)` returned a block with 0 transactions.
The canonical network (`erpc.xinfin.network`) shows block 103,309,261 has
245 transactions. Conclusion: the rawdb body for this block is a **placeholder**.

### Step 2 — Where placeholders come from

`PrimeFastSyncAnchor` (downloader.go, lines 520-527) writes `&types.Body{}`
for EVERY header in the fast-sync anchor window:

```go
for _, h := range headers {
    rawdb.WriteBody(d.stateDB, hash, num, &types.Body{})      // ← placeholder
    rawdb.WriteReceipts(d.stateDB, hash, num, types.Receipts{})
}
```

The comment explains why: `findBeaconAncestor` calls `HasFastBlock()` which
requires `HasBody + HasReceipts` for every block in the window. Placeholder
bodies satisfy this gate.

### Step 3 — A.72 pre-fetch range mismatch

`prefetchAncestorEpochs` (sync_xdc.go line 1842-1945) computes:

```
primeBaseNum = primeBase.Number.Uint64()   // = pivot - 64 = 103,309,205
originalHi   = primeBaseNum - 1            // = 103,309,204
```

It calls `prefetchAncestorBodiesAndReceipts(peer, allHeaders, lo, originalHi)`,
fetching bodies for `[lo..103,309,204]`. The gap `[103,309,205..103,309,268]`
= `[primeBaseNum..pivot-1]` = 64 blocks is **never fetched**.

### Step 4 — `PrimeAncestorBodies` idempotency check

Even if we had attempted to write to this gap, `PrimeAncestorBodies` has:

```go
if !rawdb.HasBody(d.stateDB, hash, num) {
    rawdb.WriteBody(batch, hash, num, bodies[i])
} else {
    skippedBodies++     // ← PrimeAnchor already wrote a placeholder → skip
}
```

`PrimeFastSyncAnchor` ran first and wrote placeholders for the anchor window.
`PrimeAncestorBodies` sees `HasBody=true` and skips writing the real body.

### Step 5 — Which 4 filter blocks are affected

Filter blocks in the reward window `[103,308,495..103,309,390]` that fall in
the gap `[103,309,205..103,309,268]` and have `N % 15 == 0`:

| filter block | block holding signing txs |
|---|---|
| 103,309,215 | 103,309,216 or later |
| 103,309,230 | 103,309,231 or later |
| 103,309,245 | 103,309,246 or later |
| 103,309,260 | 103,309,261 (245 txs in canonical) |

All 4 are in `[103,309,205..103,309,268]` → all have placeholder bodies →
0 signing txs seen → `totalSigner` undercounted by 4 → wrong stateRoot.

## Decision

Three-part fix (A.74):

### Part 1 — `PrimeAncestorBodies` overwrite logic (downloader.go)

Replace the hard `HasBody` idempotency check with a conditional overwrite:
if the incoming body has real transactions (`len(bodies[i].Transactions) > 0`)
AND the stored body has 0 transactions AND the header's `TxHash != EmptyTxsHash`,
then the stored body is a placeholder — overwrite it.

Apply the same rule to receipts: if we overwrote a placeholder body, also
overwrite the placeholder receipts for that block.

### Part 2 — Gap filler in `prefetchAncestorEpochs` (sync_xdc.go)

After the A.72 pre-fetch (`lo..originalHi`), fetch headers for the gap
`[primeBaseNum..pivotNum-1]` from the same peer and call
`prefetchAncestorBodiesAndReceipts`. This covers fresh fast-sync runs going
forward: real bodies get written at fast-sync time, never leaving placeholders
in the gap.

### Part 3 — Startup repair in `repairAnchorGapBodies` (sync_xdc.go)

For nodes already synced with geth-a73 or earlier (the existing xdcscan
deployment), the placeholder bodies are on disk. A one-shot startup repair:

1. Called from `fullSyncOnce` whenever `!anchorGapRepaired` and a peer is
   available.
2. Scans `[pivot-128..pivot-1]` for blocks where `header.TxHash != EmptyTxsHash`
   but `rawdb.ReadBody().Transactions == 0`.
3. Fetches real bodies from the peer via `fetchBodiesBatch`.
4. Writes directly to rawdb via `db.NewBatch() + rawdb.WriteBody` — no
   `HasBody` gate.
5. Sets `anchorGapRepaired=true` on success or when no repair is needed.
   Does NOT set on transient failure (retry on next 500ms tick).

## Consequences

* Fresh fast-sync runs: real bodies for the 64-block gap are written during
  `prefetchAncestorEpochs`, before the anchor window placeholders could
  cause any mismatch. Part 1 (overwrite) acts as belt-and-suspenders.
* Nodes restarted with existing chaindata: the startup repair fires once in
  the first `fullSyncOnce` call that has a peer available, writes the 4–64
  real bodies, and then InsertChain for block 103,310,288 succeeds because
  HookReward's signing-tx walk now sees the correct transaction counts.
* No chaindata wipe required for the xdcscan deployment.
* Zero security surface widened: the bodies are fetched from a connected
  peer (same trust model as the A.72 pre-fetch) and written to rawdb exactly
  as the canonical network delivered them.

## What this does NOT do

Does not change the PrimeFastSyncAnchor placeholder-write logic itself. The
placeholders are still needed for the `findBeaconAncestor` gate (see original
comment). The fix is in how PrimeAncestorBodies handles the case where
placeholders exist and real data arrives later.

## Validation plan

1. geth-a74 (fix build) starts on xdcscan against existing chaindata.
2. First `fullSyncOnce` with peers available logs:
   - `"xdcSyncer: A.74 gap repair — placeholder bodies detected" count=N`
   - `"xdcSyncer: A.74 gap repair — complete" real_bodies_written=N`
3. Next InsertChain for block 103,310,288 logs
   `"xdcSyncer: imported batch" from=103310288` — no BAD BLOCK error.
4. Chain continues advancing toward mainnet head.
5. Eventually logs `"TIP_CATCH complete; transitioning to AT_TIP"` or
   the A.73.1 watchdog `"TIP_CATCH watchdog forcing AT_TIP transition"`.

A.74 is green when step 3 passes without any `invalid merkle root` error.
