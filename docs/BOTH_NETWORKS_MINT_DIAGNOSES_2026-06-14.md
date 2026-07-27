# Modern geth — mint on BOTH networks: two confirmed diagnoses + fix specs (2026-06-14)

Task #193. Two background Opus/agent diagnoses, both verified against live nodes. Each fix is
**hash-changing → MUST be parity-gated before deploy.**

## ⚠️ CORRECTION (verified by direct probe) — A is a FAST-SYNC MISSING-BODY bug, NOT reward logic

Direct probe of the missed filter block (logged by `[V2-SIG-MISS] filterBlock=83,048,445`, a
MID-window block — so the agent's "boundary off-by-one" was wrong too):
- block 83,048,445: modern hash `0x88478a7a` == canonical `0x88478a7a`, both 0 txs.
- block 83,048,446: modern hash `0x69f12a8a` == canonical `0x69f12a8a` (**same hash, no fork**),
  but **modern body = 0 txs while canonical = 32 txs** (the 32 signing txs for block 83,048,445).

`GetSigningTxCount` is CORRECT — it reads an EMPTY body. The validator **fast-synced and left empty
placeholder bodies** in the V2 reward lookback window, so the 32 signers of 83,048,445 are absent →
57/58 → totalSigner 1824 vs 1856 → wrong reward → wrong root. This is the A.74 `repairAnchorGapBodies`
class (#844/#859): that repair covers only [pivot-128, pivot-1], but the V2 reward lookback is
2×Epoch (~1800 blocks) below the pivot, so 83,048,446 is outside the repaired range.

### ⚠️⚠️ FOLLOW-UP (verified): the pivot body is FROZEN — in-place backfill cannot fix it
The widened repairAnchorGapBodies (6c4f8f7c5) correctly DETECTS the gap and writes the real body
(`real_bodies_written=1`), but the body is NEVER visible on read (`txCount@83048446=0x0`, V2-SIG-SUMMARY
stays 57/58, even across cold restarts). Root cause: block 83,048,446 is the fast-sync PIVOT, written
into the **immutable ancient freezer** (`.../XDC/chaindata/ancient/chain/` bodies table; restart logs
`Truncating freezer table table=bodies`). `rawdb.WriteBody` writes to the recent kv store, but
`ReadBody`/`ReadBlock` for a frozen block read the FREEZER — so the kv write is shadowed. The freezer
is append-only; you cannot overwrite a frozen block's body in place.
CONSEQUENCE: repairAnchorGapBodies (kv write) can fix NON-frozen placeholder gaps but NOT the frozen
pivot. The apothem validator cannot mint until it has the real pivot body. Options:
 (A) Fix PrimeFastSyncAnchor to fetch + freeze the REAL pivot/anchor bodies during fast-sync (so no
     placeholder is ever frozen) → then a FRESH re-sync produces a correct chain. [proper fix + re-sync]
 (B) Sync the validator in --syncmode full (no freezer placeholders) — slow for 83M blocks.
 (C) Restore from a clean FULL-BODY snapshot + full-sync the tail.
All are multi-hour. The widened-repair commit stays (it fixes the dynamic-pivot no-op + non-frozen
gaps), but it is NOT sufficient for the frozen pivot. The minting/consensus code is correct (proven on
devnet); this is purely a fast-sync data-completeness limitation.

**(original, for non-frozen gaps) Correct fix = backfill full bodies for the V2 reward window after fast-sync (NOT a reward-logic
edit).** Either (a) extend repairAnchorGapBodies (sync_xdc.go:2146, scanWindow 128 → 2×Epoch) to cover
the reward lookback, or (b) on-demand: when GetSigningTxCount finds an empty body for an in-window
block whose header.TxHash != EmptyTxsHash, fetch the real body from a peer and write to rawdb, then
retry. Escalate fix DESIGN to Opus (architectural, sync-layer). Do NOT touch reward.go / GetSigningTxCount
arithmetic — they're correct. Parity gate unchanged: re-import 83,049,946 → root == dfe847c6.

---
## A. (SUPERSEDED — see correction above) Apothem #187 — first hypothesis: wrong reward AMOUNT

**Not spurious firing.** Block 83,049,946 IS a legitimate V2 round-based epoch-switch (extraData
round 26,813,700; 26,813,700 % 900 == 0; parentRound 26,813,699 < epochStartRound → IsEpochSwitch
true on BOTH modern engine.go:1488 and legacy epochSwitch.go:183). HookReward firing is correct;
canonical fires it too (block has 0 txs, gasUsed=0 — reward is the only state change).

**The bug = wrong signer count.** Modern `GetSigningTxCount` counts **57 of 58** signing-filter
blocks (off-by-one at the window boundary) → `totalSigner=1824` instead of **1856** → every
per-signer `calcReward_i = chainReward/totalSigner * sign_i` is a few wei low → wrong state root.
- Modern diagnostic: `V2-SIG-SUMMARY countloop_filter_matches=57 total_filter_blocks=58`.
- Foundation `0x746249c6…` final: modern …073903 vs canonical …073935 (**32 wei low** = 1/credit×32).
- Per-owner credit: modern 140624999999999999974 vs canonical …982 (**8 wei low**).
- Reward arithmetic (`CalculateRewardForSigner`, `GetRewardBalancesRate`) is bit-correct; #186
  foundation-skip is inert here. The divergence is entirely in the signer-count window.

**Fix location:** `eth/hooks/engine_v2_hooks.go` `GetSigningTxCount` (lines ~308-572). The V2
epoch-switch walk-back (lines ~368-405) sets `startBlockNumber`/`endBlockNumber`; the scan loop
(415) + count loop (521-522, `if i%MergeSignRange==0`) drop one filter block (the edge filter
block, e.g. first multiple-of-15 in the window, whose `data[mapBlkHash[i]]` is unpopulated). Filter
window `[83048185, 83049051]` has 58 multiples of 15 (first 83,048,190, last 83,049,045); modern
covers 57. Align the window+scan to legacy `contracts/utils.go GetRewardForCheckpoint` coverage so
every filter block `i` has `mapBlkHash[i]` populated → 58/58 → totalSigner 1856.

**Parity gate (mandatory):** after fix, modern `V2-SIG-SUMMARY` must show 58/58 totalSigner=1856;
re-import 83,049,946 → local root == canonical `0xdfe847c6457e3945006edf5a1724a6426254b017fd0763b054c768c50a59833a`;
re-check prior epoch reward blocks (83,049,046 / 83,048,146 / 83,047,246) still match (no regression).
Branch: feat/m-xdpos-worker (apothem validator, host 135.181.117.109, /root/.xdc-validator-apothem).

## B. Devnet under-production — MinePeriod timestamp mismatch (xdc04 mints ~2.6% vs 25%)

**Confirmed via legacy node1 log:** legacy rejects every xdc04-proposed block (peer d873…, heights
3172/3190/3211/3229…) with `[verifyHeader] invalid timestamp ParentTime=… MinePeriod=3
HeaderTime=ParentTime+2` → round times out → a legacy node re-mints the slot. 48 rejects counted.

**Two parts:**
1. **Modern stamps wrong:** `engine_v2/engine.go:683` `header.Time = parent.Time + x.config.Period`
   uses the static V1 Period (=2) instead of the per-round V2 MinePeriod that its OWN verifyHeader
   uses (`verifyHeader.go:91 x.config.V2.Config(round).MinePeriod`). Fix: stamp with the same
   per-round MinePeriod source (round/currentRound is available in Prepare — `currentRound`).
2. **Effective MinePeriod mismatch:** legacy enforces **3**, but devnet genesis.json + genesis-
   modern.json + both hardcoded params/config.go V2 configs all say **2**. Legacy's 3 is baked into
   its chaindata (init-time config), origin not in the current genesis file. To match the running
   network, modern must stamp/enforce MinePeriod=3 on this devnet. Resolve legacy's 3 origin
   (likely an init-time genesis with minePeriod=3, or an XDPoSChain V2 default/+1), then set modern's
   devnet V2 MinePeriod=3 to match. NOTE: changing modern's DB-baked config may require re-init
   (wipe+resync xdc04) — confirm whether modern reads hardcoded vs DB config first.

**Not the cause (ruled out w/ evidence):** #156 double-Ecrecover (QC forms in 0.7–9ms; already
mitigated via UniqueSignatures), clock skew (NTP-synced; legacy is ahead, wrong direction).

**Status note / correction:** xdc04 is a faithful VALIDATOR (syncs, votes, hash-matches canonical)
and DOES get ~2.6% of blocks accepted, but the earlier "V2 minting fully validated" was overstated —
most xdc04-proposed blocks are rejected on this timestamp mismatch. Fixing B makes it a fair co-producer.

## Order
1. **A first** (apothem blocker, well-grounded, parity-gateable) — implement + parity-gate root==dfe847c6 + deploy validator + sync past 83,049,946 → tip → mint V2.
2. **B** (devnet) — resolve legacy MinePeriod=3 origin, align modern, fix engine.go:683, redeploy xdc04, re-measure share → ~25%.
3. Comprehensive validation workflow (all tests, both nets) → report #951 → close loop.
