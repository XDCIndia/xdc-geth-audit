# Modern geth (v1.17.3) per-node memory ~15–27 GB caps validator density / OOMs hosts (devnet-5151)

> Draft issue body — saved locally for the user to file on `XDCIndia/go-ethereum` when ready
> (autonomous external issue-filing was declined; this preserves the finding).

## Summary
On devnet-5151, the modern XDC go-ethereum validator (v1.17.3, commit `42f91b87`) grows to **~15–27 GB RSS per node** during normal V2 operation. Running the **4 modern miners** needed for an 11-validator BFT quorum on a 62 GB host exhausts RAM within minutes, OOM-killing validators → quorum loss → **chain stall** (clean liveness stall — no fork, 0 BAD BLOCK; recovers only by restarting fewer nodes).

## Environment
- Binary: `/root/workspace/go-ethereum/build/bin/geth` `1.17.3-stable / 42f91b87` (built 2026-06-15)
- Role: XDPoS V2 validator/miner, `--mine --gcmode full --state.scheme hash --snapshot=false`
- devnet-5151 (chainId 5151), ~13.7k blocks, mostly-empty blocks
- Host: 62 GB RAM, shared with other workloads

## Observed
- One node reached **27 GB RSS** after ~9k blocks of mining; a second at ~10 GB.
- `--cache 256` and `--cache 384` did **not** bound total RSS → the growth is not the DB cache (likely Go heap / trie working set / mmap'd Pebble SST+WAL).
- 4 fresh miners (`--cache 256`) filled a 62 GB host and OOM-killed nodes within **~5 min** of restart.
- Related: devnet chaindata showed **851 MB of uncompacted Pebble `.log` (WAL)** while the actual block data (`ancient`) was only ~5 MB — suggests WAL/state isn't being compacted/released promptly.

## Impact
- Caps modern-validator density to ~2–3 per 62 GB host.
- Liveness risk: over-packing a host silently OOMs validators and stalls the chain.
- Operator memory cost for running multiple modern validators / archive nodes.

## Suggested investigation
- Break down RSS: anonymous heap vs mmap'd DB pages (Pebble memory-maps SSTs; RSS may overcount, but OOM-kills are real).
- Bound trie/snapshot in-memory working set; verify `--cache` actually caps DB memory on this fork.
- Investigate Pebble WAL/compaction retention (the 851 MB uncompacted WAL).
- Consider a hard memory-cap flag and/or periodic compaction.

## Not a consensus issue
This is purely a memory-footprint/capacity issue. Full V1+V2 cross-client consensus + execution validation (tx-type matrix, ERC20, fork parity, 7 modern + 4 legacy mixed BFT set sealing 13.5k blocks **bit-identical** across legacy XDPoSChain and modern geth, 0 BAD BLOCK) passed cleanly before this OOM-induced stall.
