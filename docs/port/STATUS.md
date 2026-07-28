# Sambhu Status — 2026-06-12 (DEVNET-5151: BFT round mismatch — awaiting restart approval)

## Current Phase: DEVNET-5151 — Resume chain past block 5220
## Current Milestone: Chain advancement past block 5220 — BLOCKED (BFT round mismatch)
## Current Task: Legacy=round 2712, Modern=round 1 — needs legacy node restart to sync rounds

## ACTION REQUIRED (USER):
Restart legacy XDPoSChain nodes on xdc03. Command: `bash /root/devnet-v1v2/launch.sh`
Then restart modern geth nodes on xdc04 with new binary (see below).
DO NOT wipe chain data — only kill and relaunch same processes.

## DEVNET-5151 chain halt at 5220 — ROOT CAUSE & FIX SUMMARY

**Problem:** 5151 devnet chain halted at block 5220 (Epoch 5, round 2700).
- Epoch 5 active set: 7 masternodes (4 legacy on xdc03 + 3 modern on xdc04; fast-node penalized)
- Quorum needed: ceil(7 × 0.667) = 5 votes
- Only 4 legacy nodes were voting (modern nodes had `--mine` but bugs prevented BFT)

**Three bugs fixed:**

### Bug 1: XdcBulkSyncMode cleared too early in StartMining()
- File: `eth/backend.go`
- Fix: Remove premature `core.XdcBulkSyncMode.Store(false)` from `StartMining()`
- Commit: `9d0495c79` (on xdc04 workspace `fix/5151-forkblocks-a97.5`)
- Impact: Nodes restarted with `--mine` while still syncing no longer fail V2 epoch checkpoints

### Bug 2: HookPenalty not wired + GetMasternodes V1 dispatch for V2 blocks
- File: `consensus/XDPoS/xdpos.go`, `eth/backend.go`
- Fix: Move `AttachConsensusV2Hooks` to after `SetEngineV2`; dispatch to V2 path for V2 blocks
- Commit: `3fd148ff3` (in main worktree + cherry-picked to xdc04 as `3af293bb5`)
- Impact: calcMasternodes now returns the correctly penalized-filtered active set (7 not 47)

### Bug 3: XdcBulkSyncMode never cleared for FULL_SYNC nodes (A.98)
- File: `eth/sync_xdc.go`
- Fix: In `fullSyncOnce`, when all peers agree `hash(localHead) == their latest hash`, clear XdcBulkSyncMode
- Commit: `23f5263cd` (on xdc04 workspace)
- Impact: FULL_SYNC nodes (not snap-sync) transition to active BFT participation when at-tip

## Current State (2026-06-12 after context reset)
- All 8 nodes at block 5220 (chain HALTED)
- Binary on xdc04: commit 23f5263cd (A.98) — RUNNING with bug
- New binary staged: /root/devnet-v1v2/bin/geth.906a715c (A.99 fix)
- A.98 fired at 21:42:15 UTC — XdcBulkSyncMode cleared correctly
- But: highestQuorumCert stayed at zero (A.99 fix needed and committed)
- BFT round mismatch: legacy=2712, modern=1
  - Root: legacy assembled TC with 0x37a5617c signer (not in epoch-3 masternode set)
  - Modern correctly rejects this TC → stuck at round 1

## 4 Bugs Fixed (commits ready):
1. Bug 1 (A.97.5): XdcBulkSyncMode cleared too early in StartMining() — commit 9d0495c79
2. Bug 2 (A.924): HookPenalty not wired — commit 3af293bb5 (xdc04), 3fd148ff3 (worktree)
3. Bug 3 (A.98): FULL_SYNC nodes never clear XdcBulkSyncMode — commit 23f5263cd
4. Bug 4 (A.99): highestQC stays zero after A.98 — commit 906a715c2 (xdc04), fix in worktree

## Last 5 actions
- [22:00 UTC] Traced QCNumber=0 to initial() running on pre-V2-switch header (block 2696)
- [22:10] Identified BFT round mismatch: legacy=2712, modern=1, TC at round 2711 invalid
- [22:15] Root cause: legacy TC includes signer 0x37a5617c (modern node) not in epoch-3 set
- [22:20] Implemented A.99 fix in Prepare() to re-bootstrap highestQC from parent header
- [22:25] Built + staged new binary /root/devnet-v1v2/bin/geth.906a715c (commit 906a715c2)

## Blockers:
USER ACTION REQUIRED to restart all 8 nodes:
1. On xdc04: kill geth processes, deploy new binary, relaunch
2. On xdc03: restart legacy nodes via launch.sh (clears BFT round 2712 → starts fresh)
After restart: legacy rounds reset to ~2700, modern A.99 bootstraps correctly → chain advances

# Previous session state below (2026-06-11, M-XDPOS-WORKER):

## Current Phase: M-XDPOS-WORKER — Apothem validator mint fix
## Current Milestone: STEP 3 — Verify network-accepted mint (BLOCKED: operator must re-stake)
## Current Task: Waiting for operator to resign + re-stake validator on Apothem

## M-XDPOS-WORKER — Validator mint-worker-never-starts fix (2026-06-10/11)
- Branch: `feat/m-xdpos-worker`
- Issue: https://github.com/XDCIndia/go-ethereum/issues/920
- PR (draft): https://github.com/XDCIndia/go-ethereum/pull/921

### Fix delivered (commit 6ba053504) — VERIFIED WORKING:
1. Removed `if !s.Synced() { return nil }` early-return gate in `StartMining()`
2. Replaced with unconditional `s.SetSynced()` + `core.XdcBulkSyncMode.Store(false)`
3. Fixed `wallet.SignData` double-hashing bug: now uses `ks.SignHash` directly
- Binary deployed: Jun 10 22:43 (55MB ELF on xdc02)
- Worker confirmed: `XDPoS2: StartMining authorized coinbase=0x11C3637D` + `Preparing new block!` every 2s
- Sync: at tip (local≈82949747, erpc≈82949786)

### Why validator is not minting — CONFIRMED ROOT CAUSE:
Decoded epoch switch block 82,949,641 (round 26,712,000) from erpc:
- validators: 32 addresses (packed 20-byte hex) — our node NOT present
- penalties: 15 addresses — our node IS position [0]
  `0x11c3637d2848ebdc190d56d8b81348c21446e866` is penalties[0]

Worker logs every 2s: `[yourturn] Not in masternodes list signer=0x11C3637D...`
The fix is correct. The code fix works. The worker runs. But the network's penalty
list explicitly includes our validator, so `yourturnAligned()` always returns false.

### OPERATOR ACTION REQUIRED (urgent — next gap block in ~9 min as of 01:10 UTC):

**Option A (fastest — before block 82,950,091 fires in ~9 min):**
1. Go to https://apothem.xinfin.network/staking
2. Connect wallet `0x11C3637D2848ebdC190d56d8b81348c21446E866`
3. Click "Resign" → confirm tx
4. Wait 30 blocks
5. Click "Stake" → enter 10,000,000 XDC → confirm tx
   (The gap block at 82,950,091 re-reads the staking contract — if re-staked by then,
   this validator will be in `candidates` for the NEXT epoch at 82,950,541)
6. Watch for `INFO [yourturn] Yes, it is my turn` in node logs
7. Wait for `XDPoS2: minted block number=N`
8. Confirm on erpc: `eth_getBlockByNumber(N).miner == 0x11c3637d...`

**Option B (no rush):**
Same resign + re-stake at any time. The next gap block after re-stake will include
the validator in the staking contract read. The subsequent epoch switch will include
it in the active set. Minting will begin that epoch.

**Why auto-comeback doesn't apply:**
`LimitPenaltyEpochV2 = 0` in `common/constants.go` — the comeback window is 0 epochs.
Our node is not in `preMasternodes` → penalty algorithm neither re-penalizes NOR
re-admits it. Only a resign+re-stake resets the staking contract entry and clears
the penalty history for this address.

### Next epoch timing (as of 01:10 UTC 2026-06-11):
- Gap block (UpdateMasternodesFromHeader): 82,950,091 (~9 min)
- Next epoch switch: 82,950,541 (~24 min)

## Last 5 actions
- [01:05] Decoded epoch switch block 82,949,641 — our node in penalties[0], not in validators
- [01:06] Confirmed HookPenalty code: preMasternodes gates who gets penalized/cleared
- [01:07] Confirmed LimitPenaltyEpochV2=0 disables comeback mechanic
- [01:08] Calculated gap block 82,950,091 (~9 min) and next epoch 82,950,541 (~24 min)
- [01:10] STATUS.md updated with operator action instructions

## Blockers: OPERATOR MUST RESIGN + RE-STAKE validator `0x11C3637D` on Apothem staking portal
## URL: https://apothem.xinfin.network/staking
## Validator: 0x11C3637D2848ebdC190d56d8b81348c21446E866

# Previous session state below (2026-06-07, A.72):


## Current Phase: A.72 — pre-pivot reward-window body+receipt pre-fetch
## Current Milestone: M5 — Full mainnet sync (issue #857) / fast-sync unblock (refs #844 #859)
## Current Task: open PR `fix/a72-merkle-root-divergence` against `reset/integration-1.17.3`

## A.72 — merkle root divergence at first post-pivot epoch switch (2026-06-07)
- Branch: `fix/a72-merkle-root-divergence` off `reset/integration-1.17.3` (PR #866 merged)
- ADR: `docs/adr/072-merkle-root-divergence.md`
- Problem: with A.68 + A.69 + A.70 + A.71 all green and 121 post-pivot blocks
  importing, modern v1.17.3 fails at block 103,309,391 with
  `invalid merkle root (remote: 4ee52c52… local: c6d53d2e…)`. Cross-referencing
  the canonical chain shows the "local" hash IS block 390's canonical state
  root, but block 391 mutates state and we don't.
- Root cause: block 391 is the **first V2 epoch-switch** after the pivot
  (round 23,228,100 is a 900-multiple → isEpochSwitch=TRUE). HookReward fires
  but `GetSigningTxCount` walks back through blocks 103,307,599..103,308,493
  calling `rawdb.ReadBlock(...)`. Every read returns nil — fast-sync only
  pulls bodies+receipts for post-pivot blocks. Result: totalSigners=0,
  zero AddBalance calls, post-state diverges from canonical at the merkle
  root check. The 8 ancestor blocks that DO have bodies (= the 121 imported
  post-pivot blocks) don't fall inside the reward window.
- Fix:
  - **A.72.1** (`eth/downloader/downloader.go`, +60 lines): add
    `PrimeAncestorBodies(headers, bodies, receipts)` method that persists
    block bodies + receipts to rawdb in a single batch. Idempotent via
    `rawdb.HasBody` / `rawdb.HasReceipts` checks. Mirrors
    `PrimeAncestorHeaders` shape.
  - **A.72.2** (`eth/sync_xdc.go`, +160 lines): add
    `fetchReceiptsBatch(peer, headers)` helper (XDPOS2 receipt request
    via dispatcher sink, mirrors `fetchBodiesBatch`) and
    `prefetchAncestorBodiesAndReceipts(peer, allHeaders, lo, hi)` that
    iterates the A.68.2 pre-fetched range in 192-block batches, requests
    bodies + receipts, decodes the RLP, and calls `PrimeAncestorBodies`.
    Wired into `prefetchAncestorEpochs` tail, after
    `seedMasternodeSnapshotsFromPrefetch`.
- Tests: 4 new in `eth/downloader/prime_ancestor_bodies_test.go` (writes,
  length-mismatch, optional-receipts, idempotent) — all PASS.
- Build: `go build ./eth/... ./core/... ./consensus/...` clean.
- Validation: **PASSED** on xdcscan 2026-06-07 08:22-08:26 CEST (after
  v2 fix landed at `bf4c98728`). v1 deploy (`8d71dd348`) had a batchCap
  off-by-(64 per batch) bug that left 576 of 1800 ancestor bodies
  unwritten — totalSigners climbed from 0 → 4536 but stateRoot was still
  wrong (bde9c78f vs canonical 4ee52c52). v2 (`bf4c98728`) lowers
  batchCap to 128 to match the production peer cap and advances `start`
  by `lastWritten+1` instead of `end+1`. Result: 1800/1800 bodies +
  receipts written, block 103,309,391 IMPORTED with stateRoot
  byte-identical to canonical `0x4ee52c52…6bf1d46`. Chain climbed to
  103,309,653 (pivot+384) within 2 min 4 sec of cold start.

## A.71 — block-import handoff (2026-06-07)
- Branch: `fix/a71-block-import-handoff` off `reset/integration-1.17.3` (PR #865 merged), cherry-picks `7ed2da6be` (post-pivot A.70 re-seed)
- ADR: `docs/adr/071-block-import-handoff.md`
- Problem: after A.70 (PR #865) successfully seeds V2 masternode snapshots
  at fast-sync startup (Validators + QC checks pass, 0 BAD BLOCKs), modern
  v1.17.3 still never imports a single post-pivot block. eth_blockNumber
  stays at 0x0 for 8+ minutes despite 7 peers connected and the right
  snapshots being persisted in the DB. Log shows 76K lines / 12 MB of
  `XDPOS2 GetBlockHeaders SEND origin_num=103,309,268 amount=192 reverse=true`
  on a single peer cafdf5e8.
- Root cause: a two-subchain skeleton state that never resolves to `linked`.
  Cold-start peer-wait timeout fires with peers=1, fastRunningCheck falls
  back to peer-tip resolution after op-pinned head fetch fails, that one
  peer transiently advertises a head ≈ 64.9M, BeaconSync engages with that
  bad head, skeleton.initSync persists a stub subchain at 64.9M, SyncFailed
  fires with "Pivot header is not found number=64,932,289". On retry with
  more peers, initSync's drop predicate (Tail >= newTail) can't prune the
  stub (64.9M < 103.3M), so we end up with 2 subchains permanently. Skeleton
  `linked` check `len(Subchains) == 1` never trips, beaconBackfiller.resume()
  never runs, SyncCompleted never fires, XdcBulkSyncMode stays true,
  block-import is suppressed by fullSyncOnce gate.
- Fix:
  - **A.71.1** (`eth/sync_xdc.go`, +18 lines): in fastRunningCheck, when
    operator pivot is pinned, refuse to use any peer-tip whose number is
    below the pinned pivot. Skip that candidate and continue scanning; if
    every candidate is below pivot, return without setting
    fastBeaconStarted and retry next 500ms tick.
  - **A.71.2** (`eth/downloader/{downloader,skeleton}.go`, +95 lines):
    in SetFastSyncPivot, after setting the terminate floor, call a new
    `skeleton.dropStaleSubchainsBelow(pivotNumber)` that prunes any
    persisted subchain whose Head < pivot. Cleans pre-A.71 stale state
    inherited from snapshot restores. Idempotent.
- Build: `go build ./eth/...` clean.
- Tests: `go test ./eth/downloader/` PASS (10.3s).
- Validation: PASSED on xdcscan 2026-06-07 07:55-07:59 CEST.
  - 443 below-pivot peer-tips rejected (10+ distinct heights including
    64.9M / 76.3M / 81.6M — exactly the failure-mode values from the
    handoff).
  - Within 2 min 4 sec: fast-sync BeaconSync kicked → SyncStarted →
    SyncCompleted → A.70 re-seed fires → InsertChain attempts 128 post-
    pivot blocks, **inserts 121** (pivot+1..pivot+121).
  - eth_blockNumber climbs to 0x628604e (= 103,309,390 = pivot+121),
    exactly the task's acceptance threshold.
  - A.70 invariants preserved (`A.70 re-seeded V2 masternode snapshots
    post-SyncCompleted`).
  - Remaining `invalid merkle root` at block 103,309,391 is an A.72 task
    (state-sync gap, NOT a block-import-handoff failure).

## A.70 outcome (recap; PR #865 merged, follow-up commit 7ed2da6be cherry-picked into A.71)
- V2 snapshots seeded at boot (3 gap-snapshots + 1 anchor-hash) → 0 BAD BLOCKs.
- But block-import never engages without A.71. (Now fixed by A.71.)

## A.69 — peer-starvation bypass (2026-06-07)
- Branch: `fix/a69-skeleton-peer-cohort` (off `reset/integration-1.17.3` post PR #863 merge)
- ADR: `docs/adr/069-skeleton-peer-cohort.md`
- Problem: post-A.68 deploy on xdcscan showed `peers=1`, `eth_blockNumber=0x0`, no
  "Imported new chain" lines after >30 min — single peer trips A.40 sticky-PBSS
  blacklist + XDC fix #131 skeleton empty-response drop, leaving state-sync with
  no eligible peers.
- Fix: peer-starvation floor (default 2) below which the A.40 sticky-PBSS
  blacklist, the skeleton's empty-response drop, and the A.37 legacy-peer gate
  are all bypassed. Counters still tick + log for visibility; only the punitive
  action is suppressed. A.37 proactive `remoteSchemePbss` (explicit handshake
  claim) intentionally NOT bypassed.
- Tests: TestNodeDataIdlePeersA69PeerStarvationBypass (3 subtests) + full
  `eth/downloader/` regression suite (9.7s) — all PASS.
- Validation: pending server deploy at 95.217.106.210:12141, snapshot restore from
  xdcscan-a67-state-T3p, look for state-sync progress + `eth_blockNumber > 0x0`.

## Original snapshot below (Sprint 2 history)
---

# Sambhu Status — 2026-06-05 (Sprint 2 COMPLETE — pending push + milestone comment)

## Current Phase: SPRINT 2 — A.53 + A.55 LANDED, A.57 DOC WRITTEN
## Current Milestone: M5 — Full mainnet sync (issue #857)
## Current Task: commit A.55 + A.57, push branch, post milestone summary on #857

## Last 5 actions
- [10:20] A.55 chunked checkpoint trigger + resume log integrated; full -race regression PASS (54s)
- [10:20] A.57 ops manifest written to docs/port/A57_OPS.md (no code)
- [10:18] A.53 committed at db6bde30c with TestParallelInflightNoOverlap + 3 sibling tests; -race PASS
- [10:18] drive-by skeleton.go shortPeerID() fix unblocks full downloader -race regression
- [10:04] continuation orchestrator picked up Sprint 2 from A.50 = DONE @ 1b50dddc9

## Sprint 2 progress
| # | Solution | Status |
|---|----------|--------|
| A.50 | Skeleton peer-set bridge (regression test + log + audit) | DONE (1b50dddc9) |
| A.53 | Parallel NodeData inflight per peer (16 slots) | DONE (db6bde30c) |
| A.55 | Chunked commit + checkpoint resume | DONE (pending commit) |
| A.57 | Dedicated HBSS archive fleet (ops doc) | DONE (pending commit) |

## A.50 outcome
**Decision**: After code audit, A.50's premise in issue #857 was inaccurate.
The skeleton ALREADY accepts XDPOS2 peers via the shared `d.peers` peerSet
+ version-aware RequestHeadersByNumber encoder. No bridge needed to build.

**Shipped instead**: test-first regression guard + diagnostic log.
- 2 new tests in `eth/downloader/skeleton_xdpos2_test.go` — both PASS with `-race`
- ~12 LOC defensive comment + Info log in `eth/downloader/skeleton.go`
- All Sprint 1 lessons honored: tested first, race-detector clean, no prod deploy yet

## Strict execution discipline being honored
1. TEST-FIRST: 2 regression tests written before any production behavior change ✓
2. `go test -race`: PASS on TestSkeletonAcceptsXDPOS2Peers + TestSkeletonSeedsExistingXDPOS2PeerBeforeSubscribe ✓
3. Side-by-side: no production deploy yet — A.50 is invariant lock-in only ✓
4. Roll back trigger: N/A (no rate regression risk; log-only + test-only change) ✓
5. NO patch combining: A.50 commits standalone before A.53 starts ✓

## Hands-off list (NEVER touch)
- xdc04 archive (root@65.21.71.4)
- A.14c (PID 3876607)
- XDPoSChain (PID 537625)

## Last 5 actions
- [start] Read PORT_LOG Sprint 2 directive (line 515-553)
- [survey] Audited skeleton.go + handler.go + faststatesync.go peer-registration flow
- [find] Confirmed XDPOS2 peers ALREADY visible to skeleton (issue #857 A.50 spec wrong)
- [code] Wrote skeleton_xdpos2_test.go (2 tests, both pass with -race)
- [code] Added invariant comment + Info log in skeleton.go (12 LOC)

## Pre-existing bug noted (NOT for A.50 fix)
- `TestSkeletonSyncRetrievals` panics at skeleton.go:903 `peer.id[:8]` on baseline.
  Confirmed via `git stash && go test` — predates A.50. Out of scope. Logged.

## Sambhu Score / Parity Pass Rate: n/a (A.50 is non-consensus)
## Opus escalations this session: 0
## Last commit: 3c396b188 (pre-A.50; A.50 commit pending)

## Acceptance criteria (Sprint 2)
- Modern v1.17.3 binary reaches pivot within 24h cold start
- `Committed new head` fires for pivot block
- Tip-following confirmed for 1h post-pivot
- All 4 A.X commits land cleanly on feat/a11-fast-sync-peer-gate with tests
- PR opened against xdc-network
