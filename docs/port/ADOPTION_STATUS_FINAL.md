# XDC adoption status — final 2026-05-23 audit

Comprehensive picture after 26 PRs landed today. What's been imported
from xdc-network, what's still pending, and a prioritized list.

## Per-file XDC marker coverage (xdc-network vs reset/integration-1.17.3)

| File | xdc-network markers | reset markers | Status |
|---|---:|---:|---|
| `consensus/XDPoS/xdpos.go` | n/a | n/a | **2038 lines, IDENTICAL** |
| `core/genesis.go` | 11 | 5 | ⚠️ 6 XDC bits unported |
| `core/blockchain.go` | 23 | 4 | ⚠️ **19 XDC bits unported** |
| `core/state_processor.go` | 6 | 1 | ⚠️ 5 XDC bits (mostly pre-merge headerless) |
| `core/state_transition.go` | 8 | 6 | ⚠️ 2 XDC bits |
| `eth/protocols/eth/*` | ~ | ~ | ✅ Wire protocol fully ported (#763, #768, #770, #772) |
| `eth/handshake.go` | n/a | n/a | ✅ StatusPacket62 + lastRange ported (#758, #764) |
| `eth/handler.go` | n/a | n/a | ✅ xdpos2 handler map ported |
| `eth/handler_xdc.go` | 100 lines | 100 lines | ✅ Just ported (#776) |
| `eth/sync_xdc.go` | 649 lines | 180 lines | ⚠️ Scaffolding only — full file = #766 + extensions |
| `core/vm/jump_table.go` + `evm.go` | n/a | n/a | ✅ XDC instruction sets ported (#774) |
| `core/xdc_sync_opts.go` | 40 lines | 40 lines | ✅ Already ported (B6) |

## What's been imported today (26 PRs on `reset/integration-1.17.3`)

### Code (load-bearing)
- **#757** — ForkID merge: genesis runtime config now matches canonical
- **#758** — StatusPacket62: XDPOS2 wire handshake
- **#761** — TIPSigning dispatch in Process()
- **#763** — handleGetBlockHeaders legacy decode
- **#764** — populate lastRange from XDPOS2 status
- **#766/767** — xdcSyncer trigger loop scaffolding
- **#768** — XDPOS2 outbound + inbound headers bidirectional
- **#770** — XDPOS2 GetBlockBodies / BlockBodies legacy
- **#772** — NewBlockHashes / NewBlock legacy stubs
- **#774** — **XDC EVM instruction sets (smoking-gun consensus fix)**
- **#776** — eth/handler_xdc.go (V2 BFT broadcast helpers)

### Documentation
- #759/769 — REMAINING_WORK.md
- #760 — XDPOS2_DATAPLANE_PORT design note
- #762 — ADRs 001 (genesis-merge) + 002 (syncmode-aliasing)
- #765 — sync-trigger architectural finding
- #771 — DOWNLOADER_XDC_PORT_ATTEMPT honest write-up
- #775 — XDC_SYNC_BREAKTHROUGH (PR #774 unlocked snap-sync)

### Scripts
- #753–756 — local-sync-test bootstrap + Apothem bootnodes + script cleanup

## What's still NOT imported (priority-ordered)

### P0 — sync mechanic to head

#### 1. `eth/downloader/downloader_xdc.go` (1330 lines)
Custom `SynchroniseXDC(peer.ID(), peerHead, peerTd, mode)` that doesn't
require forkchoiceUpdated lineage. Today's snap-sync engages but stalls
after 512 headers (skeleton peers immediately leave); SynchroniseXDC's
bulk-download approach sidesteps that. See `DOWNLOADER_XDC_PORT_ATTEMPT.md`
for the cascading deps (BlockChain interface methods, Peer.Request*Legacy
methods, body-delivery type shape).

#### 2. `core/blockchain.go` — 19 XDC markers unported

Most critical, in approximate order:

```
2063: // XDC: Also force-commit at V2 switch block — this is a critical consensus
2042: // XDC P1: Force trie commit at checkpoint blocks
2729: // XDC P1: Write block data + advance head in one batch, skip state commit
2329: // XDC v114: If parent is nil and this block is a trusted checkpoint
3498: // XDC: For trusted checkpoint anchor, write to key-value DB
3358: // XDC: When first header matches any TrustedSyncCheckpoint, skip full validation
3702: // XDC-specific: validate seal length for XDPoS chains
 489: // XDC: For XDC chains with path-based state, the state root computed by GP5
1643: // XDC fix: On XDC chains, GP5 stores trie nodes under its
2023: // XDC fix: On XDC chains, GP5 computes a different state root than
2051: // XDC fix (#308): On XDC chains, GP5 stores trie nodes under its own
2463: // XDC: When checkpoint sync without state is active and the parent state
2616: // XDC: Skip state validation when in checkpoint sync without state mode
1268: // XDC fix #35/#36: Don't crash — try to recover from disk layer root
3641: // XDC: Also update currentBlock for FullSync --syncfromblock support
3462: // XDC: Allow gap when using sync-from-block
1990: // XDC opt: during bulk sync, keep more tries in memory
2099: // XDC opt: during bulk sync, skip per-block chain events
2183: // XDC opt: skip sender caching during bulk sync
```

These cluster into:

- **V2 switch trie commit (consensus-critical)** — without this, the
  state root at block 56,828,700 (Apothem V2 switch) won't match
  canonical
- **P1 write fast-path (perf + correctness)** — 4-conjunct safety
  property; load-bearing for `chainCutoffNumber` + checkpoint-sync-no-state
- **Trusted checkpoint handling** — uses the 15 trustedSyncCheckpoints
  we now ship (ADR 001) to skip pre-checkpoint history
- **GP5 path-state-root reconciliation** — multiple sites; XDC and
  upstream compute state roots slightly differently under path-scheme
- **Bulk-sync optimizations** — perf only, can defer

### P1 — pre-merge sync hardening

#### 3. `eth/sync_xdc.go` — full version (vs 180-line scaffolding)

xdc-network's is 649 lines with:
- Stuck-flag watchdog (#588)
- Per-peer backoff to avoid hammering stale peers
- Active head probe (#725 stale-head trap)
- Adaptive stall windows (bulk/tip/snap)
- Race-condition guards on legacyHeaderReqID slot

We currently observe the legacyHeaderReqID race between xdcSyncer and
downloader — the full sync_xdc.go has the per-peer single-flight gate
that prevents it.

### P2 — masternode interop (M8)

#### 4. `miner/xdc_agent.go` (353 lines)
Block production for XDC masternodes. Touches block-hash output —
needs Opus design escalation before porting.

## Validation conclusions

1. **Wire protocol parity** — ACHIEVED. Reset peers cleanly with
   production XDC fleet via XDPOS2/100; legacy headers/bodies/gossip
   messages all decode correctly.

2. **Consensus parity** — LIKELY ACHIEVED post-PR #774. The EVM
   instruction set divergence (EIP-2929/3529/3860 deferred to EIP-1559
   on XDC) was the most likely smoking gun for state-root divergence
   at every post-block-61,290,000 transaction. Verifying requires
   actually syncing past Berlin — blocked on item P0#1 above.

3. **Sync engagement** — DEMONSTRATED. The xdcSyncer trigger fires,
   real headers fetch from prod, BeaconDevSync invokes, skeleton
   subchain creates. The skeleton sync stalls after one batch
   (~512 headers) because v1.17.3's beacon-driven skeleton expects
   forkchoiceUpdated lineage that XDC can't provide.

4. **Architectural gap remaining**: the post-merge skeleton sync /
   pre-merge XDC sync mismatch. `downloader_xdc.go` is xdc-network's
   answer — sidestep skeleton, do direct peer-driven bulk download.
   Porting it is multi-day with cascading deps (documented).

## Recommended Next Sessions

### Session A — port `core/blockchain.go` V2 switch commit
Smallest XDC marker that's CONSENSUS-CRITICAL. ~20 lines around
line 2063 in xdc-network. Without it, block 56,828,700 diverges
even if EVM gas semantics are now correct. **Do this first.**

### Session B — port `core/blockchain.go` trusted checkpoint sync
Uses the 15 trustedSyncCheckpoints we ship to skip pre-checkpoint
history. Would let reset sync from a recent checkpoint instead of
genesis — much faster path to "synced node" status.

### Session C — port `downloader_xdc.go` with Opus design
The big architectural lift. Multi-day with cascading deps in
`core/blockchain.go`, `eth/protocols/eth/peer.go`,
`eth/downloader/types.go`. Should follow A+B because those land
their prerequisites.

### Session D — port full `eth/sync_xdc.go` watchdogs
Adds production hardening to the scaffolding (stuck-flag rescue,
peer backoff, head probe). Mostly mechanical port.

### Session E — port `miner/xdc_agent.go` (M8)
Optional, only needed if reset binaries are intended to mine.
Requires Opus design escalation per CLAUDE.md (touches block-hash output).
