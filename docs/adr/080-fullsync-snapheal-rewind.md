# ADR 080 — Wire snap-heal into the FULL_SYNC at-tip path (fix snapshot-restore rewind)

Status: Accepted (canary pending)
Date: 2026-06-16
Refs: #894 (A.97), #890 (A.95), #165/#169 (#1800 parity), #157, #167

## Context

A snapshot-restored `--syncmode fast --gcmode full --state.scheme hash` mainnet
node (observed on `xdc168-fast-mainnet`, datadir `/mnt/data/workspace/FasSync/mainnet`)
exhibits a permanent **rewind-on-restart**: `eth_blockNumber` climbs to tip
(~103.85M) while running, but every restart logs `Block state missing, rewinding
further` block-by-block and rewinds the head all the way back to the snapshot base
(~103,561,402 — the only block whose state is on disk).

### Root cause (confirmed from live logs + code trace)

1. The published 11.7 GB **fast snapshot has incomplete state** (missing trie
   nodes). Every state access throws `missing trie node … not found`.
2. Because parent state is missing during catch-up, the **A.93/A.94 path**
   (`core/blockchain.go:2281-2285 / 2311-2312 / 2423-2439`) sets
   `checkpointSyncNoState=true` and **persists it** via
   `rawdb.WriteCheckpointSyncNoState`. It is **restored from DB on every boot**
   (`core/blockchain.go:589-591`, log: `restored checkpointSyncNoState from DB`).
3. In that mode block import **skips EVM execution** — the heartbeat shows
   `Imported new chain segment … mgas=0.000 … triedirty=0.00B`. The head pointer
   advances but **zero state trie nodes are produced**, so the shutdown flush logs
   `Persisted trie from memory database nodes=0`.
4. On restart, `rewindHashHead` (`core/blockchain.go:855-923`, `!bc.HasState(head.Root)`
   at ~897) finds no state for the head and walks back to the snapshot base.

A state-heal already exists to fix exactly this — `maybeStartSnapHeal`
(`eth/sync_xdc.go:1142-1329`): it runs `downloader.SnapSyncer.Sync(headRoot)` to
download the complete trie, runs **gate B** `ReexecuteAndValidateRange(128)`
(`core/blockchain_heal_status_xdc.go:38-96`), and only then calls
`SetCheckpointSyncNoState(false)`. **But it is only invoked from
`maybeTransitionToAtTip` (the snap-sync `phaseTipCatch` path).** A node that
reaches tip via **FULL_SYNC** (`xdcSyncer heartbeat … phase=FULL_SYNC`) never
calls it — the FULL_SYNC at-tip flip (`eth/sync_xdc.go:~1991`) deliberately does
NOT clear / heal (the #1800 guard comment at 1994-2001), on the assumption that
"mainnet/apothem nodes that fast-synced clean don't have this flag set." That
assumption is **false for snapshot-restored nodes** whose snapshot state is
incomplete.

## Decision

Invoke `s.maybeStartSnapHeal()` in the **FULL_SYNC at-tip flip** block
(`eth/sync_xdc.go`, right after `core.XdcBulkSyncMode.Store(false)`), **gated to
mainnet (chainID 50) and apothem (chainID 51)**. Devnet is excluded because its
#1800 cross-client state divergence is unresolved (#169) — devnet keeps the
sticky behaviour until then.

The call is safe by construction:
- no-op unless `XDC_SNAP_HEAL=1` AND `checkpointSyncNoState` is set AND snap peers
  are present (`maybeStartSnapHeal` guards, 1144/1155/1169);
- bulk mode is already `false` at this point (gate-B precondition);
- it heals the **head root** (`cur.Root`, 1211), re-targets if the head advances;
- it clears the persisted flag ONLY after `Sync` succeeds **and** gate-B
  re-executes the last 128 blocks read-only with matching state roots. Any
  mismatch leaves the flag sticky (no regression).

It does **not** change consensus rules, block production, RLP, or any hashed
field — it changes *when* the (already-present) state heal is triggered.

## Alternatives considered

- **Force a hashdb flush of catch-up state** — rejected: there is no state to
  flush (`mgas=0/triedirty=0`); execution is skipped, so nothing is computed.
- **Suppress the restart rewind in `checkpointSyncNoState` mode** — rejected:
  leaves the node permanently stateless (eth_call etc. fail); papers over it.
- **Seed from a full-state (snapfull) snapshot** — valid operational workaround
  (no code), kept as fallback, but 231 GB vs 11.7 GB and doesn't fix the class.
- **Clear the flag unconditionally at FULL_SYNC at-tip** — rejected: this is the
  exact regression #165 documents; re-enables validation that walks back through
  #1800 on devnet.

## Rollout / verification (consensus rules are sacred)

1. Build on the canary host (linux x86_64), not the arm64 dev Mac.
2. **Canary: `xdc168-fast-mainnet`** — rebuild, restart with `XDC_SNAP_HEAL=1`.
   Expect: `A.97.2: snap-heal started` → `Sync complete — running gate B` →
   `snap-heal completed` → `SetCheckpointSyncNoState(false)`. Then **stop +
   restart** and confirm `eth_blockNumber` stays at tip (no `Block state missing,
   rewinding further`).
3. Run `tools/parity-harness/xdc_rpc_parity.py` over a recent block range
   (legacy vs healed-modern) — zero divergence required.
4. Do **not** enable on devnet until #1800 (#169) is resolved.
5. Rollback: unset `XDC_SNAP_HEAL`; the call becomes a no-op. Revert the branch
   `fix/a97-fullsync-heal-rewind` if needed.

## A.97.3b — second defect found during canary prep: gate-B was structurally broken

Wiring the heal into FULL_SYNC (above) was necessary but **not sufficient**. The
heal's validation gate, `ReexecuteAndValidateRange(healGateBBlocks)`
(`core/blockchain_heal_status_xdc.go:38-96`), re-executes the most recent N
blocks and for each calls `bc.StateAt(parent.Header())` — it needs the **parent
state of every block from head-N onward**. But:

- those parent states were **never built** — `checkpointSyncNoState` skipped
  execution during catch-up; and
- `snapSyncer.Sync(root)` downloads **exactly one** state root (the healed
  near-tip `lastRoot`).

So `StateAt(head-N)` misses on the very first pre-head block, gate B always
returns an error, `SetCheckpointSyncNoState(false)` is never reached, the flag
stays stuck, and the rewind never clears. The heal was structurally incapable of
ever completing.

**Fix:** replace the impossible 128-block backward re-execution with a
head-state-completeness check — `bc.HasState(lastRoot)`. `snapSyncer.Sync`
returns nil only after downloading **and hash-verifying** the full trie against
the canonical `block.Root`, so a present + complete healed root **is** proof of
correct canonical state. Forward execution from the healed root remains protected
by normal block-import `ValidateState` (a divergent block is rejected at import).
`ReexecuteAndValidateRange` is retained (still valid for nodes that have historical
state) but is no longer the heal gate.

## Open dependency

The true root-cause fix for devnet is #1800 cross-client parity (#169). This ADR
unblocks mainnet/apothem snapshot-restored fast nodes only.
