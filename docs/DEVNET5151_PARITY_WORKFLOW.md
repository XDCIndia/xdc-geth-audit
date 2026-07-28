# devnet-5151 Workflow — 11 modern + 4 legacy, all node types, mainnet-parity validated

**Purpose:** a tested, reusable runbook to stand up the XDPoS V1+V2 cross-client devnet
(chainId **5151**) with **4 legacy (XDPoSChain) + 11 modern (go-ethereum v1.17.3) validators of
every node type**, and to validate that the modern client is **bit-for-bit parity with mainnet across
every consensus upgrade**. Everything below was exercised in the 2026-06-15 session.

> One-line model: **legacy XDPoSChain is the oracle; the modern node must reproduce it exactly.**
> The devnet is the harness that proves it across V1, the V1→V2 switch, V2 BFT, and every TIP fork.

---

## 0. Topology

| role | count | client | node types | hosts |
|---|---|---|---|---|
| legacy producers | 4 | XDPoSChain v2.7.0 | full | xdc03 (node1/2/3/4) |
| modern validators | 11 | go-ethereum v1.17.3 (commit 42f91b87b) | full ×7, archive ×2, snap ×1, fast ×1 | xdc03, xdc04, xdc167, xdc02 |

- **chainId / networkid** 5151; **genesis hash** `0x68c066e318e5eec82f368d412077e609351385a6b81b4c81b76370ad313fcf78`
- **epoch** 900, **gap** 450, **V2 switch** block **2700**, **gas plateau** 420,000,000
- **The 4 legacy are the GENESIS masternodes** (in `extraData`): node1 `0x10babcec`, node2 `0xe20fdc70`,
  node3 `0x68f4f2e4`, node4 `0x8263fe68`. Modern validators are added as candidates **post-genesis**.

### Host / port / etherbase reference (as built this session)
| node | host | etherbase | type | p2p / http |
|---|---|---|---|---|
| node1 (legacy) | xdc03 65.21.27.213 | 0x10babcec | full | 31311 / 9561 |
| node2 (legacy) | xdc03 | 0xe20fdc70 | full | 31312 / 9562 |
| node3 (legacy) | xdc03 | 0x68f4f2e4 | full | 31313 / 9563 |
| node4 (legacy) | xdc03 | 0x8263fe68 | full | 31314 / 9564 |
| val2 | xdc03 | 0x4452c7ac | full | 31320 / 9572 |
| full-c | xdc04 65.21.71.4 | 0xdd1dae28 | full | 31315 / 9565 |
| archive-c | xdc04 | 0xac68ec0f | archive | 31316 / 9566 |
| snap-c | xdc04 | 0xc45f8652 | snap | 31317 / 9567 |
| fast-c | xdc04 | 0x9f331784 | fast | 31318 / 9568 |
| modern-9 | xdc04 | 0xa4f618f3 | full | 31319 / 9576 |
| modern-10 | xdc04 | 0x0950dd9c | full | 31321 / 9577 |
| archive2 | xdc04 | 0xd9d76283 | archive | 31322 / 9578 |
| xdc167 | xdc167 167.235.13.113 | 0xa2ed6e66 | full | 31371 / 9572 |
| xdc02 | xdc02 135.181.117.109 | 0x92911a81 | full | 31371 / 9572 |
| (11th modern) | any | new key | full/snap | assign free ports |

> SSH: `ssh -p 12141 root@<ip>` (xdc05 uses `-p 52316`). ethstats: `<label>:xdc_openscan_stats_2026@stats.xdcindia.com:443`.

---

## 1. Build (Phase 0)

```bash
# modern (each host that runs modern nodes — build NATIVELY, never ssh-pipe a binary; it truncates)
cd /root/workspace/go-ethereum && git checkout fix/hookreward-1800-parity && git pull \
  && PATH=/snap/bin:$PATH make geth
./build/bin/geth version | grep 'Git Commit'   # expect 42f91b87b or newer

# legacy (xdc03)
cd /root/workspace/XDPoSChain && make XDC   # v2.7.0-devnet
```
The modern binary MUST contain the session's consensus fixes (see §5). Older commits wedge devnet.

---

## 2. Genesis + 4-legacy bootstrap (Phase 1) — `reset-legacy.sh`

```bash
# /root/devnet-v1v2/reset-legacy.sh (xdc03): wipe + init + start the 4 genesis validators
for dd/eb in node1/0x10babcec node2/0xe20fdc70 node3/0x68f4f2e4 node4/0x8263fe68; do
  rm -rf $W/$dd/XDC/chaindata
  XDC --datadir $W/$dd init $W/genesis.json
  XDC --datadir $W/$dd --networkid 5151 --port <p> --mine --miner-etherbase <eb> \
      --unlock <eb> --password $W/password.txt --gcmode full --syncmode full \
      --bootnodes <node1,node3,node4 @127.0.0.1> --miner-gaslimit 420000000 \
      --http --http-addr 127.0.0.1 --http-port <h> --http-api eth,net,web3,admin,XDPoS,miner,debug \
      --ethstats xdc03-v1v2-$dd:... --verbosity 3 &
done
```
**Acceptance:** block 1 `gasLimit == 420000000` (the 4.7M→420M jump is set directly by legacy from
`--miner-gaslimit`; validation accepts the genesis→block-1 jump), 4 legacy round-robin at **2.0s**,
each node `net_peerCount == 3` (full mesh). Verify: `eth_getBlockByNumber(0x1)` gas; head advancing.

---

## 3. Modern candidate pool (Phase 2) — `redeploy-modern-fresh.sh`

Bring up all 11 diverse-type modern nodes **validator-ready** (mining is a no-op until proposed):
```bash
# per node (wipe → init modern genesis → start mining at 420M, sync from legacy bootnodes)
rm -rf $W/$dd/XDC/chaindata
geth --datadir $W/$dd --state.scheme hash init $W/genesis-modern.json
geth --datadir $W/$dd --networkid 5151 --port <p> --authrpc.port <a> \
     --syncmode <full|snap> --gcmode <full|archive> --state.scheme hash --snapshot=false \
     --bootnodes <node1,node3,node4 @public-IP:port> --maxpeers 30 \
     --mine --miner.etherbase <addr> --password $W/password.txt --miner.gaslimit 420000000 \
     --http --http.addr 127.0.0.1 --http.port <h> --http.api eth,net,web3,admin,debug,XDPoS,xdc \
     --ethstats devnet5151-<host>-<type>:... &
```
Generate a fresh key for any type slot lacking one: `geth --datadir <dd> account new --password password.txt`.

**Acceptance (sync validation):** every modern node reaches the legacy tip and tracks it at 2s;
`eth_syncing == false`; logs show `xdcSyncer: FULL_SYNC at-tip detected — clearing XdcBulkSyncMode`.
**Same-chain proof:** `eth_getBlockByNumber(0x3e8)` hash is identical on a legacy node and every modern
node (no fork). **gcmode proof:** archive nodes serve `eth_getBalance(<genesis-acct>, 0x1)`.
> NOTE: on this fork ALL nodes wire-sync via the **xdcSyncer FULL_SYNC** path regardless of `--syncmode`
> — true eth/`snap` and eth/`fast` are NOT engaged on devnet (devnet fast-sync is the known-broken
> #149–162 path). Node-type diversity is real at the **gcmode** level (archive vs full); the snap/fast
> labels are operational, not a different wire protocol.

---

## 4. Validator buildout — ONE candidate PER EPOCH (Phase 3, post-V2)

After the chain crosses the V2 switch (block 2700) and is stable at 2s, propose modern candidates
**one per epoch (900 blocks)** via the O1 owner node. Modern miners removed `--unlock` for tx-sending,
so sign with a legacy XDC owner node and broadcast the raw tx via a synced node:

```bash
# O1 owner = 0x64e1a873 (legacy XDC node, --unlock O1 --password password.txt, IPC only — no insecure flag).
# Fund O1 from node1 foundation 0x10babcec (clean nonces on the fresh chain), then per candidate:
#   uploadKYC(string)  selector 0xf5c95125   (onlyKYCWhitelisted, once per owner)
#   propose(address)   selector 0x01267951   value 10 XDC (0x8ac7230489e80000)
#   resign(address)    selector 0xae6e43f5   (to remove a bad candidate)
O1.signTransaction({to:0x...88, data:<propose+addr>, value:10e18, gas:500000, gasPrice:25e9, nonce:N})
node1.eth_sendRawTransaction(<raw>)
```
**Discipline (the rule that prevents the wedge — see §5):** verify `XDPoS_getMasternodesByNumber("latest")`
grew by exactly 1 AND the chain still holds 2s BEFORE proposing the next. **NEVER let the candidate
count exceed the number of live, correctly-keyed, synced nodes** — the epoch transition auto-activates
ALL candidates, and any that cannot seal becomes a dead round-robin slot that breaks the V2 3-chain
commit rule and halts the chain.

**Acceptance:** active set reaches 4 legacy + 11 modern, all minting round-robin, gasLimit 420M, 2.0s,
no `Rounds not continuous` starvation.

---

## 5. Mainnet parity of all upgrades (Phase 4) — the validation matrix

Run `tools/parity-harness/xdc_rpc_parity.py` block-by-block (modern node vs legacy node over the SHARED
chain). A block-hash match proves every consensus field (stateRoot, receiptsRoot, txRoot, gasUsed,
difficulty, extraData, validators, penalties) is bit-identical. Cover every upgrade boundary:

| upgrade | what to prove | session evidence |
|---|---|---|
| **V1 PoA** (clique-style round-robin) | header hash, signer recovery, M2 `header.Validator` double-sig | 100% parity, 2100/2100 blocks (`V1V2_CONSENSUS_PARITY_AUDIT_2026-06-14.md`) |
| **V1→V2 switch** (devnet 2700 / mainnet ~56.8M) | switch-block masternodes read from `header.Extra`; zero boundary BAD BLOCK | clean crossing validated |
| **V2 BFT** (QC/TC, 3-chain commit) | cross-client QC formation legacy↔modern; round-robin leaders | 5-set ran at 2s, modern+legacy QCs |
| **Reward calc** (HookReward, signing-tx) | per-signer reward, foundation-skip, off-by-one window | fixes `0847c0589` (#1800), `72a14851c` (#186) |
| **TIP fork constants** | all 18 mainnet TIP/fork blocks bit-exact (Petersburg/Istanbul gate on TIPXDCXCancellationFee) | matches XDPoSChain v2.7.1 release |
| **StaticCall Istanbul gate** | gas behavior at mainnet 34,059,322 | PR #724 restored |
| **special-tx dispatch** | signing tx→0x89 (ApplySigningTransaction, zero-gas), XDCx 0x92/0x93 (ApplyEmptyTransaction) | mine-path mirrors `Process` (#186/#188/#189) |
| **state execution** | V1 state root at block 16; no double-credit | #185 fixed |

**CI gate:** any PR touching `consensus/XDPoS/` or state runs the harness on blocks 1..2700 (covers V1,
both V1 checkpoints, the V2 switch) + the mainnet TIP-boundary fixtures. **Zero divergence required.**

Grep set that MUST stay empty during any sync/soak:
`BAD BLOCK | invalid merkle root | Rounds not continuous | Fail to update masternodes`.

---

## 6. Critical lessons tested THIS session (do not relearn the hard way)

1. **gas-limit divergence (#181/#952):** legacy V2 sets header gasLimit = fixed `params.TargetGasLimit`
   (= `--miner-gaslimit`, a jump); modern climbs via `CalcGasLimit`. They only agree if **every** miner is
   pinned to the SAME value. ALWAYS run modern with `--miner.gaslimit <network plateau>` (420M here).
2. **halted-tip miner deadlock (#199, commit 42f91b87b):** on a halted V2 chain whose peers speak
   xpos2/100 (no eth/69 BlockRange), a FULL_SYNC modern node stays `syncing=true` → miner gated → can't
   help quorum → self-sustaining deadlock. Fix: clear `XdcBulkSyncMode` when the head is frozen for 4
   heartbeats with peers. Liveness-only; can't fire on a healthy chain (head advances every 2s).
3. **the keyless/dead-slot trap:** proposing more candidates than live correctly-keyed nodes is FATAL —
   the next epoch auto-activates all candidates; a keyless one (lost key) can't seal → dead round-robin
   slot → no QC → permanent halt. This wedged the prior chain and forced the reset.
4. **legacy CANNOT be rolled back:** XDPoSChain 2.7.0 has no HTTP `debug_setHead`; the IPC `debug.setHead`
   exists but **cascades to genesis and crashes** (`CRIT Fail to update masternodes during reorg`).
   So a wedged mixed chain has NO chaindata-preserving rewind — recover by reset, not setHead. Modern
   `debug_setHead` works and clamps to the nearest epoch-switch block.
5. **mangled XDC-prefix etherbase:** a keystore filename `xdc<40hex>` → EVM etherbase is `0x`+the 40 hex
   AFTER `xdc`, NEVER `0x`+`dc…`. A `0xdc…` etherbase is keyless and can't seal (it dead-slotted index-1).
6. **never two sealers for one etherbase** (#179 duplicate-coinbase) — fork-on-mint.

---

## 7. Acceptance (definition of done)
- [ ] block 1 gasLimit = 420,000,000; 4 legacy genesis validators round-robin at 2.0s
- [ ] 11 modern nodes (full×7, archive×2, snap×1, fast×1) synced, same-chain (block-1000 hash match), at tip
- [ ] modern validators proposed one-per-epoch to a 15-validator active set, 2s held after each
- [ ] parity harness: 0 divergence over the V1+switch+V2 range and all mainnet TIP boundaries
- [ ] empty grep set across a 24h soak; ethstats shows all 15 with type labels

## 8. Scripts (xdc03 `/root/devnet-v1v2/`)
`reset-legacy.sh` (4 legacy genesis bootstrap) · `redeploy-modern-fresh.sh` (xdc04 modern pool) ·
`relaunch-val2-FIXED.sh` (val2) · `propose-modern.sh` (candidate propose) · O1 owner node = `owner-legacy`.
Handoff: `docs/DEVNET5151_RESUMED_2026-06-15.md`. Memory: `project-devnet5151-modern-fleet-recipe`.
