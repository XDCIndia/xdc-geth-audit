# What's still missing from xdc-network — comprehensive audit

Audit point: 2026-05-23, after PRs #757/758/761/763/764/766/767/768 landed.
Method: `git ls-tree -r xdc-network --name-only | grep .go` ∖ same for
`reset/integration-1.17.3`.

## 4 XDC-specific Go files entirely absent from reset/integration-1.17.3

These are the load-bearing files for XDC consensus + sync behaviour. Without
them, only the wire-protocol skeleton exists — actual block transfer and
masternode interop cannot work.

| File | Lines | What it does |
|---|---|---|
| `eth/downloader/downloader_xdc.go` | **1330** | Custom `Downloader.SynchroniseXDC(peer.ID(), peerHead, peerTd, mode)` entry point — the pre-merge sync trigger xdc-network calls. Does NOT require a pre-verified target header (unlike v1.17.3's `BeaconDevSync`). Handles all XDC-specific sync semantics: checkpoint-aware bulk import, hash/path scheme switching, legacy response routing. |
| `eth/sync_xdc.go` | **649** | The full `xdcSyncer` with stuck-flag watchdog, peer backoff, adaptive stall windows, active head probes. We have a 100-line scaffolding (PR #766) covering forceSync ticker + heartbeat + best-peer + BeaconDevSync — the remaining 549 lines are the production-hardening watchdogs that prevent latched-syncing-flag deadlocks (#588), stale head traps (#725), and per-peer backoff loops. |
| `miner/xdc_agent.go` | **353** | XDC masternode block production. Drives the `xdc_agent` RPC, header-signing loop, mining task scheduling. M8 acceptance scope. Without this, the binary can run as a validator but can't produce blocks. |
| `eth/handler_xdc.go` | **100** | XDC-specific eth handler additions — extra peer registration hooks, V2 BFT message dispatcher (Vote / Timeout / SyncInfo broadcast helpers). Some of this overlaps with PR #749's paired-bundle scope. |

## 2 useful infrastructure files also missing

| File | Lines | What it does |
|---|---|---|
| `eth/downloader/downloader_checkpoint.go` | 111 | Checkpoint-aware sync — uses `TrustedSyncCheckpoints` from chain config to short-circuit history pre-checkpoint during a fresh sync. Pairs with PR #750's rawdb checkpoint accessors. Necessary for fast-from-genesis sync to actually use the 15 trustedSyncCheckpoints we now embed (per ADR 001). |
| `eth/protocols/snap/tracker.go` | 26 | snap-protocol request tracker — referenced by the snap-sync state machine in xdc-network. Reset's snap-sync may work without it; verify under test. |

## Combined diff stat

```
git diff --stat xdc-network..reset/integration-1.17.3 -- '*.go'
488 files changed, 27,839 insertions, 15,740 deletions
```

The +27,839 / -15,740 net is largely upstream-only changes we WANT to keep on
the reset side (triedb pathdb improvements, new EIPs, snap-sync rewrites). It
is NOT a list of things to port back. The 4 XDC files above are the actual
porting backlog.

## Priority order for landing the missing files

### P0 — sync mechanic (today's bottleneck)

1. **`eth/downloader/downloader_xdc.go`** (1330 lines)
   - Required to actually start syncing blocks.
   - PR #768 currently bridges via `Downloader.BeaconDevSync(realHeader)` but
     observed errors include `invalid header batch anchor` — BeaconDevSync
     expects a forkchoiceUpdated lineage. SynchroniseXDC sidesteps that.
   - Opus design escalation per CLAUDE.md.

2. **Body + Receipts handler XDPOS2 legacy decode** (current symptom blocking
   peering: prod's GetBlockBodies disconnects us at code 5 with
   `input string too long for uint64`). Mirror of PR #763 / #768.
   - ~50 lines net once the pattern is locked in.

### P1 — production hardening (post-sync)

3. **Full `eth/sync_xdc.go` (lift from xdc-network)**
   - Stuck-flag watchdog (#588 incident handling)
   - Per-peer backoff to avoid hammering stale peers
   - Active head probe (#725 stale-head trap)
   - Adaptive stall windows for bulk/tip/snap sync modes

4. **`eth/handler_xdc.go`**
   - V2 BFT broadcast helpers (Vote / Timeout / SyncInfo outbound)
   - Peer registration hooks for XDC-specific state tracking

5. **`eth/downloader/downloader_checkpoint.go`**
   - Use the 15 trustedSyncCheckpoints we embed in chain config (ADR 001)
   - Enables snap-sync to skip history pre-checkpoint

### P2 — masternode (M8, optional)

6. **`miner/xdc_agent.go`**
   - Only needed if reset binaries run as XDC validators producing blocks
   - Block hash output gate per CLAUDE.md — needs Opus design + escalation
   - Defer until after M5 (Apothem sync) is green

## How this relates to the PRs landed today

| Today's PR | Touches | Maps to which missing-file scope |
|---|---|---|
| #757 — forkID merge | `core/genesis.go` | (covered — chain config merge) |
| #758 — StatusPacket62 | `eth/protocols/eth/handshake.go` | Part of `eth/handler_xdc.go` scope (wire-level) |
| #761 — TIPSigning dispatch | `core/state_processor.go` | (covered — consensus parity) |
| #763 — handleGetBlockHeaders legacy | `eth/protocols/eth/handlers.go` | Part of #1c (data-plane port) |
| #764 — lastRange from XDPOS2 | `eth/protocols/eth/handshake.go` | Part of `eth/handler_xdc.go` scope |
| #766/767 — xdcSyncer scaffolding | `eth/sync_xdc.go` | 100 of the 649 lines |
| #768 — XDPOS2 bidirectional headers | `eth/protocols/eth/peer.go`, `handlers.go`, `sync_xdc.go` | Part of #1c + scaffolding extension |

## TL;DR

The remaining work to actually sync blocks is fundamentally:
- **~1400 lines** of `downloader_xdc.go` + handler / sync_xdc gap (P0 sync mechanic)
- **~100 lines** of body + receipts legacy decode (immediate peering blocker)
- **~550 lines** of sync_xdc.go production hardening (P1)

Everything else is either masternode scope (M8, deferrable) or non-XDC code.
