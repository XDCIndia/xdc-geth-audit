# devnet-5151 legacy->modern migration runbook (25% legacy target)

Source: Workflow w09yp7y3l (2026-06-14). Mechanism + cycle + gates.

All three load-bearing facts are confirmed in the codebase: the MinePeriod stamp bug (`engine.go:683` uses static `config.Period`), the per-round verify (`verifyHeader.go:91`), and the float `>=` threshold (`vote.go:134`). The runbook follows.

---

# RUNBOOK — devnet-5151: 3-legacy/1-modern → 1-legacy/3-modern (25% legacy)

Execution checklist for the loop. Network: chainId 5151, Epoch=900, Gap=450, MinePeriod target=2s, CertThreshold=0.667, V2 active (head >2700). MasternodeVotingSMC = `0x0000000000000000000000000000000000000088`.

**One invariant governs everything:** at N=4 and N=5 the QC threshold tolerates exactly **one** down node (N=4→3 sigs, N=5→4 sigs). So **only one node may ever be in transition at a time**, and a new node must be a confirmed live producer before any legacy node is resigned. Never visit N=3 (zero fault tolerance). Always grow-then-shrink (4→5→4), never shrink-first.

---

## PHASE 0 — PREREQUISITES (block the whole migration until done)

**P0.1 — MinePeriod stamping fix MUST land first. [HARD GATE — do not start Phase 1 without it]**
- Confirmed bug: `engine.go:683` stamps `header.Time = parent.Time + config.Period` (static V1=2), but `verifyHeader.go:91` verifies against per-round `V2.Config(round).MinePeriod`. Legacy enforces **3** on this chain; modern stamps **2** → legacy rejects modern blocks (`ErrInvalidTimestamp`, ParentTime+2 vs MinePeriod=3). xdc04 currently lands ~2.6% vs 25% fair share.
- With 1 modern producer this is a throughput annoyance. With **2–3 modern producers it is fatal to a stable 2s cadence** — a third+ of slots become propose→reject→timeout→re-mint storms. Scaling amplifies the defect.
- **Fix (both parts required):** (a) change `engine.go:683` to stamp using `x.config.V2.Config(round).MinePeriod` (same source verifyHeader uses); (b) reconcile the effective value — first root-cause where legacy's `3` originates (init-time genesis `minePeriod` or XDPoSChain V2 default/+1), then set modern devnet V2 MinePeriod to match the running network.
- **Constraints:** header.Time is hash-bearing → **parity-gate** the fix (re-import the shared range, confirm state-root/receipts-root/block-hash match; keep `tools/parity-harness/xdc_rpc_parity.py` at 100%). If MinePeriod is DB-baked, applying it requires **wipe+resync** of each modern node (cheap — only ~5.4k blocks).
- **[HUMAN DECISION]** Confirm the target MinePeriod value (2 vs 3) and approve the hash-changing fix before deploy.

**P0.2 — Decide candidate-join mechanism. [HUMAN DECISION]**
The `propose()` path is a known blocker (Phase F: `uploadKYC` succeeds but `propose()` reverts ~25k gas for an unidentified require beyond value/KYC/!isCandidate). Two options:
- **Option A — slot swap (recommended for this devnet, sidesteps propose):** new modern node takes an already-staked legacy genesis slot's etherbase (different address from xdc04's `0xe20fdc70`). No `propose()` needed. This is the natural fit since the migration *replaces* legacy nodes anyway.
- **Option B — true new candidate:** must first root-cause and fix the `propose()` revert (disassemble 0x88 / find contract source on xdc03). Required only if you need net-new staked addresses beyond the 4 genesis slots.
- For the 1L/3M end state with the 4 genesis slots, **Option A is sufficient and avoids the propose blocker entirely.** Below I give both the swap path and the propose path; default to swap.

**P0.3 — Binary + genesis prep (each new modern host).**
- Build `geth` natively on Linux (`fix/hookreward-1800-parity`); verify ELF: `head -c4 build/bin/geth | od -c` == `177 E L F` (Mach-O from Mac fails `Exec format error`).
- Init from `genesis-modern.json` ONLY (londonBlock=0, baseFeePerGas=0x2e90edd00; genesis-hash-equal fa0d6e…d1813d to legacy). NOT the legacy genesis.

**P0.4 — Hosts.** Place the 2 new devnet validators on **xdc04 (145G free)** and **xdc03 (325G, 20 cores)** — both already run devnet-5151, have binary/genesis/scripts. devnet chaindata is single-digit GB. Do NOT use xdc168 (13G, snapshot server) or xdc01 (16G RAM).
- **[HUMAN DECISION]** Confirm these two hosts are acceptable (co-locating a validator on xdc03 alongside the legacy producers).

**P0.5 — Staking funds.** Each `propose()` (Option B) needs `minCandidateCap = 10 XDC` deposit + gas; optional `vote()` needs `minVoterCap = 100 XDC`. Option A (swap) reuses existing genesis stake — no new funds.
- **[HUMAN DECISION]** If Option B: confirm funding source for owner accounts.

**P0.6 — Baseline green.** Before touching anything, the current N=4 set must pass all gates G1–G6 (§ gates below) over ≥1 epoch. Record baseline head-convergence hash and per-node proposer shares.

---

## STABILIZATION GATES (referenced as Gx throughout — measure over ≥30 blocks AND ≥1 epoch boundary)

| Gate | Metric | Pass condition |
|---|---|---|
| **G1 Block time** | mean inter-block Δt, last 30 blocks | 1.9s ≤ mean ≤ 2.3s; no single gap > 2× TimeoutPeriod (no timeout-driven jumps) |
| **G2 Fairness** | each active node's share of last 5×N blocks | within ±40% of fair (N=5 fair=20%→accept 12–28%; N=4 fair=25%→accept 15–35%); new node strictly >0 and trending to fair |
| **G3 QC formation** | QCs decoded from headers | every block carries valid QC at threshold (4 sigs@N=5, 3 sigs@N=4); zero rounds advanced by TC/timeout |
| **G4 Seal validity** | logs / verifyHeader | 0 invalid-timestamp, 0 invalid-merkle, 0 "not in masternode list" over window |
| **G5 Head convergence** | `eth_getBlockByNumber(head-6)` hash on all nodes | identical at common finalized height |
| **G6 Cert spare** | live in-set producers vs required sigs | ≥ required + 1 spare |

Probes: block time/fairness/heads → `eth_blockNumber`, `eth_getBlockByNumber`. QC/merkle → `XDPoS_getV2BlockByNumber` + log scan for `invalid|timeout|QC`. Set membership → `XDPoS_getMasternodesByNumber`.

---

## PHASE 1 — Stand up modern #2 (do NOT touch any legacy node yet)

**1.1 Datadir + fresh etherbase** (skip account-new if using Option A swap and you'll import the legacy slot key):
```bash
DD=/root/devnet-v1v2/modern-val2; mkdir -p "$DD/logs"
printf 'valpass2026' > /root/devnet-v1v2/modern-val2-password.txt; chmod 600 /root/devnet-v1v2/modern-val2-password.txt
/root/devnet-v1v2/bin/geth --datadir "$DD" account new --password /root/devnet-v1v2/modern-val2-password.txt   # Option B only
```
**1.2 Init from modern genesis** (confirm hash fa0d6e…d1813d):
```bash
/root/devnet-v1v2/bin/geth --datadir "$DD" init /root/devnet-v1v2/genesis-modern.json
```
**1.3 Launch + sync (mine on, unique ports, unique etherbase ≠ 0xe20fdc70):**
```bash
VAL=0x<new-or-swapped-etherbase>
/root/devnet-v1v2/bin/geth --datadir "$DD" --networkid 5151 \
  --port 31320 --syncmode full --gcmode full --state.scheme hash --snapshot=false \
  --bootnodes enode://e1ec7a19…@65.21.27.213:31311,enode://f6643aa9…@65.21.27.213:31313,enode://bc9cbd3d…@65.21.27.213:31314 \
  --mine --miner.etherbase "$VAL" --unlock "$VAL" \
  --password /root/devnet-v1v2/modern-val2-password.txt --allow-insecure-unlock --miner.gasprice 0 \
  --http --http.addr 127.0.0.1 --http.port 9570 --http.api eth,net,web3,admin,debug,miner,xdc,XDPoS \
  --authrpc.port 9670 --authrpc.addr 127.0.0.1 --maxpeers 30 --verbosity 3 \
  --log.file "$DD/logs/geth.log"
```
If discovery is blocked: add `--nodiscover` and `admin_addPeer` the 3 legacy enodes via **IPC** (HTTP addPeer unreliable).

**1.4 Sync gate (before staking):** node reaches head, clean V1→V2 switch at 2700 (zero BAD BLOCK), 0 invalid-timestamp in log (proves P0.1 fix took), heads converged with the legacy producers (G5). Node will NOT mint yet — it is not in the set.

**Safety/rollback (Phase 1):** No legacy node was touched. If the node won't sync or shows invalid-timestamp rejects → P0.1 fix did not take; stop the node, do not proceed. Clean revert = just stop modern #2.

---

## PHASE 2 — Make modern #2 a candidate → grow set to N=5

**2.A Option A (swap — recommended):** import/configure the new node's etherbase to an existing legacy genesis slot's owner key (a slot you will retire later, e.g. node3 `0xf6643aa9`-owned), distinct from xdc04's. No tx needed — the slot is already a staked candidate. Skip to 2.3.

**2.B Option B (propose — only if net-new address, requires P0.2 fix):**
```
# 2.1 KYC once if owner is new:  to=0x..88  data=0xf5c95125 || abi(string)
# 2.2 propose:                   to=0x..88  data=0x01267951 || left-pad32(VAL)   value=10 XDC (1e19 wei)
#     (optional vote to raise stake above ties: data=0x6dd7d8ea||left-pad32(VAL) value>=100 XDC)
```

**2.3 Wait for the boundary — do NOT measure before this.** The new set is computed at the **gap block** (`N % 900 == 450`) and goes live at the next **epoch-switch block** (`N % 900 == 0`). To be in epoch E, the candidate must be staked before block `E*900 - 450`.

**2.4 VERIFY GROWTH at N=5 (all must hold for ≥1 full epoch):**
- modern #2 in active set on ALL nodes (`XDPoS_getMasternodesByNumber` → MasternodesLen=5).
- Gates G1–G6 GREEN at N=5 threshold (4 sigs). modern #2 proposer share strictly >0, trending to 20%.
- State now: **3-legacy / 2-modern, N=5, healthy.**

**Safety/rollback (Phase 2):** No legacy resigned yet. If modern #2 joins but won't mint or emits invalid seals → Option A: stop it / revert etherbase; Option B: `resign(VAL)` (data=`0xae6e43f5||left-pad32(VAL)`). Returns cleanly to N=4 3L/1M. Do NOT advance to Phase 3 until modern #2 is a proven producer (G2).

---

## PHASE 3 — Resign legacy #1 → shrink back to N=4 (2L/2M)

**3.1 Resign trigger criteria (ALL required):** modern #2 GREEN at N=5 for ≥1 full epoch; G2 share trending fair; G6 spare ≥1; zero consensus warnings in any log for the last epoch.

**3.2 Resign the first legacy node** (its owner sends):
```
to=0x..88   data=0xae6e43f5 || left-pad32(<legacy1_coinbase>)
```
**Keep the legacy node RUNNING and synced** (resigned ≠ shut down) — it stays a non-producing full node for instant rollback. **[HUMAN DECISION — resigning a producer]: confirm which legacy node to retire and that its owner key is available to sign.**

**3.3 Wait gap block → epoch switch** (set returns to N=4).

**3.4 VERIFY SHRINK / re-stabilize at N=4 (≥1 epoch):** resigned legacy absent from set on all nodes; remaining 4 mint ~25% each; G1–G6 GREEN at threshold 3; heads converged.

**Safety/rollback (Phase 3):**
- **Rollback triggers (after the post-resign epoch switch):** block-time mean >4s sustained 30 blocks, OR any round advanced by TC instead of QC, OR head divergence, OR live in-set producers < required sigs.
- **Rollback action:** `propose(<legacy1_coinbase>)` again (Option B) or re-enable its set membership (Option A) — because it stayed running and synced, it rejoins at the next epoch switch with no resync, restoring N=5.
- The chain is QC-gated: a bad resign causes a **safe stall, not a fork**. Recovery = bring the node back / re-propose; chain resumes when ≥3 in-set nodes are live and the next QC forms.
- Root-cause before retrying: was modern #2 truly carrying load (G2), or a passive member masking the legacy node's work? Fix peering/unlock/clock and re-verify N=5 before retrying.

---

## PHASE 4 — Repeat for modern #3 (→ N=5 2L/3M, then resign legacy #2 → N=4 1L/3M)

Re-run **Phase 1 (modern #3 on the second host, ports e.g. 31330/9580/9680), Phase 2 (grow to N=5), Phase 3 (resign legacy #2)** identically. After this cycle: **1-legacy / 3-modern, N=4.** Each cycle is one-at-a-time; never run two transitions concurrently.

(If a third add is needed because your start truly has only 1 modern and you want 3 net-new modern rather than reusing slots, run the cycle a third time. With 4 genesis slots and the 1L/3M target, two add/resign cycles after the existing modern suffice.)

---

## PHASE 5 — DEFINITION OF DONE

All must hold over **≥2 consecutive epoch switches** (cross-epoch durability, not just one window):
- **Set composition:** `XDPoS_getMasternodesByNumber` on all 4 nodes = exactly **1 legacy coinbase + 3 modern coinbases**; the two resigned legacy addresses absent. 1/4 = **25% legacy — target met.**
- **Block time:** mean 2.0s (G1), no timeout-driven gaps.
- **Cert 0.667 satisfied:** QC every block at threshold 3 sigs (G3); all 4 nodes each ~25% ±10% proposer share (G2) — proves the modern majority genuinely carries consensus, not passive membership.
- **All minting accepted:** 0 invalid-timestamp / 0 invalid-merkle across the 2-epoch window (G4) — confirms P0.1 MinePeriod fix holds at scale.
- **Finality:** heads converged (G5) at multiple sampled heights; `XDPoS_getV2BlockByNumber` shows continuous QC chain, no timeout gaps.
- **Cross-epoch:** gap-block set computation reproduces the same 4 members across 2 switches (no silent drop-out).
- Only then stop the resigned legacy nodes (now safe — out of set, set proven stable without them).

---

## HUMAN-DECISION CHECKPOINTS (flag for the operator)
1. **P0.1** — approve the hash-changing MinePeriod fix + confirm target value (2 vs 3). **This is the hard gate: stable 2s with 2–3 modern producers is impossible without it.**
2. **P0.2 / Phase 2** — choose Option A (slot swap, no propose) vs Option B (fix `propose()` revert). Default A.
3. **P0.4** — confirm xdc04 + xdc03 as the two new-validator hosts.
4. **P0.5** — funding for `propose`/`vote` (Option B only).
5. **Phase 3.2 / 4** — approve resigning each specific legacy producer and confirm its owner key is available.

## Fault-tolerance reference (governs ordering)
| N | N×0.667 | sigs req | max down | safe |
|---|---|---|---|---|
| 4 start | 2.668 | 3 | 1 | yes |
| 5 transient | 3.335 | 4 | 1 | yes |
| 4 end | 2.668 | 3 | 1 | yes |
| 3 (NEVER) | 2.001 | 3 | 0 | NO — plan never visits |

Key source paths: MinePeriod stamp bug `consensus/XDPoS/engines/engine_v2/engine.go:683`; per-round verify `consensus/XDPoS/engines/engine_v2/verifyHeader.go:91`; QC threshold `consensus/XDPoS/engines/engine_v2/vote.go:134`; validator contract `0x..88` (propose `0x01267951`, resign `0xae6e43f5`, vote `0x6dd7d8ea`, uploadKYC `0xf5c95125`).
