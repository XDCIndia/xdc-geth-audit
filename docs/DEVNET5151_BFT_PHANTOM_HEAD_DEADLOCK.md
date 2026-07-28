# Devnet-5151 V2 BFT deadlock — phantom maxPeerHead keeps validators in FULL_SYNC → no QC

> Root-caused 2026-06-16 (Opus, live on the wedged devnet). Liveness bug, NOT a
> block-content/parity bug. Ready-to-file issue body + fix design.

## Symptom
Devnet-5151 stuck at block **13683** for days. Even with **all 11 active validators
online and fully meshed** (4 legacy v2.7.0 + 7 modern v1.17.3, peers=10 each, same
round ~11025), leaders mint fresh 13683 candidates every cycle but **no QC ever forms**
→ chain never reaches 13684. Reseed / restart / 1-block-rollback / full restore-to-11
all confirmed it is NOT operational.

## Root cause (definitive, evidence below)
1. The 4 **legacy v2.7.0 nodes** retain orphaned blocks (~13684–21784) from a PRIOR run
   that reached ~block 22023 (the chain was later rolled back to 13683). They advertise
   `BlockRange.LatestBlock ≈ 21784`.
2. Every modern validator's `xdcSyncer` therefore sees **`maxPeerHead=21784 > localHead=13683`**
   → believes it is behind → stays `s.syncing=true`, `phase=FULL_SYNC`, and repeatedly
   issues `GetBlockHeaders origin_num=13684` which return **`count=0`** (the phantom-ahead
   blocks do not exist on the canonical chain).
3. The #199 halted-tip clear flips `XdcBulkSyncMode=false` (miner ungates — modern-10 was
   observed to **mint** block 13683 `39a617`), but the node stays in the syncing/catching-up
   posture, so it never settles into steady live at-tip BFT and the cross-client vote→QC
   aggregation never converges. No QC → no commit → permanent stall.

### Evidence
- `modern-10` log: `XDPoS2: minted block number=13683 hash=39a617 coinbase=0x0950dD9c`
  immediately followed by `xdcSyncer heartbeat head=13683 peers=10 syncing=true phase=FULL_SYNC`
  and `GetBlockHeaders origin_num=13684 … BlockHeaders received count=0` (phantom-ahead).
- `modern-9/10/11`: `clearing XdcBulkSyncMode (#199)` fired, yet heartbeat still `syncing=true`.
- All 10 peers are the known validators (no external rogue); legacy heads all canonical 13683
  (`eth_getBlockByNumber 21784` → null) but they advertise maxPeerHead 21784.

## Fix design (liveness-only — does NOT change block content/hashes)
In `eth/sync_xdc.go`, extend the FULL_SYNC at-tip detection: when the node is at a tip whose
peer-advertised "ahead" blocks are **persistently unreachable** — i.e. head-number stalled +
peers>0 + the next block (head+1) returns `count=0` for N consecutive heartbeats — treat the
node as AT-TIP: clear `XdcBulkSyncMode`, mark the handler **synced**, and reset the snap phase
so it re-enters **live** block/vote handling (votes on received proposals; broadcasts its own).
Pairs with the existing #199 head-number-stall clear. Cannot false-trigger: on a healthy chain
the head advances (stall counter resets); during genuine sync the ahead blocks ARE reachable
(count>0).

Alternative/complementary: make the modern node ignore a peer's advertised `BlockRange.LatestBlock`
that it has repeatedly failed to fetch (mark that peer's range as unreachable), so `maxPeerHead`
reflects only reachable chain.

## Validation plan
Rebuild → **canary on ONE modern validator** (confirm it exits phantom-sync + the chain produces
13684 once quorum votes) → fleet-wide → verify tip advances, 0 BAD BLOCK, cross-client parity.

## Immediate workaround (no code change)
Clean reset from genesis wiping ALL chaindata + transactions.rlp fleet-wide (clears the legacy
orphan fork that sources the phantom 21784) with a small set (4 legacy + 2 modern, quorum 4).
The bug only manifests after a quorum-loss/rolled-back-head situation, so a fresh chain runs
fine until then.
