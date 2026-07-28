# Reset Baseline → upstream v1.17.3 — REMAINING WORK

Audit point: 2026-05-23, after PR #757 (forkID merge fix) landed.
Branch: `reset/integration-1.17.3`. Build: 1.17.3-stable + XDC overlay.

This is the punch-list to close out issue #740 and ship `erigon-xdc-v1.0.0`-
class quality on the `reset/integration-1.17.3` branch (the go-ethereum-based
counterpart). Items are listed in dependency order; each P0 must clear before
the next P1 work begins.

---

## P0 — blockers for Phase 3 acceptance (Apothem syncs to head)

### 1. PR #757 (forkID merge) — LANDED + DEPLOYED
- [x] Merged 2026-05-23
- [x] Rebuilt + deployed on xdc02
- [x] `admin.nodeInfo.protocols.eth.config` confirmed: all fork blocks +
      xdpos.V2 + 15 trustedSyncCheckpoints entries present
- [x] Patch logs fire at startup

### 1b. PR #758 (StatusPacket62 — XDPOS2 wire handshake) — LANDED + DEPLOYED
- [x] Merged 2026-05-23
- [x] Rebuilt + deployed on xdc02
- [x] Handshake no longer fails with `input string too short for
      common.Hash, decoding into (eth.StatusPacket).Genesis`
- [x] First successful handshake against production fleet peer
      (connection held 6.4s before downstream message rejected — see #1c)

### 1c. NEW BLOCKER — XDPOS2 data-plane message wire-shape port

After handshake succeeds, the first data-plane message exposes the
deeper gap. Production geth-XDC sends `GetBlockHeaders` (and likely
all other request/response pairs) as the bare eth/63-style payload —
no RequestId wrapping. Reset binary's upstream eth/69 packets all
have `(RequestId, *Request)` wrappers. Observed error:

```
err="invalid message: (code 3) (size 10) rlp: expected input list for
eth.GetBlockHeadersRequest, decoding into
(eth.GetBlockHeadersPacket).GetBlockHeadersRequest"
```

Port required (mirrors xdc-network's pattern):

- In every `handleGetXxx` handler: dispatch on `peer.Version()` —
  XDPOS2 path decodes the bare `GetXxxRequest` (no RequestId)
- Add `Peer.ReplyXxxRLPLegacy()` variants that send response without
  RequestId wrapping
- Affects: GetBlockHeaders / BlockHeaders / GetBlockBodies /
  BlockBodies / GetReceipts / Receipts / GetPooledTransactions /
  PooledTransactions

Until this lands, reset binaries cannot exchange blocks with
production. This is the load-bearing remainder of #749's paired bundle.

- [ ] Open `fix/xdpos2-legacy-msg-shapes` branch with the 8-message-pair port
- [ ] Verify snap node downloads first 10k blocks from production

### 2. PR #749 — paired wire/peerset bundle (still DRAFT)
**Status:** DRAFT, not merged. 9 files: `eth/handler.go`, `eth/peer.go`,
`eth/peerset.go`, `eth/downloader/*` (5 files), `eth/sync.go`.
Without these, XDPoS V2 BFT message routing is incomplete — vote/timeout/
syncinfo messages can be received but not processed correctly under
some peer-disconnect / re-handshake paths.

- [ ] Re-escalate to Opus planner for design review
- [ ] Land the 9-file bundle
- [ ] Re-run local cluster — verify peer churn no longer drops votes

### 3. blockchain.go R3 — P1 bulk-sync prune-point fallback
**Status:** rawdb prereq landed in PR #750. Actual wiring deferred —
needs reorg against v1.17.3's `switch policy.Mode` structure.

Path: `core/blockchain.go` ProcessBlock around the bulk-sync gate. The
xdc-network version short-circuits the prune-point lookup when in
bulk-sync mode; the reset version still walks the full chain.

- [ ] Re-read Opus advice in `docs/port/PORT_LOG.md` `[PLANNER-ADVICE]`
      entry from #748 review
- [ ] Wire `checkpointSyncNoState.Load()` branch into bulk-sync gate
- [ ] Bench: `local-sync-test` from genesis to checkpoint should reach
      tip in O(hours) not O(days)

### 4. blockchain.go R9 — P1 write fast-path
**Status:** deferred. 104 lines, load-bearing 4-conjunct safety property.

Path: `core/blockchain.go` writeBlockAndSetHead around the canonical-
write decision. xdc-network's version has an XDC P1-specific early
exit when the safety preconditions hold; the reset version takes the
full geth path which is correct but slow.

- [ ] Escalate the 4-conjunct invariant to Opus before porting
- [ ] Port with extensive comments tying each conjunct to a regression
      that motivated the original P1 code
- [ ] Soak under `local-sync-test start all` — no parity hits

### 5. state_processor.go R4 — SetParentState hook + Process() dispatch
**Status:** ApplySigningTransaction landed in PR #751. The DISPATCH
into it from Process() is missing.

Path: `core/state_processor.go` Process() loop. When
`types.IsSpecialTransaction(tx)` returns true, the dispatch must
route to ApplySigningTransaction with the parent state injected, not
to ApplyTransaction.

- [ ] Add SetParentState hook on StateDB or thread parent root through
      Process() signature
- [ ] Wire `IsSpecialTransaction → ApplySigningTransaction` switch in
      the Process() block-tx loop
- [ ] Verify TIPSigning block range parity against xdc-network archive

---

## P1 — Phase 4 7-day soak prerequisites

### 6. Validate existing-chaindata compatibility
**Status:** deferred. Direct test requires either stopping a prod node
or copying 129G of chaindata. Practical alternative: once #757 lands
and forkID matches, the live wire-protocol block transfer IS the
compat test — every block from prod that decodes under reset binary
proves byte-level compat for that block.

- [ ] After #757 lands, watch reset snap node accept ~10k blocks from
      prod fleet without "invalid block" / "merkle root mismatch"
- [ ] If failures: spawn db-inspect on a paused prod node copy

### 7. Phase 4 7-day soak on xdc02
- [ ] All 8 reset nodes (4 hash + 4 path) up
- [ ] Head advances within 10% of production fleet rate
- [ ] No `BAD BLOCK` / `invalid merkle root` / `XDC: bypass` lines in any log
- [ ] Peer count stable (≥ 4 each, including production fleet members)
- [ ] Memory + CPU usage stable

### 8. RPC parity harness — Phase 4
- [ ] Catalogue every XDC RPC method from `xdc-network/internal/ethapi/*`
- [ ] Run each against synced reset node vs prod snap node, compare
- [ ] Special focus: `xdc_getMasternodes`, `xdc_getEpoch`,
      `xdc_getRewards`, `eth_getBlockByNumber` (XDC-specific fields),
      `debug_*` (XDC tracing additions)

---

## P2 — Phase 5 cleanup & release

### 9. ADR for the genesis-merge approach (PR #757 motivation)
- [ ] Write `docs/adr/NNN-genesis-config-merge.md` capturing:
  - Why the embedded JSONs stay minimal (canonical source-of-truth)
  - Why the merge happens in SetupGenesisBlock not in genesis_xdc.go
  - Why we don't update the embedded JSONs to be richer (would
    diverge from xdc-network's apothem.json)

### 10. ADR for dropping fast/fastpath modes from local-sync-test
- [ ] Write `docs/adr/NNN-syncmode-aliasing.md` capturing:
  - v1.17.3 dropped fast-sync upstream
  - Aliasing fast→snap creates ambiguous ethstats labels
  - One-syncmode-per-mode is the convention going forward

### 11. Mainnet sync (M6)
**DO NOT ATTEMPT UNTIL APOTHEM 7-DAY SOAK IS GREEN.**

- [ ] Same as Apothem soak but for mainnet (chainId 50)
- [ ] Verify all 14 mainnet trustedSyncCheckpoints fire correctly
- [ ] Verify TIPV2 switch (block 80M+ on mainnet) is handled correctly

### 12. Operator docs
- [ ] `docs/HOW_TO_RUN.md` — fresh-host bring-up
- [ ] `docs/MIGRATION_FROM_XDC_GETH.md` — for operators moving from
      `xdc-network` (v1.17.0-unstable) to the reset binary (v1.17.3-based)

### 13. Tag release
- [ ] Once #740 closes, tag `xdc-v1.17.3-reset-1` (or similar)
- [ ] Publish binary artefacts via GitHub release
- [ ] Update `docs/UPSTREAM_BASELINE.md` cycle history

---

## Already complete (do not reopen)

- ✓ B1-B10 bundle-merge of 677 XDC overlay commits
- ✓ All 11 deferred files (B5-B10): peer.go, handler_eth.go,
  blockchain_reader.go, state_transition.go, state_processor.go (partial),
  blockchain.go (partial), and the 5-file paired bundle
- ✓ CLI flags wired (`--apothem`, `--xdcmainnet`) — PR #752
- ✓ Real bootnodes pulled from xdc-network — PR #754
- ✓ `local-sync-test.sh` pulled verbatim — PR #754, then trimmed PR
  on this branch
- ✓ Phase 3 deployment on xdc02 — 5/5 nodes on ethstats (now 4/5 after
  fast/fastpath dropped)
- ✓ Audit: no `XDC:*bypass*` log lines in reset codebase
- ✓ Audit: no merge conflict markers in reset codebase
- ✓ ForkID divergence root-caused — PR #757 in flight

---

## Open PRs

| PR | Title | Status |
|---|---|---|
| #757 | core/genesis: patch missing XDC fork blocks at SetupGenesisBlock | OPEN — needs review + merge |
| #749 | Paired wire/peerset bundle (9 files) | DRAFT — needs Opus design |
