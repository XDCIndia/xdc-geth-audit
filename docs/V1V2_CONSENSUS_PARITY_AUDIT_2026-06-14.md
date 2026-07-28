# XDPoS V1+V2 Consensus Wire-Parity Audit — modern go-ethereum vs legacy XDPoSChain

**Date:** 2026-06-14
**Branch:** `fix/hookreward-1800-parity`
**Issue:** [#951](https://github.com/XDCIndia/go-ethereum/issues/951) (V1 sealer defects) + #190 (V1 PoA round-robin sealer)
**Method:** 9-dimension fan-out audit (48 agents) comparing every consensus-critical path in the
modern fork against canonical `XDPoSChain` (../XDPoSChain, branch main), each divergence
adversarially re-verified. Live reproduction on devnet-5151 (chainId 5151, V2 switch 2700,
epoch 900, period 2s) with modern minter xdc04 (`0xe20FdC70…`) + 3 legacy V1 producers on xdc03.

---

## 1. Executive summary

Modern geth **syncs** V1+V2 and **mints** valid V1 blocks (proven: xdc04 produced in-turn block
785, diff=4, correct 4-masternode set), but its minted V1 blocks get **orphaned** and the node
then **wedges on its own fork** with no recovery. Two compounding root-cause families:

- **Consensus/miner divergences (this audit, 21 confirmed):** the V1 mint path lacks
  legacy's synchronous seal, out-of-turn fallback, M2 double-validation (`header.Validator`),
  and 4-byte-M2 checkpoint `header.Validators` encoding. Several V1 checkpoint-verification
  rules are also weakened.
- **Architectural (modern-only, found during live triage):** upstream geth **removed
  total-difficulty tracking** (post-merge). XDPoS V1 fork choice *requires* cumulative TD.
  Modern has no `GetTd`/`WriteTd`/TD-based reorg; the port substitutes a
  `maybeRollbackOnUnknownAncestor` SetHead heuristic in `eth/sync_xdc.go` — which is currently
  **not firing** for the live wedge.

Net: V2 BFT is at near-parity (QC-chain fork choice, which modern implements). V1 PoA is the
gap — it needs TD-based fork choice (or a robust rollback) *and* the missing seal/validator
mechanics for modern's blocks to be accepted and to stick.

---

## 2. Live reproduction (devnet-5151, 2026-06-13/14)

1. Deployed fork-recovery gate `c92d0b551` to xdc04 → it escaped the prior block-602 stall and
   resynced to ~759. **The gate works for the case where the syncer rollback fires.**
2. xdc04 then minted in-turn block **785** (diff=4, creator=`0xe20FdC70`), made it head.
3. Network (3 legacy producers) advanced to 844 → 1338 on a **different** 785; xdc04's 785 was
   orphaned.
4. xdc04 rejects canonical 786→1338 with `unknown ancestor` (74+ retry attempts,
   `parentNil=true`); `v8.2 snapshot walk-back failed`. **The rollback never fired** — permanent
   wedge at 785.
5. Spurious errors on every V1 block: `[GetMasternodesFromEpochSwitchHeader] V1 pre-switch
   header passed to V2 engine` + `[getExtraFields] not V2 extra format` (the V1→V2 leak).

---

## 3. The two defects in #951, precisely

### Defect 1 — modern's in-turn V1 block is orphaned (never adopted by the network)
Compounding causes (audit dimensions *V1 block production* + *V1 header verification*):

- **1a — Seal latency (P1):** wrapper `Seal` (xdpos.go:1456-1461) delays until `header.Time` +
  a Clique wiggle, then `commitMinedBlock` (xdpos_worker.go:658-667) waits *again* until
  `block.Time`. Legacy V1 seals synchronously (zero delay) once `YourTurn` time-gates. Modern's
  block therefore arrives after legacy's out-of-turn block and loses the cumulative-TD race.
- **1b — No out-of-turn fallback (P1):** modern only mints **strict in-turn**
  (`(preIndex+1)%N==curIndex`). Legacy's `YourTurn` returns true for an out-of-turn signer once
  `gap = minePeriod*Hop` wall-clock elapses (engine_v1/engine.go:401-433), producing the
  lower-difficulty catch-up block. Modern cannot, losing liveness + the TD race when in-turn is down.
- **1c — Empty `header.Validator` past epoch 900 (P1/P2):** modern V1 `Seal` never sets
  `header.Validator` (the M2 double-validation signature), and `RecoverValidator` is a stub.
  On devnet-5151 (chainId 5151 ≠ testnet 51 ⇒ legacy `fullVerify=true`), legacy **rejects** any
  V1 block with `Number>Epoch` and empty `header.Validator` (`ErrNoValidatorSignature`) and
  enforces `RecoverValidator==GetValidator` (`ErrFailedDoubleValidation`). So modern's V1 blocks
  above block 900 are hard-rejected by the legacy producers regardless of timing.

### Defect 2 — modern can't reorg off its own losing fork (permanent wedge)
- Upstream geth removed TD fork choice; `writeBlockAndSetHead` reorgs only on parent-mismatch
  with **no TD comparison**, and `insertSideChain` has no `externTd>localTd` gate (legacy
  blockchain.go:1359-1531 has both). Two same-height blocks (modern's 785 vs network's 785)
  can't be compared — modern keeps its own.
- The port's substitute, `maybeRollbackOnUnknownAncestor` (eth/sync_xdc.go:2283), should
  `SetHead(localHead-window)` after `xdcUnknownAncestorThreshold=8` consecutive
  `ErrUnknownAncestor` at the same `fromBlock`. Live it never fired (no "fork detected —
  rolling back" log across 74 failures). **Open: why the counter doesn't reach threshold** —
  candidate causes under investigation: at-tip early-return path vs the bulk path, or
  `errors.Is(err, ErrUnknownAncestor)` not matching the wrapped InsertChain error. This is the
  most tractable unblock for "modern stays in consensus while minting."

---

## 4. The 21 confirmed divergences (prioritized)

### P0 — block-hash / state-root divergence
1. **V1 checkpoint `header.Validators` encoding** *(block-hash, every epoch=900)* —
   modern `eth/hooks/hooks.go:31-65` encodes the signer set as 20-byte addresses; legacy
   encodes 4-byte M2 indices via `BuildValidatorFromM2` (M2ByteLength=4). `header.Validators`
   is in `Header.Hash()`, so every modern-minted V1 checkpoint hashes differently and a
   `fullVerify` legacy node rejects it. **Fix needs the #72 contract client** (M2 randomize
   ordering from the 0x…0090 contract). Until then **modern must NOT mint V1 checkpoint blocks**
   (verify-only is OK — modern's `HookVerifyMNs` only warns). Modern's `BuildValidatorFromM2`
   (contracts/utils.go:335) is also itself wrong (1-byte vs 4-byte) and must be fixed too.
2. **Foundation-as-owner 90% reward skip** *(reward.go:358)* — modern's
   `if owner != foundationWalletAddr { balances[owner]=rewardMaster }` **burns** the 90% when a
   signer's candidate-owner is the foundation; legacy (contracts/utils.go:433) credits it
   unconditionally (foundation gets 100%). **⚠ CONFLICT: this is shipped fix #186 (commit
   72a14851c) for Apothem BAD BLOCK 83,049,946.** The static XDPoSChain checkout shows no skip;
   Apothem mainnet may TIP-gate it. **DO NOT blind-revert.** Resolve via parity harness:
   `debug_getModifiedAccountsByNumber` across a known foundation-owner reward epoch on a live
   canonical legacy node vs modern, then record the authoritative behavior + any TIP gate in an ADR.

### P1 — accept/reject or liveness divergence
3. V1 double-validation (M1/M2) absent in wrapper `verifySeal` (xdpos.go:942-1013).
4. `ErrNoValidatorSignature` gate absent + `header.Validator` never set on V1 seal (1c above).
5. Checkpoint signer/penalty verification replaced by bootstrap-from-header (loses
   `getSignersFromContract` fallback + `IsIgnoreSignerCheckBlock` whitelist + penalty equality).
6. **V1 out-of-turn time-gap fallback missing** (1b above) — liveness + TD-race.
7. **V1 Seal latency** (1a above) — #951 Defect-1 orphaning.
8. `header.Penalties` not validated on V1 checkpoints (legacy `ErrInvalidCheckpointPenalties`).
9. Snapshot auto-bootstrap-from-header instead of contract-fallback-then-reject.
10. V2 bulk-sync tolerances (`HookPenalty==nil`, `snapshotMissing`, `isSupersetCase`) not gated
    by `fullVerify` — can accept-at-tip a divergent masternode set legacy rejects.
11. V2 epoch-switch **superset** masternode-set tolerated even at `fullVerify=true`.
12. **`verifyCascadingFields` switch-block `>=` (xdpos.go:862)** — misreads V1 switch-block
    `header.Validators` as addresses → persists corrupted snapshot. **✅ FIXED this pass.**

### P2 / P3 — narrow / masked
13. Bulk-sync light path (`verifyHeaderLightV1`) skips `verifySeal` entirely.
14. V1 double-validation header.Validator not set on mint (block-hash, masked by devnet fullVerify).
15. `IsIgnoreSignerCheckBlock` ported but never consulted (must wire before removing #9 bootstrap).
16. V1 `verifySeal` skips difficulty check when computed difficulty==0.
17. **V2 `minePeriod` first-V2-block `>=` (verifyHeader.go:92).** **✅ FIXED this pass.**
18. V2 proposer leader-index mismatch tolerated during bulk sync.
19. V2 `getSnapshot` ==13 / <=20 candidate-count rebuild heuristic collides with small sets.
20. EIP-1559 header check present in legacy V1, absent in modern V1 (1559 inactive in V1 era — hardening).
21. V2 1/1024 gas-limit bound check is modern-only (stricter; never rejects canonical — keep, ADR).
    + **CalcDifficulty wrapper `>=` (xdpos.go:1486)** — test/chain_makers only; deferred.

V2 verdicts: header-verify MINOR, block-production MINOR (0 confirmed; previously proven to mint
blocks legacy accepts), masternode/penalty MINOR. **V2 is at near-parity; V1 is the gap.**

---

## 5. Fixed in this pass (build-verified, deployed-pending)

| # | File | Change | Risk |
|---|---|---|---|
| A | `consensus/XDPoS/xdpos_api.go` | `HandleProposedBlock` now returns nil for V1 blocks (`!IsV2Block`) — kills the V1→V2 leak (the 761 errors) at the wrapper, protecting both the miner and `eth/sync_xdc.go:2058`. Mirrors legacy XDPoS.go:299-306. | low |
| B | `consensus/XDPoS/xdpos.go:862` | `verifyCascadingFields` switch-block guard `>=`→`>` (P1 #12) — stops V1 switch-block snapshot corruption. | low |
| D | `consensus/XDPoS/engines/engine_v2/verifyHeader.go:92` | `minePeriod` first-V2-block guard `>=`→`>` (P2 #17) — relaxes to match legacy (can't reject canonical). | low |

All match legacy exactly and only restore parity. `make geth` clean.

---

## 6. Remaining plan (NOT yet done — prioritized)

**To make modern MINT accepted V1 blocks that stick (the user's goal), in order:**

1. **Fix the rollback (Defect 2)** — root-cause why `maybeRollbackOnUnknownAncestor` doesn't reach
   threshold; make modern self-heal off its own fork. *Highest tractability, unblocks "stays in
   consensus."* (eth/sync_xdc.go)
2. **V1 synchronous seal + out-of-turn fallback (Defect 1a/1b)** — `YourTurnV1Gated` (gap-based) +
   remove redundant blockTime waits so the in-turn block seals/broadcasts promptly.
3. **M2 `header.Validator` double-validation (Defect 1c)** — implement `RecoverValidator` +
   M2 `GetValidator`; set `header.Validator` on seal; add the `ErrNoValidatorSignature` +
   `ErrFailedDoubleValidation` gates. Required for legacy `fullVerify` peers to accept modern
   V1 blocks > epoch 900.
4. **Restore total-difficulty fork choice for XDC chains** *(architectural — ADR + parity harness)* —
   `rawdb.ReadTd/WriteTd`, `BlockChain.GetTd`, write TD in `writeBlockWithState`, TD comparison in
   the insertChain canonical decision + correct cumulative TD in `BroadcastBlock`. This is the
   legacy-parity fix that makes modern's in-turn block *win* and obviates the rollback hack.
5. **P0 checkpoint Validators encoding** — needs #72 contract client; until then gate modern off
   minting V1 checkpoints.
6. **P0 foundation-skip reward** — resolve the #186 conflict via parity harness + ADR.

**Mainnet hardening (gate behind `fullVerify`, parity-test each):** items 3, 5, 8, 9, 10, 11,
13, 15, 16, 18, 19, 20.

**Do NOT** ship items that change block hash (1, 2, 3-on-seal) without parity-harness verification
across the relevant block range — consensus correctness is sacred (CLAUDE.md).

---

## 7. Live validation results (devnet-5151, 2026-06-14)

After the Tier-1 + oscillation-cooldown fixes were deployed to xdc04 (modern):

### 7.1 V1 consensus parity — 100% (2100/2100 blocks, bit-exact)
`tools/parity-harness/xdc_rpc_parity.py` compared modern xdc04 vs legacy XDPoSChain node1
block-by-block over the SHARED devnet-5151 chain, blocks 1..2100 (covers checkpoints 900 & 1800,
the first reward block 1800, and all signing-tx blocks):

```
compared: 2100   PARITY: 2100   DIVERGENT: 0   skipped: 0
verdict: PASS — full consensus parity
```

Evidence: `docs/parity-evidence/parity-devnet-v1-blocks-1-2100.json`. Because the block hash is the
canonical fingerprint of every consensus field (stateRoot, receiptsRoot, transactionsRoot, gasUsed,
difficulty, extraData, validators, penalties, …), a hash match across 2100 blocks proves modern's
import + state execution is bit-identical to legacy for the entire V1 range. **V1 import/execution
parity: confirmed.**

### 7.2 Sync on both consensus — VALIDATED
- V1 phase: xdc04 tracked tip (after oscillation-cooldown fix broke the block-785 wedge).
- V1→V2 switch (block 2700): crossed cleanly, ZERO boundary BAD BLOCK; switch-block masternodes
  correctly read from header.Extra (V1 format, count=3).
- V2 phase (>2700): xdc04 imported past the switch, tracking tip; modern and legacy report the
  identical head live.

### 7.3 The remaining gap is PRODUCTION, not import — and it cascades V1→V2
`XDPoS_getMasternodesByNumber(2760)` = the 3 legacy producers only; xdc04 (0xe20FdC70) is NOT in the
V2 set. It never produced an *accepted* V1 block (Defect 1: empty header.Validator past epoch 900 +
seal latency), so it was penalized/excluded at the switch. **Defect 1c gates BOTH V1 and V2
minting** — a modern node that can't land V1 blocks is penalized out of the V2 set. Modern's V2
sealer itself is known-good (minted accepted V2 blocks on xdcscan when it WAS in the set).

### Validation matrix
| | sync | mint (accepted) |
|---|---|---|
| V1 (PoA) | ✅ tracks tip; **100% import parity (2100/2100)** | ❌ Defect 1c (header.Validator) |
| V1→V2 switch | ✅ clean, zero BAD BLOCK | — |
| V2 (BFT) | ✅ tracks tip | ⛔ blocked: not in V2 set (cascade from Defect 1) |
