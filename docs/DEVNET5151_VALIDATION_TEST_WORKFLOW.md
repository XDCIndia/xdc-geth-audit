# devnet-5151 Validation Test Workflow — trigger: chain ≥ 5000 blocks

**Purpose:** once devnet-5151 reaches **block 5000** (well past the V2 switch at 2700, with the
15-validator set live), run a full validation pass proving the modern XDC node (go-ethereum v1.17.3)
(a) supports **every transaction type**, (b) **deploys + runs every required contract**, (c) is at
**Ethereum-mainnet parity for every fork/upgrade**, and (d) grows + serves **1 GB of chaindata
identically across all node types**. Any failure → **open a GitHub issue in `XDCIndia/go-ethereum`
with repro + a proposed fix**.

> Prereqs from the build workflow: `docs/DEVNET5151_PARITY_WORKFLOW.md` (genesis, 4 legacy + 11 modern,
> 420M/2s). This doc is the *test* pass that runs on top of that fleet.

## Operational constants (verified 2026-06-15)
- **min gasPrice = 12.5 gwei** (`eth.gasPrice` = 12500000000). Any tx below this is rejected
  "underpriced" — a load test MUST use `gasPrice >= 12500000000`.
- Funding/sender: legacy node1 coinbase **0x10babcec** (unlocked, genesis-funded ~9e74 wei). For bulk
  load raise `--txpool.accountslots 60000 --txpool.globalslots 120000 --txpool.globalqueue 120000`.
- Tx submit: legacy nodes support `eth.sendTransaction` (unlocked); modern nodes do NOT (use
  `eth_sendRawTransaction` with an externally-signed tx).
- All RPC over 127.0.0.1 (fleet-firewalled). `ssh -p 12141 root@<ip>`.

---

## ⛔ HARD PREREQUISITE — load + churn ONLY post-V2 (block ≥ 2700)
**Do NOT pour transactions or restart a producer during the V1 phase (blocks < 2700).** Observed
2026-06-15: pouring 50k txs and restarting node1 (a producer) mid-production made block 1750 carry an
empty/invalid V1 `header.Validator` (the M2 double-validation co-signature, signed by the *next* block's
validator). node1 came back, rejected the block (`retrieved hash chain is invalid: no validator in
header`), and the chain wedged at 1750 — **unrecoverable without a reset** because legacy XDPoSChain
cannot rewind. V1's M2 mechanism is fragile to producer churn under load. Therefore: let the chain reach
**block 2700 (V2 switch) cleanly with zero load and no restarts**, then run all of Phase 1–4. V2 BFT
(with the #199 fix) tolerates churn/load far better. (Possible code hardening to file: make V1 seal set
`header.Validator` resiliently when the M2 partner is briefly absent, and/or give legacy a safe rewind.)

## Phase 0 — Gate
- `eth_blockNumber >= 0x1388` (5000); `XDPoS_getMasternodesByNumber("latest")` shows the expected
  active set; head advancing ~2s; gasLimit 420M. If not yet at 5000, wait (loop).

## Phase 1 — Transaction-type coverage
Submit one of each, confirm it mines AND is identically importable by every node type:

| # | tx type | how | pass = |
|---|---|---|---|
| 1 | **legacy value transfer** (type 0x0) | `sendTransaction{to,value,gas:21000,gasPrice:>=12.5g}` | receipt status 0x1, balance moved |
| 2 | **EIP-2930 access-list** (type 0x1) | signed type-1 tx w/ `accessList` | mines; gasUsed reflects 2930 discount (only if Berlin active — see Phase 3) |
| 3 | **EIP-1559 dynamic-fee** (type 0x2) | signed type-2 tx `maxFeePerGas/maxPriorityFeePerGas` | mines IFF London active; else must be **rejected cleanly** (validate behavior matches fork state) |
| 4 | **contract creation** | deploy bytecode (`to:null`) | receipt has `contractAddress`, code present |
| 5 | **contract call (state-changing)** | call a setter | event/log emitted, state changed |
| 6 | **contract call (view/eth_call)** | `eth_call` | returns expected value, no state change |
| 7 | **XDC signing tx → 0x89** (post-TIPSigning) | masternode sign tx | `ApplySigningTransaction` path (zero-gas), no EVM, no merkle mismatch |
| 8 | **masternode 0x88**: propose/resign/vote/uploadKYC | selectors 0x01267951 / 0xae6e43f5 / vote / 0xf5c95125 | set changes at epoch boundary |
| 9 | **XDCx/XDCxlending 0x91/0x92/0x93** (if enabled) | empty/special tx | `ApplyEmptyTransaction` path |
| 10 | **large-data tx** (e.g. 100 KB calldata) | high-gas tx w/ big input | mines; body size accounted |
| 11 | **zero-value / self-transfer**, **revert tx**, **out-of-gas tx** | edge cases | receipt status 0x0 where expected, gas consumed |

**Cross-node check for each:** the tx's block hash + receiptsRoot is identical on a legacy node and on
a full/archive/snap/fast modern node (no divergence). FAIL → Phase 6 issue.

## Phase 2 — Contract deployment + functional suite
Deploy and exercise (record address + a passing call for each):
- **Tokens:** ERC-20, ERC-721, ERC-1155 (mint/transfer/approve/transferFrom; events).
- **Patterns:** multisig (Gnosis-style), proxy (EIP-1967 upgradeable), CREATE2 factory + deterministic deploy.
- **Opcode/feature probes** (one contract per fork — doubles as Phase 3 evidence):
  - Constantinople: `SHL/SHR/SAR`, `CREATE2`, `EXTCODEHASH`
  - Istanbul: `CHAINID`, `SELFBALANCE`, EIP-1108 bn256 cheaper gas, EIP-1884 SLOAD repricing
  - Berlin: access-list gas accounting (EIP-2929/2930)
  - London: `BASEFEE` opcode + EIP-1559 fee market (only if XDC has London) 
- **Precompiles:** ecrecover(0x1), sha256(0x2), ripemd160(0x3), identity(0x4), modexp(0x5),
  bn256Add/Mul/Pairing(0x6/7/8), blake2f(0x9) — call each, compare output to Ethereum mainnet expected.
- **XDC system contracts:** validator/masternode (0x88), block-signer (0x89), randomize/foundation as configured.

Each contract: deploy → call → assert expected result → assert identical across node types. **If a
contract fails to deploy or behaves differently from Ethereum mainnet → Phase 6 issue with the bytecode,
tx hash, expected-vs-actual, and a proposed fix** (which fork gate / opcode / precompile is wrong).

## Phase 3 — Ethereum-mainnet fork/upgrade parity matrix
Read the XDC chain config fork blocks and validate each behaves per the corresponding Ethereum upgrade:

| Ethereum fork | key EIPs | XDC validation |
|---|---|---|
| Homestead | — | base tx/contract semantics |
| EIP-150/155/158 | gas repricing, replay-protection (chainId in sig), state-clearing | type-0 tx with chainId 5151 signature accepted; empty-account pruning |
| Byzantium | REVERT, RETURNDATASIZE, staticcall, modexp/bn256 precompiles, status receipts | Phase-2 probes + receipt has `status` not `root` |
| Constantinople / Petersburg | SHL/SHR/SAR, CREATE2, EXTCODEHASH (EIP-1283 reverted) | probe contract |
| Istanbul | CHAINID, SELFBALANCE, EIP-1884/2200 repricing, EIP-1108 | probe; **note XDC gates Istanbul on `TIPXDCXCancellationFee`, not the struct block** |
| Berlin | EIP-2929/2930 access lists | type-1 tx + gas accounting |
| London | EIP-1559, BASEFEE, EIP-3198/3529 | type-2 tx + base-fee in header (validate ONLY if XDC enables London; if not, document the gap) |
| Shanghai/Cancun+ | PUSH0, transient storage, blobs | confirm enabled/disabled state matches intent; XDC is pre-merge PoW-replaced-by-XDPoS, so PoS-only EIPs N/A |

Method: deploy the probe contract per fork, run the opcode, and **diff the result + gasUsed against
Ethereum mainnet** (geth on mainnet, or known spec values). Also cross-check the **18 XDC TIP/fork
constants are bit-exact with XDPoSChain v2.7.1** (already verified — see memory
`reference-xdc-mainnet-fork-constants-v271`). Any opcode/gas/precompile mismatch vs Ethereum → Phase 6 issue.

## Phase 4 — 1 GB chaindata load + cross-node-type comparison
**Estimate:** the compacted legacy (full/pruned) baseline is ~1.0 MB at ~1.5k empty blocks (~640 B/empty
block). A simple value-transfer adds ≈ body(~110B) + receipt + tx-index(~50B) + state(unique recipient
≈ +100–200B) ≈ **~400–600 B/tx** in a pruned node ⇒ **~1.7M–2.5M txs to reach 1 GB** on the legacy full
node. **Archive nodes reach 1 GB with FAR fewer txs** (they retain every block's state trie, so size
grows per-block, not just per-tx). Measure, don't trust the estimate.

**Load procedure** (correct, post-gasPrice-fix):
```
# raise txpool on the load source (legacy node1), then pour with gasPrice >= 12.5 gwei, unique recipients
base = eth.getTransactionCount(from,"pending")
for i in 0..N: eth.sendTransaction({from, to: <unique addr i>, value:1, gas:21000, gasPrice:12500000000, nonce: base+i})
# at 420M gas, ~20000 transfers/block; pour in waves; let mine; repeat until target
```
**Calibrate per-tx cost first:** pour 50k txs, `geth/XDC` restart to COMPACT leveldb (compaction shrinks
size 2× — measure compacted before/after), `bytes/tx = Δcompacted / 50000`, then `txs_to_1GB =
(1e9 − current) / bytes_per_tx`. Then drive to 1 GB on legacy node1.

**Cross-node comparison at 1 GB-legacy:** record `du -sb <datadir>/XDC/chaindata` on EACH type and expect:
| node type | expected relative size | why |
|---|---|---|
| legacy full (node1) | 1.0 GB (reference) | pruned state |
| modern full (full-c) | ≈ legacy ± a few % | same pruning; validates modern storage parity |
| modern archive (archive-c) | **>> 1 GB** | retains all historical state tries |
| modern snap (snap-c) | ≈ full | snap normalizes to xdcSyncer FULL_SYNC on devnet |
| modern fast (fast-c) | ≈ full | fast falls back to full on devnet |
**Pass:** modern-full chaindata within a few % of legacy-full (storage parity); archive strictly larger;
all nodes serve the same head + same historical-state queries (block-hash + `eth_getBalance` at old block).

## Phase 5 — Soak + report
24h soak under continued tx load; grep set MUST stay empty on every node:
`BAD BLOCK | invalid merkle root | Rounds not continuous | missing trie node | Fatal`.
Write results table to `docs/parity-evidence/` and update this doc's checklist.

---

## Phase 6 — GitHub issue protocol (on ANY failure)
```
gh issue create --repo XDCIndia/go-ethereum \
  --title "[devnet-5151] <component>: <one-line symptom>" \
  --label devnet,parity \
  --body "<<'B'
## Symptom
<what failed: tx type / contract / fork probe / node-type divergence>
## Repro
- chain block: <N>, fork state: <...>
- tx/contract: <hash / address / bytecode>
- command: <exact RPC>
## Expected vs Actual
- Ethereum mainnet / legacy XDPoSChain: <expected>
- modern node (<type>): <actual>
## Evidence
<logs, block hash diff, gasUsed diff, receiptsRoot diff>
## Likely root cause + proposed fix
<which fork gate / opcode / precompile / sync path; file:line; suggested change>
B"
```
Triage rule: a **block-hash / state-root / receiptsRoot divergence between legacy and modern is P0**
(consensus) — stop and root-cause before continuing. A failed contract that legacy ALSO rejects is a
chain-config gap, not a modern bug (still file it, lower priority).

## Acceptance (definition of done)
- [ ] all 11 tx types in Phase 1 mine + cross-node-identical (or cleanly rejected per fork state)
- [ ] all Phase-2 contracts deploy + function + cross-node-identical; precompiles match Ethereum
- [ ] Phase-3 fork matrix: every enabled fork's EIPs behave per Ethereum spec; gaps documented as issues
- [ ] legacy full chaindata = 1 GB; modern full within a few % (storage parity); archive strictly larger;
      all node types serve identical head + historical state
- [ ] 24h soak clean (empty grep set); any failure filed as a GitHub issue with proposed fix
- [ ] results recorded under `docs/parity-evidence/`

## Automation hook (loop / cron)
Wire into the apothem+devnet loop: ON WAKE, if devnet `eth_blockNumber >= 5000` and Phase not started,
begin Phase 1 and proceed phase-by-phase, filing issues on failure, committing evidence after each phase.
Until 5000, just report progress. Build/sender hosts + scripts: see `DEVNET5151_PARITY_WORKFLOW.md` §8.
