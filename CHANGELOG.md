# XDC geth changelog

Changes in the XDC overlay of this fork. Upstream go-ethereum changes are in
the upstream release notes for the matching geth version. The client reports
`Geth/v<geth version>-xdc.<n>`; the `xdc.<n>` counter only goes up.

## v1.17.8-xdc.13 — 2026-10-14

Consensus parity fix for fee crediting. No change to mainnet or Apothem history.

- **No fee credit for special transactions (#1460, closes #1455).** Like
  XDPoSChain v2.9.2 (`core/state_transition.go:505`), fee crediting now skips
  special transactions to `0x89` (BlockSigners) and `0x90` (Randomize). Before
  this fix, on a chain where EIP-1559 is active and the coinbase route is
  taken, a 0-price special transaction made `effectiveTip = 0 - baseFee` wrap
  in uint256, and the coinbase got a wrapped balance. The state root then
  split. Private network 2004 hit this at block 2252 (EIP-1559 at 2250).
- **Owner-less coinbase after TIPTRC21Fee gets no fee (#1460).** After
  TIPTRC21Fee, canonical credits only the candidate owner of the coinbase. If
  there is no owner, it credits nothing. This fork credited the coinbase. Now
  it does the same as canonical.
- On mainnet and Apothem, every coinbase has an owner and the special
  transactions have gas price 0, so these blocks get the same state as before.
  Tests: `core/state_transition_xdc_fee_test.go`.
- Validated on private network 2004: a node on v1.17.8-xdc.12 stopped at 2251
  with `invalid merkle root`. On this build, the same datadir imports to the
  tip, with block hashes equal to legacy XDPoSChain at 2252, 3150 (Cancun) and
  4400.

### Known issues
- #1429: the A.77 sync lookahead fails with `unknown ancestor` on every round.
- #1458: full sync on the path scheme can truncate state and re-import a
  sidechain.
- #1464: V2 `getSnapshot` rounds the gap block down to the V2 switch block.
- #1430 tracks the security-audit findings #1404–#1428.

## v1.17.8-xdc.12 — 2026-10-13

Consensus parity fix, live on mainnet and Apothem.

- **`0x0a` precompile matches XDPoSChain (#1403).** From the Cancun block
  (mainnet 98,802,000, Apothem 71,551,800), XDC used upstream's Cancun
  precompile set, which has the real KZG point evaluation at `0x0a` and warms it
  in the access list. XDPoSChain keeps these networks on its EIP1559 set, where
  `0x0a` is an empty account: a CALL succeeds and the access is cold. A
  transaction that touches `0x0a` therefore got a different gas cost and
  result. XDC chains now use their own sets (`core/vm/contracts_xdc.go`):
  - Cancun: no `0x0a`.
  - Prague and Osaka: an always-failing `0x0a` stub at 50000 gas, the same as
    XDPoSChain v2.9.2-testnet. These forks are not scheduled on mainnet or
    Apothem.
  Non-XDC chains are unchanged. Tests: `core/vm/contracts_xdc_test.go`.

### Known issues
- #1429: the A.77 sync lookahead fails with `unknown ancestor` on every round.
- #1430 tracks the security-audit findings #1404–#1428.
- This build has not had a mainnet sync run past the Cancun block.

## v1.17.8-xdc.11 — 2026-10-13

Rebased onto upstream **go-ethereum v1.17.8**, a security release: DoS
hardening of the `eth` and `snap` packet handlers, a block-body shape check
before hashing, IP endpoint statements accepted only from bonded discovery
nodes, and a STUN response crash fix.

- `core/state_transition.go`: upstream made the `Message` skip flags private
  with builder methods (`SkipNonceCheck`, `SkipEOACheck`,
  `SkipGasLimitCapCheck`); XDC's `BalanceTokenFee` stays on the struct. The fee
  payment block is restructured. On the consensus path it pays the same
  recipients as before: the TRC21 owner gets `gasUsed*gasPrice` after
  TIPTRC21Fee, otherwise the coinbase gets `gasUsed*tip`. For `eth_call` with
  zero fee fields, it follows upstream: the tip is zeroed and the recipient is
  still touched. `TestXDCFeeRecipient` pins this and passes on v1.17.7-xdc.10
  as well.
- `params/config.go`: upstream loads the Ethereum Mainnet, Sepolia and Hoodi
  configs from `params/chainspecs/*.json`; every XDC network config is
  unchanged.
- The body-shape check (at least 10 bytes per transaction) cannot reject any
  valid block, XDC included.

### Known issues
- #1403 (`0x0a` precompile parity, live on mainnet/Apothem) is not in this
  release yet.
- #1429: the A.77 sync lookahead fails with `unknown ancestor` on every round.
- #1430 tracks the security-audit findings #1404–#1428.
- This build has not had a mainnet sync run; the fee-routing change is covered
  by `TestXDCFeeRecipient`.

## v1.17.7-xdc.10 — 2026-10-13

Rebased onto upstream **go-ethereum v1.17.7**.

- XDC signing-tx and empty receipts feed upstream's new digest pipeline
  (`feedReceipt`). TRC21 fee settlement still runs before `PostExecution`,
  which now also takes the block's withdrawals (none on XDC).
- `StateAt` takes `(root, number, time)`. Every XDC caller is updated,
  including two runtime interface checks in `engine_v2` that read masternode
  candidates from state at the gap block. Those still compiled with the old
  signature but would have silently stopped matching.
- `XDPoS.Finalize` uses upstream's shorter signature; XDPoS never used the
  dropped block-access-list arguments.
- Downloader: XDPOS2 peers are exempt from upstream's "skip peers whose
  announced range doesn't cover the block" check (#35723). Their range slot
  holds the total difficulty from the handshake, not a block number.
- Downloader: upstream moved the snap-sync pivot mover into the snap content
  loop (#35749). It now applies XDC's freeze decision (operator pin, A.97.1b
  freeze, commit, frozen snap/2 pivot). XDC's NodeData fast sync keeps its own
  mover in `beaconsync.go`, so each sync mode has exactly one mover.
  `pivotLock` is kept for the XDC readers outside the snap loop.
- Expected test failures: upstream's snap/2 BAL catch-up tests fail because
  XDC's header RLP intentionally stops at `RequestsHash` (block-hash parity).
  XDC blocks carry no block-level access lists.

### Validation
- Mainnet full sync from genesis to 4,299,328 with 0 consensus errors; blocks
  3,000,000 and 3,100,000 (TIPSigning) match the public chain. This build took
  270 min to reach 4,299,328; v1.17.6-xdc.9 took 362 min over the same range.

### Known issues
- #1429: the A.77 sync lookahead starts 64 blocks past the imported head, so
  each lookahead batch fails with `unknown ancestor`. This build has it, and so
  does v1.17.6-xdc.9.
- #1403 (`0x0a` precompile parity) is not in this release yet.
- #1404–#1428: open security-audit findings, under triage.

### XDPoSChain v2.9.2-testnet parity (`4c0295a97`, 2026-10-06)
Audited all 82 commits since v2.9.1-testnet.
- **Found, fix pending in #1403 (not in this release), live on mainnet and
  Apothem:** from the Cancun block
  (98,802,000 / 71,551,800) XDC used upstream's Cancun precompile set, with the
  real KZG point evaluation at `0x0a`, and warmed it. XDPoSChain keeps those
  networks on its EIP1559 set, where `0x0a` is an empty account. #1403 removes
  `0x0a` from XDC Cancun and gives XDC Prague and Osaka XDPoSChain's
  always-failing `0x0a` stub.
- **Already equivalent:** BLS12-381 (`0x0b`–`0x11`) and P256VERIFY (`0x100`)
  code and gas; modexp; opcode tables; the mainnet bootnode list (all 60 enodes
  match #2697); the Apothem round-27,360,000 and round-27,400,500 configs;
  known-block adoption and ancestor rebuild (upstream `insertChain`); the
  finalized-tag guard. BFT messages are already handled during sync, not
  dropped.
- **Open, Prague-only (devnet chain 5551 at block 50,000; not scheduled on
  mainnet or Apothem):** the Multicall3 preinstall at the Prague block, the
  EIP-2935 history contract (our gate is timestamp-based, so it never runs on
  XDC), and the Prague transaction signer (EIP-7702 type) keyed on
  `PragueBlock`.
- **Open, sync liveness (not consensus):** the xdcSyncer rollback trigger
  matches "not its turn" by string, so a local snapshot hole can rewind the
  chain and taint honest peers. XDPoSChain now classifies such errors as local.
  Failed-sync backoff is global, not per peer.
- Not applicable: the TD, `getResultBlock` and `CalculatedBlock` fixes (no
  counterpart in this fork), `--set-head` offsets (`debug_setHead` covers it),
  and the RPC, accounts and abigen fixes.

## v1.17.6-xdc.9 — 2026-10-05

Rebased onto upstream **go-ethereum v1.17.6**, with XDPoSChain **v2.9.1-testnet**
parity for the Apothem restart at block 83,600,000.

### Upstream
- Merge go-ethereum v1.17.6 (#1389). The XDC signing-tx and empty receipts feed
  the new receipt pipeline. `IntermediateRoot`/`Finalise`/`Commit` take
  `params.Rules`. The precompile cache is keyed per precompile set. The parallel
  BAL processor is disabled on XDC chains. The header RLP is unchanged.

### Consensus parity with XDPoSChain v2.9.1-testnet (#1396)
- **Gas2500x** (Apothem from 83,600,000): the base fee is 625 gwei, taken from the
  block's own number (`parent+1`), and headers must match it exactly. `BASEFEE`
  falls back to 12.5 gwei on headers without a base fee.
- **Apothem V2 configs** at rounds 20,277,000 and 27,400,500. The 27,400,500
  config has 108 masternodes, 432 protectors and rewards 63.4196 / 50.7357 /
  25.3678.
- **Standby nodes** (#2612): reward tiers read historical standby membership at
  the gap block (`GetStandbynodes`), not the current state.
- **TRC21 token fees** (#1390, #1391): fee capacity is read from the TRC21 issuer
  contract. Gas is paid from that capacity, with no refund, and settled after the
  block in both the importer and the miner.
- Not ported: DynamicGasLimit (would reopen #952).
- Checked XDPoSChain `demo-v2.9.2-testnet` (`f226bd7ee`, "reimport known blocks
  above head in insertChain"). This fork already does that through upstream's
  `insertChain` (rollback re-import plus `writeKnownBlock`). Nothing to port.

### Fixes
- **V1 minting** (#1400, closes #1383, #1385): the V1 mint path now builds its EVM
  env with the signer as coinbase, matching block import. Before, a geth V1
  masternode rejected its own first block that carried a 0x89 sign-tx
  (`invalid merkle root`), which stalled the chain.
- **Fresh-chain bootstrap** (#1384, closes #1382): a new V1 chain can now produce
  block 1. The sync watchdog also fires at genesis, but only when every peer is at
  genesis too. *(@flexykrn)*
- **Signing-tx rewards** (#1397, fixes #1366): tiered signing-tx cache misses
  are resolved through `CacheSigner`, not raw receipts. Raw receipts carry no
  tx hash, so they dropped every signing tx and wedged on `invalid merkle root`.
- **SyncInfo** (#1398): a SyncInfo carrying the sender's blank bootstrap TC is
  accepted.
- **txpool** (#1399): a special (system) tx takes its nonce from a regular tx,
  and a regular tx can no longer evict a special one.

### Validation
- Mainnet full sync from genesis to 54,015,971: state, receipt and bloom roots
  validated, 0 consensus errors, canonical through TIPSigning and TIPTRC21Fee.
  Stopped by the disk guard.
- Apothem full sync from genesis to 12,716,238 with 0 consensus errors.
- Two-masternode V1 devnet (chainId 42427) bootstraps from genesis and runs to
  block 91 across six sign-tx blocks, with identical hashes on both nodes.

### Known issues
- In a mixed devnet of legacy (XDPoSChain) and geth V1 masternodes started from
  genesis, geth reads the legacy peers' total difficulty as a block height. It
  stays in syncing mode and never mints its turn.
- Apothem past 83,600,000 is only checked against the published v2.9.x rules;
  the network hasn't restarted yet.

## v1.17.5-xdc.8 — 2026-07-28 (merged, not released)

Shipped as the `v-xdc-geth-mint-20260725` and `v-xdc-geth-mint-20260727` builds.

- Merge go-ethereum v1.17.5 (#1370).
- Guard a nil QC in `FindParentBlockToAssign` (#1369, fixes #1368).
- V2 rewards: fix `totalSigners=0` after a restart (#1365, fixes #1364).
- Genesis-authoritative forks with a fork ceiling, ethstats coinbase and role,
  and checkpoint-sync-no-state and in-flight sign-tx fixes (#1363).
- Legacy-compatible default XDC profile (gas-charged TIPSigning) and a fix for
  minting at V1 epoch checkpoints (#1344).
- geth masternode minting on devnet 5151: live masternode-set derivation and
  `tipSigningBlock` (#1332, closes #1331).
- Bulk sync: the synthetic epoch uses the QC GapNumber snapshot (#1328, fixes #1327).

## v1.17.4-xdc.7 — 2026-07-05

See the [GitHub release](https://github.com/XDCIndia/go-ethereum/releases/tag/v1.17.4-xdc.7)
for this release and earlier ones.
