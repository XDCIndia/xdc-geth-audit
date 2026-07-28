# #952 — Apothem testnet halt RECOVERED (2026-06-14 ~14:46 UTC)

The public Apothem testnet was halted at block **83,088,791** after our modern-geth validator
(`0x11c3637d`) minted there. Recovery required fixing **three** distinct modern-geth bugs; the third
was decisive. All on `fix/hookreward-1800-parity`.

## The three bugs

1. **Gas-limit target (`629b8d02b`)** — `miner.applyTransaction`/header build used the upstream
   `GasCeil` default (60M); XDC pins the limit to the genesis value (420M). Minting dropped the gas
   limit off the 420M plateau → out-of-bound successor → halt. Fix: `XdcWorker.xdcGasCeil()` targets the
   genesis gas limit. (Also runnable as `--miner.gaslimit 420000000`.)

2. **Bulk-sync miner gate (`f2aca43b2`)** — the validator stayed in `XdcBulkSyncMode` (Gate 3),
   miner suppressed, because it looped trying to import the network's invalid 792 and the at-tip clear
   never fired (stale head / legacy peers send no BlockRange). Fix: clear `XdcBulkSyncMode` when the
   advertised head+1 is persistently invalid at tip (non-mainnet gated).

3. **Special-tx mine/import divergence (`978f469f7`) — THE root cause.** `miner.applyTransaction`
   ran every tx through plain `core.ApplyTransaction`, omitting the XDC special-tx dispatch that
   `core/state_processor.go Process` performs. A masternode **signing tx (to `0x89`, post-TIPSigning)**
   executed via the EVM in the mine-path but via `ApplySigningTransaction` (zero-gas, no EVM) on import,
   and normal txs misrouted the TRC21 fee. → the producer's own minted block failed self-import with
   **`invalid merkle root`** → no node could produce a block containing the signing-tx backlog → wedge.
   Fix: the miner now mirrors `Process` exactly:
   - to `0x89` + `IsTIPSigning` → `core.ApplySigningTransaction`
   - to `0x92`/`0x93` + `IsTIPXDCXReceiver` → `core.ApplyEmptyTransaction`
   - otherwise → `core.ApplyTransactionWithEVM(..., env.coinbaseOwner)`
   with `env.coinbaseOwner = state.GetCandidateOwner(env.state, w.coinbase)` resolved ONCE before
   `fillTransactions` (matching Process's once-per-block resolution).

## Recovery
Built `978f469f7` on xdc02, redeployed the validator as a **direct binary (setsid, no systemd
service)** with `--miner.gaslimit 420000000`. It minted block **83,088,792** (coinbase `0x11c3637d`);
the chain advanced 791 → 792 → 793 → … on `erpc.apothem.network` + `earpc.apothem.network`
(network-wide). Zero new "invalid merkle root" after the deploy (last was 14:15:33, pre-deploy). Other
masternodes resumed minting normally.

## Significance
This is the mine-vs-import state-determinism class (#185/#186/#187/#189): a modern XDPoS producer MUST
replicate the import-path's XDC special-tx + reward handling exactly, or it rejects its own blocks.
The validator now mints Apothem blocks bit-for-bit consistent with re-execution.

## 4th fix — gas-limit pin (`2548f06cd`), and full recovery
After 978f469f7 unwedged 791, the prior `xdcGasCeil()` (targeting the genesis BLOCK gas limit =
4,700,000 on apothem; legacy's `XDCGenesisGasLimit` constant is 84M — neither is the live 420M
operator plateau) honed the limit DOWN ~409k/block, dragging the chain down while our node was the sole
producer. Fix: gas target = `max(parentGasLimit, miner.config.GasCeil)` — never drift below the
established plateau, climb toward the configured ceiling (420M) otherwise. The validator climbed
419.6M → 420M; at the flat plateau the legacy masternodes' fixed-420M proposals became in-bound and
they all resumed.

## Confirmed end-state (15:48 UTC)
Head 83,089,312 (erpc=earpc), block time 2.0s, gasLimit 420,000,000, 11 distinct masternodes minting
(our `0x11c3637d` = normal 1/N share). ~521 blocks produced since the wedge, no merkle errors, no gas
drift, no fork. Modern validator mints 420M blocks in lockstep with the legacy nodes.

Refs #952 #187 #189 #193 #182. Commits: 629b8d02b, f2aca43b2, 978f469f7, 2548f06cd.
