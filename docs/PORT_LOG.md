# Baseline reset port log (refs #740)

Running diary of decisions during the v1.17.3 baseline reset. Append-only.

---

## [PLANNER-ADVICE 2026-05-23] Opus planner — rebase strategy

Briefed with: 677-commit overlay between `54a91b3a..xdc-network`, bundle catalog
draft (12 bundles), F-bucket file list (`core/blockchain.go` 75 commits,
`eth/downloader/` 115 commits), result of a trial linear rebase (3 conflicts
resolved as union-of-additive-changes before tool policy aborted it).

### 1. Bundle merge order — INVERTED FROM DRAFT

Keep B0–B3 first, but invert the collision zone: run **B4 → B5 → B7 → B2** (V2
engine moves to *after* the F-bucket bundles).

Rationale: V2 engine reads from header plumbing, `XDC:` markers, P1 sync gating,
and the bypass cleanup. If V2 lands on top of unported plumbing, every consensus
test for V2 fails for plumbing reasons and you can't tell whether the V2 port
itself is correct. Land the dumb-but-collision-heavy substrate first against a
v1.17.3 skeleton that still runs vanilla, *then* layer V2 on a substrate already
validated bundle-by-bundle. B6 (snap-sync) stays after B5 because it depends on
P1 hooks. B8–B11 unchanged.

**New order**: B0 → B1 → B3 → **B4 → B5 → B7 → B2** → B6 → B8 → B9 → B10 → B11.

### 2. F-bucket conflict policy

**Default: upstream skeleton + re-apply XDC additions.** Drift cost compounds —
every cycle this is deferred, the next reset is harder.

**Tripwire**: if upstream "refactor" changes a function signature that XDC's
addition depends on semantically (e.g., `Process()` return shape, downloader
peer-state transitions), do *not* mechanically port — stop and verify XDC's
addition's invariants still hold under the new signature. The `XDC:` marker
tells you *what* to preserve, not *that* the surrounding contract is preserved.

Document each F-bucket file resolution in this log with:
- upstream-hash
- XDC-hash
- resolution-mode (skeleton / verbatim / hybrid)
- invariant-checked

### 3. Execution mechanic — bundle-branches, not linear rebase

The "merge-commit-per-bundle" wording in #740 is architecturally load-bearing —
it's how 677 original SHAs stay reachable while presenting 12 reviewable units.

**Mechanic per bundle**:
```
git checkout -b bundle/Bn reset/baseline-1.17.3
git cherry-pick <bundle-commits>      # (or rebase --onto for whole range)
# resolve conflicts per §2 policy
git checkout reset/baseline-1.17.3
git merge --no-ff bundle/Bn -m "merge: <bundle title> (refs #740) — absorbs XDC PRs #x, #y, #z"
```

Linear `git rebase --onto` across 677 commits flattens the bundle structure,
makes F-bucket gate boundaries invisible in `git log`, and forfeits the
`--first-parent` review view that reviewers will need.

### 4. Checkpoint cadence

Push after **every bundle**. Force-push only within the in-flight bundle
branch. Reasons:
- F-bucket gate results need an immutable git ref to attach evidence to in
  the PR body.
- Snapshot regression runs are slow; each bundle's tip should be a recoverable
  rollback point.

### 5. Authority to proceed

**Escalate per F-bucket bundle (B4, B5, B7); single-agent for the rest.**

Non-F bundles are mechanical. F bundles are where consensus correctness can
silently break, and a fresh-context human review of the diff + gate output
before merging into `reset/baseline-1.17.3` is cheap insurance.

### Top 3 risks of this recommendation

1. **Hidden semantic drift in a "refactor"** — upstream renames a field but
   changes its lifecycle; XDC addition compiles but is wrong.
   *Flips to XDC-verbatim default* if triage of the 187 `?`-bucket commits
   reveals >20% touch F-bucket files (accept the drift debt).
2. **Bundle order assumes B4/B5/B7 are V2-independent** — if any of the XDPoS
   commits in B4's plumbing encode V2 assumptions, B4 won't build without B2.
   *Flips* if `grep -l "v2\|V2" $(git log --name-only B4-range)` shows
   entanglement.
3. **Snapshot restore under new binary may fail for non-consensus reasons**
   (DB schema, state-scheme change in v1.17.3) — masking real regressions as
   restore failures.
   *Flips cadence* if first B0 snapshot-restore fails — add a Phase 0.5 to
   characterize schema delta before any bundle merges.

### Critical files

- `docs/UPSTREAM_1.17.3_TRIAGE.md`
- `docs/RESET_BUNDLES.md`
- `core/blockchain.go`
- `eth/downloader/downloader.go`
- `consensus/XDPoS/XDPoS.go`

---

## [DISCOVERY 2026-05-23] Phase A `cmd/XDC` upstream API drift — planner's tripwire fired

While attempting bundle B1 (Phase A continuation after B0), tried cherry-picking
the next 10 chronological XDC commits (`f5749c631..e40a3414c`) onto
`reset/baseline-1.17.3` with `-X theirs` (intra-XDC: later XDC version wins on
overlap). All 10 cherry-picks applied, but `go build ./...` revealed:

```
cmd/XDC/chaincmd.go:64:  undefined: utils.OverrideVerkle
cmd/XDC/chaincmd.go:290: undefined: utils.OverrideVerkle
cmd/XDC/chaincmd.go:519: not enough arguments in call to utils.ImportHistory
                         have (chain, string, string)
                         want (chain, string, string, func(era.ReadAtSeekCloser) (era.Era, error))
cmd/XDC/chaincmd.go:553: not enough arguments in call to utils.ExportHistory
                         have (chain, string, uint64, uint64, fn1)
                         want (chain, string, uint64, uint64, fn1, fn2)
cmd/XDC/chaincmd.go:683: undefined: history.PrunePoints
cmd/XDC/main.go:68:      undefined: utils.OverrideVerkle
```

This is precisely the **planner's tripwire** (from `[PLANNER-ADVICE 2026-05-23]`):

> "if upstream `refactor` changes a function signature that XDC's addition
> depends on semantically (e.g., `Process()` return shape, downloader
> peer-state transitions), do not mechanically port — stop and verify the
> XDC addition still has the same invariants under the new signature."

`cmd/XDC` (the XDC `geth` binary fork) was authored against v1.17.0 era APIs:
- `utils.OverrideVerkle` — Verkle override flag, restructured/removed in v1.17.x
- `utils.ImportHistory` / `utils.ExportHistory` — added Era1 callback args
- `history.PrunePoints` — symbol moved or renamed
- `core.ChainOverrides` — no longer has `OverrideVerkle` field
- `core.Genesis.IsVerkle()` — no longer exists

**Captured state**: `bundle/B1-wip-blocked-by-verkle-drift` pushed to origin.
10 commits applied; final tree doesn't build.

**Resolution options for next session**:

A. Port `cmd/XDC` to v1.17.3 APIs (small migration patch on top of B1's
   cherry-picks; treat as its own merge-commit-per-bundle unit).
B. Drop `cmd/XDC` from the reset baseline (it's a build-and-ops convenience,
   not consensus-critical). The `cmd/geth` binary is already there from
   v1.17.3.
C. Defer `cmd/XDC` to a later, dedicated bundle that lands after the core
   consensus bundles validate.

`reset/baseline-1.17.3` rolled back to `c25528c75` (= B0 merged, known-good).

Escalation requested before continuing.

---

## [PLANNER-ADVICE 2026-05-23 eth-protocols] Opus planner — eth wire policy

F-bucket escalation for cherry-picking XDC commits `93bbb7c13` (eth/62, eth/63, xdpos2 protocol support) and `fbc86ff61` (eth/62 handshake) onto v1.17.3 which removed ETH/62/66/67 and added ETH/70.

### Decision: Option C — minimal wire

Drop ETH/62, ETH/63, and ETH/68 entirely. Keep only:
- `XDPOS2 = 100` (XDC↔XDC consensus protocol — load-bearing for V2 finality)
- `ETH/69` and `ETH/70` (from upstream — ecosystem interop with stock geth)

Rationale: ETH/62 and ETH/63 are upstream-deprecated since v1.17.0 with no XDC-specific payload. ETH/68 has zero unique capability vs ETH/69. Re-introducing wire surface that upstream deliberately deleted guarantees pain every reset cycle.

### Minimum conflict-resolution rules

For `eth/protocols/eth/protocol.go`:
- `ProtocolVersions = []uint{XDPOS2, ETH70, ETH69}` (XDPOS2 first so XDC peers prefer it)
- `protocolLengths = {ETH69: 18, ETH70: 18, XDPOS2: 227}`
- Add `const XDPOS2 = 100` next to ETH69/ETH70
- Add `VoteMsg` / `TimeoutMsg` / `SyncInfoMsg` message-code constants (preserve XDC's numeric values: 0xe0, 0xe1, 0xe2)

For `eth/protocols/eth/handler.go`:
- In `handleMessage` switch: keep ETH69 / ETH70 cases verbatim; add `case XDPOS2: handlers = xdpos2`
- Build `xdpos2` handler map = `eth70` ∪ `{VoteMsg, TimeoutMsg, SyncInfoMsg → handleVoteMsg/handleTimeoutMsg/handleSyncInfoMsg}`
- Do **not** add `eth62` / `eth63` handler maps
- `MakeProtocols` loop: no change (already version-agnostic)

### Status packets

- **Drop** `StatusPacket62` (no ETH/62 → no consumer)
- **Drop** `StatusPacket68NoTD` (ETH/69 already dropped TD upstream natively)
- XDPoS2's own status frame goes through an `xdpos2` handshake path, not via stock-eth status

### Risk that flips the decision

XDPoS V2 finality may require Vote/Timeout gossip to **any** peer that can route them, not only direct XDC↔XDC. If mainnet topology has XDC validators separated by stock-geth relay nodes, dropping ETH/68 (where votes could piggyback) breaks finality.

**Smoke test**: on a 3-node Apothem testnet, force two XDC nodes to peer only through a stock-geth bridge node, observe V2 finality. If vote count at validator B never reaches quorum within 4 epochs → flip to Option A and tunnel VoteMsg via an ETH/68 extension.

### Critical files

- `eth/protocols/eth/protocol.go`
- `eth/protocols/eth/handler.go`
- `eth/protocols/eth/handlers.go`
- `eth/protocols/eth/handshake.go`
- `eth/protocols/eth/peer.go`

---

## [PLANNER-ADVICE 2026-05-23 meta-strategy] Opus planner — pivot to Strategy Y per-file

After B0–B4 (25 raw commits absorbed of 677), F-bucket density in remaining
overlay measured: 195 F-bucket-touching commits across 693 remaining
commits. At B3-pace (≈60 min per F-bucket bundle including escalation),
per-commit replay = ~200 hours. Mathematically untenable.

### Recommendation: Strategy Y (F-bucket final-state copy) per-file

The "skeleton + re-apply" tripwire policy from the first Opus advice was
right in principle but mis-priced the cost. F-bucket files diff ~3,600 net
lines across 19 files; replaying 195 commits to land 3,600 lines is paying
50× in process overhead for history fidelity that's already preserved on
`xdc-network` and `xdc-network-pre-reset`.

X (stay the course) capitulates to sunk cost.
Z (one big merge) destroys the per-bundle review boundary that's working
for the 73% non-F mass — B0/B1/B2/B4 landed in 5–15 min each.
**Y keeps the cheap path cheap and surgically collapses the expensive path.**

**Amendment**: Y per-**file**, not per-bucket. Four review units total:
- `core/state_processor.go` (337 lines)
- `eth/handler.go` (220 lines)
- `core/blockchain.go` (1368 lines)
- `eth/downloader/downloader_xdc.go` (1330 lines new in XDC)

### Quality-gate sequence for consensus drift detection (Y replaces per-commit gating)

1. Three-segment archive sync from genesis with diff-vs-canonical:
   - 0 → 3.5M, 3.5M → 56.8M (V2 switch), 56.8M → tip
2. **Receipts-root parity check at every 100k-block checkpoint**, not just end-of-segment (drift on a single tx-receipt encoding silently propagates and surfaces millions of blocks later)
3. **V1→V2 transition replay at 56.8M done twice**: once cold-start from snapshot at 56.7M, once warm continuation (the bug class is "state carried across switch differs from state rebuilt at switch")
4. **txpool admission diff on live mainnet feed for 24h post-sync** (XDC bypass bugs only manifest under real mempool pressure)
5. **`eth_getProof` spot-check at 10 randomly-chosen heights** vs canonical RPC — proves trie shape, not just root hash

### Risk that would flip to Z

Cross-file invariants between F-bucket files that per-file copy would silently break (e.g., downloader.go and handler.go sharing a peer-state contract that co-evolved).

**Cheapest probe** (results below):
```
$ git log --oneline 54a91b3a..xdc-network -- eth/downloader/downloader.go eth/handler.go | wc -l
40 commits touching either file
$ count commits touching BOTH
2 of 36 downloader.go commits also touched handler.go
co-touch rate: 5%
```

5% ≤ 30% threshold. **Y per-file is safe.**

### Snapshot reuse (more valuable now, not less)

Under Y, per-commit gating is gone, so the 7 captured Apothem snapshots
become the **primary regression oracle**:
- snapfull-stalled snapshot (82208088) → pin as permanent CI fixture (captures a known failure mode)
- archive + snap pair at 82.2M → V2-era state-root cross-check
- archivepath at 82220189 → path-scheme regression test

### Critical files

- `core/blockchain.go`
- `core/state_processor.go`
- `eth/handler.go`
- `eth/downloader/downloader.go`
- `docs/PORT_LOG.md`

---

## [DISCOVERY 2026-05-23 strategy-Y-eth-handler] Strategy Y is more expensive than estimated

Attempted Y on the smallest F-bucket file (`eth/handler.go`, 220-line diff
between v1.17.3 and xdc-network HEAD). Imported the file plus its transitive
XDC dependencies (`eth/bft/handler.go`, `core/types/consensus_v2.go`,
`core/types/vote_pool.go`). Build failed with substantial v1.17.0 → v1.17.3
API drift in upstream dependencies that XDC's `eth/handler.go` calls:

```
eth/handler.go:140:  undefined: xdcSyncer
eth/handler.go:180:  undefined: downloader.NewWithSyncFromBlock
eth/handler.go:182:  too many arguments in call to downloader.New
                     have (db, syncmode, eventMux, chain, dropFn, doneFn)
                     want (db, syncmode, BlockChain, peerDropFn, doneFn)
eth/handler.go:186:  undefined: ethconfig.FastSync
eth/handler.go:210:  fetcher.NewTxFetcher signature changed
                     have (txGet, txAdd, txReq, drop)
                     want (chain, txGet, txAdd, txReq, drop)
eth/handler.go:277:  undefined: ethconfig.FastSync
eth/handler.go:590:  undefined: downloader.{StartEvent,DoneEvent}
eth/backend.go:340:  txpool.Pending signature changed
                     have Pending(filter) (map, int)
                     want Pending(filter) map
```

This is the same class of drift that broke `cmd/XDC` in B1 (option B = drop)
— XDC's code is coupled to v1.17.0 APIs that v1.17.3 has restructured.

### Implication for Strategy Y

Opus's per-file estimate (4 review units, ~3,600 lines) was the **lower
bound** — it assumed XDC's F-bucket files would be additively compatible
with v1.17.3's surrounding context. They're not. Each F-bucket file pulls in:
1. Transitive XDC dependencies (`eth/bft`, `core/types/consensus_v2.go`, etc.) — manageable, finite cascade
2. v1.17.0 → v1.17.3 upstream API drift inside the F-bucket file itself — variable per file, may be large

For `eth/handler.go`, the in-file API drift count is roughly 10 distinct upstream API changes (downloader.New signature, ethconfig.FastSync removal, fetcher.NewTxFetcher signature, downloader event types, etc.). Porting each requires understanding the upstream change and rewriting the XDC call site.

This is mini-`cmd/XDC` work, per file. Larger files (core/blockchain.go 1368 lines) likely have proportionally more drift.

### Three sub-options for proceeding under Strategy Y

a. **Port each F-bucket file's upstream API calls to v1.17.3 forms.** Most thorough; multi-hour per file. Total: probably 2-3 weeks of dedicated work for the 4 files.

b. **Drop the eth/handler.go XDC overlay entirely** (mirrors cmd/XDC's Option B). Use v1.17.3's vanilla eth/handler.go; XDC features that need it (BFT broadcast, xdcSyncer) wait for a future bundle that ports them properly. Risks: V2 finality may need the BFT broadcast wiring.

c. **Partial port**: extract from XDC's eth/handler.go ONLY the additions that don't touch upstream-changed APIs (e.g., the XDPoS engine wiring, getOrInitBft helper) and apply as a minimal patch on v1.17.3's stock handler. The 10+ upstream-drift call sites stay using v1.17.3's forms.

Authority gate — need a decision before continuing. `reset/baseline-1.17.3` is at `f67f62583` (B0-B4 + planner advice docs).

---

## [DISCOVERY 2026-05-23 chaindata-in-commits] Phase A commits carry accidental chaindata

Inspecting the next chronological non-F commits after B4 (`e79d52ba2`,
`26e9cd9c7`, `f209f2365`, `46657eff3`) revealed they bundle real code
changes WITH accidentally-committed chaindata (leveldb `.log` and `.ldb`
files under `testdata/geth/chaindata/`, `data.bak.part4/`, etc. —
gigabytes of stale dev-machine state).

Cherry-picking with `-X theirs` would import all of this into the
reset baseline. Each such commit needs:
1. Cherry-pick
2. `git rm -rf testdata/geth/chaindata/ data.bak.part4/ ...`
3. Amend

This raises the per-commit overhead to ~5 minutes each — adds up across
the remaining ~80 Phase A + 600 Phase B commits. The autopilot pace is
no longer realistic, and proceeding without manual review per-commit
risks polluting `reset/baseline-1.17.3` with non-source artifacts.

### Decision: pause autonomous Phase A continuation

`reset/baseline-1.17.3` is at `8363d7623` — clean known-good with B0–B4
merged and all advice docs.

The remaining work is genuinely multi-week and benefits from PR-by-PR
human review:
- 4 F-bucket files needing per-file v1.17.3 API porting (~2-3 weeks)
- ~600 non-F Phase A+B commits that need filtering for the chaindata
  noise (~1-2 weeks of careful cherry-pick + cleanup)
- Phase 3 validation (snapshot restores, archive sync to 3.5M, V2 switch
  replay, txpool diff, eth_getProof spot-checks) — separate workstream
  on remote xdc02/xdc04 nodes
- Phase 4 soak (7 days on xdc02 + xdc04)

This is the natural project shape: dedicated focused work, not autopilot.
The foundation built in this session (B0–B4 + planner advice + gate spec
+ bundle mechanic) is the right starting point for that work.

---

## [PLANNER-ADVICE 2026-05-23 state_transition] Opus — F-bucket resolution for #746

Concrete per-region resolutions for the 8 conflicts in core/state_transition.go.

### Pre-flight
types.IsSpecialTx must exist (added in PR #746 prep commit).

### Per-region

R1 — ApplyMessage signature: variadic owner cascade through stateTransition.owner field +
newStateTransition arg + execute() reads st.owner. SetTxContext stays inside ApplyMessage
(v1.17.3 moved it there).

R2 — buyGas balance pre-check: insert hasBigBalance short-circuit using interface
assertion on st.state.GetBigBalance(common.Address). Keep v1.17.3's uint256
balanceCheck.

R3 — maxFeePerGas: wrap with !types.IsSpecialTx(msg.To). Keep CmpBig (v1.17.3 uint256
API). BaseFee is *big.Int on v1.17.3 BlockContext.

R4 — IntrinsicGas: keep v1.17.3 8-arg signature. Add XDC override: isEIP2028 := IsIstanbul,
then if IsXDC() set false. Pass: IsHomestead, isEIP2028, IsShanghai, IsAmsterdam.

R5 — init code size: NO-OP. v1.17.3's vm.CheckMaxInitCodeSize already gates on
rules.IsEIP3860 which is set correctly via params/config.go IsEIP3860 (gated on
Eip1559Block for XDC).

R6 — effectiveTip: keep uint256 math. Add BaseFee != nil guard inside the IsLondon
branch.

R7 — fee routing: split three ways — NoBaseFee short-circuit / TIPTRC21Fee+owner /
default-coinbase. Owner path uses gasUsed * gasPrice (full fee, no base-fee deduction)
to match XDPoSChain v2.6.8 semantics.

R8 — calcRefund: KEEP vm.GasBudget return type. Add XDC postEIP3529 = IsLondon, with
override postEIP3529 = IsEIP1559 for XDC chains. Return vm.NewGasBudget(refund).

### Commit order

1. Pre-flight: IsSpecialTx (already done)
2. ATOMIC: R1 + R7 (owner cascade — same commit, breaks build if split)
3. R2, R3, R4, R6 independent
4. R5 no-op (document in commit)
5. R8 last

### Trip-wires per region

R1 — forgetting SetTxContext breaks eth_call/tracers; smoke: go test ./internal/ethapi/...
R2 — whale account (balance > 2^256) fails buyGas; smoke: state test with BigBalance set
R3 — XDC engine signing tx rejected ErrFeeCapTooLow at London; smoke: consensus/XDPoS test
R4 — gas accounting drift on non-zero calldata; smoke: replay XDC mainnet block, assert gasUsed
R5 — N/A (no-op)
R6 — panic on Sub when BaseFee==nil but IsLondon==true; smoke: unit test that combo
R7 — TRC21 owner balance fails to grow on TRC21 transfer; smoke: replay post-TIPTRC21Fee block
R8 — XDC London-but-pre-EIP1559 SSTORE-clear has wrong refund; smoke: replay XDC block in that window

---

## [PLANNER-ADVICE 2026-05-23 blockchain] Opus — F-bucket resolution for #748

9 conflicts in core/blockchain.go. Resolution direction per region:

### Unilateral (5 regions)

R1 (line 21, imports): take v1.17.3 ("context"). XDC's "bytes" import was used only by stale paths.

R2 (line 442, constructor): take v1.17.3. m.theirs re-asserts a statedb assignment already done.

R5 (line 2531, IsUBT/MPT sdb selection): already merged; just gate NoPrefetch on
`bc.cfg.NoPrefetch || bc.checkpointSyncNoState.Load()`.

R6 (line 2607, prefetch reader stats): take v1.17.3 entirely. DROP the m.theirs
"first-1000-blocks auto-degrade" — Opus flagged it as an anti-pattern that
caused the Apothem-block-16 consensus break (documented inline at file lines
2904-2915 in xdc-network).

R7 (line 2725, Process call): synthesize. Wrap v1.17.3's processor.Process
call in `if bc.checkpointSyncNoState.Load() { skip-with-empty-receipt } else { execute }`.
Preserve telemetry span on the execute path only.

R8 (line 2769, ValidateState): synthesize parallel to R7. Gate
`bc.validator.ValidateState(...)` on `!bc.checkpointSyncNoState.Load()`.

### Needs reviewer (3 regions)

R3 (line 822, P1 bulk-sync + checkpoint prune-point fallback) — requires adding
`rawdb.{Read,Write}CheckpointSyncMetadata` accessors (new API surface).

R4 (line 2383, ExecuteConfig + parentRoot empty-state coercion) — interacts with
MakeWitness and StatelessSelfValidation; needs witness-mode reviewer sanity check.
3-line guard: `if parentRoot != {} && bc.checkpointSyncNoState.Load() && !bc.HasState(parentRoot) { parentRoot = types.EmptyRootHash }`.

R9 (line 2857, P1 write fast-path) — 104 lines, largest blast radius. Four-conjunct
gate `XdcBulkSyncMode.Load() && IsXDC() && checkpointSyncNoState.Load() && XDPoS.Epoch>0 && N%Epoch!=0`.

### Atomic groups

Group A: R4 + R5 + R7 + R8 share `bc.checkpointSyncNoState` atomic.Bool field. Must land together.
Group B: R9 (P1 fast-path) depends on Group A.

### Long-running risk

Trie-reference leak under P1 fast-path. Single-block tests can't catch this. Smoke
test recommendation: `TestBulkSyncTrieReferenceSurvives900Blocks` — import 901 mock
blocks with block 900 being a checkpoint that requires real state read of block 0's
root; assert HasState(genesisRoot) still true after the 899 non-checkpoint blocks.
