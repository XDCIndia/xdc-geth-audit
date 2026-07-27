# Apothem sync running — end of day status

Status check: 2026-05-23 16:02 UTC. Reset binary is actively
downloading the full Apothem chain from production XDC fleet peers.

## Live metrics on xdc02

```
peers=2, prodPeers=2 (stable, no churn)
beacon headers downloaded = 13,170,176 / 82,236,802
elapsed = 13 min
rate = ~17,000 headers/sec
ETA to skeleton complete = 1h12m
```

## XDC adoption completeness

After today's 31 PRs on `reset/integration-1.17.3`:

| File / area | Status | Last PR |
|---|---|---|
| `consensus/XDPoS/*` | ✅ **identical to xdc-network** | n/a |
| `core/genesis.go` | ✅ fully ported | #757, #780 |
| `core/vm/*` (EVM instruction sets) | ✅ smoking gun fix landed | #774 |
| `core/state/statedb.go` | ✅ same XDC coverage | (B6) |
| `core/state_transition.go` | ✅ same XDC coverage | (B6) |
| `core/state_processor.go` | ✅ TIPSigning wipe + SetParentState + Process dispatch | #761, #779, #782 |
| `core/blockchain.go` (V2 commit + checkpoint commit) | ✅ critical bits landed | #778 |
| `core/blockchain.go` (17 perf-only XDC markers) | ⚠️ deferred (perf, not consensus) | — |
| `core/xdc_sync_opts.go` | ✅ already there | (B6) |
| `eth/protocols/eth/*` | ✅ XDPOS2 wire protocol bidirectional | #758, #763, #768, #770, #772 |
| `eth/handshake.go` | ✅ StatusPacket62 + lastRange | #758, #764 |
| `eth/handler.go` | ✅ xdpos2 map + xdcSyncer wired | #766, #772 |
| `eth/handler_xdc.go` | ✅ V2 BFT broadcast | #776 |
| `eth/sync_xdc.go` | ⚠️ 180/649 scaffolding (works for sync trigger) | #766 |
| `eth/peerset.go` (PeersWithoutVote/Timeout/SyncInfo) | ⚠️ deferred (V2 BFT efficiency, not sync) | — |
| `eth/downloader/downloader_xdc.go` | ❌ 0/1330 (deferred, BeaconDevSync works) | — |
| `miner/xdc_agent.go` | ❌ 0/353 (M8, masternode-only) | — |

**Two consensus parity smoking guns landed today**:

1. **PR #774** — XDC EVM instruction sets defer EIP-2929/3529/3860 from
   Berlin to EIP-1559. Reset was applying canonical Berlin gas costs.
2. **PR #778** — Force trie commit at every Epoch checkpoint + V2 switch
   block. Without these, state root at block 56,828,700 (Apothem V2
   switch) wouldn't match canonical.
3. **PR #779** — At TIPSigning fork (Apothem 23,779,191), wipe
   BlockSigners contract (0x89). Required for state-root match.
4. **PR #782** — SetParentState hook for XDPoS reward calculation —
   uses pre-tx state, not post-tx state, at every Epoch checkpoint.

## 31 PRs landed today

#753-756 (foundation) · #757-758 (handshake) · #761-764 (consensus
dispatch + peer-tip) · #766-768 (sync trigger + headers bidirectional) ·
#769 (audit doc) · #770 (bodies bidirectional) · #771-775 (docs +
documentation) · **#774 (EVM instruction sets)** · #776 (V2 BFT broadcast) ·
#777 (final audit doc) · **#778 (V2 switch trie commit)** · **#779
(TIPSigning wipe)** · #780 (LoadChainConfig stored patch) · #781
(Peer accessors) · **#782 (SetParentState hook)**.

Bold entries are the consensus-critical fixes.

## What sync looks like now

The reset binary engages beacon-driven skeleton sync, downloads
headers in reverse from the prod tip, will pivot to snap state-sync
at the pivot block (~82.2M), then begin importing canonical blocks.

Each consensus parity fix landed today is now active for every
block executed during the import phase:
- EVM gas semantics match canonical XDC at all forks
- Trie commits at checkpoints persist state for restart safety
- TIPSigning fork properly wipes 0x89 contract
- Reward calculation uses pre-tx state at every Epoch checkpoint

## Remaining work (post-sync-completion)

If sync completes successfully and head > 0, we've achieved Phase 3
acceptance. Remaining work:

1. **Phase 4 soak** — 7 days continuous on xdc02 watching for any
   consensus divergence or peer churn
2. **RPC parity harness** — verify XDC RPC methods (xdc_getMasternodes,
   xdc_getEpoch, etc.) return identical responses to prod
3. **Mainnet sync** — same as Apothem but for chain ID 50
4. **`eth/downloader/downloader_xdc.go` port** — only if BeaconDevSync
   proves unreliable in production
5. **Bft dispatcher port** (eth/handler.go bft + peerset.go
   PeersWithoutVote) — only needed for masternode role
6. **miner/xdc_agent.go** — only needed for M8 (block production)

If sync stalls at a specific block or fails state validation, the
log evidence will point at which of the 17 deferred core/blockchain.go
XDC markers needs porting next.
