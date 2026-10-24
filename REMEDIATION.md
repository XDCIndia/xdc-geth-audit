# Hacken audit remediation: XDC Network (XinFin L1)

Report: Hacken final report, 2026-10-13. Audited commit: `45e2b6ee`.

## Third hand-over (24 October 2026): revalidate `08d60e39096625b798a901e91a2470ed994c6be8`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Branch | `audit/main-uplift-11710-xdc20-r10` |
| Commit to revalidate | **`08d60e39096625b798a901e91a2470ed994c6be8`** |
| Last fix commit | `71a581f0079cd7cae87397a9a9859a00920a83f7` (the 79th fix; the head commit only removes test files) |
| Previously revalidated commit (base) | `4bf67046816d5d2731c4f2bf78d9f327d7886618` |
| Source | XDCIndia/go-ethereum `d0eda1964`: release v1.17.10-xdc.20 plus the sync fix for peers that cannot serve a range |
| Upstream | go-ethereum v1.17.9 `9dffe6e976cb` (branch `1179`) and v1.17.10 `1df30b18d9ed` (branch `11710`) |
| Out of scope | docs, scripts, CI and audit reports. Pure-upstream code arrives through the two upstream merges with their own history. |

### Structure: one commit per fix

From the base, the branch holds **79 commits, one per source fix**, in source order, then one commit that removes the XDPoS test files added after the original audit:
- Each commit has the fix's original message and author, its code (and changes to existing tests), and a `Source: XDCIndia/go-ethereum <sha>` line.
- Commits that fix a finding name it in the subject (CertiK, F-2026-…, or an internal review ID).
- **Upstream go-ethereum v1.17.9** enters as a real merge, `cf0dbb99f35de1df765bc6b7d8b735e388118e04` (second parent `9dffe6e976cb`, 38 upstream commits), at the point it was merged in the source.
- **Upstream go-ethereum v1.17.10** enters as a real merge, **`0076aa0ddcb4f9693205f7e0209b1db7a967cf89`** (second parent `1df30b18d9ed`, 9 upstream commits). XDC decisions in it:
  - the new transaction field validation is taken as-is (XDC special transactions are ordinary signed transactions);
  - the DUP/SWAP interpreter fold is taken (parity vectors pass);
  - snap.v2 auto-enable is gated off on XDPoS chains unless `--snap.v2` is set;
  - `cmd/geth/default.pgo` is upstream's.

**XDC test files.** 66 XDC test files that would be new in this hand-over are not included, to keep the review diff small. Upstream tests and XDC tests already in `4bf67046816d` are kept and follow the source. The suite compiles (`go vet ./...` passes).

**XDPoS test files removed.** The head commit `08d60e390966` (`consensus/XDPoS: remove test files added after the original audit`) deletes the 43 Go test files under `consensus/XDPoS` (18 in the package root, 24 in `engines/engine_v2`, 1 in `utils`) that were added after the originally audited commit `45e2b6ee`. The 21 test files present at `45e2b6ee` and the `testvectors` fixtures are kept, as is `unsealed_fields_test.go`, which holds a helper a kept test uses. The commit changes no non-test code, so it can be skipped when reviewing fixes.

Excluded files:

- `consensus/XDPoS/api_epoch_range_1756_test.go`
- `consensus/XDPoS/api_network_info_1757_test.go`
- `consensus/XDPoS/api_proposals_1755_test.go`
- `consensus/XDPoS/api_reward_scan_limit_1787_test.go`
- `consensus/XDPoS/engines/engine_v2/backfill_qc_1847_test.go`
- `consensus/XDPoS/engines/engine_v2/backfill_qc_1859_test.go`
- `consensus/XDPoS/engines/engine_v2/calcdiff_signer_1785_test.go`
- `consensus/XDPoS/engines/engine_v2/certik_1719_deferred_switch_recheck_test.go`
- `consensus/XDPoS/engines/engine_v2/certik_1722_validator_order_test.go`
- `consensus/XDPoS/engines/engine_v2/epochswitch_between_begineqend_test.go`
- `consensus/XDPoS/engines/engine_v2/forensics_guards_1854_test.go`
- `consensus/XDPoS/engines/engine_v2/forensics_mutex_1784_test.go`
- `consensus/XDPoS/engines/engine_v2/processforensics_guard_1786_test.go`
- `consensus/XDPoS/engines/engine_v2/selfmined_lock_1837_test.go`
- `consensus/XDPoS/engines/engine_v2/sigbomb_1707_test.go`
- `consensus/XDPoS/engines/engine_v2/snap_gap_seed_1863_test.go`
- `consensus/XDPoS/engines/engine_v2/snapshot_empty_test.go`
- `consensus/XDPoS/engines/engine_v2/verify_currentround_race_test.go`
- `consensus/XDPoS/engines/engine_v2/verifyHeader_sync11_1863_test.go`
- `consensus/XDPoS/engines/engine_v2/verifysyncinfo_race_test.go`
- `consensus/XDPoS/engines/engine_v2/yourturn_init_race_test.go`
- `consensus/XDPoS/pre_tx_state_reward_test.go`
- `consensus/XDPoS/reward_missing_header_test.go`
- `consensus/XDPoS/signer_lock_1783_test.go`
- `consensus/XDPoS/utils/pool_signer_cap_test.go`
- `core/blockchain_fastsync_pathdb_test.go`
- `core/blockchain_stale_canonicals_test.go`
- `core/blockchain_xdc_nilblock_test.go`
- `core/blockchain_xdc_sidechaincap_test.go`
- `core/certik_1721_signing_tx_bound_test.go`
- `core/rawdb/accessors_xdc_schema_test.go`
- `core/txpool/legacypool/legacypool_certik15_test.go`
- `core/txpool/legacypool/legacypool_certik31_test.go`
- `core/txpool/legacypool/legacypool_certik36_test.go`
- `core/vm/interpreter_xdc_tableloop_test.go`
- `core/vm/privacy/bulletproof_1790_test.go`
- `core/vm/privacy/bulletproof_1791_test.go`
- `core/vm/privacy/bulletproof_1824_test.go`
- `core/vm/privacy/bulletproof_1834_test.go`
- `core/vm/privacy/ringct_1771_test.go`
- `core/vm/privacy/ringct_1792_test.go`
- `eth/downloader/fast_closeonerr_sync14_test.go`
- `eth/downloader/fast_pathdb_snap_xdc_test.go`
- `eth/downloader/fast_snap_fallback_xdc_test.go`
- `eth/downloader/fast_stalepivot_sync16_test.go`
- `eth/downloader/faststatesync_nodedatacap_test.go`
- `eth/handler_eth_1799_test.go`
- `eth/handler_eth_1844_test.go`
- `eth/handler_xdc_test.go`
- `eth/hooks/certik_1723_parole_dedup_test.go`
- `eth/hooks/penalty_missing_header_test.go`
- `eth/protocols/eth/receipts_xdpos2_1839_test.go`
- `eth/sync_xdc_1761_test.go`
- `eth/sync_xdc_1762_test.go`
- `eth/sync_xdc_1798_test.go`
- `eth/sync_xdc_1823_test.go`
- `eth/sync_xdc_1848_test.go`
- `eth/sync_xdc_1857_test.go`
- `eth/sync_xdc_1861_test.go`
- `eth/sync_xdc_1869_test.go`
- `eth/sync_xdc_1872_test.go`
- `eth/sync_xdc_progress_test.go`
- `eth/sync_xdc_snap_gap_1863_test.go`
- `internal/ethapi/api_proof_delete_1758_test.go`
- `p2p/discover/table_reseed_test.go`
- `params/certik_placeholder_test.go`

### How to verify

```
git log --first-parent --oneline 4bf67046816d..08d60e390966       # 79 fix commits + 1 XDPoS test-file removal
git log -1 --format=%P cf0dbb99f35d                                 # <previous> 9dffe6e976cb… (v1.17.9)
git log -1 --format=%P 0076aa0ddcb4                                # <previous> 1df30b18d9ed… (v1.17.10)
git checkout 08d60e390966 && go build ./... && go vet ./...
go test -timeout 60m ./consensus/XDPoS/... ./core/... ./eth/...
# known: core/rawdb/eradb TestEreDatabase fails in the source too (upstream era fixture
# holds Ethereum headers without the XDC header fields); being fixed in the source
```

### Finding → commit

#### Hacken findings

| Finding | Commit | Change |
|---|---|---|
| F-2026-19525 | `62cdaf95709d052e713f080a8daf595e96969cad` | consensus/XDPoS/utils: cap vote-pool entries to one per signer per round (CertiK XDC-07) |

#### CertiK findings and internal review items

| ID | Commit | Change |
|---|---|---|
| CertiK P3 | `a2dd99f5c68e286e55a62fc197e455ef560edb07` | consensus/XDPoS: stop qc/tc recovery at the first non-masternode signer (CertiK P3) |
| CertiK XDC-07 | `62cdaf95709d052e713f080a8daf595e96969cad` | consensus/XDPoS/utils: cap vote-pool entries to one per signer per round (CertiK XDC-07) |
| CertiK XDC-30 | `87a97c84d24f989fda4908f6091184263bfab9a7` | consensus/XDPoS: include epoch-switch block in GetEpochSwitchInfoBetween when begin equals end (CertiK XDC-30) |
| CertiK XDC-35 | `c4614d3f0b7b360d01891a04247b0e4fbca4caf6` | consensus/XDPoS: reject empty persisted v2 gap snapshots (CertiK XDC-35) |
| CertiK XDC-13 | `5caa4f0d4efc68c48702bc1144241aec3f02a563` | eth: send bft broadcasts to peers without blocking on slow peers (CertiK XDC-13) |
| CertiK XDC-16 | `abc445c3585aa51dbf1fd3c5236feddd70b156d3` | eth/downloader: cap nodedata responses to the requested item count (CertiK XDC-16) |
| CertiK XDC-33 | `698fdd910caecbead52483afcb72e92fbc4f6c74` | p2p/discover: re-seed from bootnodes when the discovery table empties (CertiK XDC-33) |
| CertiK XDC-31 | `84cf44b40258ac00ffa7f707602a2c8ea8bd36b4` | core/txpool/legacypool: wait for tx events in the eviction test (CertiK XDC-31) |
| CertiK XDC-36 | `53be39b1edce5245f915a44406a4a3ac836dc2b3` | core/txpool/legacypool: skip reset post-processing when pool reset fails (CertiK XDC-36) |
| CertiK XDC-15 | `46cc367576ba6b9c13b543f038dc0fa6415a50c0` | core/txpool/legacypool: decouple pool reset from txFeed subscriber delivery (CertiK XDC-15) |
| CertiK XDC-22 | `61591fb2449cb07e3de7dcfc36ad49604c77bebd` | core: nil-check GetBlock in insertSideChain pruned-ancestor loop (CertiK XDC-22) |
| CertiK XDC-19 | `0761f4800796d67ec44e44dccb2bccf9cd45736f` | core: bound sidechain children per pruned parent (CertiK XDC-19) |
| CertiK XDC-26 | `03650a9ac4ef35326dc98fa6d27d1c401b542413` | core: emit reorg log events after the canonical indexes are written (CertiK XDC-26) |
| review R1-A01 | `88384097cfab2a29740770b064b30da2c324e18e` | consensus/XDPoS: initialise the v2 engine under the write lock in yourturn (review R1-A01) |
| review R1-A02 | `e26a9f0c3121a6b498f50aa8b97d05d14f513166` | consensus/XDPoS: read currentround under the lock in vote and timeout verification (review R1-A02) |
| review R1-A03 | `5e558f2fff1221c9577885dc09c4dde819cdc31f` | consensus/XDPoS: read the highest qc and tc under the lock in syncinfo verification (review R1-A03) |
| review R1-F01 | `6f40506857e55ad1f789d48ee28fcf06cb68d6df` | eth/hooks: return the reward-signers error instead of exiting (review R1-F01) |
| review R1-F02 | `652fc37f7307f0ca72dfce9a299b05ee1d936e20` | eth/hooks: return an error when a parent header is missing in the v2 penalty walk (review R1-F02) |
| review R1-F03 | `626cf89a54450acf74dd21070f0f93abcb0a28ec` | consensus/XDPoS, miner: use pre-transaction state for v1 rewards when producing a block (review R1-F03) |
| review R1-S02 | `9cc49eb825758317ccfa213473296ae1984e126e` | eth: require anchor on every ancestor prefetch batch, reject short responses (review R1-S02) |
| review R1-M01 | `6304ec35e48c49959ac2d658cdcc7a71df480f90` | core/txpool/legacypool: do not lower pending nonce on expired signing-tx replacement (review R1-M01) |
| review R1-M02 | `615127b3db85d20959fae41bff6bda80c9e23447` | core/txpool/legacypool: restore the test file header (review R1-M02) |
| review R1-F04 | `04b465a97a4f260d8e5c810dab4a173b573cce1a` | consensus/XDPoS: remove unused default penalty hook functions (review R1-F04) |
| review R1-R01 | `d5340e2e08a2e7469dad2ad5847f320a7c392d32` | consensus/XDPoS: move propose/discard to authenticated api and bound proposals map (review R1-R01) |
| review R1-R02 | `5d4c25f4cc0a9dec9b11f44cc392376c7b38f0c4` | consensus/XDPoS: reject epoch ranges spanning more than 1000 epochs (review R1-R02) |
| review R1-R03 | `27398e6e64956981459d751e73f537039754ea41` | consensus/XDPoS: report the configured chain id in networkinformation (review R1-R03) |
| review R1-R04 | `e6959b860558fded53c55929779a73031095b098` | internal/ethapi: return an error instead of panicking in proofpairlist delete (review R1-R04) |
| review R1-S01 | `a5d34bc9d408c7febef6037150a7c80caa656978` | eth: stop the rollback window from wrapping to zero (review R1-S01) |
| review R1-S04 | `2e928aa3c2ed48347282450b16eb036d0b987db8` | eth: remove the unused snapkeepalive goroutine fan-out (review R1-S04) |
| review R1-C01 | `433b43bf47644a3d908954a140433e4914ab12d6` | core/vm/privacy: reject nil from deserializecompressed in ring signature (review R1-C01) |
| review R2-P01 | `4aca0fe791b80fee54575183468b091391114da4` | core/vm/privacy: fix uint32 overflow in deserializePointArray (review R2-P01) |
| review R2-P02 | `1e184824c15bae763dcd4a35f366827b45f5455a` | core/vm/privacy: guard EC global with mutex in MRPProve and MRPVerify (review R2-P02) |
| review R2-P03 | `3fdd7fa2d246c28de302171a275ec5f32007a732` | core/vm/privacy: lower HashPoint log calls from Info to Trace (review R2-P03) |
| review R2-W01 | `8b2d44e7ec8f78470930ec8240abdd61637ded65` | consensus/XDPoS: read c.signer under c.lock in prepare and calcdifficulty (review R2-W01) |
| review R2-W02 | `f9dc20e26e7d6a83b9b612e496c6145691ea0d4a` | consensus/XDPoS: guard forensics committed-qc state with a mutex (review R2-W02) |
| review R2-W03 | `380b313620328ed785101260be2fdbdfeeeaf013` | consensus/XDPoS: drop the unlocked signer read in the v2 calcdifficulty (review R2-W03) |
| review R1-R02, review R2-N01 | `e4d7dfcf46c54a6108124b8702267aa2f307d847` | consensus/XDPoS: bound the epoch range in getrewardbyaccount before folder scan (review R2-N01) |
| review R2-N02 | `6cef6a2b06f903357d8d05555abe964123d0da7a` | node: delete unused xdc config file that holds a raw private-key field (review R2-N02) |
| review R2-N03 | `69532baf08c4900a61f9a86f1bd9f17176044603` | core/rawdb: remove unused xdc accessors and fix vote-prefix verkle overlap (review R2-N03) |
| review R2-W04 | `8ebc2c487a466d3710e6fef14c094299eb8403b9` | consensus/XDPoS: stop processforensics after the ancestor-qc lookup fails (review R2-W04) |
| review R2-D03 | `d3ca548f51ef408525b05523ea57b39d3fb00434` | eth: send minted block to peers without blocking the minter's own vote (review R2-D03) |
| review R2-D01 | `c52cf1c5038d90d6b73426fb903c1aa16baa226e` | eth: match header and body responses to the xdcsyncer request (review R2-D01) |
| review R2-D02 | `87a6715b57409c98a442ced96508f0bbff588cad` | eth: cancel delayed future-block handling on handler shutdown (review R2-D02) |
| review R2-D01 | `16075b94d9f1597f0bb79eb2a1f0b43cf2e0197d` | eth: restore peer judgement for mismatched sync responses and accept partial bodies (review R2-D01) |
| CertiK XDC-21 | `928f94ff372c34a33100a4adb363321623d16464` | core: sweep stale canonical hashes and tx-lookup entries on startup (CertiK XDC-21, XDC-27, XDC-28) |
| CertiK XDC-04 | `06682e25774187cfc441cb5642476be972d610c4` | params, core: add SigningTxBoundBlock fork and reject oversized/spam sign-tx blocks (CertiK XDC-04, XDC-09) |
| CertiK XDC-04 | `a07044690417221924a44861692db8693c78206d` | params, core: schedule certik rule blocks as far-future placeholders (CertiK XDC-04/09/11/12) |
| CertiK XDC-10 | `c064ee73c2d9593cef295fc445c8cf82bae67b22` | consensus/XDPoS: recheck switches accepted by the bulk-sync mismatch tolerance (CertiK XDC-10) |
| review R3-C01 | `856ed6d30388d9d8aa2d9feead811a6ea4d52b3d` | params: guard certik rule blocks in checkcompatible (review R3-C01) |
| review R3-R03 | `b5418e61ddca6dbaee1e3c5c456f7ff52430caf5` | cmd/geth: fail start-up when the miner key cannot be unlocked (review R3-R03) |
| review R3-R04 | `d3261741dae900a1844f7113bbab7ec3f70237e5` | internal/ethapi: make GetStakerROIMasternode use its masternode argument (review R3-R04) |
| review R3-R02 | `99a2237e9471b2435a5e140750f7deb9360e5dd2` | node, consensus/XDPoS: remove the unwired health handler and event api (review R3-R02, R3-R05) |
| review R3-P01 | `0b08f0a0c75bbf875b51d181865ee577d62cfc61` | core/vm/privacy: propagate the inner-product decode error in the bulletproof verifier (review R3-P01) |
| review R3-S01 | `eab5fd9c989d2565129a25f553a1ce09cb888af0` | eth: check the returned hash in fetchheaderbyhash (review R3-S01) |
| review R4-V1 | `6069f19c41c5117400bbfce778ab6b3855cc53b6` | consensus/XDPoS: delete the unused engines/engine_v1 package (review R4-V1-01) |
| review R4-P02 | `c47ebb0dc94bab9133f11f5574ddd25c730f73c7` | core/vm/privacy: guard ecpoint.neg against the point at infinity (review R4-P02) |
| review R4-W02 | `20869c5fb567359f738ddadf9b055dd5331f14fa` | consensus/XDPoS: write highestselfminedround under the engine lock in seal (review R4-W02) |
| review R5-F01 | `8eae3c321f52c43e64f9190c58ae92f9fd4c214c` | consensus/XDPoS: bound the forensics ancestor-qc walk and guard a nil qc (review R5-F01) |
| review R6-01 | `acbbd68f71603bfcc1997643eed96b1de6fe92c7` | eth/downloader: return a kept peer to idles after a reverted skeleton request (review R6-01) |
| review SYNC-10 | `0ae2b0ee2bc02c623d068c6b0dcd91d90cf4be51` | eth, eth/protocols/eth: xdpos2 version 101 with request ids for header, body and receipt fetches (review SYNC-10) |
| review SYNC-12 | `d4ab57afb5cbf05ae146b734064c2b744521c201` | eth: hedge slow body requests, rank peers by body latency, pipeline lookahead bodies (review SYNC-12) |
| review SYNC-13 | `71a581f0079cd7cae87397a9a9859a00920a83f7` | eth, eth/downloader: keep sync moving past peers that cannot serve a range (review SYNC-13) |

Areas we advise including, all changed since the previous revalidation commit `4bf67046816d5d2731c4f2bf78d9f327d7886618`:

1. **Upstream go-ethereum v1.17.9 and v1.17.10 merges.**
2. **CertiK findings XDC-07 to XDC-36 and P3**: QC/TC signer recovery, vote-pool caps, BFT broadcast, discovery reseed, txpool and sidechain bounds, reorg log ordering.
3. **Consensus hardening from internal review**: signer locks, vote/timeout round locks, reward and penalty header errors, a pre-transaction state reward, epoch range limits, empty-gap-snapshot rejection, and the V1 engine path removed.
4. **Sync** (snap/fast sync, history backfill, peer handling):
   - **One trust change to review specifically:** under full verification, an epoch switch whose gap block lies below a snap or fast-sync pivot is accepted through the existing header fallback. No state exists locally for that gap. The leader seal, QC and signer-list checks all still run, and the gap snapshot is stored only after every check passes. All other full-verification failures remain fatal.
   - **XDPOS2 protocol version 101**: version 100 plus request ids on the header, body and receipt messages. Legacy peers stay on 100, and consensus messages are unchanged.

### Source changes behind the XDC commit (XDCIndia/go-ethereum main since the previously reviewed source, oldest first)

| Source commit | Change |
|---|---|
| `19efdb071904` | Certik Audit |
| `30d68b5fcdbe` | consensus/XDPoS: stop qc/tc recovery at the first non-masternode signer (CertiK P3) |
| `ffe81e13da89` | consensus/XDPoS/utils: cap vote-pool entries to one per signer per round (CertiK XDC-07) |
| `661a3c95d884` | consensus/XDPoS: include epoch-switch block in GetEpochSwitchInfoBetween when begin equals end (CertiK XDC-30) |
| `f31eb0effbbe` | consensus/XDPoS: reject empty persisted v2 gap snapshots (CertiK XDC-35) |
| `fee62fb21775` | eth: send bft broadcasts to peers without blocking on slow peers (CertiK XDC-13) |
| `a2543b27ce00` | eth/downloader: cap nodedata responses to the requested item count (CertiK XDC-16) |
| `9c2dbb7907bd` | p2p/discover: re-seed from bootnodes when the discovery table empties (CertiK XDC-33) |
| `3f0f46353782` | p2p/discover: run the eip-868 test with xdc ping off |
| `a6720ca64f0a` | core/txpool/legacypool: wait for tx events in the eviction test (CertiK XDC-31) |
| `3c155e573e48` | core/txpool/legacypool: skip reset post-processing when pool reset fails (CertiK XDC-36) |
| `940d3aa73e0b` | core/txpool/legacypool: decouple pool reset from txFeed subscriber delivery (CertiK XDC-15) |
| `f48b2e4860ad` | core: nil-check GetBlock in insertSideChain pruned-ancestor loop (CertiK XDC-22) |
| `f74525d3f3a4` | core: bound sidechain children per pruned parent (CertiK XDC-19) |
| `5f6802d8b812` | core: emit reorg log events after the canonical indexes are written (CertiK XDC-26) |
| `dcf0c22fc533` | consensus/XDPoS: initialise the v2 engine under the write lock in yourturn (review R1-A01) |
| `27eab35189a3` | consensus/XDPoS: read currentround under the lock in vote and timeout verification (review R1-A02) |
| `6c316911876d` | consensus/XDPoS: read the highest qc and tc under the lock in syncinfo verification (review R1-A03) |
| `5b99e93d7fb8` | eth/hooks: return the reward-signers error instead of exiting (review R1-F01) |
| `3b9612b1b329` | eth/hooks: return an error when a parent header is missing in the v2 penalty walk (review R1-F02) |
| `9729cd6e892f` | consensus/XDPoS, miner: use pre-transaction state for v1 rewards when producing a block (review R1-F03) |
| `8e85a272a189` | eth: require anchor on every ancestor prefetch batch, reject short responses (review R1-S02) |
| `59f79508f397` | core/txpool/legacypool: do not lower pending nonce on expired signing-tx replacement (review R1-M01) |
| `4d9602d0f338` | core/txpool/legacypool: restore the test file header (review R1-M02) |
| `f0bf5bc1ff59` | consensus/XDPoS: remove unused default penalty hook functions (review R1-F04) |
| `e96ecec7c20c` | consensus/XDPoS: move propose/discard to authenticated api and bound proposals map (review R1-R01) |
| `0dc3f0819227` | consensus/XDPoS: reject epoch ranges spanning more than 1000 epochs (review R1-R02) |
| `3ee3ba598c81` | consensus/XDPoS: report the configured chain id in networkinformation (review R1-R03) |
| `e0909a30afaf` | internal/ethapi: return an error instead of panicking in proofpairlist delete (review R1-R04) |
| `3c9a0036c66f` | eth: stop the rollback window from wrapping to zero (review R1-S01) |
| `df62fd42d279` | eth: remove the unused snapkeepalive goroutine fan-out (review R1-S04) |
| `d2db20d47a58` | core/vm/privacy: reject nil from deserializecompressed in ring signature (review R1-C01) |
| `a63724e62ec1` | core/vm/privacy: fix uint32 overflow in deserializePointArray (review R2-P01) |
| `8fd7a46ee5d3` | core/vm/privacy: guard EC global with mutex in MRPProve and MRPVerify (review R2-P02) |
| `3af437366928` | core/vm/privacy: lower HashPoint log calls from Info to Trace (review R2-P03) |
| `3b94fe4d7dc2` | consensus/XDPoS: read c.signer under c.lock in prepare and calcdifficulty (review R2-W01) |
| `27aa394e4368` | consensus/XDPoS: guard forensics committed-qc state with a mutex (review R2-W02) |
| `40c2bfb2dae6` | consensus/XDPoS: drop the unlocked signer read in the v2 calcdifficulty (review R2-W03) |
| `6d1af01decd6` | consensus/XDPoS: bound the epoch range in getrewardbyaccount before folder scan (review R2-N01) |
| `12f0646708a7` | node: delete unused xdc config file that holds a raw private-key field (review R2-N02) |
| `7cf82f8c8c8d` | core/rawdb: remove unused xdc accessors and fix vote-prefix verkle overlap (review R2-N03) |
| `be02caba3d02` | consensus/XDPoS: stop processforensics after the ancestor-qc lookup fails (review R2-W04) |
| `2fe759f0dcf7` | eth: send minted block to peers without blocking the minter's own vote (review R2-D03) |
| `ff429ee03f9e` | eth: match header and body responses to the xdcsyncer request (review R2-D01) |
| `42cc47157eda` | eth: cancel delayed future-block handling on handler shutdown (review R2-D02) |
| `53f1b74dff17` | eth: restore peer judgement for mismatched sync responses and accept partial bodies (review R2-D01) |
| `50169c460835` | Merge origin/main into chore/merge-upstream-v1.17.9 |
| `3ffc2cb5b81c` | core: sweep stale canonical hashes and tx-lookup entries on startup (CertiK XDC-21, XDC-27, XDC-28) |
| `1ac91c62cb31` | params, core: add SigningTxBoundBlock fork and reject oversized/spam sign-tx blocks (CertiK XDC-04, XDC-09) |
| `a097ef484e96` | Merge origin/main into fix/1722-validator-order |
| `7624b1f4137d` | Merge origin/main into fix/1723-parole-dedup |
| `65958074a752` | params, core: schedule certik rule blocks as far-future placeholders (CertiK XDC-04/09/11/12) |
| `79f5cc0ad23a` | consensus/XDPoS: recheck switches accepted by the bulk-sync mismatch tolerance (CertiK XDC-10) |
| `180b97317695` | params: guard certik rule blocks in checkcompatible (review R3-C01) |
| `40d66a913837` | cmd/geth: fail start-up when the miner key cannot be unlocked (review R3-R03) |
| `3ef042709ba5` | internal/ethapi: make GetStakerROIMasternode use its masternode argument (review R3-R04) |
| `584db4585f8f` | node, consensus/XDPoS: remove the unwired health handler and event api (review R3-R02, R3-R05) |
| `e6e1742ef395` | core/vm/privacy: propagate the inner-product decode error in the bulletproof verifier (review R3-P01) |
| `a079609a377f` | eth: check the returned hash in fetchheaderbyhash (review R3-S01) |
| `8957f64f75ae` | release: v1.17.9-xdc.19 changelog |
| `5552a68c8647` | consensus/XDPoS: delete the unused engines/engine_v1 package (review R4-V1-01) |
| `3cf0aaf348c4` | core/vm/privacy: guard ecpoint.neg against the point at infinity (review R4-P02) |
| `485b4094056c` | consensus/XDPoS: write highestselfminedround under the engine lock in seal (review R4-W02) |
| `8cefa3030bcc` | eth/protocols/eth: serve xdpos2 receipts in the consensus encoding (sync test SYNC-01) |
| `70d9c4552a03` | eth: bound and race-proof the bulk-sync progress counters (sync test SYNC-03) |
| `094384ea2e16` | eth: make required-block challenges work on xdpos2 and pin the apothem reset (sync test SYNC-04) |
| `e6bf6c2b0ad6` | consensus/XDPoS: find the round-based epoch switch and clean up seeded snap info (sync test SYNC-02) |
| `5fa29106b55e` | consensus/XDPoS, eth: verify backfilled qcs against their own epoch in fast sync (sync test SYNC-05) |
| `62e6546a7509` | ethstats, eth: read the sync info through a backend method, not reflection |
| `8a189c53336f` | consensus/XDPoS: bound the forensics ancestor-qc walk and guard a nil qc (review R5-F01) |
| `545d2094bb20` | eth: allow one xdpos2 header and one body request in flight per peer (sync test SYNC-08) |
| `7286ef6bb1b1` | eth: backfill chain history after xdc snap sync (sync test SYNC-06) |
| `15cd80f4699b` | consensus/XDPoS, eth: skip qc verification for backfilled headers below the fast-sync anchor (sync test SYNC-09) |
| `3e705b6df984` | core, eth: fast sync on path-scheme databases via snap state and header gap snapshots (sync test SYNC-07) |
| `805831b498ed` | eth: read the required-block challenge timeout once per peer |
| `c8e1ce914526` | consensus/XDPoS, eth: give snap and fast sync the gap snapshot for the first epoch switch after the pivot (sync test SYNC-11) |
| `cec5aad61858` | eth/downloader: return a kept peer to idles after a reverted skeleton request (review R6-01) |
| `24d7192e8c00` | eth, eth/protocols/eth: xdpos2 version 101 with request ids for header, body and receipt fetches (review SYNC-10) |


## Final commit

**`4b6ba2d056a7a62740e6c0c6e67521e90fb34f10`**: the head code commit of the remediation branch `hacken/remediation-2026-10`. It is the first hand-over, the follow-up fixes, and the second-report fixes below. Use this commit for per-finding review.

- First hand-over: `main` = `c5e7c37de28ed1b4b1da01918b7fdee03a58f151`.
- On top of `45e2b6ee`: the v1.17.8 audit view (upstream go-ethereum v1.17.8 base), merge `42f2db823550308bf34992bcfdd1bc77cc456340`, then one commit per finding.
- Previous head code commit (before the second-report fixes): `37d91b8d95d7efe72b7dcd7abc8df9d9e409950e`.

## Previous request (second hand-over): revalidate the codebase at `4bf67046816d5d2731c4f2bf78d9f327d7886618`

We ask Hacken to revalidate the codebase at **`4bf67046816d5d2731c4f2bf78d9f327d7886618`**, the head of branch `audit/main-uplift-1178-xdc15`. That commit is the current XDC client source (go-ethereum fork release v1.17.8-xdc.18 plus the fixes below) in the same code-only audit view: `git diff --name-only 4bf67046816d5d2731c4f2bf78d9f327d7886618 <source>` lists only the excluded upstream-history, ops and docs paths.

We advise including two areas in this audit. Both changed after the audited commit `45e2b6ee`:

1. **Upstream go-ethereum v1.17.6, v1.17.7 and v1.17.8.** We merged all three upstream releases. The audited commit was based on v1.17.5. The merges touch core, eth, p2p, trie and rpc, and every XDC-specific path that runs on them.
2. **Reward pipeline.** Several fixes change how epoch rewards are calculated, stored and reported:
   - the TIPUpgradePenalty walk follows the epoch-switch parent hash when the cached link is missing. Before, penalised signers were released early and canonical Apothem blocks were rejected;
   - every V2 gap-snapshot candidate is authorised for signing transactions, and signing counts drive the reward split;
   - reward files are written to the `--store-reward` folder and the reward RPCs read them back (F-2026-19805);
   - signing transactions behind a paid nonce are now included in produced blocks (F-2026-19727), so masternodes keep their signing credit.

## Second report (22 October 2026): residual fixes

The second report rated 20 findings Mitigated. Each residual it described is now either fixed or closed with a reason. Each fix is one commit on its own branch, based on `37d91b8d95d7efe72b7dcd7abc8df9d9e409950e`, with the finding ID in the subject and a `Hacken:` trailer. The same commits are merged into `hacken/remediation-2026-10`. Only the `-r2-` branches below belong to this hand-over.

| Finding | Branch | Commit | What changed |
|---|---|---|---|
| F-2026-19727 | `hacken/f-2026-19727-r2-signing-tx-behind-paid-nonce` | `81990d9e7bcf64a442d1f9b2b1b6a7ccfe824a38` | A zero-price signing transaction behind a paid nonce from the same account is now included in nonce order. Only the leading run of special transactions is committed before the price heap, and the price ordering gives a special transaction below the base fee a zero tip instead of dropping the account. Producer side only. |
| F-2026-19457 | `hacken/f-2026-19457-r2-special-tx-selector` | `6897043e4a090bfeb30f38155adbb32fbb08f5f6` | The masternode fast path in the pool also requires known calldata: a 68-byte `sign` call to 0x89, or `setSecret` / `setOpening` to 0x90. Other calldata takes the normal pool path. |
| F-2026-19460 | `hacken/f-2026-19460-r2-bft-forward-bound` | `896dfac729988e0f7ab3b093b5465efcec0d9780` | BFT forwarding uses 4 workers and a 1024-entry queue. Relayed messages are dropped when the queue is full; the node's own votes and timeouts are never dropped. |
| F-2026-19463 | `hacken/f-2026-19463-r2-tip-leader-turn` | `4abe0d18b1379298baafd9157d40566b44f37de0` | The leader-turn tolerance applies only while bulk sync is on. While `CheckpointSyncNoStateActive` is set, a new tip header from a non-leader is rejected with `ErrNotItsTurn`. |
| F-2026-19638 | `hacken/f-2026-19638-r2-v2-committed-finality` | `3df88cbcb9af357f98f329de8351483927867f33` | A V2 block at or below the finalized header, on the canonical chain, reports finality 100. |
| F-2026-19639 | `hacken/f-2026-19639-r2-v2-epoch-rpcs` | `0d927ef4a95b6b0fcd653a781007839ace927f8b` | Epoch and checkpoint RPCs use the V2 round-based epochs; a V1 range returns an error; `GetV2BlockByHeader` fills `EncodedQC`. |
| F-2026-19462 | `hacken/f-2026-19462-r2-verify-mns-observable` | `8800153a163c13b9318bf5e63e141cf570217da3` | The Apothem and missing-state skips stay (canonical disables full verification on chain 51). Each skip is counted in a metric, and the Apothem skip logs one warning. |
| F-2026-19467 | `hacken/f-2026-19467-r2-signing-tx-cap` | `d6ca8e9a0bc3ee84f3a5d02352bbd95c7d562890` | The miner commits at most 2 × the V2 MaxMasternodes signing transactions per block (216 on mainnet). See the note below on the import-side recommendations. |
| F-2026-19469 | `hacken/f-2026-19469-r2-forensics-decode-error` | `260b099752fc1006b8de8cbecc060679ce22b3f5` | `findAncestorQCs` returns an error on a QC decode error or a nil QC instead of panicking. |
| F-2026-19596, F-2026-19612 | `hacken/f-2026-19596-r2-tc-active-signers` | `b1821fb765654e7334dc5c28d3b8dc15e28a25d5` | A timeout certificate is built only from signers in the active masternode set of the epoch that contains the timeout round, the set `verifyTC` checks. The threshold is checked again after the filter. |
| F-2026-19614 | `hacken/f-2026-19614-r2-v1-light-path-m2` | `9ced28cedde3b6f5984628e1847491cbdf86692a` | The M2 co-signature check runs on the bulk-sync light path too, and an empty Validator is refused on both paths. On the light path only, a creator with no M2 assignment in the checkpoint set is skipped; under full verification it still fails. Backed by a scan of 530,815 mainnet V1 headers (blocks 900–80,370,000): 0 mismatches. The same commit records why F-2026-19602 and F-2026-19621 stay as they are. |
| F-2026-19724 | `hacken/f-2026-19724-r2-at-tip-head-fresh` | `f8bfa2f392ee216f2eccc5c450015c39cef0e0bf` | The at-tip signal is the local head's age (one V2 timeout-sync budget plus a mine period, 30 s floor), not the peer-controlled batch length. Two commits: the fix, then the adapted bulk-mode test. |
| F-2026-19471 | `hacken/f-2026-19471-r2-pivot-hash-before-state` | `087d04e0f9f80f30e2ed0049b025315563351466` | An operator-pinned pivot is checked against the served header hash before state download, and the served header's root is used. |
| F-2026-19459 | `hacken/f-2026-19459-r2-empty-route-reinclusion` | `74cf3965540dfc532dc4195bf9c72c8b03cd0b92` | The miner skips an empty-route transaction (0x91–0x94) that is already in the canonical chain, and the pool drops it once the chain includes it, so the same payload is not mined again on chains where the receiver window stays open. The import rule and the nonce handling are unchanged (see below). |
| F-2026-19805 | `hacken/f-2026-19805-r2-reward-folder` | `c3ba2d4f067a4bf01aa5a8b0f87db53a6ad5bffc` | Reward RPCs and the V1 reward writer read the folder that `--store-reward` sets. |

### Residuals closed without a code change

- **F-2026-19467, import side.** Charging signing transactions to the block gas pool would change header `GasUsed` against canonical XDPoSChain for every historical block. Canonical enforces `MaxBlockSize` only from Osaka, which is unscheduled on mainnet, so an import-side size check would reject blocks canonical accepts. The producer-side cap above bounds blocks this client builds.
- **F-2026-19602.** The `verifySeal` fallback to the checkpoint's own signer list stays for ignore-listed checkpoints, the light path and snapshot recovery. Tightening it would wedge a node that lacks the snapshot. Full verification compares the signer list exactly.
- **F-2026-19621.** Adding Penalties to `encodeSigHeader` would change the seal hash of every historical V1 block. The penalty compare needs parent state, so it is skipped when that state is missing and on the light path.
- **F-2026-19459, import side.** Sender recovery already runs before the empty-transaction route on import and in the miner. The nonce is not advanced in `ApplyEmptyTransaction`, as in canonical XDPoSChain; changing that would alter state roots across mainnet blocks 38,383,838–80,370,899 and Apothem blocks 23,779,191–66,824,999. The producer-side guard above stops this client from including the same payload again.
- **F-2026-19471, QC verification.** The compiled checkpoint does not carry a quorum certificate. The pivot is now checked by hash before state download; the trust root stays the binary, and auto-pin stays opt-in.
- **F-2026-19612, admission.** Single timeouts are still admitted against the gap snapshot, as canonical does. A GapNumber-to-round-epoch check on incoming timeouts could reject honest canonical messages.
- **F-2026-19525.** Fully addressed: QC and TC verification keep one signature per recovered address, and the vote and timeout pool gates count distinct in-set signers (follow-up `512744a9973d00eed2619d53a041830633acf9c1`, listed above). No residual remains in the current code.
- **F-2026-19723.** A state-root mismatch on a complete parent trie is rejected. A mismatch caused by a missing trie node is still imported with the empty root while checkpoint sync without state is active: that mode exists for nodes restored from a snapshot or fast sync before their state is built, and rejecting those blocks would stop such a node permanently. Full verification on a node with complete state rejects every mismatch.
- **F-2026-19815.** Warn-only by design. Canonical XDPoSChain compares the epoch-switch validator list without regard to order, so rejecting a reordered list would split this client from the canonical chain. The warning records any order difference.
- **F-2026-19608, F-2026-19609, F-2026-19615 (Accepted).** No change: the client behaves as canonical XDPoSChain, and a change needs an upstream protocol change first.

## How to verify

- **One commit per finding.** The finding ID is in the commit subject and in a `Hacken:` trailer. Find the fix for one finding with:
  ```sh
  git log --grep 'Hacken: F-2026-19525' 4b6ba2d056a7a62740e6c0c6e67521e90fb34f10
  ```
- **Shared root cause.** Two commits each fix two findings, and list both IDs:
  - F-2026-19596 + F-2026-19612;
  - F-2026-19466 + F-2026-19468.
- **Three commits for F-2026-19525.** One fixes `verifyQC` / `verifyTC`; one dedupes the vote / timeout pools in the threshold callbacks; the follow-up below makes the pool threshold gates count distinct signers.
- **Tests.** Each fix commit adds a test that fails without the fix and passes with it. There are two exceptions:
  - **F-2026-19615:** documentation only. Canonical XDPoSChain's V1 engine has no gas-limit bound either, and adding one would reject historical blocks, so the commit records that in a code comment.
  - **F-2026-19815:** diagnostic only. Canonical compares the epoch-switch validator list without regard to order, so the commit logs a warning when the order differs and does not reject the block.
- **Tests that fail with and without the fixes:** 5 `internal/ethapi` tests (`TestEIP7910Config`, `TestRPC*`) fail on the code before the fixes too.

## Follow-up fixes after the first hand-over

Each follow-up fix is one commit on its own branch, with the finding ID in the subject and in a `Hacken:` trailer. The same commit is merged into `hacken/remediation-2026-10`.

| Finding | Branch | Commit | What changed |
|---|---|---|---|
| F-2026-19614 | `hacken/f-2026-19614-v1-double-validation-mainnet` | `0f1b4363dd0d709d163ab1673c9f71e56547f179` | The V1 double-validation check (empty-Validator and M2-assignment rejection) now runs on mainnet (chain 50) only, where another check already enforces it. The first fix applied it to every chain except Apothem. On private and mixed legacy + geth networks that rejected V1 blocks that canonical XDPoSChain accepts: blocks without an M2 co-signature, when the assigned M2 does not co-sign. Mainnet behaviour does not change. |
| F-2026-19525 | `hacken/f-2026-19525-distinct-signer-gates` | `512744a9973d00eed2619d53a041830633acf9c1` | The vote and timeout pool threshold gates now count distinct, in-set signers instead of raw pool entries. The pool keys entries by message hash, which includes the signature bytes, so before this one masternode key could open the gate with several validly signed entries. On the timeout path that built a single-signer timeout certificate, advanced the local round, and broadcast a certificate that peers reject. The unused `UniqueSignatures` byte-dedupe helper is removed. The test `threshold_distinct_signers_test.go` fails on the first hand-over code and passes with this fix. |
| F-2026-19614 | `hacken/f-2026-19614-v1-double-validation-all-chains` | `3ce495578ef58da43ae56b2ce3fb7f9b211bb576` | Second follow-up. The reference client is now XDPoSChain v2.9.3-testnet, which enforces the V1 double-validation check (empty Validator past epoch 1, or a co-signer other than the assigned M2) on every chain except Apothem. This fork now does the same: mainnet keeps the same behaviour, and Apothem still accepts such blocks. A gossiped V1 block that still waits for its M2 co-signature is no longer imported unsigned, as canonical's fetcher does, so it is not stored as a bad block. `TestV1FullVerifyEnforcesDoubleValidation` fails on the previous hand-over code and passes with this fix. |

## Summary

| Severity | Findings | Fixed with a commit | Closed without a code change |
|---|---|---|---|
| High | 4 | 4 | 0 |
| Medium | 16 | 14 | 2 |
| Low | 16 | 16 | 0 |
| Info | 4 | 4 | 0 |
| **Total** | **40** | **38** | **2** |

## Finding → commit

| Finding | Severity | Title | Commit on `main` (full SHA, subject) or reason |
|---|---|---|---|
| F-2026-19525 | High | One Masternode Key Can Satisfy A Quorum Or Timeout Certificate | `3127524992766aff9bf78ea28edf7c20b25dbc8d`<br>consensus/XDPoS: dedup qc/tc signatures by address and reject high-s (F-2026-19525)<br>`9a1c11225688819019e3f722ee61c7036cab88d9`<br>consensus/XDPoS/engines/engine_v2: dedupe pool entries by signer in threshold callbacks (F-2026-19525)<br>Follow-up: `512744a9973d00eed2619d53a041830633acf9c1`<br>consensus/XDPoS/engines/engine_v2: count distinct signers at the vote and timeout threshold gates (F-2026-19525) |
| F-2026-19601 | High | V2 Proposer Seal Omits Header Fields That Change The Block Hash | `322af2f93960d988c812c685c8c67e9a91904486`<br>consensus/XDPoS, miner: reject post-london header fields xdpos does not seal (F-2026-19601) |
| F-2026-19636 | High | Full Sync Canonizes A Header Batch That Does Not Extend The Local Head | `b7d201b2656b63dc4e3f6fcaf61dde9385844a57`<br>eth: only import full-sync header batches that extend the local head (F-2026-19636) |
| F-2026-19727 | High | A Zero Price Signing Transaction Keeps That Account Out Of The Block | `bdd0fdc0aaa2f8e6175b0e5f3325ecd4bb319729`<br>miner: commit special transactions before the price heap (F-2026-19727) |
| F-2026-19457 | Medium | Destination Only Special Transactions Skip The Tip Check And The Slot Cap | `cd3cd000cdcf9edd74c6e8d35028afd461708a21`<br>core/txpool, eth: apply the slot cap to special txs and require a signer (F-2026-19457) |
| F-2026-19458 | Medium | A Compiled Ethstats Secret Authenticates Default Node Reporting | `59ca7901d9dab453f7adaa846159748edef07c91`<br>cmd/geth, cmd/utils: make ethstats opt-in and drop the compiled secret (F-2026-19458) |
| F-2026-19460 | Medium | Verified BFT Forwarding Can Exhaust Node Through Unbounded Goroutines | `61847faeb6b7719f9af012c71b91ef5179150218`<br>eth/bft: add known-message cache and quit-aware enqueue to Bfter (F-2026-19460) |
| F-2026-19463 | Medium | Snapshot Heal Imports New V2 Headers Without Leader Turn Enforcement | `27fe11ef7de0e149fba7853d6b9962501952cdb4`<br>consensus/XDPoS: do not cache V2 headers whose leader check was skipped (F-2026-19463) |
| F-2026-19466 | Medium | Epoch Switch Full Verification Trusts Header Validators When They Are A Superset Of The Local Set | `5943fad6f59910c544ad7156302e2c7ff3c10534`<br>consensus/XDPoS: require exact epoch-switch validators under full verification (F-2026-19466, F-2026-19468) |
| F-2026-19468 | Medium | Epoch Switch Full Verification Trusts Header Fields When The V2 Snapshot Is Missing | `5943fad6f59910c544ad7156302e2c7ff3c10534`<br>consensus/XDPoS: require exact epoch-switch validators under full verification (F-2026-19466, F-2026-19468) |
| F-2026-19608 | Medium | Header Verification Accepts A Future Round With No Timeout Certificate | No code change. Same behaviour as canonical XDPoSChain (`consensus/XDPoS/engines/engine_v2/verifyHeader.go:89` in v2.9.3-testnet). V2 headers carry no timeout certificate, so a fix needs a protocol change made upstream first. |
| F-2026-19609 | Medium | The Vote Lock Is The Parent Certificate, So A Certified Block Is Not Locked | No code change. Same behaviour as canonical XDPoSChain: `processQC` sets `lockQuorumCert` to the QC carried in the certified block (chained-HotStuff two-chain lock), as v2.9.3-testnet `consensus/XDPoS/engines/engine_v2/engine.go:1035-1036` does. Changing it changes the voting rule for every node. |
| F-2026-19622 | Medium | A Vote That Completes A Certificate Latches The Next Round | `736c67919694047272a131eb83e81752d6b4723a`<br>consensus/XDPoS/engines/engine_v2: fix vote latch to use voted round not currentRound (F-2026-19622) |
| F-2026-19637 | Medium | Apothem Penalty Upgrade Releases A Penalized Signer After One Epoch Instead Of Five | `81050a7e29fafd79e31f70c3b1b6a71bf2d59294`<br>eth/hooks: restore post-upgrade penalty comeback walk (F-2026-19637) |
| F-2026-19638 | Medium | Block Signer And Finality RPCs Read The Seal As Addresses | `1e73ee008d59ccba65e3a5ceec3ad94f24531bc7`<br>internal/ethapi: recover real signer from 65-byte V2 validator seal (F-2026-19638) |
| F-2026-19639 | Medium | Epoch RPCs Use The Block Number Instead Of The V2 Round | `4a38cf842de38b68fe56ca4f9026c6456ec3579a`<br>consensus/XDPoS, internal/ethapi: fix epoch RPCs to use v2 round-based epoch logic (F-2026-19639) |
| F-2026-19643 | Medium | Block Import Does Not Apply The Address Blacklist | `2936ef8ee135bf4d381467fecc48690d20ec1d3e`<br>core: reject blacklisted addresses on block import (F-2026-19643) |
| F-2026-19723 | Medium | Bulk Sync Canonizes A Block Whose State Root Does Not Match Execution | `9e43c84aece17572428a7afbc052971d35e35722`<br>core: reject state root mismatch on complete state in bulk sync (F-2026-19723) |
| F-2026-19726 | Medium | Round Timeout Uses The Round Zero Period | `7194b901f1eb9ef51e08bfdae8cf0d840e701af4`<br>consensus/XDPoS/engines/engine_v2: update per-round timeout config in setNewRound (F-2026-19726) |
| F-2026-19773 | Medium | Chain Reorganisation Can Revert A Committed Block | `424c6285a0741f46b42e85b514dbdb2f34e60a00`<br>core, consensus/XDPoS: refuse reorgs below the XDPoS v2 committed block (F-2026-19773) |
| F-2026-19459 | Low | Empty Transaction Dispatch Imports Unsigned TradingState Payloads Without Nonce | `0a831773d11ddee13973f571189c2db134be0f97`<br>core, miner: route 0x91 and 0x94 txs to ApplyEmptyTransaction (F-2026-19459) |
| F-2026-19462 | Low | V1 Checkpoint Validator Verification Never Rejects Populated Validators Field | `7088fdb476986f9668436d8cda6449c7c8effc4e`<br>eth/hooks, contracts: recompute v1 checkpoint validators from the 0x90 state (F-2026-19462) |
| F-2026-19467 | Low | Block Gas Accounting Does Not Bound BlockSigners Transactions | `fbdd70fd0db78c9a2502b3ef4c83988867462e22`<br>miner: skip malformed and future-block signing transactions (F-2026-19467) |
| F-2026-19469 | Low | Quorum Certificate Forensics Is Never Invoked After processQC | `696d6315a7caaf2ab8e4297f4761a3d7d734ef57`<br>consensus/XDPoS/engines/engine_v2: wire forensics processor for QC and vote monitoring (F-2026-19469) |
| F-2026-19596 | Low | Timeout Certificates Count Penalized Signers Toward The Active Set Quorum | `eb4caa2b12abda7a724c4786abb767ad30092521`<br>consensus/XDPoS: verify tc signers against the tc round epoch masternodes (F-2026-19596, F-2026-19612) |
| F-2026-19602 | Low | V1 Checkpoint Extra Replaces The Signer Set And Authorizes That Sealer | `5fbbf8d385c400ad35f0bce3f86a57995011c45c`<br>consensus/XDPoS, eth/hooks: reject v1 checkpoints with a mismatched signer list (F-2026-19602) |
| F-2026-19612 | Low | Timeout Gap Number Selects The Signer Set Independently Of The Round Epoch | `eb4caa2b12abda7a724c4786abb767ad30092521`<br>consensus/XDPoS: verify tc signers against the tc round epoch masternodes (F-2026-19596, F-2026-19612) |
| F-2026-19614 | Low | V1 Full Seal Logs The M2 Co-Sign And Does Not Reject It | `ab1b7b90c178328df219242885d4e913e445f8ec`<br>consensus/XDPoS: enforce v1 double validation under full verification (F-2026-19614)<br>Follow-up: `0f1b4363dd0d709d163ab1673c9f71e56547f179`<br>consensus/XDPoS: restrict v1 double-validation to mainnet (F-2026-19614)<br>Second follow-up: `3ce495578ef58da43ae56b2ce3fb7f9b211bb576`<br>consensus/XDPoS: enforce v1 double validation on every chain but apothem (F-2026-19614) |
| F-2026-19621 | Low | V1 Checkpoint Penalties Are Outside The Seal And Never Checked | `08d4ec587dbae5b497e87e964b655f3435a9d19a`<br>consensus/XDPoS, eth/hooks: check v1 checkpoint penalties against the computed list (F-2026-19621) |
| F-2026-19640 | Low | A Sealed Sign-Slot Block Submits A Second Signing Transaction | `fbed2764957600cecf9d03bedbcddc0512bd8561`<br>miner: skip sign tx for self-sealed blocks (F-2026-19640) |
| F-2026-19641 | Low | NewBlock Imports A V1 Extension Without Recovering The Seal | `c2541e1ccda34028d4614c51b9a71a171171cae6`<br>consensus/XDPoS: verify the v1 seal on the bulk-sync path (F-2026-19641) |
| F-2026-19642 | Low | Bulk Sync Accepts A V2 Block Dated In The Future | `d7bc331f085ab9a658fd70d0cb007e9f3f68c73b`<br>consensus/XDPoS: reject future-dated v2 headers during bulk sync (F-2026-19642) |
| F-2026-19644 | Low | Apothem Protector Rewards Use Stake From The Payout Block | Fixed by the v1.17.8 uplift: merge `42f2db823550308bf34992bcfdd1bc77cc456340`, file content from commit `60f2a187288a3b491707102256d38015ebc47cb3`. `GetSigningTxCountTiered` reads the tiers from `GetStandbynodes(chain, h)` (the epoch gap snapshot), not from the payout-block stake. Check: `eth/hooks/engine_v2_hooks.go` (`standbyPool := c.GetStandbynodes(chain, h)`). The audited commit `45e2b6ee` does not have this line. |
| F-2026-19724 | Low | A Non-Mainnet Insert Error Clears Bulk Sync Away From The Tip | `5658b3984e65038dccffd097af46a06eb60d223b`<br>eth: keep bulk sync on after an invalid next block while far behind (F-2026-19724) |
| F-2026-19774 | Low | Voted Round, Mined Round And Lock Are Not Kept Across A Restart | `69edf12387724efe3d337c15cd7ec32bb2180312`<br>consensus/XDPoS/engines/engine_v2, core/rawdb: persist BFT local safety state across restart (F-2026-19774) |
| F-2026-19815 | Low | Epoch-Switch Verification Accepts a Reordered Validator List | `7b4c8aa898bd99d50d0625b3c7635e21f7ba751e`<br>consensus/XDPoS/utils, consensus/XDPoS/engines/engine_v2: add validator-order diagnostic for epoch-switch headers (F-2026-19815) |
| F-2026-19471 | Info | FastSync Auto Pin Trusts Compiled Checkpoints Without Quorum Verification | `e68ce155ca23fa63e93b2744fe05050f0b24e40e`<br>eth/ethconfig, eth, cmd: make auto-checkpoint-pivot opt-in (F-2026-19471) |
| F-2026-19615 | Info | V1 Header Verification Does Not Bound The Gas Limit | `71a502fa5127d9fcc1087e8c7ae1c4937fdd6d3a`<br>consensus/XDPoS: document intentional absence of V1 gas-limit bound (F-2026-19615) |
| F-2026-19725 | Info | Protector Ranking Uses A Stable Sort On Equal Stake | Fixed by the v1.17.8 uplift: merge `42f2db823550308bf34992bcfdd1bc77cc456340`, file content from commit `60f2a187288a3b491707102256d38015ebc47cb3`. The tiers now come from `GetStandbynodes`, so `slices.SortStableFunc` is gone from `eth/hooks`. The audited commit `45e2b6ee` has it (`git grep slices.SortStableFunc 45e2b6ee -- eth/hooks`). |
| F-2026-19805 | Info | Reward RPCs Do Not Return The Credited Epoch Reward | `9384370a175bc8d086f0f9156bd03b25a7053e14`<br>consensus/XDPoS, cmd: port reward file storage so reward RPCs return data (F-2026-19805) |
