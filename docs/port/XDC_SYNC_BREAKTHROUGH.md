# XDC sync breakthrough — 2026-05-23 EOD status

After 24+ PRs landed today, a single critical fix (PR #774 — XDC EVM
instruction sets) flipped the sync from "never starts" to "skeleton
subchain downloading actual headers from production".

## What's now demonstrably working on xdc02

```
✓ Handshake against production XDC fleet
✓ XDPOS2 wire-protocol bidirectional (headers, bodies, NewBlockHashes)
✓ Peer tip tracking (lastRange.LatestBlock = 82,236,231)
✓ Real-header fetch via outbound legacy GetBlockHeaders
✓ BeaconDevSync invocation with the REAL prod target header
✓ Skeleton subchain created at head=82,236,231
✓ Beacon headers downloading (512 headers fetched in first round)
✓ Peers stable: 2/2 connected, no churn
```

Observable evidence in `nodes-local/snapfullpath.log`:

```
INFO  xdcSyncer.fetchHeader: got real header
      peer=eb7b7119 requested=82,236,231 got=82,236,231 hash=0xf8a8d158
WARN  Beacon syncing with hash as target
      number=82,236,231 hash=f8a8d1..64e4dd
DEBUG Created initial skeleton subchain head=82,236,231 tail=82,236,231
INFO  Syncing beacon headers downloaded=512 left=82,235,718
```

## What was the unlock — PR #774

XDC defers EIP-2929 / EIP-3529 / EIP-3860 from Berlin/London/Shanghai
to the EIP-1559 block. Reset binary was using upstream's Berlin
instruction set (which DOES enable EIP-2929 access lists / cold-warm
storage gas) — so every post-Berlin transaction on Apothem (block
61,290,000+) executed with different gas costs vs canonical.

Found via method-level audit: xdc-network has 21 XDC-named functions
that reset doesn't. The 4 EVM instruction-set functions
(`newXDCBerlinInstructionSet`, `newXDCLondonInstructionSet`,
`newXDCMergeInstructionSet`, `newXDCShanghaiInstructionSet`) were
the load-bearing ones.

This was the answer to "why doesn't sync ever start working" —
production peers wouldn't serve headers because our forkID-derived
sync target couldn't pass their validation (different EVM means
different state means different anchor invariants).

## What's still needed for FULL sync

### Immediate next blockers

1. **Skeleton sync stall** — first 512 headers downloaded, then snap
   protocol EOFs from prod peers. The snap/1 protocol may also need
   XDPOS2 wire-shape adaptations (similar to what we did for eth/100).

2. **Race in `legacyHeaderReqID`** — xdcSyncer's fetchHeader and the
   downloader's RequestHeadersByXxx share the single atomic slot.
   Once BeaconDevSync starts, the downloader's outbound headers
   overwrite the slot, breaking xdcSyncer's next fetchHeader. Need
   per-caller request tracking or a dedicated xdcSyncer path.

3. **"Invalid header batch anchor"** — race between when we fetch
   the target header and when the downloader requests its anchor.
   Production chain advances ~10-20 blocks in the gap. Need to
   re-fetch the latest header when the anchor stales.

### Other XDC additions still missing (broader audit)

From `git grep "^func.*[Xx][Dd][Cc]" xdc-network`, 17 functions still
present in xdc-network but missing from reset (after the 4 EVM ones
landed today):

```
Downloader-related (10):  DeliverBodiesXDC, DeliverHeadersXDC,
                          SynchroniseXDC, synchroniseXDC, syncWithPeerXDC,
                          fetchHeightXDC, fetchHeadersXDC, fetchBodiesXDC,
                          findAncestorXDC, requestHeadersByNumberXDC,
                          requestHeadersByNumberXDCWithTimeout,
                          calculateRequestSpanXDC
Peer accessors (2):       LastXDCHeaderReqID, LastXDCBodyReqID
Miner (2):                NewXdcAgent, drainXdcAgentResults
Utility (1):              hasXdcPrefix
```

The Downloader-related ones live in `eth/downloader/downloader_xdc.go`
(1330 lines, port attempt documented in DOWNLOADER_XDC_PORT_ATTEMPT.md).
With #774 unlocking BeaconDevSync engagement, we may NOT need the full
downloader_xdc.go port — BeaconDevSync is doing the skeleton sync for
us. The xdc-network version exists because their geth predates v1.17.3's
post-merge downloader; our reset gets to ride the new architecture.

### `core/blockchain.go` XDC markers we haven't fully audited

Grep finds ~15 `// XDC` markers in xdc-network's `core/blockchain.go`
covering: checkpoint sync no-state mode, bulk-sync optimizations,
trie commit at V2 switch block, P1 write fast-path, sender-cache skip,
chainHead event throttling. Some of these are perf optimizations
(deferrable); others may be load-bearing for consensus.

## All PRs landed today (25 total)

1.  #753 — local-sync-test bootstrap
2.  #754 — real Apothem bootnodes
3.  #755 — script fast→snap interim
4.  #756 — final fast→snap mapping
5.  #757 — **genesis config merge (forkID fix)**
6.  #758 — **StatusPacket62 (XDPOS2 handshake)**
7.  #759 — REMAINING_WORK.md update
8.  #760 — XDPOS2_DATAPLANE_PORT design note
9.  #761 — **TIPSigning Process dispatch**
10. #762 — ADRs 001 + 002
11. #763 — **handleGetBlockHeaders legacy decode**
12. #764 — **populate lastRange from XDPOS2 status**
13. #765 — sync-trigger finding doc
14. #766 — **xdcSyncer Option A scaffolding**
15. #767 — gate BeaconDevSync on legacy data-plane
16. #768 — **XDPOS2 outbound + inbound headers + real-header fetch**
17. #769 — tracker non-fatal on legacy + missing-files audit
18. #770 — **XDPOS2 GetBlockBodies / BlockBodies legacy**
19. #771 — downloader_xdc.go port-attempt write-up
20. #772 — **NewBlockHashes / NewBlock legacy stubs**
21. #773 — verbose fetchHeader diagnostic logging
22. #774 — **XDC EVM instruction-set markers (SMOKING GUN)**
23-25. Various docs

Bold entries are the load-bearing code changes.

## Conclusion

The reset binary now meaningfully engages snap-sync against the
production XDC fleet. It's not at head yet, but it's no longer stuck
at trigger-loop — it's downloading real headers. The architectural
foundation is sound; remaining work is refinement.
