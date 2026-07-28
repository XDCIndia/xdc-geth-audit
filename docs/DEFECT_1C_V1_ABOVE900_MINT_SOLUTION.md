# Defect 1c — make modern geth mint accepted V1 blocks above the epoch (#951)

**Date:** 2026-06-14 · **Branch:** `fix/hookreward-1800-parity` · **Issue:** #951
**Escalation:** Opus architectural design (workflow `wc1ex2mxj`, 4 code-mapping agents + 1 synthesis)

## The ask
Make modern xdc04 (the `0xe20fdc70` slot) mint V1 blocks above the epoch boundary (block 900)
that legacy producers accept, on devnet-5151, then carry through the V2 switch at 2700.

## Root cause (two defects, both from modern go-ethereum losing TD fork-choice post-merge)

**RC1 — ADOPTION / churn.** Above the epoch, every V1 block N exists in two forms that share
parent N-1, number, and Difficulty (Difficulty is inside SigHash, unchanged by co-signing) but
differ in `header.Validator` and therefore in block hash: the M1's just-minted **EMPTY** version
and the assigned-M2's **CO-SIGNED** version. Legacy canonical is always the co-signed version, so
block N+1 builds on co-signed-N. The xdcSyncer fetches by NUMBER (`sync_xdc.go:1905/1919`); a
by-number request resolves to the peer's canonical = co-signed (`handlers.go:106/213`). The wedge
fires only when xdc04's local head is already a fork-side **empty-N** (pre-existing chaindata, a
self-minted M1 block, or a transient race): fetching N+1 by number returns co-signed-N+1 whose
parent co-signed-N is **absent** → `consensus.ErrUnknownAncestor` (`xdpos.go:646`) → InsertChain
fails (`sync_xdc.go:2032`). The only escape was `maybeRollbackOnUnknownAncestor` (8 consecutive
failures, then `SetHead(head−window)` doubling from 128); every rollback re-stamps
`XdcLastForkRollbackUnix` + `XdcForkRecovering`, which **re-gate the miner**. `writeBlockAndSetHead`
(`blockchain.go:1877`) does fork choice on parent-hash **only, with no TD compare**, and `reorg`
adopts whichever same-height block arrived **last** → head ping-pongs, never stays fresh for the 3
heartbeats needed to flip `XdcBulkSyncMode=false` → miner stays gated.

**RC2 — PRODUCTION.** The shipped self-M2 mint gate (`xdpos_worker.go:611-617`) sealed an
above-epoch slot only when self was BOTH the M1 creator AND its own assigned M2. M2 is a randomized
permutation (`double_validation.go:62`), so self==M2 is rare with N=4 → xdc04 deferred nearly every
slot. That traded the churn for near-zero minting and is **not** legacy behaviour. Legacy M1 ALWAYS
seals its creator slot, fills `Validator` only if self==M2 (XDPoSChain `engine_v1/engine.go:933-939`),
else broadcasts the empty version for the assigned M2 to co-sign in place (`fetcher.go:681-748`).

## Why NOT restore TD (rejected Option A)
TD restoration does **not** resolve the same-height contest: empty-N and co-signed-N have
**identical TD and identical number**, so the legacy equal-TD tiebreak (only fires for a *higher*
number) never selects between them. Legacy converges cumulatively at N+1, and only if co-signed-N
is **already retrievable on disk** — which is exactly the acquisition xdc04 lacks. TD is
necessary-but-insufficient for legacy and unnecessary once acquisition is fixed; restoring it is the
most invasive change (rawdb schema, every write path, genesis, core head-selection hot path) and
would still need the acquisition fix below. So we did NOT restore TD.

## Solution — Option D (implemented)

### Part B — by-hash co-signed-sibling adoption (`eth/sync_xdc.go`)
On `InsertChain` failure with `ErrUnknownAncestor`, before the slow rollback, fetch the batch's
missing parent (= co-signed-N) **by its hash** from the same peer, insert it (it links to the
co-signed-(N-1) we already have), then re-insert the batch. The existing `bc.reorg` swaps the
empty-N fork tip for the co-signed canonical in a single 1-block reorg. Head stays fresh →
`XdcBulkSyncMode` flips false at-tip → miner ungates. Uses only existing primitives
(`fetchHeaderByHash`, `fetchBodiesBatch`, `buildBlocks`, `HasBlock`) — no wire/rawdb/core/consensus
change, zero change to block bytes (the adopted block is legacy's canonical co-signed block verbatim).
- **B1** new `func (s *xdcSyncer) adoptCoSignedSibling(blocks types.Blocks, peer *eth.Peer) bool` (V1-only, gated `>Epoch`).
- **B2** range-0 failure path: `if !(errors.Is(err, ErrUnknownAncestor) && adoptCoSignedSibling(...)) { rollback; return }` — on heal, fall through to the success bookkeeping.
- **B3** identical guard on the A.77 lookahead failure path.

### Part C — legacy mint-gate parity (`miner/xdpos_worker.go`)
- **C1** deleted the self-M2 defer gate (`611-617`); the M1 now always Seals its creator slot. `Seal` (`xdpos.go:1513-1519`) already fills `header.Validator` only when self==M2 — already legacy-correct.
- **C2** in `commitMinedBlock`, for an above-epoch block with empty `header.Validator`: broadcast it for the assigned M2 to co-sign and **return without writing it to our own head** (writing it is what created the fork-side empty-N tip). Part B re-adopts the resulting co-signed canonical.

The empty-Validator reject gate (`xdpos.go:662`) is **kept** — the empty version never becomes any head.

## Consensus safety
Zero change to reward calc, masternode selection, epoch boundaries, RLP, or block-hash derivation.
Only changes (a) which sibling the syncer fetches on an ancestor miss, and (b) when the local miner
seals / whether it self-heads an empty block. Pre-deploy gate: RPC parity harness over devnet-5151
blocks 850–1200 must stay 100% (post-state root + receipts root + block hash == legacy co-signed
canonical). Hash mismatch = P0 stop.

## Status
- [x] B1/B2/B3 implemented · C1/C2 implemented
- [x] `make geth` clean, `go vet ./eth/ ./miner/` clean, `go test ./consensus/XDPoS/` ok
- [x] **deployed to xdc04 (`c52d29570`) + VALIDATED — see below**
- [x] **carried through V2 switch (2700) at cert 0.667 — VALIDATED (see V2 section)**

## Validation — devnet-5151, xdc04 (modern+fix) vs legacy node1 (2026-06-14 ~08:30 UTC)
Deployed `c52d29570` to xdc04 (0xe20fdc70 slot), restarted on the **existing churned chaindata**
(the live test of Part B). Within ~90s the node reached at-tip and stayed there.

**Both root causes fixed:**
| metric | before fix | after fix (~10 min) |
|---|---|---|
| rollbacks ("rolling back to escape") | 5000+ | 2 (initial catch-up only, 0 ongoing) |
| invalid merkle root (P0 gate) | — | **0** |
| co-signed siblings adopted (Part B) | n/a | 13 |
| >900 blocks minted (Part C: 10 self-M2 + 8 broadcast-empty) | ~4 frozen | 18 |
| `XdcBulkSyncMode` flip at-tip | never | `head=1955 ticks=3` (miner ungated) |

**Cross-client hash parity (definitive):**
- block 1973: xdc04 `0xaa56d3b9…376a37` == legacy node1 `0xaa56d3b9…376a37`
- block 1959 (xdc04 minted empty as M1 → legacy M2 co-signed → xdc04 adopted): legacy node1
  `0xa90f9f13…680e0c2` == xdc04 adopted co-signed-1959 `a90f9f…80e0c2`
  → **an xdc04-created >900 block is canonical on legacy.** Acceptance met.

**Note on BAD BLOCK log noise:** modern geth routes the empty-Validator reject
(`xdpos v1: missing M2 validator signature`) through `reportBadBlock`, so each empty sibling
seen before its co-signed twin emits a verbose "BAD BLOCK" banner. These are benign (the
empty-reject gate working as designed); zero are "invalid merkle root". Optional follow-up:
suppress that banner for `errNoValidatorSignature` to cut log noise (cosmetic, not correctness).

## V2 BFT phase — VALIDATED across the 2700 switch (2026-06-14 ~09:38 UTC, 3-agent workflow)
xdc04 crossed the V2 switch cleanly and mints accepted V2 BFT blocks. #951 comment 4701081844.
- **V2 minting:** xdc04 (`0xe20fdc70`) minted 5 V2 blocks >2700 — heights 2703/2707/2711/2726/2730.
  Clean V1→V2 boundary: coinbase zero ≤2689, flips to the masternode addr at the first block >2700.
- **Cross-client acceptance (5/5 byte-identical, xdc04==legacy node1):** 2705 `0x6eed83a1…`,
  2710 `0x3fa44251…`, 2715 `0xa0c78730…`, 2720 `0x52c6fd26…`, 2725 `0x9c5d61d9…`. All V2
  (extraData `0x02…` QC, difficulty `0x1`); parentHash + stateRoot match. Zero divergence.
- **BFT health:** QCs forming signatures=3; vote+timeout threshold **2.668 = 0.667×4** (mainnet
  cert); rounds 21→71+ monotonic; 3-chain commit firing; 0 invalid merkle; heads dead-even (Δ 1/0/1).
- Boundary noise (`Parent round not continuous`/`Round mismatch`/timeout) is normal BFT
  recovery-via-TC — QCs still form, head advances.

**END-TO-END RESULT: modern go-ethereum syncs AND mints on both V1 (PoA, >900 M2 double-validation)
and V2 (BFT, cert 0.667) consensus on devnet-5151, bit-identical to legacy.**
