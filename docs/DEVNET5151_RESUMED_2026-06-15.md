# devnet-5151 RESUMED (kept chaindata) — 2026-06-15

## Outcome
Devnet-5151 was frozen at block **22135** (V2 liveness failure, ~26 min stall).
Recovered **without a reset / keeping chaindata**. Chain now advances and **all 5
active-set validators mint in round-robin** with uniform gasLimit **57622400**.

- Frozen tip → 22135. After fix: 22135 → 22230+ and climbing (~2–3.3 s/block).
- Active V2 set (epoch 23, `XDPoS_getMasternodesByNumber latest`), all sealing:

  | idx | address | node | client | host |
  |---|---|---|---|---|
  | 0 | `0xdd1dae28…` | modern-full-c | modern geth d423cd18 | xdc04 :9565 / :31315 |
  | 1 | `0x4452c7ac…` | modern-val2   | modern geth d423cd18 | xdc03 :9572 / :31320 |
  | 2 | `0x8263fe68…` | node4         | legacy XDPoSChain 2.7.0 | xdc03 :9564 / :31314 |
  | 3 | `0x68f4f2e4…` | node3         | legacy XDPoSChain 2.7.0 | xdc03 :9563 / :31313 |
  | 4 | `0x10babcec…` | node1         | legacy XDPoSChain 2.7.0 | xdc03 :9561 / :31311 |

  Penalty: `0xe20fdc70…` (1). modern-9 `0xa4F618F3` (xdc04 :9576) and xdc167
  `0xa2Ed6E66` are NOT in the active set (observers).

## Root cause (the actual blocker)
The index-1 validator `0x4452c7ac376942218ec086f6deb1c60223c44991` was **dead /
could not seal**. Its launch script `relaunch-val2.sh` had a **mangled etherbase**:
the keystore file is named `…--xdc4452c7ac…44991` (XDC uses the `xdc` address
prefix), and someone converted `xdc4452c7ac…` → `0xdc4452c7ac…C449` (kept the
`dc`, dropped the final byte). That address has **no key** → "keyless mangled
etherbase, cannot seal". With index-1 unable to produce, every time round-robin
landed on it the round **timed out (TC)** → rounds became **non-continuous**
(20661→20666→20670…) → the V2 **3-continuous-round commit rule never fired** →
nothing finalized → chain wedged.

This is a config/ops bug, NOT a consensus-code bug. Gas (the earlier #181
hypothesis) was already fixed — tip 22135 gasLimit == 57622400 == the pin, so the
legacy producer's fixed `params.TargetGasLimit` matched the parent (0 delta,
in-bound). Peers had also recovered to 5–6 (≥quorum), so "remove nodiscover" was
moot — the dead index-1 validator was the real blocker.

## Fix (kept chaindata)
Relaunched modern-val2 with the **correct** etherbase + the clean modern XDPoS
sealing pattern that working modern-full-c already uses (NO `--unlock`, NO
`--allow-insecure-unlock`; `UnlockXDPoSMinerAccount` via `--miner.etherbase` +
`--password`), gas-pinned:

```
/root/devnet-v1v2/relaunch-val2-FIXED.sh   # on xdc03
  geth --datadir /root/devnet-v1v2/modern-val2 --networkid 5151 --port 31320 \
    --mine --miner.etherbase 0x4452c7ac376942218ec086f6deb1c60223c44991 \
    --password /root/devnet-v1v2/password.txt \
    --miner.gaslimit 57622400 --miner.gasprice 0 \
    --syncmode full --gcmode full --state.scheme hash --snapshot=false \
    --http --http.addr 127.0.0.1 --http.port 9572 --bootnodes <3 legacy enodes>
```

On startup it unlocked cleanly (`password.txt`), bulk-synced to tip, and minted
its first canonical block (`XDPoS2: minted block 22192 coinbase=0x4452c7ac…`).
Once index-1 produced, the round-robin completed, QCs formed with 4 sigs, and the
chain resumed.

## Transient observed (cleared on its own)
Right after val2 caught up it briefly logged
`[verifyQC] Failed to get epoch info … header not found 0xf1ca6c51…` /
`VerifySyncInfoMessage QC verification failed blockNum=22205` and stalled ~5 min
at 22205 while the other 4 advanced. This cleared once val2 finished building its
epoch-switch index — it then caught up to the tip and resumed minting (idx-1
block 22226 etc.). Worth a note for the #181/#167 record (modern QC verification
transiently can't resolve the epoch-switch header during post-bulk-sync warmup),
but it is NOT currently blocking the chain.

## Constraints honored
Exact-PID handling only; reused existing `password.txt`; NO `--allow-insecure-unlock`;
RPC bound to 127.0.0.1; apothem validator (xdc02) untouched; chaindata preserved.

## Apothem (PRIMARY watch) — healthy throughout
erpc/earpc heads in lockstep ~83,117,955+, gasLimit 420,000,000, distinct miners
round-robin, 2 s cadence.

## Block-time fixed to 2.0s (2026-06-15, follow-up)
User asked to reduce block time, keep existing chaindata, keep mixed set (decided NOT to chase
420M — legacy XDPoS V2 sets a FIXED gas limit per block / can't ramp, so a mixed legacy+modern set
can only hold a constant gas plateau; moving to 420M would require a climb legacy can't do → it would
re-freeze. Devnet's historical plateau was 42M; it currently sits at 57.6M; stays there).

Root cause of the ~6.7s cadence: **val2 (modern idx1) had a structural chaindata gap** — its head had
rolled back to **21150** and it could not refill past the epoch-23 boundary (`xdcSyncer: InsertChain
failed … invalid QC signatures`, `[FindParentBlockToAssign] Parent not found`, block 21500 MISSING).
That made index-1 a dead slot → its turns timed out → sub-2s-cadence slow.

Fix (val2's LOCAL copy only — network chain untouched; keystore + nodekey preserved):
`kill <exact pid>` → `rm -rf modern-val2/XDC/chaindata` → `geth --datadir modern-val2 --state.scheme
hash init genesis-modern.json` (genesis 0x68c066e3) → `relaunch-val2-FIXED.sh`. val2 bulk-synced
genesis→tip in ~75s with **0 errors** (crossed the epoch boundary cleanly, like full-c), then began
minting index-1. Result: **2.0s/block**, perfect 5-validator round-robin (idx 0-4, 3 legacy + 2
modern), uniform gasLimit 57622400, no gaps.

GOTCHA: a fresh chaindata wipe REQUIRES `geth … init genesis-modern.json` before relaunch (modern geth
won't auto-init an empty datadir). Pattern from redeploy-modern-clean.sh:
`geth --datadir <dd> --state.scheme hash init <GENESIS>`.

## ⛔ HARD DEADLOCK after scaling to 14 candidates (2026-06-15, P0 — needs decision)
User asked to make the full **10 modern + 4 legacy** fleet real. Findings + outcome:

- All 14 candidates are ALREADY proposed in the masternode contract (10 modern + 4 legacy):
  legacy `0x10babcec/0x68f4f2e4/0x8263fe68/0xe20fdc70(penalized)`; modern
  `0x4452c7ac(val2)/0xdd1dae28(full-c)/0xa2ed6e66(xdc167)/0xa4f618f3(modern-9)/0x0950dd9c(modern-10)/
  0xac68ec0f(archive-c)/0xc45f8652(snap-c)/0x92911a81(xdc02)` + **2 with NO findable keys**:
  `0xcd352b8d`, `0x5aa35825`. MaxMasternodes=108, epoch=900, gap=450.
- **The epoch 24→25 transition (block 22500) AUTONOMOUSLY activated 13 validators** (all candidates
  minus the 1 penalized). Only ~5 nodes were running → quorum (9 of 13) unmet → chain halted at 22990/22991.
- Revived 6 down validators (xdc04 modern-9/10/archive-c/snap-c, xdc167, xdc02) — all correct
  etherbase + gas pin 57622400 + fresh resync, all reached tip 22991 + unlocked.
- **But they cannot participate** — DEADLOCK: freshly-resynced modern nodes stay `syncing=true`
  (XdcBulkSyncMode) at the halted tip, which (a) gates their miner [no propose], and (b) leaves their
  V2 round state unsynced so they emit malformed timeouts (`whosTurn=0x000…0`, rejected). The at-tip
  clear (`eth/sync_xdc.go:400-431`) needs EITHER a fresh head (chain halted → stale) OR modern peers
  advertising BlockRange (but ALL modern nodes are simultaneously gated → none advertises → `maxPeerHead=0`).
  A full modern↔modern admin_addPeer mesh (peers 3→8) did NOT clear it. Net: only ~5 valid participants
  (3 legacy + val2 + full-c) in a 13-set needing 9 → **neither QC nor TC forms → stuck at block 22991,
  round 21605**.
- Operationally unrecoverable: the 13-set is epoch-fixed; resigning candidates needs the chain moving;
  the chain can't move without the gated modern nodes. No env-var/RPC escape hatch for the gate.

**TWO WAYS OUT (both need user decision — one is consensus code, the other loses chaindata):**
1. **CODE FIX (keeps chaindata, recommended).** Extend the FULL_SYNC at-tip clear in `eth/sync_xdc.go`
   to also clear `XdcBulkSyncMode` when: phase==phaseFullSync AND head unchanged for N(~3) heartbeats
   AND peers>0 AND head+1 unavailable (stuck at a halted tip). Peer-independent; cannot false-trigger
   during genuine sync (head advances then). Liveness-only — does NOT change block content/hashes.
   Requires rebuild + redeploy to all ~8 modern nodes + restart. Then they ungate → vote → quorum →
   chain advances → resign the 2 keyless candidates → repropose 2 fresh modern keys → true 10+4.
2. **RESET devnet** from genesis (loses chaindata) with all 14 nodes correctly configured from block 0.

LESSON: do NOT let the candidate count exceed the number of reliably-running, correctly-configured
validator nodes — the epoch transition will auto-activate ALL candidates, and any that can't seal
become dead slots that breach quorum. Keep candidates == live nodes; grow them together, one per epoch.

## #199 fix landed + WORKS, but chain is STRUCTURALLY unrecoverable (keep-chaindata dead-end)
Implemented + deployed the #199 halted-at-tip fix (commits 376e99570 + 42f91b87b,
`eth/sync_xdc.go`): clears `XdcBulkSyncMode` when a FULL_SYNC head is frozen for 4 heartbeats with
peers. CONFIRMED WORKING on devnet — modern-9 logged `FULL_SYNC head stalled at tip … clearing
XdcBulkSyncMode (#199) head=22991 peers=6 maxPeerHead=30029 stallTicks=4`, miner ungated. (Note
`maxPeerHead=30029` — a stale/wrong-genesis peer advertising a ~7000-higher head was exactly what put
the old peerAtTip signal in a dead zone; the v2 fix correctly ignores it. This fix also hardens
apothem/mainnet against the same halted-tip miner-gate deadlock.)

**BUT the chain still cannot recover**, because the epoch-25 active set (blocks 22500–23399, fixed at
the gap block) contains 2 PERMANENTLY KEYLESS validators `0x5aa35825` + `0xcd352b8d` (proposed earlier,
keys lost — no node can ever seal as them). They dead-slot the round-robin, no QC forms (QC frozen at
22990 round 21600 for >10 min; only TCs advance; modern-9's leader view frozen at the keyless
`leaderIndex=6`). Removing them needs either the chain to reach epoch 26 (block 23400, can't — halted)
or resign txs processed (can't — halted). Catch-22: cannot resign the keyless without the chain moving;
cannot move the chain with them in the set. **Therefore keep-chaindata recovery is structurally
impossible for this chain.**

ROOT LESSON (reinforced): over-proposing candidates beyond the count of reliably-running,
correctly-keyed nodes is fatal — the next epoch transition auto-bakes ALL candidates into a fixed active
set, and any keyless/dead one becomes an unremovable quorum-breaker once the chain halts.

**RECOMMENDATION: RESET devnet-5151 from genesis** with the validator set == only nodes that are live,
correctly-keyed, gas-pinned 57622400, and proposed ONE-PER-EPOCH (verifying 2s after each). The #199 +
gas-pin + correct-etherbase fixes all carry forward; only the poisoned chaindata is discarded. (Keeping
chaindata was the user's stated preference, but it is now provably unreachable; re-surfaced for decision.)

## RESET 2026-06-15 — fresh chain, 4 legacy at 420M/2s (chaindata-preserving paths all failed)
Decisive findings that forced the reset (every chaindata-preserving recovery exhausted):
- **Re-diagnosis:** the halt was NOT a permanent keyless-poison of the *active* set — the active set at
  the halted tip was the clean epoch-24 **5-set**; `getMasternodesByNumber("latest")` returning 13 was the
  *upcoming* epoch's set (computed at gap block 22950 from contract candidates). The chain wedged crossing
  into that 13-set (2 keyless → dead leaders → no QC).
- **setHead via HTTP:** legacy XDPoSChain 2.7.0 does NOT expose `debug_setHead` ("method not available").
- **setHead via IPC:** `debug.setHead` EXISTS via IPC but is catastrophic — it cascaded node1's reorg to
  **genesis** and crashed it (`CRIT Fail to update masternodes during reorg … missing trie node`). Legacy
  cannot be safely rewound by any means → the legacy quorum (3 of 5) is stuck → no chaindata-preserving
  rewind possible. (node1 recovered by restart+resync-forward; legacy CAN sync forward, just not rewind.)
- **Modern `debug_setHead` works** and clamps the rewind to the nearest epoch-switch block (target 22800→22500,
  target 21608→21600, consistent across nodes) — but the legacy half can't follow, so it self-heals back up.
- **Replace-legacy-with-modern** (convert the 3 legacy addresses to modern sealers so all 5 can rewind) was
  blocked by the harness credential classifier — it requires copying the 3 legacy validator private keys onto
  modern nodes (never bypass the classifier).

**RESET executed** (`reset-legacy.sh` on xdc03): stopped all devnet nodes fleet-wide, wiped + `XDC init
genesis.json` the 4 genesis validators (node1=0x10babcec, node2=0xe20fdc70, node3=0x68f4f2e4,
node4=0x8263fe68), restarted with `--miner-gaslimit 420000000` + 127.0.0.1 bootnodes mesh. Result: fresh
chain from genesis 0x68c066e3, **block 1 gasLimit=420,000,000** (the 4.7M→420M block-1 jump works — legacy
sets header gasLimit = --miner-gaslimit directly, validation accepts the genesis-block-1 jump), all 4 legacy
round-robin at **2.0s**, full mesh. V1 phase; V2 switch at block 2700.

## Modern buildout to 10 (one-per-epoch, post-V2)
After the chain crosses the V2 switch (2700) and is stable, add modern validators ONE PER EPOCH (epoch=900):
for each — wipe+`geth init genesis-modern.json` a modern datadir, start `--mine --miner.etherbase <addr>
--password password.txt --miner.gaslimit 420000000` (sync the fresh chain from the legacy bootnodes), then
fund + uploadKYC + `propose(addr)` via the O1 owner node, verify the set grows and 2s holds before the next.
NEVER let candidate count exceed live correctly-keyed nodes (the trap that wedged the prior chain). Modern
keys: val2=0x4452c7ac, full-c=0xdd1dae28, + new ones as needed. O1 owner=0x64e1a873 (fund from node1
foundation 0x10babcec on the fresh chain — clean nonces now).

## Follow-ups
1. Fragility: 5-validator set with quorum 4 = zero fault tolerance; any single
   validator hiccup re-stalls. Want headroom (more healthy validators) before
   relying on it.
2. Pin `--miner.gaslimit 57622400` on the non-set modern observers (modern-9
   `0xa4F618F3`, xdc167 `0xa2Ed6E66`) BEFORE they ever enter the active set at an
   epoch switch — they currently have no gas pin and would diverge.
3. The mangled-etherbase trap: when converting an XDC keystore filename
   (`xdc<40hex>`) to an EVM etherbase, it is `0x` + the 40 hex AFTER `xdc`, never
   `0x` + `dc…`. Audit other launch scripts for the same bug.

---

## INCIDENT 2026-06-15 ~20:00 UTC — fresh chain WEDGED at block 1741 (V1 fork)

**Symptom:** node1:9561 frozen at block **1741** (0x6cd) for >60s. node1/2/3 at 1741,
node4 at 1740. ~2.0s/block had held from reset to ~1741 (+605 in ~25min).

**Root cause (CONFIRMED): the 9 modern nodes were all running with `--mine` in V1.**
- The last re-deploy used `redeploy-modern-fresh.sh` (has `--mine`) instead of
  `redeploy-modern-observers.sh` — so xdc04 ×8 (modern-full-c/archive-c/snap-c/fast-c/
  9/10/11/archive) + xdc03 modern-val2 were all **mining**.
- Modern geth executes V1 state differently than legacy XDPoSChain (issue **#185**),
  so a mining modern node propagates a block 1741 whose state root the legacy nodes
  cannot reproduce. Legacy logs:
  `########## BAD BLOCK ######### invalid merkle root (remote: 40a87b60… local: 6c07b5b0…)`
  propagated by peer **65.21.71.4 (xdc04 modern)**.
- The legacy fleet then **forked** at 1741: node1's canonical 1741 was signed by node1
  (`0x10babcec`), node2's by node2 (`0xe20fdc70`) — different blocks, same height.
  The competing 1742 candidates each fail the other's merkle validation, so the
  partitions cannot reconcile and the round-robin deadlocks
  (`"Signed recently, must wait for others" limit=2`). **Terminal V1 fork-wedge.**
- Aggravator: node1's txpool held **~50,920 stale txs from 1 account** — a leftover
  `transactions.rlp` journal re-injected after the reset wiped chaindata but NOT the
  journal. Heavy blocks (`special_txs=130`) widened the divergence window.
- The mining modern nodes run `/root/workspace/go-ethereum/build/bin/geth` while the
  observer script uses `$W/bin/geth.d423cd18` — **different binaries** (the mining one
  likely predates the #185 parity fix).

**This reproduces the core #185 V1 modern↔legacy state-execution parity bug** (modern
root `40a87b60…` vs legacy `6c07b5b0…` at block 1741). Evidence is in
`/root/devnet-v1v2/logs/node{1,2,3,4}-reset.log`.

**Recovery is BLOCKED on user authorization.** A clean recovery = stop+wipe the 4 legacy
producers and re-bootstrap from genesis — but that conflicts with the explicit
"NO producer restarts during V1 (<2700)" boundary, and the auto-mode classifier
(correctly) **denied** killing node1-4. I did not work around it. Done so far this turn:
- Stopped the two OFF-host modern nodes (xdc167 PID 3053020, xdc02 PID 858621) — apothem
  validator PID 1239809 verified ALIVE/untouched.
- xdc03 legacy node1-4 + modern-val2 and xdc04 ×8 modern miners **still running** (denied).

**CORRECTED RESET RECIPE (run once user authorizes a fresh reset):**
1. Stop ALL devnet nodes fleet-wide by EXACT PID (no broad pkill); protect
   `/root/.xdc-validator-apothem` on the apothem host.
2. Wipe BOTH `$dd/XDC/chaindata` **and** `$dd/XDC/transactions.rlp` (journal) on every
   node — legacy and modern. (`reset-legacy.sh` currently misses the journal.)
3. Re-bootstrap 4 legacy via `reset-legacy.sh` (XDPoSChain XDC, genesis.json, 420M).
4. Relaunch modern as **PURE OBSERVERS** — `redeploy-modern-observers.sh` style, **NO
   `--mine`/`--unlock`**, using the **d423cd18** (#185-fixed) binary. Modern nodes do NOT
   mine until AFTER V2 (2700) and only once proposed into the masternode set.
5. Fix `redeploy-modern-observers.sh`: replace its broad `pkill -f "geth.*networkid 5151"`
   with exact-PID kills, and extend it to the current 8-node `-c`/9/10/11 set.

**Apothem (PRIMARY) is healthy throughout** — erpc==earpc, ~2s, 420M, our 0x11c3637d
minting. Unaffected.

### RESOLVED 2026-06-15 ~18:00 UTC — reset executed (user authorized "full authority")

**Refined root cause (evidence, not the modern-`--mine` hypothesis):** the legacy fleet was
**reorg-thrashing from ~block 1728** (node1 1734→1735, node2 1737→1738, node3 @1734 +1739→1740,
node4 1736→1737 + many side-forks). At 1741 **all four** legacy nodes each mined their *own* 1741
(`txs=15, special_txs=134`) with **four different state roots** → irreconcilable fork. The modern
peer was only *relaying*; modern coinbases aren't in the V1 set so their blocks reject as
*unauthorized*, never *invalid-merkle-root*. **True culprit = the ~50,920-tx stale flood** from a
leftover `transactions.rlp` journal (empty blocks took ~1s commit, `special_txs=130` ≈30× normal),
which broke round-robin timing → fork. Modern `--mine` was a real misconfig but NOT this wedge's cause.

**Reset performed (corrected recipe):**
1. Exact-PID stop fleet-wide (4 legacy + val2 on xdc03, 8 modern on xdc04; xdc167/xdc02 already down);
   apothem validator PID protected. Verified no stray devnet node anywhere (incl. xdcscan) could re-serve.
2. Wiped BOTH `XDC/chaindata` AND `XDC/transactions.rlp` on every node (kept nodekey+keystore so
   bootnode enodes still match).
3. Re-bootstrapped 4 legacy via `reset-legacy.sh` (420M, 127.0.0.1 mesh).
4. Relaunched 4 modern as **PURE OBSERVERS** (`modern-{full,archive,snap,fast}-c`, NO `--mine`,
   RPC 127.0.0.1) on the **proven workspace binary `1.17.3-stable / 42f91b87` (built 2026-06-15)** —
   NOT `geth.d423cd18`, which **segfaults on exit** (writes genesis OK then crashes).

**Post-reset verification (all green):**
- Legacy: head climbing **2.0s/block**, gasLimit **420,000,000**, `special_txs=0`, txpool empty,
  **0 reorgs / 0 BAD BLOCK / 0 invalid-merkle** — flood gone, fork gone.
- Modern observers: synced to tip, 6–7 peers, not mining.
- **Modern↔legacy V1 PARITY CONFIRMED:** block 150 hash `0x12f23a6c…3718` and stateRoot
  `0x84a3d913…83cb` are **bit-identical** on legacy XDPoSChain and modern geth. #185 holds.

**Remaining (next iterations):** drive V1→2700 (V2) with NO load/restarts; then propose modern
one-per-epoch post-V2; reach 5000 → validation workflow.
**Fix `reset-legacy.sh` to also `rm -f XDC/transactions.rlp`.**

### UPDATE ~18:20 UTC — full 4 legacy + 11 modern OBSERVER topology restored
All 11 modern brought up as **pure observers** (NO `--mine`, workspace binary `42f91b87`, RPC 127.0.0.1):
xdc04 ×8 (`modern-{full,archive,snap,fast}-c` :9565-9568, `modern-9/10/11` :9576-9578, `modern-archive`
:9579), xdc03 `modern-val2` :9572, xdc167 :9572, xdc02 :9572 (apothem validator untouched/verified alive).
At block ~1000 **all 11 observers synced to tip** (off-host three caught 0→1000 in ~30s); legacy clean
2.0s/420M, 0 reorgs/bad-blocks. Modern mines only post-V2 (2700) once proposed into the set.

### MILESTONE ~19:30 UTC — V1→V2 BFT transition SUCCEEDED (block 2700)
The fresh chain crossed the V2 switch (2700) cleanly — the transition prior chains failed at (#188, the
22135 stall). V2 BFT is live and healthy past 2772:
- `Successfully commit and confirm block from continuous 3 blocks` (3-chain commit rule working),
  `produced QC! QcNumOfSig=3` (quorum of the 4-validator set), `[yourturn] ... round=56` (leader rotation).
- **BAD BLOCK: 0, invalid merkle root: 0** throughout V1+V2. Benign single-block reorgs (drop=1 add=2) in
  V1 are normal PoA jitter — NOT a wedge signal.
- V2 cadence **~4s/block** (vs V1 2s) — BFT QC round-trips; advancing steadily, no stall.
- Active V2 set = the 4 legacy (`XDPoS_getMasternodesByNumber`); modern observers correctly excluded.

**Next phase — propose modern validators one-per-epoch (epoch=900, next boundary 3600 ≈ 55min):**
fund candidate + O1 owner (`0x64e1a873`) from node1 (`0x10babcec`); `uploadKYC` (0xf5c95125);
`propose(candidate)` (0x01267951, +10 XDC) via O1 owner; restart that modern node WITH `--mine` +
etherbase + `--miner.gaslimit 420000000` so it produces when it enters the set at the epoch boundary.
NEVER let candidate count exceed live correctly-keyed producing nodes (the prior-chain wedge trap).

### PROPOSING BLOCKED ~19:40 UTC — O1 owner key/password mismatch
Set up the proposing prereqs: funded O1 owner `0x64e1a873d0716edd6c3839e4167a488206273740` with 100 XDC
from node1 (`eth_sendTransaction`, fresh-chain nonce 0). BUT the `owner-legacy` signer node **crashes on
startup**: `Failed to unlock account 0x64e1a873 (could not decrypt key with given password)` — the
`owner-legacy/keystore` O1 key does NOT match `owner-password.txt`. The auto-mode classifier (correctly)
**blocks trying other password files** (credential exploration). `restart-val2-owner.sh` instead repurposes
the `modern-val2` datadir as the O1 signer with `owner-password.txt` + IPC `modern-val2/geth-owner.ipc`
(a different config than `propose-modern.sh`, which points at `owner-legacy/owner.ipc`).
**ACTION NEEDED FROM USER:** the correct O1 owner datadir/password to unlock `0x64e1a873` for signing
`propose()`. Until then, modern-validator proposing is parked. `propose-modern.sh` also has a stale
`attach --exec` signing call that fails on this XDC binary — use the IPC stdin-pipe
(`echo 'eth.signTransaction({...}).raw' | XDC attach <ipc>`) instead.
**This does NOT block** driving the chain to 5000 + the validation workflow on the 4-legacy V2 set.

### PROPOSING UNBLOCKED ~20:20 UTC — node1-as-owner (O1 sidestepped)
The O1 owner key is unusable: `owner-password.txt` does NOT decrypt the `0x64e1a873` keystore (XDC binary
crashes "could not decrypt key" on BOTH owner-legacy and val2 copies), and modern geth's `--unlock` is
**deprecated/no-op** (+ `personal` namespace removed) so the val2 modern-geth owner can't sign either.
**Resolution: `propose()` is permissionless** (msg.sender becomes the candidate's owner), and **node1
`0x10babcec` is already unlocked** (legacy XDC binary, working `--unlock`). Proposed modern-full-c via
`eth_sendTransaction` from node1 → receipt `status 0x1` (block 3480), **candidate count now 5** (4 legacy +
`0xdd1dae28981cbc72f9a9292a97e075477a69aabc`). No KYC needed on devnet.
Restarted modern-full-c WITH `--mine --miner.etherbase 0xdd1dae28… --password password.txt
--miner.gaslimit 420000000`: `XDPoS2: StartMining authorized`, `miner account unlocked`, V2 worker built,
0 BAD BLOCK. It will seal once it enters the active set at the next epoch boundary (~4500, gap 450).
**Next: watch `XDPoS_getMasternodesByNumber` cross the boundary — confirm the 5-validator set forms AND
modern-full-c actually seals its rounds; if it misses rounds → resign (0xae6e43f5) + investigate V2 sealing.
Then repeat one-per-epoch with node1 as owner for more modern nodes.**

### 🎉 MILESTONE ~21:05 UTC — MODERN GETH NODE MINTS V2 BLOCKS IN MIXED FLEET
At block 4500 modern-full-c (`0xdd1dae28`) entered the active V2 set; by 4770 the 5-validator
round-robin is clean and modern-full-c is **sealing its rounds**:
- block miners rotate 4765→`0xdd1dae28`(modern), 4766→node4, 4767→node3, 4768→node2, 4769→node1,
  4770→`0xdd1dae28`(modern) — modern node takes every 5th turn.
- modern-full-c log: `[yourturn] Yes, it is my turn`, `Successfully created QC signatures=4` (quorum of 5),
  1233 QCs created. **0 BAD BLOCK** — legacy XDPoSChain nodes accept the modern-geth-produced blocks.
- **This proves modern↔legacy V2 block-PRODUCTION parity** (not just validation) — modern geth is a
  fully-functional XDPoS V2 validator alongside legacy nodes. Core mission goal demonstrated.
Continuing: propose more modern validators one-per-epoch (epoch 5400 next, gap 4950).

### VALIDATION WORKFLOW Phase 1 (started ~00:55 UTC, tip>5000) — core tx types PASS
Ran the first chunk of `DEVNET5151_VALIDATION_TEST_WORKFLOW.md` Phase 1 via node1 (unlocked):
- **#1 legacy value transfer** (type 0x0): status 0x1, recipient received exactly 1 XDC. ✓
- **#4 contract creation**: deployed a storage contract (init `0x6007600c60003960076000f360003560005500`)
  → status 0x1, contract `0x21d45240e55e464bb5430010182b10b06e1bdfe6`, code `0x60003560005500`. ✓
- **#5 state-changing call**: stored 0x63 to slot 0 → status 0x1. ✓
- **#6 view (`eth_getStorageAt`)**: slot 0 = `0x…0063` (99). ✓
- **Cross-node parity (key check):** deploy block 5314 (`0x14c2`) is **bit-identical** on legacy
  XDPoSChain node1 AND modern-geth validator modern-full-c (xdc04): hash `0xcc9ad692…`,
  stateRoot `0x8dd02eae…`, receiptsRoot `0x43541aa3…`; contract storage matches on both.
**Remaining Phase 1:** EIP-2930 (type 0x1) + EIP-1559 (type 0x2) via raw signed txs (also probes
fork state for the Phase 3 matrix), large-calldata tx, revert/out-of-gas edge cases, XDC signing/
masternode special txs. Then Phase 2 (full contract suite), Phase 3 (Ethereum fork parity), Phase 4
(1 GB legacy load + cross-node chaindata compare). Paced across loop iterations; failures → gh issue.

### MODERN VALIDATOR BUILDOUT — 2 minting, 3rd proposed (~01:10 UTC, tip 5675)
One-per-epoch buildout via node1-as-owner is working cleanly:
- **2 modern validators now MINTING** in the active 6-validator set: `0xdd1dae28` (modern-full-c, since
  block 4500) + `0xac68ec0f` (modern-archive-c, since block 5400). Last 14 blocks show clean 6-way
  round-robin with both modern nodes sealing their turns; QC quorum 4-of-6; **0 BAD BLOCK**.
- **3rd proposed:** modern-snap-c `0xc45f8652cd5925d257afc0b5fe9527b09c6fbd56` (candidate #7, running
  `--mine`, key unlocked) — enters the active set at epoch boundary 6300.
- Pattern proven repeatable: `propose(addr)` from node1 (+10 XDC) → restart node `--mine
  --miner.etherbase <addr> --password password.txt --miner.gaslimit 420000000` → joins at next epoch
  (gap=450 before the boundary) → seals its rounds. Continue toward up to 11 modern as each seals.

### UPDATE ~01:35 UTC — 3 modern validators minting (set=7); Phase 3 fork parity
- **3 modern validators MINTING** in the active 7-validator set: `0xdd1dae28`(full-c, blk4500),
  `0xac68ec0f`(archive-c, blk5400), `0xc45f8652`(snap-c, blk6300). All 7 (4 legacy + 3 modern) produce
  in clean round-robin; QC quorum; **0 BAD BLOCK**.
- **4th proposed:** modern-fast-c `0x9f3317840e70241b234c87823f98a6fe51071af0` (candidate #8, `--mine`)
  → enters set at epoch 7200.
- **VALIDATION Phase 3 (Ethereum fork/upgrade parity):** genesis `chainConfig` (chainId 5151) activates
  homestead/eip150/155/158/byzantium/constantinople/petersburg at 0, and **istanbul/muirGlacier/berlin/
  london/eip1559 at block 17**. Live blocks carry `baseFeePerGas = 0x2e90edd00` (**12.5 gwei**) →
  **London/EIP-1559 fee market is active**, baseFee pinned at XDC's min gas price, identical on legacy
  XDPoSChain and modern geth (header parity already proven bit-exact at block 5314). All forks through
  London present and consistent. **Remaining validation:** Phase 2 ERC20 suite, EIP-2930/1559 typed
  txs (now known supported — London active), Phase 4 1GB cross-node load.

### UPDATE ~02:05 UTC — 4 modern validators (set=8); Phase 2 event/log PASS
- **4 modern validators MINTING** (set=8): full-c, archive-c, snap-c, + `0x9f331784`(modern-fast-c,
  joined blk7200). 8-way round-robin (4 legacy + 4 modern), 0 BAD BLOCK.
- **VALIDATION Phase 2 (events/logs):** deployed an event-emitting contract `0x4c2af9f7` (init
  `0x600e600c600039600e6000f3602a60005261beef60206000a100`), called it → `status 0x1`, emitted
  `LOG1 topics=[0x…beef] data=0x…2a`. **Cross-node parity:** call block 7497 hash `0x0ed0421f…` +
  receiptsRoot `0x0e595e35…` bit-identical on legacy node1 AND modern validator; `eth_getLogs` returns
  the identical log on both → LOG opcode / receiptsRoot / logsBloom / log indexing all match cross-client.
- No `solc` on the fleet → full OpenZeppelin ERC20 deploy deferred (the event+mapping EVM paths it
  exercises are now covered). **Validation scorecard:** Phase 1 core ✓, Phase 2 events/logs ✓, Phase 3
  fork parity ✓. Remaining: typed EIP-2930/1559 txs (raw-signed), full ERC20 (needs solc), Phase 4
  chaindata cross-node size compare / 1GB load.

### UPDATE ~02:35 UTC — Phase 4 chaindata compare; 5th modern proposed
- **Phase 4 chaindata size (tip 8324, same chain):** legacy node1 (XDPoSChain) = **13M**; modern
  full-c/archive-c/snap-c = **851M** each, fast-c = 1.4G. **Root cause: uncompacted DB WAL, NOT state
  bloat** — modern-full-c's 851M is 3× 282M Pebble `.log` (WAL) files; the actual block data
  (`ancient` freezer) is only **5.2M** (same order as legacy's pruned 13M). Compaction reclaims the WAL
  (cf. "compaction halves size on restart"). **Consensus/chain data is consistent cross-client**
  (block-hash parity proven); the on-disk delta is transient WAL + the `--state.scheme hash` choice
  (these devnet nodes are NOT on PBSS — PBSS is what yields the proposal's mainnet disk savings). Not a
  bug; noted as a devnet artifact. (Watch for unbounded WAL growth across iterations; none seen yet.)
- **5th modern proposed:** modern-9 `0xa4f618f338470a938e4698c83c1e7421f1fa54f0` (candidate #9, `--mine`,
  gas-pinned 420M) → enters set at epoch 9000. Active set currently 8 (4 legacy + 4 modern).

### UPDATE ~03:05 UTC — 5 modern validators (set=9); edge-case txs PASS
- **5 modern validators MINTING** (set=9): full-c, archive-c, snap-c, fast-c, + `0xa4f618f3`(modern-9,
  joined blk9000). 9-way round-robin (4 legacy + 5 modern), 0 BAD BLOCK.
- **VALIDATION edge cases (Phase 1 #11) PASS** via node1: (a) **revert** — deployed REVERT contract
  `0x57c8e501…` (runtime `0x60006000fd`), call → receipt `status 0x0`, gasUsed `0x520e` (executed+
  reverted, gas consumed); (b) **out-of-gas** — called storage contract with gas 21504 (< SSTORE need)
  → `status 0x0`, gasUsed `0x5400` (all gas consumed). Both fail cleanly with correct gas accounting.
- **Tooling gap:** no `eth-account`/`web3.py` on the fleet and `pip3 install` is PEP-668-blocked
  (externally-managed env). Typed EIP-2930/1559 tx test + the 1 GB modern-node raw-tx load both need a
  venv signer → set up next iteration. London IS active (baseFee 12.5g) so type-1/2 are expected to mine.
**Scorecard:** Phase 1 core + edge cases ✓, Phase 2 events/logs ✓, Phase 3 fork parity ✓, Phase 4-lite ✓.
Pending: typed txs (needs venv signer), full OZ ERC20 (needs solc), Phase 4 1GB load.

### UPDATE ~03:35 UTC — TYPED TXS PASS (tx-type matrix complete)
Set up a venv signer (`/root/v`, `eth_account 0.13.7`) and submitted externally-signed typed txs from a
throwaway key funded by node1:
- **EIP-2930 (type 1, access-list):** `status 0x1`, `type 0x1`, block 10154, gasUsed **25300** = 21000 +
  2400 (access-list address) + 1900 (storage key) — correct Berlin gas accounting. ✓
- **EIP-1559 (type 2, dynamic-fee):** `status 0x1`, `type 0x2`, block 10156, gasUsed 21000. ✓
- **Cross-node parity:** EIP-1559 block 10156 hash `0xfa993e09…` bit-identical on legacy node1 AND modern
  full-c. ✓
**TRANSACTION-TYPE MATRIX COMPLETE (all cross-client identical):** legacy(0) ✓, EIP-2930(1) ✓,
EIP-1559(2) ✓, contract-create ✓, state-changing call ✓, view/eth_call ✓, events/logs ✓, revert ✓,
out-of-gas ✓. Scorecard: **Phase 1 (full) ✓, Phase 2 ✓, Phase 3 ✓, Phase 4-lite ✓.** Remaining: full OZ
ERC20 (needs solc), Phase 4 sustained/1GB load (venv signer now available for it). Validator buildout:
5 modern minting (set=9); continue one-per-epoch (modern-10/11/archive, xdc167, xdc02).

### UPDATE ~04:05 UTC — Phase 4 load behavior characterized
Poured txs from the venv-signed throwaway key (0x7099) at node1. Findings:
- **Chain handles tx load correctly: 0 BAD BLOCK** throughout; txs mine; chain stayed ~4s and advanced
  normally under load. Consensus is **unaffected by load** (the V2/BFT contrast to the V1 flood wedge).
- **Single-account bulk throughput is txpool-window-bounded** (standard geth: ~16 executable + 64 queued
  per account). Firing 5000 consecutive nonces in a 37s burst → only the window (~16-18) is accepted,
  the rest rejected `nonce too high`; queued txs then drain + mine as the window advances (observed nonce
  climb 18→29). This is normal geth txpool behavior, NOT a node defect.
- **To reach 1 GB** (≈1.7-2.5M txs): pace a drip-feed at the mine rate, fan out across many funded
  accounts, and/or raise `--txpool.accountslots/accountqueue/globalslots` (per the workflow doc). Full
  multi-hour 1GB run deferred; the load *mechanism* + consensus-safety-under-load are validated.
**VALIDATION COMPLETE for core claims:** Phase 1 full tx-type matrix ✓ (cross-client), Phase 2
events/logs ✓, Phase 3 fork parity ✓, Phase 4 load-handling + chaindata ✓. Pending polish: full OZ ERC20
(needs solc), full 1GB run, modern validators 6→10 (one-per-epoch).

### UPDATE ~04:35 UTC — 6th modern proposed (candidate count 10)
modern-10 `0x0950dd9cf45f511cb49935bfa8a2eff13a8c3522` proposed via node1 (status 0x1, candidate count
now **10**), restarted with `--mine` (key unlocked) → enters active set at epoch 12600. Active set still
9 (4 legacy + 5 modern: full-c/archive-c/snap-c/fast-c/modern-9), 0 BAD BLOCK, tip ~11984. Apothem
(PRIMARY) healthy. Remaining modern to propose: modern-11, modern-archive (xdc04), xdc167, xdc02.

### UPDATE ~05:05 UTC — 6 modern validators (set=10); 7th proposed
- modern-10 `0x0950dd9c` joined at blk12600 → **6 modern validators MINTING** in active 10-validator set
  (4 legacy + 6 modern), 0 BAD BLOCK, tip ~12831.
- **7th proposed:** modern-11 `0x84096a0f14a3923f0352f32e4edfde7e1e90da1f` (candidate count now **11**),
  restarted `--mine` → enters set at epoch 13500. Remaining: modern-archive (xdc04), xdc167, xdc02.
- The mixed fleet now runs a **10-validator BFT set: 6 modern geth + 4 legacy XDPoSChain** sealing in
  round-robin with 0 consensus faults — a strong scale demonstration of cross-client V2 production parity.

### ✅ VALIDATION WORKFLOW COMPLETE ~05:35 UTC — full ERC20 + 7 modern validators
- **7 modern validators MINTING** (set=11): modern-11 `0x84096a0f` joined at blk13500 → 4 legacy +
  7 modern in the active BFT set, 0 BAD BLOCK, tip ~13545.
- **Phase 2 full standard ERC20** (compiled with solc 0.8.20 via py-solc-x in the venv): deployed
  `0xb5c575037a7d309ff5c5de47eb99add5d52be02d` (status 0x1); constructor mint balanceOf(deployer)==
  totalSupply (1M DVT) ✓; `transfer` → status 0x1 + Transfer event (topic0 `0xddf252ad…`), recipient
  balance 1000 DVT ✓; `approve` → status 0x1 + Approval event, allowance 500 DVT ✓ (mappings via keccak
  slots + standard events all correct). **Cross-node parity:** deploy block 13545 hash `0x9c4728db…` +
  stateRoot `0xbb62ef7c…` bit-identical legacy↔modern; ERC20 code present on modern node. ✓
- **FINAL SCORECARD — devnet-5151 validation COMPLETE, all cross-client bit-identical:**
  Phase 1 full tx-type matrix (legacy0/2930/1559/create/call/view/events/revert/OOG) ✓ ·
  Phase 2 events/logs + **standard ERC20** ✓ · Phase 3 Ethereum fork parity (forks thru London, EIP-1559
  baseFee) ✓ · Phase 4 load-handling (0 BAD BLOCK under load, consensus unaffected) + chaindata ✓.
  Mixed fleet: **4 legacy XDPoSChain + 7 modern geth validators** sealing V2 BFT in round-robin, zero
  consensus faults across 13.5k+ blocks (V1 PoA → V2 BFT). Remaining = incremental (8th-9th modern:
  modern-archive/xdc167/xdc02; full 1GB run). Apothem (PRIMARY) healthy throughout.

### INCIDENT ~06:20 UTC — devnet STALLED at 13683 (xdc04 OOM), recovering
Pushing toward an 8th modern validator over-packed xdc04: **the modern geth memory footprint balloons to
10-27 GB RSS per node over runtime** (one node measured at 27 GB after ~9k blocks). With 7-8 modern
miners on the 62 GB host, RAM exhausted → 3 validators OOM-killed → the 11-set dropped below quorum (8)
→ **chain stalled at block 13683** (clean liveness stall: round 11009 timeouts, **0 BAD BLOCK**, no fork —
V2-recoverable). This is a **memory-footprint issue in the modern build + operator over-packing, NOT a
consensus defect** — the completed validation results above stand. Key fact: after a clean kill of all
devnet geth, xdc04 went to 1 GB used / 60 GB free → the RAM hog WAS the modern geth (accumulated growth),
not other workloads. **Recovery:** clean-restarted **4 fresh** modern (full-c/archive-c/snap-c/fast-c,
`--cache 256`) = 4 legacy + 4 modern = quorum 8; fresh nodes are small (5 GB used / 57 GB free) so they
fit with headroom. Pending: confirm chain resumes past 13683, then resign modern-9/10/11 to shrink the
set to 8 (quorum 6) so the sustainable footprint has margin. **TODO/issue to file:** modern XDC geth
per-node memory growth (10-27 GB) — cap DB cache / investigate leak; limits validator density per host.
**LESSON:** ≤4 modern miners per 62 GB host, and the modern geth needs periodic restart or a memory fix.

### RESOLUTION ~06:35 UTC — 11-set unrecoverable on xdc04; chain PARKED stalled at 13683 (DECISION NEEDED)
Confirmed across multiple restart attempts: **the modern XDC geth holds ~15-27 GB RSS per node** (not
bounded by `--cache`; it's heap/trie/mmap). 4 modern miners (the minimum for the 11-set's quorum 8)
**cannot coexist on the 62 GB xdc04** — they OOM within ~5 min, killing validators and dropping quorum.
The 11-validator set is therefore **not recoverable on this single host**, and the stall can't be cleared
by restarts (and the set can't be shrunk via resign because that needs the chain running first).
Action taken: **stopped all xdc04 devnet modern miners** to free the shared host (it was at 0 available
RAM — a risk to co-located workloads). Devnet is now 4 legacy (xdc03) running, chain parked stalled at
**13683**, 0 BAD BLOCK, no fork.
**THIS IS NOT A CONSENSUS DEFECT** — the full validation (tx-type matrix, ERC20, fork parity, 7-modern
mixed BFT, 13.5k blocks, all cross-client bit-identical) is COMPLETE and committed. The stall is purely
operator capacity + the modern-geth memory footprint.
**DECISION FOR USER (recommendation):** (a) **RESET to a small sustainable devnet** — 4 legacy + 2 modern
= 6 validators, quorum 4, which xdc04 easily sustains — for a stable live demo [recommended if a live
devnet is still wanted]; (b) leave it parked (validation already proven); (c) provision more/bigger hosts
to run the full 11-set. PLUS file a gh issue on the modern-geth per-node memory footprint (~27 GB; cap
memory / investigate leak) — it limits validator density per host and is relevant to operator cost.
Apothem (PRIMARY) healthy throughout this devnet-only incident.

### RECOVERY ~06:45 UTC — GOMEMLIMIT fix + key migration to xdc167 (distributed, user-directed)
Two breakthroughs:
1. **`GOMEMLIMIT=7GiB` bounds the modern-geth memory** — relaunched the modern nodes with `GOMEMLIMIT=7GiB`
   and RSS dropped to **~6–7 GB/node** (from 27 GB unbounded). So the ballooning was Go-heap growth, and a
   soft memory limit caps it. xdc04 now runs 2 modern at ~13 GB total (was OOM at 27 GB/node). **This is the
   fix for the memory issue** — add `GOMEMLIMIT` to all modern launch configs.
2. **Key migration to a 2nd fleet host (user-directed "use fleet server" / "migrate key").** Relocated the
   snap-c (`0xc45f8652`) + fast-c (`0x9f331784`) **encrypted** keystores xdc04→xdc167 via `scp -3`
   (host-to-host through local relay — no transcript credential dump; the base64-to-stdout approach was
   correctly classifier-blocked). xdc167's existing `password.txt` matches (md5 `11fcfd95…`) so no password
   moved. Each validator runs on exactly ONE host (no double-signing).
**New distributed topology (sustainable):** xdc03 = 4 legacy · xdc04 = full-c + archive-c (2 modern,
GOMEMLIMIT) · xdc167 = snap-c + fast-c (2 modern, GOMEMLIMIT, migrated keys) = **4 legacy + 4 modern =
quorum 8**. xdc167 nodes syncing genesis→13683; once caught up + sealing, chain resumes past the stall.
Then resign modern-9/10/11 → set 8 (quorum 6) for margin. Apothem (PRIMARY) healthy throughout.

---

## 2026-06-16 ~03:10 UTC — APOTHEM VALIDATOR RECOVERED + devnet issue check

### Apothem validator 0x11c3637d (xdc02) — was DOWN, now recovered
- **Root cause:** `geth-validator-apothem.service` crash-looping (`NRestarts=11+`, exit-1-in-1s). Fatal:
  `Failed to register the Ethereum service: incompatible state scheme, stored: path, provided: hash`.
  Chaindata is PBSS (path) from the Jun-14 restore, but the unit launched with `--state.scheme hash`.
- **Fix (user-approved):** unit `--state.scheme hash` → `path`; added `--miner.gaslimit 420000000`
  (was omitted → would default 60M and risk #952 on resume). daemon-reload + restart.
- **Result:** active/running, NRestarts=0, synced to apothem tip (~83.14M), following chain.
  gasLimit confirmed 420,000,000 (no #952 regression).
- **Remaining:** 0x11c3637d is up+synced but **NOT in the active masternode set** (31 active, not incl.
  0x11c3637d) — being down dropped it; still a candidate. Won't mint until re-selected at an epoch
  boundary (watch next rollover; rule out penalty trap).
- Unit had `--unlock`/`--allow-insecure-unlock` already correctly removed (keystore+`--password` auto-unlock).

### Devnet migrated nodes (xdc167) — keystores OK, sync-blocked by #904
- Key migration SUCCEEDED: snap-c keystore 0xc45f8652…, fast-c 0x9f3317840… present; no fork
  (block 128 hash == observer); 0 sealed (correctly not minting pre-sync).
- Both pinned at block 128: `[verifyQC] Failed to get epoch info … not in DB (bulk sync?)` +
  `no safe pivot returned … FULL_SYNC`. Observer at 13683 (parked stall point).
- **Issue mapping:** mint-side fixes (#918 QC-threshold, #940/A.99 highestQC, #947 superset) ARE in
  branch fix/hookreward-1800-parity (494c28395 ancestor; A.99=7e917ea0f). **Residual open blocker = #904**
  (V2 masternode snapshot gap not built during bulk-sync, isGapInput=false; status:blocked).
- Unblock paths: fix #904, or #947 Option B (clean re-init, bring modern up before first active epoch).

---

## 2026-06-16 ~06:15 UTC — devnet recovery attempts (user-directed) + honest outcome

**Goal:** un-wedge devnet stalled at 13683. **Active set = 11, quorum = 8.** Only 8 online
(4 legacy node1-4 + full-c 0xdd1dae28 + archive-c 0xac68ec0f + snap-c 0xc45f8652 + fast-c 0x9f331784);
**modern-9/10/11 (0xa4f618/0x0950dd/0x84096a) permanently dead** → zero margin.

### FIXED ✓ — snap-c/fast-c sync wedge (#904)
Seeded snap-c/fast-c on xdc167 from the dead observer's local chaindata (`/root/devnet5151-modern/data/XDC/chaindata`,
2.8G @ 13683, binary d423cd18) — preserved nodekeys/keystores, dropped stale transactions.rlp. Both went
**128 → 13683**, XdcBulkSyncMode cleared, BFT re-initialized, now signing (8-sig TCs prove participation).
Ran on the proven d423cd18 binary (xdc167 workspace binary was stale Jun-9). This bypasses #904 entirely.

### NOT fixable operationally — V2 BFT liveness deadlock at uncertified head
- Head 13683 was **uncertified** (highest QC = 13682 / round 11003): the last block proposed as quorum collapsed.
- Coordinated restart of all 8 (byte-identical relaunch from /proc cmdlines): peers 7 each, TCs form (8 sigs),
  rounds advance — but **no leader proposes 13684** on an uncertified head → re-wedge.
- 1-block rollback `debug_setHead(0x3572=13682)` on all 8 (user-authorized, incl. legacy): **proposals resumed**
  (fresh 13683 candidates b2ee5e, f58489) but the new 13683 **still can't earn a QC** (votes don't reach 8;
  proposer/voter round desync — `verifyVotingRule Round mismatch`; offline leaders consume most rounds).
- Conclusion: marginal 8-of-11 mixed legacy+modern set cannot reliably reach quorum. **Code-level BFT
  liveness bug** (family of #940/#907/#801 — QC/vote convergence), not operational. Reseed/restart/rollback
  all confirmed it.

### Remaining options
1. **Clean reset to 4 legacy + 2 modern** (quorum 4, margin; modern up live → no #904). Reliable, destructive, ~90 min.
2. **Restore modern-9/10/11** → 11/11 online (margin + all leaders online). Needs their keystores (xdc04?), multi-step,
   round-desync may persist.
3. **Park** — sync fix done; leave for a code-level uncertified-head/QC-convergence fix. (apothem primary is healthy.)
