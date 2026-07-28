# ADR 001 — Patch missing XDC chain-config fields at SetupGenesisBlock

Status: ACCEPTED (PR #757, 2026-05-23)
Context: `reset/integration-1.17.3` baseline reset onto upstream geth v1.17.3.

## Context

The embedded XDC Apothem and Mainnet genesis JSONs (`core/xdc_genesis/
apothem.json`, `mainnet.json`) carry only a minimal chain-config subset:

```json
{
  "chainId": 51,
  "homesteadBlock": 1,
  "eip150Block": 2,
  "eip155Block": 3,
  "eip158Block": 3,
  "byzantiumBlock": 4,
  "eip1559Block": 71550000,
  "XDPoS": {}
}
```

The richer config lives in `params/config.go` (`XDCApothemChainConfig`,
`XDCMainnetChainConfig`) and includes:

- Post-Byzantium fork blocks (Constantinople / Petersburg / Istanbul /
  Berlin / London / Cancun)
- `xdpos.V2.SwitchBlock` + `SwitchEpoch` (Apothem: 56,828,700)
- `TrustedSyncCheckpoints` (15 entries on Apothem)
- `XDPoSConfig` numeric fields (Period, Epoch, Reward, RewardCheckpoint,
  Gap, FoudationWalletAddr)

`Genesis.chainConfigOrDefault(ghash, stored)` returns `g.Config` verbatim
when a genesis spec is provided (the `--apothem` / `--xdcmainnet` flag
constructs one), bypassing the `LoadChainConfig` patch path. Result: the
runtime config is the minimal JSON subset, **not** the rich
`params.XDCApothemChainConfig`.

Why this matters: `forkid.NewID` hashes the full fork-block list. With
fork blocks missing the forkID hash diverges from canonical, and
production XDC peers reject the wire-handshake `eth_status` exchange.
Reset nodes can never peer with the production fleet.

## Decision

In `core/genesis.go` `SetupGenesisBlock`, immediately after
`chainConfigOrDefault`, merge missing fields from
`params.XDC{Mainnet,Apothem}ChainConfig` by chainID dispatch:

1. Patch `TrustedSyncCheckpoints` if empty (chainID 50 or 51)
2. Patch `XDPoS.V2` if nil (chainID 50 or 51)
3. Patch missing fork blocks via `patchMissingForkBlocks()` (chainID
   50 or 51) — Constantinople, Petersburg, Istanbul, Berlin, London,
   Eip1559, Cancun

Each patch logs at INFO so operators can confirm the merge fired.

## Alternatives considered

### A. Update the embedded JSONs to be richer

Rejected. The embedded JSONs are deliberately minimal because they match
the canonical xdc-network apothem.json / mainnet.json byte-for-byte
(both teams ship the same genesis files). Diverging would create two
sources of truth and force ongoing reconciliation. The merge pattern
also lets future v1.17.4+ upstreams add fields without re-editing JSON.

### B. Compute the rich config in `DefaultXDCApothemGenesisBlock()`

Tempting but loses the merge invariant for the user-supplied-genesis
case (`geth --datadir X init custom.json`). SetupGenesisBlock is the
only path that runs for every node startup; the patch belongs there.

### C. Override `chainConfigOrDefault` to dispatch on genesis hash

The default-config switch already dispatches by `params.MainnetGenesisHash`
etc. Adding `XDCApothemGenesisHash` / `XDCMainnetGenesisHash` cases would
work for the no-genesis path, but the genesis-supplied path (`g != nil`)
returns early before that switch. We'd still need the merge in
SetupGenesisBlock for the common case.

## Consequences

Positive:
- Reset binary's runtime config now matches canonical XDC chain config
- forkID hash matches production
- Wire handshake against production fleet succeeds (verified)
- Trusted sync checkpoints fire for fast-from-genesis sync

Negative:
- Three INFO logs at every node startup (acceptable noise; operators
  expect verbose startup)
- Slight CPU overhead per startup (~microseconds) for the merge

Neutral:
- Embedded JSONs continue to mirror xdc-network's minimal shape — no
  divergence to manage

## Validation

After PR #757 land + deploy on xdc02:

```
INFO  XDC: patched missing TrustedSyncCheckpoints in SetupGenesis for apothem count=15
INFO  XDC: patched missing V2 config in SetupGenesis for apothem (chain ID 51) switchBlock=56,828,700
INFO  XDC: patched missing ConstantinopleBlock network=apothem block=61,290,000
INFO  XDC: patched missing PetersburgBlock     network=apothem block=61,290,000
INFO  XDC: patched missing IstanbulBlock       network=apothem block=61,290,000
INFO  XDC: patched missing BerlinBlock         network=apothem block=61,290,000
INFO  XDC: patched missing LondonBlock         network=apothem block=61,290,000
INFO  XDC: patched missing CancunBlock         network=apothem block=71,551,800
```

`admin.nodeInfo.protocols.eth.config` shows the full rich config.

## References

- PR #757 — implementation
- `docs/port/REMAINING_WORK.md` §1 — root-cause diagnostic trail
- xdc-network/`core/genesis.go:409-595` — the merge pattern this mirrors
