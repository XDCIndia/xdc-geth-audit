# Defect 1c — V1 M2 double-validation: execution state + devnet-5151 reset plan

**Issue:** [#951](https://github.com/XDCIndia/go-ethereum/issues/951) · **Date:** 2026-06-14 ·
**Branch:** `fix/hookreward-1800-parity`

## Done (committed + pushed)
- **Stage A** (`6fdf4c33c`) — verify side: `getM1M2`/`getM1M2FromCheckpointHeader`/
  `decodeMasternodesFromHeaderExtra` (byte-exact legacy port, unit-tested incl. moveM2),
  M2-map `GetValidator` (was `(i+1)%N`), real `RecoverValidator` (was a stub),
  **non-enforcing `[DIAG-1c]` diagnostic** in verifySeal.
- **Stage B1** (`96cc6848c`) — produce side part 1: V1 `Seal` sets `header.Validator = sighash`
  when `GetValidator(signer)==signer` (self is own M2). Mirrors engine_v1/engine.go:933-939.

## Remaining
- **Stage B2 — M2 co-sign hook** (required for a functioning 4-node V1 net; without it, ~1/4 of
  slots — those whose assigned M2 is xdc04 — never get `header.Validator` and stall):
  - Legacy: `eth/fetcher` `appendM2HeaderHook` → `eth/backend.go:283-318` — on receiving a block,
    if `GetValidator(RecoverSigner(header)) == myEtherbase` and `header.Validator` empty, sign
    `sigHash(header)` → set `header.Validator` → re-broadcast + import the co-signed block.
  - Modern has NO block fetcher (xdcSyncer replaced it) — integrate the hook into the NewBlock
    receive path: `handleNewBlockXDC` → `backend.Handle` (eth/handler.go) just before InsertChain.
    Handler has `accountManager` + the XDPoS engine; resolve the unlocked etherbase wallet
    (see `StartMining` in eth/backend.go:688 for the wallet-find pattern), `SignHash(sigHash)`,
    set Validator, then `BroadcastBlock` the co-signed block.
  - **Open signature-compat question:** modern Seal signs via `signFn(acct, accounts.MimetypeClique,
    sigHash.Bytes())`; the M2 co-sign must produce a signature legacy's `ecrecover(sigHash)` accepts.
    Confirm MimetypeClique doesn't prefix/rehash vs legacy `wallet.SignHash(acct, sigHash.Bytes())`.
- **Enforcing flip** — once `[DIAG-1c]` shows zero mismatches on a live V1 chain, change verifySeal's
  diagnostic to hard rejects: empty `header.Validator` past epoch → `errNoValidatorSignature`;
  `RecoverValidator != GetValidator(creator)` → `errFailedDoubleValidation`. Thread/derive
  `fullVerify` (use `!core.XdcBulkSyncMode.Load()`), mirror legacy `IsTestnet→fullVerify=false`.

## devnet-5151 reset procedure (reverse-engineered; NOT yet executed)
**Topology:** 4 V1 genesis masternodes = node1 `0x10babcec`, node2-slot `0xe20fdc70`,
node3 `0x68f4f2e4`, node4 `0x8263fe68`. The `0xe20fdc70` slot is played by **modern xdc04**
(node2-legacy stays DOWN — the #179 duplicate-coinbase). V2 switch at block 2700, epoch/gap 900/450.

**Genesis:** legacy `genesis.json` ↔ modern `genesis-modern.json` (londonBlock=0,
`baseFeePerGas=0x2e90edd00`) — genesis-hash-equal (task #184). Both `geth/XDC init` from these.

**Hosts/dirs:**
- xdc03 (65.21.27.213): legacy `XDC` = `/root/workspace/XDPoSChain/build/bin/XDC`; producers
  `/root/devnet-v1v2/node{1,3,4}` (RPC 9561/9563/9564, P2P 31311/31313/31314); launch `launch.sh`
  (kills node*.pid, relaunches all — EDIT to skip node2). Modern observers
  `/root/devnet-v1v2/modern-{full,fast,snap,archive}` via `launch-modern-nodes.sh`.
- xdc04 (65.21.71.4): modern MINTER `/root/devnet-v1v2/modern-fast` (--mine --miner.etherbase
  0xe20fdc70 --syncmode full); start = `/tmp/deploy-v1sealer-xdc04.sh` (git pull+make+swap+restart).

**Reset steps (coordinated; SSH port 12141 root):**
1. Build + scp the new modern binary to xdc04 (deploy script does git pull+make) and to xdc03's
   modern node dir if observers are kept.
2. STOP all: xdc03 `node{1,2,3,4}` (kill node*.pid) + `systemctl stop geth-v1v2-modern-*`;
   xdc04 kill the modern-fast geth.
3. WIPE chaindata only (KEEP keystore + nodekey): remove `<datadir>/XDC/chaindata` +
   `<datadir>/XDC/lightchaindata` (legacy) and `<datadir>/geth/chaindata` (modern). Verify paths first.
4. RE-INIT: `XDC --datadir node{1,3,4} init genesis.json`; `geth --datadir modern-fast init
   genesis-modern.json` (+ observers). Confirm identical genesis hash across one legacy + one modern.
5. START: legacy `node1/3/4` (NOT node2); modern xdc04 minter (0xe20fdc70). Wire peers
   (launch.sh all-to-all for legacy; addPeer legacy enodes into xdc04 — see bootnodes in deploy script).
6. WATCH (arm a monitor): V1 replay 0→2700. Validate:
   - `[DIAG-1c]` logs `M2 double-validation OK` for blocks >900 (modern's M2 read matches chain).
   - xdc04 mints V1 blocks that LEGACY accepts (its self==M2 blocks have valid header.Validator) —
     check `XDPoS_getMasternodesByNumber` and that 0xe20fdc70 appears as a block `miner`/creator and
     stays in the set (not penalized out).
   - chain progresses cleanly across 2700 into V2 with xdc04 IN the V2 set (the cascade goal).
7. If diagnostic shows mismatches → fix `getM1M2`/`GetValidator`, redeploy xdc04 (no full reset).
   Once clean → land B2 (co-sign) + flip enforcing, redeploy.

## Hard gate
This is hash-changing. Before flipping enforcing, `[DIAG-1c]` MUST show zero mismatches on a live
V1 chain, and the RPC parity harness (`tools/parity-harness/xdc_rpc_parity.py`) MUST stay 100% on
the V1 range. A wrong M2 map would reject the whole chain past block 900 and wedge the node.

## Reset attempt 2026-06-14 — devnet revived, but a genesis-timestamp obstacle blocks at-tip V1 validation
- Found devnet-5151 DOWN (legacy node1-4 dead, xdc04 rolled back to 15). Reset legacy node1/3/4 to
  fresh genesis (hash fa0d6e…d1813d confirmed == modern genesis-modern.json). First attempt raced on
  stale .pid files (real procs survived, head came back 13552); clean kill-by-binary-path + wipe
  (chaindata+lightchaindata+triecache+nodes) + `XDC init` ("Successfully wrote genesis") fixed it.
  Nodes converged at head ~4033 and are producing — **devnet is healthy again** (3 legacy producers).
- **OBSTACLE (new):** the fresh chain's block timestamps start ~7h behind wall-clock, so all nodes
  mint AS FAST AS POSSIBLE (~4.5s/block observed, effectively unthrottled) until block.Time catches
  up to now (~head 9680). V1 (0–2700) therefore elapses during this catch-up rush in seconds — a
  node cannot be "at tip during V1", so the verifySeal [DIAG-1c] diagnostic (at-tip/fullVerify only)
  never fires for V1, and xdc04 cannot mint V1 blocks at a real cadence. Same root reason xdc04 was
  penalized out of the V2 set before (it bulk-syncs all of V1).
- **FIX for next attempt:** set the genesis `timestamp` to ≈now before the reset so block production
  runs at the wall-clock period (V1 = 2700 blocks × period ≈ 90 min at 2s), and start xdc04 (modern,
  Stage A+B1 binary, 0xe20fdc70 slot) FROM GENESIS alongside legacy node1/3/4 so it stays at-tip
  through V1. Then [DIAG-1c] fires on V1 blocks 901+ and xdc04's V1 minting is exercised live.
  Alternatively, validate the M2 READ computation offline: fetch fresh-chain V1 blocks (which carry
  legacy-correct header.Validator) via RPC and check modern getM1M2/GetValidator vs
  RecoverValidator(header.Validator) — needs a small Go/RPC tool (sigHash + ecrecover).
