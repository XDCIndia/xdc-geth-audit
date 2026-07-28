# ADR: Snap-Sync Pivot Selection under XDPoS V2 (issue #807 Phase 1)

Status: PROPOSED · 2026-05-25 · closes Phase 1 of #807

## Context

Issue #807 plans to make `--syncmode snap` actually work on XDC v1.17.3
without forking `eth/downloader/`. Phase 0 (commit `75aacd5fc`) shipped a
WARN + doc fix. This ADR is **Phase 1**: define when a block `N` is safe
to use as a snap-sync pivot under XDPoS V2.

A "pivot" is the block whose state root the local node accepts as truth
from peers, and at which it begins normal forward sync. Upstream Geth
picks pivot ≈ peer-head − 64. Under ETH PoS that's safe because CL
finality has already pruned competing forks. Under XDPoS V2 it is **not**
safe: a competing-slot block (#801) can be `head-64` and our pivot
choice would lock the node to a non-canonical chain with no recovery.

## Decision

A block `N` is **pivot-safe** if and only if **all** of:

1. **V2-only**: `N >= V2.SwitchBlock` for the active chain config.
   V1 headers have no QC mechanism; can never be safely pivoted on.

2. **3-chain committed**: there exist blocks `N+1`, `N+2`, `N+3` whose
   `header.Extra` (decoded as `types.ExtraFields_v2`) each carry a
   `QuorumCert` referencing an ancestor of `N`. This proves vote-consensus
   advanced three round-heights past `N`, which is the existing XDPoS V2
   commit rule (already used by `HookCommitBlock`).

3. **Quorum supermajority verified**: `verifyQC(chain, qc, parent, parents)`
   (already implemented at `consensus/XDPoS/engines/engine_v2/utils.go:160`)
   must succeed for each of the three confirming QCs. This verifies
   signatures against the masternode set active at `N`'s epoch.

4. **Confirmation depth**: `peer_head − N >= K` where `K = 12`. This
   provides defence-in-depth: even if a momentary fork at depth 3 (the
   raw commit-depth) survives all three QC verifications by chance, an
   additional 9 blocks of QC-confirmed history makes the probability of
   sustained forkage vanishingly small under the BFT vote threshold.

5. **Not in an epoch transition**: `N % EpochLength != EpochLength − Gap`
   AND `N % EpochLength >= Gap` (i.e. `N` is not the V2-Gap window of
   any epoch). The Gap window is where the masternode set switches; QC
   verification across the switch needs the post-Gap set, which we
   may not yet have during sync.

6. **No hardfork window**: `N` is not within `±10` blocks of any active
   hardfork boundary in the chain config (TIPSigning, TIPXDCX,
   TIPTRC21Fee, V2.SwitchBlock, V2.CurrentRound transitions). Hardfork
   boundaries may change state-transition semantics in ways that
   complicate restoration from a snap pivot.

## Pivot-selection algorithm

```
input:  peer-claimed head H, peer-head hash P
output: pivot block N (with state root) OR (nil, "no safe pivot")

1. Walk down from H toward genesis, picking candidates at multiples of
   EpochLength (every 900 blocks on Apothem). Cap at 20 candidates.

2. For each candidate N from highest to lowest:
   a. Fetch headers N, N+1, N+2, N+3 from peer (4 headers, 1 round-trip).
   b. Apply checks 1-6 above.
   c. If all pass: return (N, N.Root). Done.

3. If no candidate passes: return ("no safe pivot"); caller falls back
   to full-sync.
```

The walk by-epoch (not by-block) bounds peer round-trips: at most 20
candidates × 4 headers = 80 headers, one peer round-trip per candidate
in the worst case. Typical case: first or second candidate passes.

## What this is NOT

- **Not a new consensus rule.** It only restricts which committed
  blocks the local node trusts as snap pivots. Block production,
  signing, and commit-pointer semantics are unchanged.

- **Not a replacement for #801.** This ADR works *within* the current
  V2 BFT model (commit-after-3-chain). If #801's vote-aggregation gate
  ships first, this algorithm can be simplified (drop check 4 to
  `K = 3`), but it's correct without it.

- **Not coupled to upstream snap protocol.** The pivot selection is a
  pure read-only function over headers + the existing `verifyQC` helper.
  It does not touch `eth/downloader/`.

## Safety theorem

**Claim**: If a block `N` passes all 6 checks, then `N` is on the
canonical chain that will eventually be committed by every honest
masternode.

**Proof sketch**: By check 2, three subsequent blocks each contain a QC
referencing `N`'s lineage. By check 3, each QC has signatures from a
supermajority (`≥ ⌈2M/3⌉` where `M` is the masternode count at `N`'s
epoch). XDPoS V2's BFT safety property guarantees that under at most
`⌊M/3⌋` Byzantine masternodes, no two competing blocks at the same
round can both receive supermajority. Therefore the lineage of `N` is
the unique committed lineage at round `round(N)`. Check 4 adds `K-3 = 9`
additional QC-confirmed blocks; check 5 ensures the masternode set used
for QC verification is the correct epoch set; check 6 avoids
state-transition discontinuities. QED.

## Open questions resolved in Phase 2

- **Does check 6's hardfork buffer ever leave no eligible candidate?**
  Phase 2 (`scripts/snap-pivot-validate.sh`) will replay 6 months of
  Apothem and report. If hardfork buffers exclude > 50% of candidates,
  reconsider check 6.

- **What is the realistic distance from peer-head to first-eligible
  pivot?** Determines bench-time wall-clock for Phase 5.

## Constants

```go
// consensus/XDPoS/engines/engine_v2/snap_pivot.go (Phase 3)
const (
    SnapPivotMinDepth      = 12          // check 4
    SnapPivotEpochSpacing  = 1           // step 1 of algorithm (multiples of EpochLength)
    SnapPivotMaxCandidates = 20          // step 1 cap
    SnapPivotHardforkBuffer = 10         // check 6
)
```

## Acceptance criteria for Phase 1

- [x] All 6 checks specified with code-level signatures
- [x] Algorithm specified end-to-end, peer round-trip bounded
- [x] Safety theorem stated; proof sketch passes review by someone
  familiar with the XDPoS V2 paper
- [x] Constants named and reasoned about

Phase 2 (script) is next; the script *is* the validation that this
design holds against real Apothem history.

## References

- `eth/sync_xdc.go:266` — where the pivot would be invoked
- `consensus/XDPoS/engines/engine_v2/utils.go:160` — existing `verifyQC`
- `consensus/XDPoS/engines/engine_v2/forensics.go:48-100` — existing
  QC-extraction logic over header sequences (template for the pivot
  validator)
- `consensus/XDPoS/xdpos.go:91` — `defaultEpochLength = 900`
- ADR 801 (V2 BFT 3-chain finalization fix) — adjacent design
- Issue #807 — tracker
- Issue #806 — bench validating the original snap-sync claim
