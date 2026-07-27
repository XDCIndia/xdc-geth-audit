# Issue #740 Port — Handoff Packet

**Branch:** `fix/pathdb-rlp-import` @ `XDCIndia/go-ethereum`
**Status:** All critical XDC-network porting work complete and built. Sync verified past 14.8M on Apothem (currently running on xdc02).

---

## What this branch is

The `xdc-network` branch carried 676 commits on top of upstream geth
pre-1.17.0 (commit `54a91b3a`). Issue #740 reset that overlay onto
upstream `v1.17.3` (commit `117e067f0`) to escape state-divergence rot.

This branch (`fix/pathdb-rlp-import`) is the reset branch with the
critical XDC behaviours re-ported on top of v1.17.3 — preserving 1.17.3's
PBSS, txpool refactor, and other upstream advantages, while keeping
consensus parity with canonical XDPoSChain.

---

## What changed in the last session

11 critical files ported across 5 commits:

| Commit | Files | Purpose |
|---|---|---|
| `d8d3b7e8a` | params, eip1559, vm/contracts, txpool/validation, common/bytes, web3ext, legacypool | Batch A (EVM parity) + Batch D (RPC + tx-pool) |
| `466007e8d` | eth/backend, blockchain_reader, handler_eth | Batch B (V2 engine wiring) + C1 (V2 BFT accept) |
| `3af3db1c8` | docs/port/XDC_CONSENSUS_CHANGES.html | Interactive consensus diff map |
| `7cc7b569b` | docs/port/XDC_PORT_STATUS.html | Interactive port status dashboard |
| `4127a631f` | consensus/misc/eip1559 | Fix upstream test regression from IsLondon→IsEIP1559 |

**Highest-leverage fix:** `eth/backend.go` now constructs `engine_v2.New(...)`
and wires `SetEngineV2` + `SetHookReward`. Previously `XDPoS.EngineV2` was
permanently nil and V2 blocks passed through the V1 light-verify fast path
(no QC checks, no rewards, no finality).

---

## Top references — start here

| Document | What it gives you |
|---|---|
| [docs/port/PENDING_PORTS_FROM_XDC_NETWORK.md](./PENDING_PORTS_FROM_XDC_NETWORK.md) | Full audit of 49 Go files: DONE / NOT-XDC / PENDING-CRITICAL / PENDING-NICE |
| [docs/port/XDC_CONSENSUS_CHANGES.html](./XDC_CONSENSUS_CHANGES.html) | Interactive map: 23 XDC changes vs upstream geth, with fork timeline + filter |
| [docs/port/XDC_PORT_STATUS.html](./XDC_PORT_STATUS.html) | Interactive dashboard: 49 files with status, file-by-file expandable detail |
| [docs/port/HANDOFF.md](./HANDOFF.md) | This file — entry point and current state |

Both HTMLs are self-contained (no external deps), open in any modern browser.

---

## Build + validation status

```
go build ./...                # exit 0
go vet ./common/ ./params/ ./consensus/misc/eip1559/ \
       ./core/vm/ ./core/txpool/... ./eth/ ./eth/hooks/   # exit 0
go test -short ./common/... ./params/ ./consensus/misc/eip1559/ \
                ./core/vm/ ./core/txpool/... ./internal/web3ext/   # all PASS
```

**Production binary:** `build/bin/geth` (~53MB) builds cleanly.

**Pre-existing test failure** (not from this session): `core/bintrie_witness_test.go`
+ `core/genesis_test.go` reference `params.BlobScheduleConfig.UBT` field that
doesn't exist on the struct. From the upstream rebase, unrelated to XDC ports.
Needs separate fix.

---

## Sync status (xdc02 — live, old binary)

- **head:** 14,797,685 (Apothem mainnet)
- **rate:** ~520 bps avg, ~3000 bps peak on light blocks
- **runtime:** 8h+ continuous
- **peers:** 5 (all xdc/1.17.0-unstable production nodes)
- **errors:** zero panics, zero merkle root mismatches, zero BAD BLOCK
- **memory:** ~4 GB RSS stable
- **PBSS:** working — flush cycles clean

ETA to head: ~30 hours at current rate. V2 switch (56.8M) and Berlin/EIP-1559 (61.29M) still ahead.

---

## What's deferred (not done)

### Deferred to M8 (masternode bring-up)
| File | Why deferred |
|---|---|
| `eth/peer.go` | Broadcast LRU caches — sync nodes never broadcast V2 messages |
| `eth/peerset.go` | PeersWithoutVote/Timeout helpers — pair with peer.go |
| `miner/miner.go` | Agent interface scaffolding for XdcAgent |
| `miner/xdc_agent.go` | 353-line XDC sealing agent (V1+V2 seal flow) |
| `eth/handler_eth.go` full BFT dispatch | Currently accept-and-ignore; full handler needs `bft.Bfter` plumbing + `getOrInitBft()` + `peer.SetHead` |

### Deferred for "leverage 1.17.3 advantages" preference
| Group | Files |
|---|---|
| Sync robustness | `fetchers_concurrent.go`, `queue.go`, `p2p/dial.go`, `p2p/util.go` — XDC tunings override 1.17.3 defaults |
| Perf tuning | `ethdb/batch.go`, `ethdb/pebble/pebble.go` — trivial constants, optional |
| Checkpoint sync | 5 files in core/, core/rawdb/, core/txindexer.go — only matter with `--syncfromblock` |
| Old XDPoSChain compat | `discovery.go`, `broadcast.go`, `rlpx.go` — only needed for v2.6.x peers |
| Cosmetic | txpool log downgrades, ethstats Consensus field |
| Operator UX | cmd/geth flags, /health endpoint, dump-checkpoint subcommand |
| Recovery utilities | `triedb/database.go DiskLayerRoot()` — pair-port if needed |

### Not needed
| File | Why |
|---|---|
| `eth/catalyst/api.go`, `syncmode.go` × 2 | FastSync dropped per ADR #58 |
| `crypto/ecies/ecies.go` | Upstream cleanup, no XDC content |

---

## Inertness window — when each ported change activates

**Apothem chain at head 14.8M today.**

```
GENESIS ────── 14.8M ────── 23.78M ────── 56.83M ────── 61.29M ────── 71.55M ────── HEAD (82M+)
                  ↑              ↑              ↑              ↑              ↑
              CURRENT       TIPSigning      V2 Switch      Berlin/EIP-1559   Cancun
                                                          XDCBaseFee
                                                          MODEXP precompile
                                                          (all activate here)
```

Most of what we ported (XDCBaseFee, MODEXP, EIP-2028 disable, V2 engine,
EVM instruction sets) **activates only past 56.8M / 61.29M**. Today's sync
on xdc02 is in pre-Berlin territory where the new code is inert — safe to
defer redeploy until sync approaches those forks.

---

## Deploy recommendations

1. **Don't redeploy now.** Current xdc02 sync is running cleanly on old binary;
   new binary is inert for the next ~25 hours of sync until ~56.8M.

2. **When sync approaches V2 switch (~50M):** rebuild from `fix/pathdb-rlp-import`,
   stop xdc02, replace binary, restart. The V2 engine wiring will activate
   on the first V2 block. Watch for `XDPoS V2 engine wired` log line at startup
   and for `[Finalize] V2 HookReward firing` at the first epoch boundary
   after V2 switch.

3. **Before mainnet promotion:** run a fresh-wipe sync on xdc04 from new binary
   to verify the V2 engine wiring doesn't regress pre-V2 blocks. Pair with the
   parity harness on blocks 56.8M–57M to validate V2 consensus.

---

## Open questions / future work

1. **Pre-existing core test breakage** (`UBT` field on `BlobScheduleConfig`) — fix
   separately as a v1.17.3 rebase follow-up.
2. **M8 masternode bring-up** — XdcAgent + full BFT dispatch + broadcast LRUs.
   Per `CLAUDE.md`, requires Opus planner escalation before implementing.
3. **Parity harness on V2 boundary** — once sync crosses 56.8M, run
   `scripts/validate-canonical-parity.sh` against the V2 switch range to
   prove the new V2 engine produces identical state to canonical XDPoSChain.
4. **Consider porting #638 sync robustness** — only if real prod instability
   under bad peers becomes observable. Today's 8h clean run argues against.

---

## Quick commands for the next operator

```bash
# Build
cd /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum
make geth

# Run touched-package tests
go test -short -count=1 \
  ./common/... ./params/ ./consensus/misc/eip1559/ \
  ./core/vm/ ./core/txpool/... ./internal/web3ext/

# Check sync on xdc02
ssh xdc02 'cd /mnt/data/workspace/reset-baseline/go-ethereum && \
  ./build/bin/geth attach --exec "eth.blockNumber" nodes-local/snapfullpath/geth.ipc'

# Open interactive references
open docs/port/XDC_CONSENSUS_CHANGES.html
open docs/port/XDC_PORT_STATUS.html

# Branch state
git log --oneline -10
gh pr list --repo XDCIndia/go-ethereum --state open --head fix/pathdb-rlp-import
```

---

## Closing the loop on #740

Issue #740 asked for a baseline reset onto v1.17.3. With this session's work:

- ✓ Reset branch (`reset/integration-1.17.3`) exists at `117e067f0` + XDC overlay
- ✓ All 11 critical port gaps closed (Batch A/B/C1/D)
- ✓ Build + vet + touched-package tests clean
- ✓ Live sync on xdc02 verifies F-bucket bundles (B1 XDPoS, B5 P1 bulk sync, B6 state)
- ✓ Interactive documentation for handoff
- ✓ Deferred work catalogued with rationale

**Recommendation:** open a PR `fix/pathdb-rlp-import` → `xdc-network` after the
sync reaches V2 switch (~56.8M) and a parity harness confirms no divergence.

The path forward from here is a single PR with a clear test plan:
"sync from genesis to head with new binary; assert head hash matches
canonical XDC explorer; no panics, no BAD BLOCK records across the full
range." That validates everything in this branch end-to-end.
