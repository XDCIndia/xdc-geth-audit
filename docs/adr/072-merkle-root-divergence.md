# ADR 072 — A.72: Fast-sync merkle-root divergence at first post-pivot epoch switch

* **Status**: Accepted
* **Date**: 2026-06-07
* **Refs**: #844, #859, ADR-068 (QC ancestor pre-fetch), ADR-069 (peer-starvation
  bypass), ADR-070 (post-pivot masternode seed), ADR-071 (block-import handoff)

## Context

Fast-sync against mainnet repeatedly fails at the same point with:

```
invalid merkle root
  remote: 4ee52c52e901e2306ead08ccdf1f743e3f5c531569ce37654a98e7b306bf1d46
  local:  c6d53d2eddf9780f312495e3a6a44de540f19643c696968973665ea92829b7e3
```

The block where this fires is **103,309,391** (operator pivot + 122 on the
xdcscan node). Cross-referencing canonical (legacy v2.7.0) via
`eth_getBlockByNumber`:

| block | canonical stateRoot | txs |
|---|---|---|
| 103,309,390 | `c6d53d2e…29b7e3` | 1 |
| 103,309,391 | `4ee52c52…6bf1d46` | 0 |

So the "local" root reported by the validator is **block 390's canonical
post-state**, and the "remote" is block 391's. Modern v1.17.3 has correctly
applied block 390 but **fails to mutate state at all when processing block
391**, even though canonical *does* mutate state there.

### Why block 391 is special

Block 391 has zero transactions and `gasUsed = 0`, so there is no EVM execution
to diverge on. But decoding the V2 `extraData` shows:

* `round = 23,228,100` (= 25,809 × 900 — an exact epoch multiple)
* `parentRound = 23,228,099`
* `epochStartRound = round − round%900 = 23,228,100`
* `isEpochSwitch = parentRound < epochStartRound → TRUE`

Block 391 is the **first V2 epoch-switch after the operator pivot**, so the V2
`HookReward` must fire and distribute masternode + holder rewards. Those
`AddBalance` calls are the per-epoch state mutation that produces canonical's
post-state root.

### Why HookReward computes zero on modern

Live logs from the run that produced the failure confirm `HookReward` does
fire, but the signing-tx counting walks ~895 ancestor blocks and finds 0
signers:

```
[Finalize] V2 HookReward firing            block=103,309,391
GetSigningTxCount starting                 currentBlock=103,309,391 scanFrom=103,307,599 scanTo=103,308,493 isV1=false
[V2-SIG] block not found in rawdb          number=103,309,204 hash=513c9b…fad5b7
[V2-SIG] block not found in rawdb          number=103,309,203 hash=786865…a10728
...
[V2-SIG-SUMMARY]                           data_map_entries=8 countloop_filter_matches=0 masternodes_len=108
Calculate reward at checkpoint             totalSigners=0 uniqueSigners=0
```

Every entry in the scan window logs `block not found in rawdb`. The reward
hook (`eth/hooks/engine_v2_hooks.go:GetSigningTxCount`) calls
`rawdb.ReadBlock(... )` for each ancestor — which needs the **block body**
(transactions). It then calls `rawdb.ReadRawReceipts(... )` to look up signing
transaction status — needs **receipts**. Neither is present.

The reason is structural: fast-sync only pulls bodies + receipts for the
**post-pivot** range (and for the pivot block itself for state-sync). The
~895 blocks the reward window needs are all **pre-pivot ancestors**. A.68.2
already pre-fetches the headers for this range (so the V2 engine's
`getEpochSwitchInfo` recursion can resolve validator sets), but bodies and
receipts were never pre-fetched because nothing before now needed them.

### Why this only manifests at the first post-pivot epoch switch

* Pre-pivot, fast-sync skips block-import entirely (state-sync only)
* Block 391 is the **first** post-pivot block whose `Finalize` invokes
  `HookReward`
* Earlier post-pivot blocks (pivot+1..pivot+121) are non-epoch-switch — they
  finalize without any state mutation, so the missing bodies don't matter
* Subsequent epoch-switches would hit the same failure if 391 were skipped

## Decision

After A.68.2 prefetches ancestor **headers** and A.70 forward-fetches the
near-future epoch headers, also pre-fetch the corresponding **block bodies
and receipts** for the same ancestor range. Persist them to rawdb so
`rawdb.ReadBlock` and `rawdb.ReadRawReceipts` can resolve them from disk
during `HookReward`'s signing-tx walk.

Concretely, in `eth/sync_xdc.go`:

1. New `fetchReceiptsBatch(peer, hashes)` helper that issues
   `peer.RequestReceipts(...)` with a per-call response sink and decodes
   `eth.ReceiptsRLPResponse`. This mirrors `fetchBodiesBatch` for the
   XDPOS2 wire path.
2. After `prefetchAncestorEpochs` completes its header-only walk, call a new
   `prefetchAncestorBodiesAndReceipts` that:
   * Iterates the same `[lo, hi]` range in `batchCap`-sized batches
   * For each batch: collects the hashes, requests bodies + receipts,
     persists them via `rawdb.WriteBody` + `rawdb.WriteReceipts`
   * Best-effort: any peer-side failure logs + breaks out, preserving
     whatever was written so far. We rely on multiple ancestor pre-fetch
     cycles to backfill if a single peer drops.
3. Call this from the existing `prefetchAncestorEpochs` tail, immediately
   after `seedMasternodeSnapshotsFromPrefetch`. The range covered is
   `[primeBase − 2*Epoch, primeBase − 1]` = 1,800 blocks — same as the
   header pre-fetch.

The walk depth (2 × Epoch) is intentionally one full reward window: the
`GetSigningTxCount` scan-back is `2 × rCheckpoint = 1,800` blocks for a
mainnet/Apothem reward at the first post-pivot epoch-switch. Pre-fetching
the same range gives byte-identical inputs to canonical's `HookReward`.

This is the minimal change that resolves the divergence without altering
consensus rules, reward math, or any state-mutating code path.

## Alternatives considered

1. **Compute rewards from headers alone (no body fetch).** Rejected: the
   `signing transaction` mechanism is fundamental to XDPoS V2 reward
   accounting — they are real on-chain transactions whose sender (recovered
   via ecrecover) determines the per-masternode sign count. Headers alone
   carry no transaction data.

2. **Tag the reward hook to no-op when bodies are missing.** Rejected:
   any path where modern produces a different post-state from canonical at
   block 391 means the merkle-root check fails. We cannot opt out of the
   reward distribution — canonical applied it.

3. **Move HookReward to the wrapper level, gated on body availability.**
   Rejected: same reason as (2). HookReward is the canonical state
   mutation; we need it to fire and produce the canonical mutation.

4. **Fetch bodies/receipts lazily on-demand inside HookReward.** Rejected:
   `HookReward` is invoked inside `Finalize`, which has no peer context and
   no upgrade path for synchronous wire I/O. Doing it in the sync layer is
   structurally cleaner.

5. **Persist bodies+receipts during state-sync.** Rejected: state-sync
   operates on state trie nodes and doesn't touch block-body storage.
   Layering body fetch into state-sync is a much larger structural change
   than mirroring the existing A.68.2 ancestor-header walk.

## Consequences

### Positive

* Block 391 imports successfully — `HookReward` fires with the full ancestor
  signing-tx set, distributes rewards identically to canonical, post-state
  matches.
* All subsequent epoch switches inside the 1,800-block window get the same
  benefit at no additional cost.
* Future epoch-switches (block 391 + 900, +1800, …) will need the next
  reward window's bodies+receipts. Those will become available naturally as
  block-import proceeds forward from block 391 — each block's body+receipts
  are written by the normal `InsertChain` path.

### Negative

* One extra round-trip per ~192-block batch over the ancestor range
  (~10 round-trips total for 1,800 blocks of bodies + 10 round-trips for
  receipts). Each batch is small; the existing ~30-second pre-fetch budget
  has plenty of headroom.
* Best-effort: a flaky peer can leave gaps in the body cache. The retry on
  next sync cycle will fill them, but the first attempt may still fail at
  block 391. Operators currently restart the node after a state-sync to
  retry; this remains the documented recovery path.

### Neutral

* No new public APIs. The `fetchReceiptsBatch` helper is package-private and
  uses the same `peer.RequestReceipts` already used by the downloader's
  receipt fetcher.

## Validation plan

Same harness as A.68/A.69/A.70/A.71:

* Snapshot restore on the xdcscan server (8.0 GB chaindata containing the
  state-sync output, 83.3M state entries preserved at the pivot).
* Build natively on server: `git fetch && checkout fix/a72-merkle-root-divergence && go build`.
* Restart geth.
* Watch for `Imported new chain segment number=103,309,391..` and
  `eth_blockNumber` climbing past 103,309,390 over the next few minutes.
* Verify HookReward log line shows non-zero totalSigners and uniqueSigners,
  and `[V2-SIG] block not found in rawdb` is gone.

## Validation result — 2026-06-07 08:22-08:26 CEST

**PASSED.** The merkle-root divergence is resolved.

### v1 deploy (commit `8d71dd348`, batchCap=192)
Failed with partial success: HookReward fired and computed totalSigners=4536,
uniqueSigners=108 (up from 0/0 pre-A.72), but local state root was
`bde9c78f…` vs canonical's `4ee52c52…` — still a mismatch. Root cause: my
`batchCap=192` exceeded the production XDC peer's per-response cap (128
bodies). The loop took `pairCount=128` correctly but advanced
`start = end + 1` (+192 ahead), silently skipping 64 blocks per batch. Net:
1224/1800 bodies written, 576 missing — signing-tx counts were too low.

### v2 deploy (commit `bf4c98728`, batchCap=128 + last-written advance)
PASSED. `xdcSyncer: A.72 ancestor body+receipt pre-fetch complete
bodies_written=1800 receipts_written=1800 failed=0 lo=103,307,405
hi=103,309,204` — full coverage. The first post-pivot epoch-switch block
imports successfully:

```
INFO Imported new chain segment number=103,309,391 hash=517ef2..a58dd9 blocks=122
INFO Imported new chain segment number=103,309,397 hash=b0d966..6311a2 blocks=6
INFO Imported new chain segment number=103,309,525 hash=6d5f83..193954 blocks=128
INFO Imported new chain segment number=103,309,653 hash=8c6800..2b7a05 blocks=128
```

`eth_blockNumber` climbed from `0x628604e` (pivot+121, the v1 ceiling)
to `0x62860f5` (pivot+384) and continued advancing. The stateRoot
returned by `eth_getBlockByNumber(0x628604f)` is
`0x4ee52c52e901e2306ead08ccdf1f743e3f5c531569ce37654a98e7b306bf1d46` —
byte-identical to canonical.

A.72 acceptance: `eth_blockNumber > 103,309,390` — **satisfied.**
