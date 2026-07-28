# ADR: V2 BFT 3-Chain Finalization Fix (issue #801)

Status: PROPOSED · 2026-05-25 · author: session swarm · closes loophole L10

## Context

Every recovery mechanism shipped in PR #805 (self-heal rollback, peer-taint,
cache-invalidate, ErrNotItsTurn extension) is a workaround for the same
underlying defect: **the V2 BFT engine commits a block under the 3-chain rule
before BFT vote-consensus for that slot has actually settled.**

When a competing-slot block arrives — two masternodes simultaneously propose
for the same round — the engine accepts whichever arrives first via
parent-hash continuity and immediately advances the 3-chain commit pointer.
If the network later finalizes the OTHER block via BFT vote consensus, our
node is on the losing fork, and the only recovery is rollback.

Observed live three times in this session:
- block 82,219,687 (closed as #798's manifestation)
- block 82,265,072 (rollback-induced loop)
- block 82,278,450 (ErrNotItsTurn cascade)

## Decision

Introduce a **BFT vote-aggregation gate** before 3-chain commit fires.
A block N is eligible for commit-pointer advance only when one of these holds:

1. **Quorum-cert observed:** we have received valid Vote messages from
   ≥ supermajority (≥ ⌈2·M/3⌉) of the masternodes for the round in which
   N was proposed.
2. **Trusted checkpoint:** N falls at or below a hardcoded
   `TrustedSyncCheckpoints[]` entry (the engine already keeps a list).
3. **Time-deadline:** the round has expired (timeout fired) AND N is
   the highest-vote candidate at expiration.

Until one of these holds, the block is **provisional** — written to the
chain head pointer but NOT to the 3-chain finalized pointer. A later
better-supported competing block can supersede it via standard reorg.

## Why this works

Under V2 BFT, the canonical chain is the one a supermajority votes for.
Today the engine commits before counting votes, so a fast-arriving non-canonical
block locks the chain to the wrong branch. Gating on vote count delegates the
decision to the consensus the chain was designed around.

## Why we haven't done it in this session

The change touches consensus correctness: a bug ships chain forks at every
masternode that adopts it. Specifically:
- **Vote storage:** today the engine has `timeoutPool` + `votePool`; both
  are LRU and ephemeral. Need a per-round vote store that survives until
  round + 3 (the depth at which votes can no longer matter).
- **Replay safety:** when re-syncing from snapshot, we don't have vote
  messages for old blocks. The gate must short-circuit through
  `TrustedSyncCheckpoints` and `header.Validators` quorum-cert in the
  block header itself (the masternodes embed a quorum-cert in each block's
  Extra field — this is the durable form of the vote we need).
- **Round identification:** the engine reads `round` from `header.Extra`.
  Verifying the QC inside Extra requires walking the validator set at
  the round's epoch. This already exists (`verifyQC`) but currently runs
  AFTER 3-chain commit — the order has to flip.

## Concrete plan

1. **Phase 1 — instrumented reorder.** Move `verifyQC` so it runs BEFORE
   the 3-chain commit-pointer advance, not after. Validate against the
   archive (which has all historical QCs) that no canonical block fails.
2. **Phase 2 — provisional state.** Add a `provisional_head` pointer in
   the engine, parallel to `committed_head`. Imports update
   `provisional_head` first. `committed_head` advances only after the
   QC check passes.
3. **Phase 3 — vote pool.** Persist incoming Vote / Timeout messages in
   a per-round structure that survives across blocks until `round + 3`.
   Use this to validate at-tip blocks where the QC in the block's Extra
   may lag by one round.
4. **Phase 4 — reorg path.** When a competing-slot block arrives whose
   QC has more votes than our provisional head's QC, reorg to it. This
   replaces the current "first-wins" behavior.

## Acceptance criteria

- [ ] xdc02 fresh-sync from genesis reaches canonical tip with ZERO
  rollback events fired in the entire sync.
- [ ] xdc04 snapfullpath-test reaches tip after restart with ZERO
  rollback events.
- [ ] Three historical incident blocks (82,219,687, 82,265,072,
  82,278,450) all import with stateRoot matching canonical on first try
  in a freshly-restored datadir.
- [ ] No `ErrNotItsTurn` or `unknown ancestor` errors observed during
  a 24-hour soak test.

## Estimated effort

10-15 engineering days. Consensus changes need test-net validation
before mainnet. The phasing above lets us land Phase 1 (reorder) as a
small, reversible commit and gather evidence before the deeper Phases 2-4.

## What this ADR replaces

Without this fix, the workarounds in PR #805 (self-heal rollback, peer
taint, cache invalidate, ErrNotItsTurn trigger) remain mandatory for any
node that catches up to tip. With this fix, they become defense-in-depth
that should rarely fire.

## References

- Workaround stack: PR #805
- Original observation: #798's investigation (closed)
- Loophole audit: docs/port/V117_UPGRADE_LOOPHOLES.md item L10
- xdc-network reference (the canonical XDPoSChain v2.7.x source — has
  the proper QC-before-commit ordering): `consensus/XDPoS/engines/engine_v2/`
  on branch `xdc-network`
