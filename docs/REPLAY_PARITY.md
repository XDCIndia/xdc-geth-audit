# Replay-parity harness

Gate for consensus-tightening PRs (tracker #1430): a candidate binary must
accept every canonical block in the chosen mainnet/Apothem ranges with XDC full
verification forced on.

## How it works

`scripts/replay-parity.sh` uses stock `geth export` / `geth import` and
`debug.setHead`:

1. Export each range `FROM-TO` as RLP from a synced datadir.
2. Rewind the work datadir to `FROM-1`. `geth import` skips blocks it already
   has, so without the rewind nothing would be re-verified.
3. Run `XDC_FORCE_FULL_VERIFY=1 geth import <range>.rlp`.

Ranges run from the highest to the lowest. A rewind only goes backwards, so one
disposable datadir serves every range.

`XDC_FORCE_FULL_VERIFY=1` sets `core.XdcForceFullVerify` at process start.
While it is set:

| Relaxed path | Normal trigger | With the switch |
|---|---|---|
| `xdcFullVerify()` false: light V1 header path, V2 `fullVerify=false` | `XdcBulkSyncMode` or `CheckpointSyncNoStateActive` | always true |
| A.93/A.94 masking: missing state or root mismatch imported as `EmptyRootHash` | `canMaskMissingState()` | false |
| `checkpointSyncNoState`: no execution, no state/receipt-root check | DB flag at startup, A.93/A.94 | refused, logged as `XDC_FORCE_FULL_VERIFY: refusing ...` |

Nothing in the node sets the switch, so default behaviour does not change. A
plain `geth import` never starts the xdcSyncer, so `XdcBulkSyncMode` is already
false there. The switch closes the other routes: a persisted
`checkpointSyncNoState` flag in the work datadir, and the masking paths.

`cmd/xdc-replay` is not used. Its import step is still a stub ("GP5 ephemeral
chain not yet implemented"), so it cannot re-execute blocks.

## Requirements

- **Work datadir (`--datadir`)**: the script rewinds it, so use a disposable
  copy or snapshot (ZFS/btrfs snapshot, `cp --reflink`). It must hold full
  state at every `FROM-1`, so in practice a hash-scheme archive
  (`--gcmode archive --state.scheme hash`). A path-scheme node keeps only
  recent state history. When the rewind can't reach `FROM-1`, the script
  reports `REWIND_FAILED`. A range that starts at block 1 also works on an
  empty datadir (full replay from genesis).
- **Source (`--source`, default the work datadir)**: any synced datadir that
  holds the blocks. Before you trust its blocks, check them against a canonical
  RPC with `scripts/validate-canonical-parity.sh`.

## Default ranges

Constants come from `common/types.go` (`TIPSigning`, `TIPRandomize`,
`CopyXDCConstants`) and `params/config.go` (`XDPoS.V2.SwitchBlock`,
`Eip1559Block`, `CancunBlock`, the `V2.AllConfigs` round entries,
`TIPUpgradeRewardBlock`). V1 epochs are 900 blocks, so every V1 range covers 4
checkpoints. Each V2 range covers about 6 epoch switches after the fork.

| Network | Range | Covers |
|---|---|---|
| mainnet | 2,998,200–3,001,800 | TIPSigning 3,000,000 |
| mainnet | 3,462,300–3,465,900 | TIPRandomize 3,464,000 |
| mainnet | 38,382,000–38,385,600 | TIPTRC21Fee / TIPXDCX / BlackListHF 38,383,838 |
| mainnet | 80,368,200–80,375,400 | V2 switch 80,370,000, then V2 epoch switches; XDCX miner/receiver disable 80,370,000 / 80,370,900 |
| mainnet | 98,799,300–98,805,000 | EIP-1559 98,800,200, Cancun 98,802,000, then 3,000 blocks |
| apothem | 2,998,200–3,001,800 | TIPSigning 3,000,000 |
| apothem | 3,462,300–3,465,900 | TIPRandomize 3,464,000 (package default, not overridden for chain 51) |
| apothem | 23,777,400–23,781,000 | TIPTRC21Fee / TIPXDCX 23,779,191 |
| apothem | 56,826,900–56,834,100 | V2 switch 56,828,700, then V2 epoch switches |
| apothem | 71,549,100–71,555,000 | EIP-1559 71,550,000, Cancun 71,551,800, then 3,000 blocks |
| apothem | 83,575,300–83,579,000 | round 27,360,000 (25-masternode tier), block 83,577,112 |
| apothem | 83,598,200–83,601,800 | TIPUpgradeReward/Penalty, Gas2500x 83,600,000 |

## Running it

```sh
make geth
# mainnet, default ranges, on a disposable archive snapshot
scripts/replay-parity.sh --network mainnet --datadir /snap/mainnet-archive-copy --out /data/rp-mainnet
# apothem
scripts/replay-parity.sh --network apothem --datadir /snap/apothem-archive-copy --out /data/rp-apothem
# custom ranges, blocks exported from a separate synced node
scripts/replay-parity.sh --datadir /snap/copy --source /data/xdc-full 80368200-80375400
```

## Output

The script prints one `OK` or `FAIL` line per range. It also writes
`<out>/<network>-summary.tsv` with these columns: range, result
(`OK` / `REJECTED` / `REWIND_FAILED`), blocks imported, elapsed seconds,
rejected block number and hash, state-root mismatch flag, receipt-root mismatch
flag, and error. Per-range export, rewind and import logs sit beside it.

The exit code is 0 when every block was accepted, 1 for any rejection or failed
rewind, and 2 for bad arguments.
