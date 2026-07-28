# A.97 Phase 2 (snap-heal at tip) — Opus Design Brief (escalation 2)
<!-- Verbatim Opus planner output, 2026-06-10. Implementation brief for Sonnet. -->

## Executive summary / the load-bearing finding

The issue's framing ("start a `snap.Syncer.Heal()` pass") **does not match the code**. There is no separable `Heal()` method. `snap.Syncer.Sync(root, cancel)` (`eth/protocols/snap/sync.go:607`) is one monolithic loop: range-download first, then heal, both keyed on a single `root`, terminating when `len(s.tasks)==0 && s.healer.scheduler.Pending()==0` (`sync.go:689`). You cannot run "heal only."

But there is a real wedge that makes Phase 2 viable: when `Sync(root)` is called and the **account-range tasks are already complete** (because A.93/A.94 already imported most of the trie via block execution / a prior partial snap-range), `loadSyncStatus` sets `s.snapped=true`, `len(s.tasks)` collapses to 0, and the loop goes straight into the heal branch (`sync.go:705-719`), issuing only `GetTrieNodes`/`GetByteCodes`. Those two message types ARE served by the XDC snap handler (`eth/protocols/snap/handler.go:131,141`) without needing a generated snapshot index. The thing that's dead on this fleet is `GetAccountRange` (issue #96, confirmed at `sync_xdc.go:1208-1226`), which heal does not use.

**So Phase 2 = invoke `SnapSyncer.Sync(headRoot, cancel)` in a state where the account tasks are already satisfied, so it degenerates into a pure trie-node heal against peers.** If the account tasks are *not* satisfied, `Sync` will try `GetAccountRange`, stall on the #96 dead path, and the timeout must fire and fall back cleanly. The design must detect and handle both.

## DESIGN — answers to the 6 questions

### 1. WHERE to trigger heal
Trigger from `snapEventLoop`'s fast-sync `SyncCompleted` branch (`eth/sync_xdc.go:1408-1415`) → heal kicked from `maybeTransitionToAtTip` (`sync_xdc.go:950`) right BEFORE `core.XdcBulkSyncMode.Store(false)` at line 978, gated on `bc.checkpointSyncNoState.Load()`.
- Snap-heal does NOT call SetHead and does NOT rewind — only writes trie nodes by hash into state DB (`sync.go:640-648` via stateWriter/commitHealer) → A.95's "Impossible reorg" class structurally absent.
- gcmode=full safe (heal writes direct to stateDb, independent of commit cadence).
- At AT_TIP head is quiescent (`localHash == peer head hash`, `sync_xdc.go:964`); head movement handled by re-target (Q2).
- Do NOT trigger at pivot-commit or fastStateSync completion (moving target — recreates what Phase 1 fixed).
- Concretely: `s.maybeStartSnapHeal()` invoked once, background goroutine, from `maybeTransitionToAtTip` just before line 978. Do not block the AT_TIP flip.

### 2. WHICH root
Current canonical head root (`bc.CurrentBlock().Root`) captured at heal-start, NOT the frozen pivot root. Phase 1 guarantees range-downloaded trie consistent at frozen pivot; heal fills pivot→head delta + gaps (`state.NewStateSync(headRoot, ...)`, `sync.go:613`, schedules exactly the missing nodes). If head advances during heal: re-run `Sync(newHeadRoot)`, max `healMaxRootRetargets = 3`.

### 3. Peer plumbing
Exists: snap negotiated/registered (`eth/handler.go:329` SnapSyncer.Register, `:458` Unregister; `eth/peerset.go:55-56` snapWait/snapPend); Syncer Register/Unregister (`sync.go:548/576`) feed peerJoin/peerDrop (`sync.go:662-668`); response routing wired (`downloader.go:2086 OnByteCodes`, `:2089 OnTrieNodes`); peer-side handlers registered (`handler.go:131,141`).
Missing/risk: whether XDC peers advertise snap cap at all (`peerset.go:107 RunningCap`). GetTrieNodes works without snapshot index IF snap/1 negotiated. MUST add `SnapSyncer.PeerCount()` accessor + heal-start log + abort-to-fallback if zero. NO heal-peer prioritization in Phase 2 (`assignTrienodeHealTasks`, `sync.go:717` is sufficient).

### 4. Completion + failure semantics
- Gate A: `Sync()` returns nil (implies pending==0); `Progress()` (`downloader.go:756`, `sync.go:945`) feeds status RPC.
- Gate B: re-execute most recent **N=128** blocks (= TriesInMemory, > fsMinFullBlocks=64 pivot→head window, `sync_xdc.go:1292`) with full validation, NO InsertChain/SetHead — read-only: `state.New(parent.Root)` + `processor.Process` + `validator.ValidateState`, compare computed root vs `block.Root`. Run with `XdcBulkSyncMode` already false so A.94 cannot fire (`blockchain.go:2406` gate).
- Success: `bc.SetCheckpointSyncNoState(false)` (`core/blockchain_reader.go:384`). Log `A.97.2: snap-heal completed`. Flag clear is the LAST step.
- Timeout T = 30 min (3× `snapStallGrace` 10 min at `sync_xdc.go:1215`), via `time.AfterFunc(T)` closing `cancel`.
- Failure/timeout: leave state as-is, do NOT clear flag, `WARN A.97.2: snap-heal incomplete after T`, surface via RPC.
- RPC: `xdc_getStateHealStatus` does NOT exist on this branch (only on unmerged a95 branch). Define minimal: `{enabled: checkpointSyncNoState.Load(), inProgress, mode:"snap-heal", targetRoot, trienodePending, bytecodePending, healStartUnix, lastError}`. Register under BOTH `eth` and new `xdc` namespace (`eth/backend.go:504-521` pattern). Acceptance: `enabled == false` at tip.

### 5. Interaction matrix
- vs concurrent tip import: trie node writes content-addressed → idempotent; re-target loop absorbs head advance. Safe.
- vs A.93/A.94: heal is the cure; node that fired A.94 MUST heal before validator-ready (else proposes EmptyRootHash; `block_validator.go:134`, `blockchain.go:2393`).
- vs A.96 default: checkpointSyncNoState stays false → heal no-op/skip. Free regression safety.
- vs --no-auto-pivot dynamic path: the target win (A.94 fires → flag set → heal → flag cleared → validator-ready).

### 6. Gating
- Keep `XDC_EXPERIMENTAL_SNAPSYNC` gating the (dead) range path (`snapRunningCheck`, `sync_xdc.go:1189`) — do not lift.
- New opt-in `XDC_SNAP_HEAL=1` env for heal only. Default OFF first rollout cycle.

## IMPLEMENTATION-PLAN (ordered)
1. `eth/protocols/snap/sync.go` — `func (s *Syncer) PeerCount() int` (~6 LOC).
2. `eth/sync_xdc.go` — `snapHealEnabled()` env helper mirroring `snapsyncExperimentalEnabled` (line 45) (~5 LOC).
3. `eth/sync_xdc.go` — `maybeStartSnapHeal()`: guards (env + IsXDC + checkpointSyncNoState), one-shot `healStarted atomic.Bool`, PeerCount()==0 abort WARN, goroutine: capture targetRoot, cancel + 30min AfterFunc, retarget loop ≤3, gate B on Sync==nil, success → SetCheckpointSyncNoState(false). Log markers `A.97.2: snap-heal started|completed|incomplete|skipped|no-snap-peers|re-targeting root`. (~120-150 LOC)
4. `eth/sync_xdc.go:978` — call from `maybeTransitionToAtTip`; ordering: flip bulk false, THEN start heal goroutine (so gate B runs with bulk false). (~2 LOC)
5. `core/` (new `blockchain_heal_status_xdc.go` or blockchain_reader.go) — `ReexecuteAndValidateRange(from, to)` read-only + `SnapHealStatusFields()` (~60-80 LOC).
6. `internal/ethapi/api_xdc.go` — `GetStateHealStatus` (~30 LOC).
7. `internal/ethapi/backend.go` + `eth/api_backend.go` — `SnapHealStatus()` passthrough (~25 LOC).
8. `eth/backend.go:504` — append `{Namespace:"xdc", Service: ethapi.NewBlockChainAPI(s.APIBackend)}` (~4 LOC).
9. `xdcSyncer` struct fields: `healStarted atomic.Bool`, `healStatusMu sync.Mutex`, `healStatus xdcHealStatus` (~10 LOC).

### Test plan
- Unit `eth/sync_xdc_heal_test.go`: gate logic (skip when flag false / env unset / PeerCount 0); status fields; gate-B success+mismatch on synthetic chain; fakePeer serving GetTrieNodes per downloader_test.go patterns.
- Unit `eth/protocols/snap/sync_test.go`: PeerCount; Sync with pre-satisfied account tasks degenerates to heal-only (no GetAccountRange issued) — extend existing tests.
- Integration: scripts harness on fresh devnet `--no-auto-pivot --syncmode fast XDC_SNAP_HEAL=1`; assert at AT_TIP `xdc_getStateHealStatus.enabled==false`, zero A.94, canonical balance 0x..088.

### Non-goals
No GetAccountRange/#96 fix; no SetHead/InsertChain heal (= broken A.95); no heal-peer prioritization; A.94 stays; no XDPoS/XdcBulkSyncMode semantics change; XDC_SNAP_HEAL stays default-off.

## RISKS / deploy log watch
1. No XDC peer runs snap extension → `A.97.2: snap-heal skipped — no-snap-peers` (highest probability).
2. Account tasks not pre-satisfied → GetAccountRange stall → 30-min timeout; watch `Peer rejected account range` + `snap-heal incomplete`.
3. Gate-B false pass → any later `A.94 auto-recover` after `snap-heal completed` = regression, page.
4. Busy chain outruns heal → repeated `re-targeting root` then WARN-incomplete.
5. Flag-clear race → watch `BAD BLOCK`/`invalid merkle root` immediately after `snap-heal completed` (mitigated: gate B before clear, clear last).

## ESTIMATED-SCOPE
~330-420 LOC production + ~250-350 LOC tests.
