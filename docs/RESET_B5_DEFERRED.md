# Bundle B5 — XDC additions deferred to follow-up bundles (refs #740)

`bundle/B5-bulk-import` lands the bulk of the XDC overlay on top of v1.17.3
with a green build. To get there, the following XDC functionality was
**deferred** (dropped from this bundle, needs proper porting in follow-up):

## XDC files dropped entirely (v1.17.0 API coupling)

| File / area | Reason | Follow-up |
|---|---|---|
| `cmd/XDC/**` | v1.17.0 `utils.OverrideVerkle`, `ImportHistory`, `history.PrunePoints` | Use vanilla `cmd/geth` (Option B precedent) |
| `miner/xdc_agent.go` | v1.17.0 `core.GasPool.AddGas`, `ApplyTransactionWithEVM` signature, `txpool.Pending` signature | Port to v1.17.3 APIs (own bundle: M8 block production) |
| `eth/handler_xdc.go` | `p.Peer.Send` removed, `ETH66` removed | Replace with explicit handlers calling `peer.SendMsg(code, ...)` against v1.17.3 |
| `eth/sync_xdc.go` | `downloader.SynchroniseXDC`, `p.Peer.Head`, `ETH66` removed | Re-port XDC bulk-sync helpers against v1.17.3 downloader |
| `eth/downloader/downloader_xdc.go` | `Downloader.mux` removed, `StartEvent`/`FailedEvent` gone | Port XDC sync state coordination to v1.17.3 event model |
| `eth/downloader/downloader_checkpoint.go` | `BlockChain.SetCheckpointSyncNoState` doesn't exist | Re-add XDC checkpoint sync after blockchain.go porting |
| `trie/bintrie/empty.go`, `empty_test.go` | XDC additions to bintrie use v1.17.0 `NodeResolverFn`/`BinaryNode` shapes | Drop — bintrie isn't consensus-critical for XDC |
| `internal/era/*` XDC mods | XDC restructured `era.Era` as struct (v1.17.3 made it interface again) | Drop Era1 archive support — XDC doesn't use it |

## XDC modifications to upstream files reverted to v1.17.3

| Package | XDC mods lost | Impact |
|---|---|---|
| `core/state/` | TIPSigning special-tx state hooks, masternode state helpers (state_reader.go and statedb_utils.go RESTORED) | **Consensus risk** — F-bucket re-port needed |
| `core/vm/` | XDC precompile wiring, gas table mods | Affects block validation for TIPSigning, BlockSigners |
| `triedb/pathdb/` | XDC path-scheme tweaks | Performance regression on path-scheme sync |
| `trie/` | XDC verkle/bintrie mods | Compatible — XDC didn't activate verkle |
| `ethdb/{batch,memorydb,leveldb,pebble}` | XDC's 2MB `IdealBatchSize` (was 100KB) | Performance regression — 20x more flushes during sync |
| `core/rawdb/{accessors_chain,table}` | XDC's rawdb XDC-specific accessors restored selectively | Some accessors missing — rawdb_xdc.go imports may fail |
| `core/genesis.go`, `core/blockchain.go` | XDC consensus hooks | Consensus-critical follow-up |
| `core/types/log.go` | Reverted to v1.17.3 (XDC removed `EthBurnLog`/`EthTransferLog` helpers) | XDC mainnet ERC-20 burn/transfer events may not log properly |

## What's kept (the XDC essence)

- **`consensus/XDPoS/`** — V1 engine + V2 engine + all utilities intact
- **`contracts/blocksigner`, `contracts/validator`, `contracts/randomize`** — full
- **`params/config.go`** — XDCMainnetChainConfig + XDCApothemChainConfig + XDPoSConfig + V2/V2Config + XDCFeatures + TrustedSyncCheckpoints; PLUS `IsUBT` field + `UBTTime`/`EnableUBTAtGenesis` stubs for v1.17.3 compat
- **`core/types/block.go`** — XDC Header fields (Validators, Validator, Penalties) + XDC HashNoValidator method; PLUS `BlockAccessListHash` + `SlotNumber` fields for v1.17.3 compat
- **`core/types/{consensus_v2,vote_pool}.go`** — XDC consensus types (Vote, Timeout, SyncInfo, etc.)
- **`core/genesis_xdc.go`** — XDC genesis logic
- **`eth/protocols/eth/`** — XDPOS2/100 wire from B3 (Option C minimal)
- **`internal/ethapi/api_xdc.go`** — XDC RPC methods (with `GetRewardByHash` returning nil stub)

## Sequencing for follow-up bundles

Recommended order (per Opus's earlier "F-bucket per-file" advice, refined):

1. **B6 — core/state F-bucket port**: TIPSigning + masternode state helpers. **Consensus-critical.**
2. **B7 — core/blockchain.go F-bucket port**: XDC validation hooks, state-root cache, header verification.
3. **B8 — core/state_processor.go F-bucket port**: TIPSigning bypass logic, reward distribution.
4. **B9 — eth/downloader F-bucket port**: re-port XDC sync optimizations (P1 bulk sync, checkpoint cache).
5. **B10 — eth/handler.go + eth/handler_xdc.go**: re-port XDC peer handlers.
6. **B11 — performance**: ethdb 2MB batch, pathdb tweaks.
7. **B12 — miner**: XDC mining agent for block production (M8 — optional per #683).
8. **B13 — internal/ethapi**: real `GetRewardByHash` + reward computation.

The current bundle/B5 is a **syncing baseline** — the binary builds and runs as `geth-xdc-overlay`, can recognize XDC chain configs, but lacks the F-bucket consensus logic needed for actual XDC consensus validation. **Do not deploy without bundles B6–B8 landed first.**
