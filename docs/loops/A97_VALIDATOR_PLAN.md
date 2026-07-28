# A.97 Validator-Completion Advisory + Execution Plan

**Synthesized:** 2026-06-10  
**Status of PR stack:** #897 freeze / #898 snap-heal / #899 pivot=head-64 / #900 gap fallback / #901 deploy branch — all complete and in-flight.  
**Key live evidence:** Apothem :8652 VALIDATOR-READY 8/8 (3h21m genesis-to-AT_TIP, 17,132 entries/s state-DL, A94=0, BAD=0). Mainnet :8654 dynamic pivot=head-64 PROVED (pivot 103,606,426 = tip-64); first pivot at stale 76M epoch hit pruned wall, recovered via SyncFailed→retry in 30s. State-DL in flight (~15.7M entries at t+22min, clean).

---

## 1. VERDICT — Is A.97 the right path to geth-aligned real fast-sync?

**Yes, with three accepted-risk items and three acknowledged code gaps.**

### What A.97 achieves that aligns with upstream geth's pre-removal fast-sync semantics

| Upstream pre-removal property | A.97 status | Evidence |
|---|---|---|
| P1: pivot selected from peer-reported head | ALIGNED (pivot = head-64, A.97.3) | Live: pivot 103,606,426 = tip(103,606,490)-64 exactly |
| P2: no block execution before pivot | ALIGNED | fast-sync code path unchanged |
| P3: state complete before pivot committed | ACCEPTED-RISK — Pending()==0 drain, no merkle audit | Same as upstream geth pre-removal; A.97.2 heal is backstop |
| P4: full validation pivot→tip | ALIGNED on clean path | Apothem: full ValidateState at tip, A94=0 |
| P5: end-state equivalent to full-sync | ACCEPTED-RISK on degraded path | Clean path: 8/8 validator-ready. Degraded (no snap-peers, checkpointSyncNoState=true): A.97.2 heal skips → A.94 risk lives. Documented as RISK #1. |
| P6: no trusted anchor bypass | ALIGNED (--no-auto-pivot removes A.96 anchor) | A.97 marker fires on both nodes |
| P7: freeze vs re-pivot | DELIBERATE DIVERGENCE — freeze blocks pivot advance; if peers GC frozen-pivot state, SyncFailed→ResetPivotFreeze→retry | Proved live: it.28 first-cycle 76M stale anchor hit pruned wall, recovered to 103M clean |
| P8: validator-ready at AT_TIP | ALIGNED on clean path | 8/8 on apothem |

### What upstream geth still does differently (and whether it matters)

1. **Merkle audit before commit** — upstream's pre-removal fast-sync ran a local `stateBloom` trie audit before committing the pivot block to confirm no missing nodes. A.97 only checks `Pending()==0` (work-queue drain). This means a peer that silently discards some NodeData responses could produce a committed trie with missing nodes. A.97.2 heal is the backstop. This does NOT matter in practice: (a) apothem reached 8/8 with zero A.94 events on the clean path; (b) the same property existed in upstream geth before snap-heal was introduced; (c) the only mitigation upstream added was snap-heal itself, which A.97.2 ports.

2. **Snap-range coverage** — upstream's snap-sync (replacement for fast-sync) downloads account/storage ranges via GetAccountRange before healing. XDC fleet does not serve GetAccountRange (issue #96 dead path confirmed). A.97 skips range download and relies on NodeData (fast-sync state-DL path) for the initial bulk, then A.97.2 GetTrieNodes for healing. This is correct given fleet capabilities.

3. **Pivot distance** — upstream's snap-sync uses pivot=head-64 as well (fsMinFullBlocks). A.97.3 aligns this. DONE.

**Conclusion:** A.97 is the correct and only viable path given the XDC fleet's snap capabilities. The three accepted-risk items are lower severity than they would be on a general-purpose chain because: the fleet serves NodeData reliably (proven by 8/8 acceptance), the heal backstop exists (A.97.2), and the divergence at P7 (freeze vs repivot) recovered cleanly in production. No architectural revision is warranted.

---

## 2. RESIDUAL GAPS RANKED

### Code gaps — block minting (must all be closed before first minted block)

| Rank | Gap | File:Line | Effort | Blocks minting? | Owner |
|---|---|---|---|---|---|
| M1 | Block-production loop entirely absent. No goroutine drives XDPoS-V2 block production. `miner/miner.go` is the upstream post-merge payload-builder stub: no `Start()`, no timer loop, no `commitNewWork()` connected to `NewRoundCh`/`MinePeriodCh`. | `miner/miner.go:82-91`, `eth/backend.go:315`, ref: `XDPoSChain/miner/worker.go:279-337` | Large (400-600 LOC): port XDPoSChain update/commitNewWork loop, wire `Prepare`/`Seal`/`InsertChain` | YES — fatal | CODE PR |
| M2 | `minePeriodCh`/`newRoundCh` passed as `nil` to `engine_v2.New()`. `UpdateParams` at `engine.go:233` sends unconditionally — goroutine-leaks silently in sync mode but deadlocks when mining loop (M1) is added. | `eth/backend.go:315`, `engine_v2/engine.go:232-234` | Trivial (5 LOC nil guard or channel alloc, resolves with M1) | YES — fatal corollary of M1 | CODE PR |
| M3 | `engine.Authorize(signer, signFn)` never called from any startup path. `Seal()` reads `x.signer`/`x.signFn` — both zero-value without Authorize. Block produced without Authorize has all-zero signature; VerifyHeader rejects it. | `consensus/XDPoS/xdpos.go:1477-1494`, `eth/backend.go:305-337` | Medium (50 LOC): wire account-unlock → Authorize at node start or via `--unlock` processing | YES — blocks valid seal | CODE PR |
| M4 | `HandleProposedBlock` not wired post-`InsertChain`. After a validator seals and inserts its own block, it must call `engine.HandleProposedBlock()` to broadcast the BFT vote → QC threshold → block committed. Without it, mined blocks are never finalized. | `consensus/XDPoS/xdpos_api.go:94`, `consensus/XDPoS/engines/engine_v2/utils.go:504-527`, eth/ (caller missing) | Small (10 LOC hookup, requires M1 first) | YES — blocks BFT commit | CODE PR |
| M5 | `--mine` flag deprecated with no XDPoS-aware production-start path. Currently only logs a deprecation warning; triggers no production loop. | `cmd/utils/flags.go:1788` | Small (re-activate flag or new `--xdc-mine` flag to trigger M1 goroutine) | YES — blocks triggering M1 | CODE PR |

**Critical path note:** M1→M2→M3→M4→M5 must ALL be resolved before a single block can be minted. M2 is implicitly fixed when M1 allocates real channels. M4 is 10 LOC that plugs into the M1 mining loop. M5 is the on/off switch for M1.

### Accepted-risk fast-sync gaps (no action required)

| Gap | Disposition | Rationale |
|---|---|---|
| P3: state completeness pre-commit — Pending()==0 only | ACCEPT | Same as upstream geth pre-removal; A.97.2 heal is backstop; apothem 8/8 proves sufficient in practice |
| P5: end-state equivalence when snap-heal skips | ACCEPT — document | Only triggered when checkpointSyncNoState=true AND no snap-peers AND no A.97.2 heal run. Documented as RISK #1 in A97_PHASE2_DESIGN.md. Monitor for A.94 events post-tip. |
| P7: freeze vs re-pivot recovery | ACCEPT | Recovery via SyncFailed→ResetPivotFreeze→retry proved live on mainnet it.28. No code change needed. |

### OPS gaps (non-blocking for minting mechanics, blocking for network election)

| Gap | Disposition | Owner |
|---|---|---|
| M6: propose() flow — KYC whitelist + 10,000,000 TXDC stake + epoch wait | USER DECISION: fund EOA, obtain KYC whitelist, execute propose() | User |
| M7: gcmode=full+hash-scheme+snapshot=false is mint-compatible | ACCEPTED — no change needed; proven by stage-6 validator-ready check | — |
| M8: nodekey/enode stability | ACCEPTED — standard geth behavior, nodekey already in datadir | — |

---

## 3. FLEET PLAN

### Host roles

| Host | IP | Recommended role | Rationale |
|---|---|---|---|
| **xdc02** | 135.181.117.109 | Primary A.97 test fleet, validator dry-run host for apothem | 5 instances already active; :8652 is 8/8 validator-ready; :8654 dynamic mainnet in-flight; 163G free — tight but sufficient for one more validator instance (~35G datadir). **Best validator dry-run candidate today.** |
| **xdc01** | 95.217.112.125 | State-serving peer + A.97 expansion slot | path-scheme full node (snapfull2, :8651, 303G, PID 3970) is the best state-serving node in the fleet — serves GetTrieNodes/GetNodeData against live 303G path-scheme trie. Peer ALL fast-sync nodes to xdc01:30303. Fast-sync apothem :8552 @ 82.9M healthy. 322G free. Second validator candidate (apothem fast-sync is AT_TIP). |
| **xdc03 (65.21.27.213)** | 65.21.27.213 | Second validator dry-run candidate | 267G free, 51G RAM, both nodes AT_TIP (apothem 82,905,723 / mainnet 103,608,051). go-ethereum already in /root/workspace. No A.97 build yet — requires: push a97 branch, fetch+build, launch side-by-side. **Best candidate for mainnet validator dry-run.** |
| xdc168 | 95.217.56.168 | Snapshot-serving (nginx, 1.2T snapshots) — keep as-is | 404G free on /mnt/data; both geth instances near-tip and healthy. Validator dry-run feasible (404G headroom) but low priority — snapshot serving is more valuable. Do not disturb. |
| xdc167 (65.21.27.213 hostname xdc03) | 167.235.13.113 | Needs remediation before any new deploy | mainnet has missing trie node bf95a5e (state gaps); /mnt/data 100% full (dead xdcchain node); only 81G free on /. Priority: (1) free /mnt/data by removing dead xdcchain datadir (user authorization required — wiping /work/xdcchain recovers ~N00G); (2) restart mainnet fast-sync after /mnt/data freed; (3) then validator candidate. |
| xdc05 | 185.180.220.183 | Monitor only — disk at floor | 60G free (exactly at threshold); swap exhausted; apothem wedged/stopped. Do NOT add new instances. If slot opens: wipe /root/.xdc-fastsync-apothem (39G) after user auth, which frees ~99G. |
| xdc04 | 65.21.71.4 | Active mainnet validator — leave alone | DISK CRITICAL (5.6G free). Two live --mine validators running (mainnet :9845, apothem :9645, both PBSS path-scheme). No new instances until disk freed. |

### Quick wins

1. **Peer ALL fast-sync nodes to xdc01:snapfull2 (:30303 or its actual p2p port)** — the path-scheme 303G full node serves NodeData against the full historical trie, eliminating the "failed with all peers" pruned-epoch wall that caused the first mainnet pivot to abort. Execute via IPC `admin.addPeer("enode://<xdc01-snapfull2-enode>@95.217.112.125:<p2p-port>")` on each syncing instance.

2. **Peer xdc02 :8654 (dynamic mainnet) with xdc01 snapfull2** — most impactful single peering for the in-flight mainnet sync. Run immediately without waiting.

3. **xdc03 (65.21.27.213) validator dry-run** — 267G free, AT_TIP nodes already running, go-ethereum in workspace. First mainnet validator dry-run candidate. Requires A.97 binary deploy (same pattern as xdc02 it.11/it.15).

4. **xdc167 /mnt/data cleanup** — remove dead /work/xdcchain (user must authorize wipe of that datadir). Unblocks xdc167 as a fleet member.

---

## 4. VALIDATOR-MINT EXECUTION PLAN

### Phase 0 — Pre-conditions (today, ~30 minutes)

**A. Confirm A.97 node identity (xdc02, no restart needed)**

```bash
ssh -p 12141 root@135.181.117.109

# Read current coinbase (this becomes CANDIDATE_ADDR)
curl -s -X POST http://127.0.0.1:8652 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_coinbase","params":[],"id":1}'

# Read enode for peering
/root/workspace/go-ethereum-a97/build/bin/geth attach \
  /root/.xdc-a97-apothem/geth.ipc \
  --exec 'admin.nodeInfo.enode'

# Confirm still AT_TIP and gap <= 64
curl -s -X POST http://127.0.0.1:8652 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":1}'
```

**B. Verify contract values (read-only, no gas)**

```bash
# minCandidateCap: confirmed 10,000,000 TXDC (10000000000000000000000000 wei)
curl -s -X POST http://127.0.0.1:8652 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"0x0000000000000000000000000000000000000088","data":"0xd55b7dff"},"latest"],"id":1}'

# Confirm getCandidates count (46 non-zero candidates currently)
curl -s -X POST http://127.0.0.1:8652 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"0x0000000000000000000000000000000000000088","data":"0x06a49fce"},"latest"],"id":1}'
```

**USER DECISION REQUIRED — items that cannot be automated:**

| # | Decision | Value | Where to get it |
|---|---|---|---|
| D1 | Owner EOA address and private key | — | User's wallet |
| D2 | Candidate address (= node coinbase = `--miner.etherbase`) | Read from `eth_coinbase` on :8652 OR generate new: `geth account new --datadir /root/.xdc-a97-apothem` | Existing or new |
| D3 | 10,000,000 TXDC funding of owner EOA | Faucet: https://faucet.apothem.network (public cap ~1000 TXDC/day). For 10M: request manual transfer via xdc05 Faucet2.0 admin or team funded account. **This is the most likely delay item.** | User action |
| D4 | KYC status of owner EOA | Run `uploadKYC()` — if it reverts with "KYC not whitelisted", request team whitelist. On testnet KYC may be auto-approved. Confirm by dry-run. | User + team |
| D5 | Mint on existing :8652 node or clone? | Recommendation: clone to preserve acceptance evidence. User may choose to convert :8652 directly — see Phase 4. | User |

---

### Phase 1 — Fast-sync to validator-ready (already complete on xdc02)

Status: DONE. Apothem :8652 at xdc02 is 8/8 VALIDATOR-READY. No action for apothem.

For mainnet (xdc02 :8654): in-flight. Wait for AT_TIP + 8/8 acceptance check before mainnet validator dry-run.

For a fresh host (xdc03 65.21.27.213), the complete launch command:

```bash
ssh -p 12141 root@65.21.27.213

# Build A.97 on xdc03
cd /root/workspace/go-ethereum
git remote | grep a97origin || git remote add a97origin https://github.com/XDCIndia/go-ethereum.git
git fetch a97origin deploy/a97v2-mainnet-dynamic
git checkout a97origin/deploy/a97v2-mainnet-dynamic -b deploy-a97v2
make geth

# Launch apothem side-by-side
TS=$(date +%s)
mkdir -p /root/.xdc-a97-apothem/logs
systemctl reset-failed geth-a97-apothem.service 2>/dev/null || true
systemd-run \
  --unit=geth-a97-apothem \
  --setenv=XDC_SNAP_HEAL=1 \
  /root/workspace/go-ethereum/build/bin/geth \
    --datadir /root/.xdc-a97-apothem \
    --apothem \
    --port 30415 \
    --syncmode fast \
    --no-auto-pivot \
    --gcmode full \
    --state.scheme hash \
    --snapshot=false \
    --http \
    --http.addr 127.0.0.1 \
    --http.port 8656 \
    --http.api eth,net,web3,debug,admin,XDPoS,xdc \
    --authrpc.port 8756 \
    --authrpc.addr 127.0.0.1 \
    --cache 8192 \
    --maxpeers 100 \
    --verbosity 3 \
    --log.file /root/.xdc-a97-apothem/logs/geth-a97.log
```

8-condition validator-ready probe (substitute PORT=8656 or 8652):

```bash
PORT=8652
LOG=$(ls -t /root/.xdc-a97-apothem/logs/*.log | head -1)

# 1. gap <= 64
BLOCK=$(curl -s -X POST http://127.0.0.1:$PORT -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  | python3 -c "import sys,json; print(int(json.load(sys.stdin)['result'],16))")
CANON=$(curl -s https://erpc.apothem.network -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  | python3 -c "import sys,json; print(int(json.load(sys.stdin)['result'],16))")
echo "gap: $((CANON - BLOCK))"

# 2-3. A.94 events = 0, BAD BLOCK = 0
grep -c 'A.94 auto-recover' "$LOG" || echo 0
grep -cE '########## BAD BLOCK|invalid merkle root' "$LOG" || echo 0

# 4. eth_syncing = false
curl -s -X POST http://127.0.0.1:$PORT -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":1}'

# 5. balance(0x88) evolving (not pre-alloc 0x18d0bf423c03d8de000000)
curl -s -X POST http://127.0.0.1:$PORT -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x0000000000000000000000000000000000000088","latest"],"id":1}'

# 6. xdc_getStateHealStatus.enabled = false
curl -s -X POST http://127.0.0.1:$PORT -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"xdc_getStateHealStatus","params":[],"id":1}'
```

---

### Phase 2 — Propose flow (user executes after D1-D4 resolved)

**Epoch context:** Apothem epoch = 900 blocks (~2s/block = ~30 min/epoch). Current ~epoch 92135. Propose tx in epoch N → eligible from epoch N+1 boundary. Worst-case wait: 2 epochs (~60 minutes) after propose() lands.

**Step 2.1 — uploadKYC (owner EOA → 0x88)**

```bash
RPC="https://erpc.apothem.network"
OWNER_KEY="<your-owner-private-key-no-0x-prefix>"

# Using cast (foundry) — install: curl -L https://foundry.paradigm.xyz | bash && foundryup
cast send \
  --rpc-url $RPC \
  --private-key $OWNER_KEY \
  0x0000000000000000000000000000000000000088 \
  "uploadKYC(string)" \
  "apothem-validator-2026"

# Verify
cast call --rpc-url $RPC \
  0x0000000000000000000000000000000000000088 \
  "getLatestKYC(address)(string)" \
  "<your-owner-address>"
```

If this reverts with "KYC not whitelisted": request team whitelist for owner EOA address on the MasternodeVotingSMC contract.

**Step 2.2 — propose (payable, exactly 10,000,000 TXDC = 10000000000000000000000000 wei)**

```bash
CANDIDATE_ADDR="<node-coinbase-from-eth_coinbase>"

cast send \
  --rpc-url $RPC \
  --private-key $OWNER_KEY \
  --value 10000000000000000000000000 \
  0x0000000000000000000000000000000000000088 \
  "propose(address)" \
  "$CANDIDATE_ADDR"
```

**Step 2.3 — Confirm**

```bash
# Must return: true
cast call --rpc-url $RPC \
  0x0000000000000000000000000000000000000088 \
  "isCandidate(address)(bool)" \
  "$CANDIDATE_ADDR"

# Must equal 10000000000000000000000000
cast call --rpc-url $RPC \
  0x0000000000000000000000000000000000000088 \
  "getCandidateCap(address)(uint256)" \
  "$CANDIDATE_ADDR"
```

---

### Phase 3 — Code gaps (M1-M5) — engineering sprint

These are independent of the OPS path above but MUST land before --mine works.

**Recommended sequencing:**

```
Sprint A (1-2 days, one engineer):
  A1. Port XDPoSChain miner/worker.go update()+commitNewWork() loop into this
      codebase as miner/xdpos_worker.go (or miner/worker.go extension).
      Wire NewRoundCh+MinePeriodCh: allocate in engine_v2.New() call at
      eth/backend.go:315, pass to miner.New() alongside the engine.
      Canonical reference: XDPoSChain/miner/worker.go:279-337.

  A2. Wire engine.Authorize() in eth/backend.go at node start:
      After miner.New(), if miner.Etherbase() is non-zero and keystore has
      matching account, call xdposEngine.Authorize(coinbase, signFn).
      signFn = func(accounts.Account, string, []byte) ([]byte, error) wrapping
      node.AccountManager().Find(account).SignHash(...).

  A3. Wire HandleProposedBlock post-InsertChain in the mining loop:
      After blockchain.InsertChain([sealedBlock]) returns nil error,
      call xdposEngine.HandleProposedBlock(chain, sealedBlock.Header()).

  A4. Re-activate --mine flag (or add --xdc-mine):
      In cmd/utils/flags.go remove the deprecation guard; or add a new
      XdcMineFlag that starts the A1 goroutine.

Sprint B (same sprint or next):
  B1. Integration test: private devnet with 3 masternodes (2 existing geth
      nodes + 1 new erigon/this-geth), verify blocks accepted cross-client.
```

**Effort estimate:** Sprint A = ~500 LOC production code + ~200 LOC tests. Sprint B = devnet harness (~150 LOC scripts).

---

### Phase 4 — Switch validator node to minting mode

**Option A (recommended): Clone datadir, new instance**

Preserves :8652 as unmodified acceptance-evidence node. Stage 6 log evidence remains clean.

```bash
ssh -p 12141 root@135.181.117.109

# Save acceptance evidence first
cp /root/.xdc-a97-apothem/logs/geth-a97.log \
   /root/.xdc-a97-apothem/logs/geth-a97.log.stage6-evidence

# Clone chaindata (hardlink to save ~32G disk copy time)
mkdir -p /root/.xdc-a97-apothem-validator/logs
cp -al /root/.xdc-a97-apothem/geth /root/.xdc-a97-apothem-validator/geth

# Write password file (plaintext, 0600)
echo "YOUR_KEYSTORE_PASSWORD" > /root/.xdc-validator-password.txt
chmod 600 /root/.xdc-validator-password.txt

# Launch validator (--syncmode full: switch from fast after pivot committed)
TS=$(date +%s)
systemctl reset-failed geth-a97-apothem-validator.service 2>/dev/null || true
systemd-run \
  --unit=geth-a97-apothem-validator \
  --setenv=XDC_SNAP_HEAL=1 \
  /root/workspace/go-ethereum-a97/build/bin/geth \
    --datadir /root/.xdc-a97-apothem-validator \
    --apothem \
    --port 30416 \
    --syncmode full \
    --gcmode full \
    --state.scheme hash \
    --snapshot=false \
    --mine \
    --miner.etherbase "$CANDIDATE_ADDR" \
    --unlock "$CANDIDATE_ADDR" \
    --password /root/.xdc-validator-password.txt \
    --gasprice 0 \
    --http \
    --http.addr 127.0.0.1 \
    --http.port 8658 \
    --http.api eth,net,web3,debug,admin,XDPoS,xdc,miner \
    --authrpc.port 8758 \
    --authrpc.addr 127.0.0.1 \
    --cache 8192 \
    --maxpeers 100 \
    --verbosity 3 \
    --ethstats "xdc02-apothem-validator-${TS}:xdc_openscan_stats_2026@stats.xdcindia.com:443" \
    --log.file /root/.xdc-a97-apothem-validator/logs/geth-validator.log
```

**NOTE: --mine currently has no XDPoS-aware production loop behind it (M1-M5 code gaps). The node will start but will not produce blocks until Sprint A lands. The propose() + epoch-wait can run in parallel with Sprint A. Both paths converge at Phase 5.**

**Option B: Convert :8652 directly** — restart with the --mine flags above, same datadir, same port. Saves disk. Log evidence gets commingled. Choose this only if disk is tight.

---

### Phase 5 — Verify minting

**Pre-minting checks (immediately after validator node starts)**

```bash
# Mining must be enabled
curl -s http://127.0.0.1:8658 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_mining","params":[],"id":1}'
# Expected: true

# Coinbase matches CANDIDATE_ADDR
curl -s http://127.0.0.1:8658 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_coinbase","params":[],"id":1}'

# Still AT_TIP
curl -s http://127.0.0.1:8658 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":1}'
# Expected: false
```

**Log markers to watch (after epoch boundary, in order)**

```bash
LOG=/root/.xdc-a97-apothem-validator/logs/geth-validator.log
tail -f "$LOG" | grep -E '\[yourturn\]|Successfully sealed|Successfully created QC|Imported new chain'
```

1. `[yourturn] Yes, it is my turn` — confirms node is in active validator set for this round. Source: `consensus/XDPoS/engines/engine_v2/mining.go:77`. If this never appears: candidate not in active set (propose() not yet reflected, or epoch boundary not crossed).

2. `Imported new chain segment  blocks=1  ...  number=<N>` with `miner` field == CANDIDATE_ADDR — block sealed and accepted locally.

3. `Successfully created QC  Round=...  BlockHash=...` — BFT quorum reached, block finalized. Source: `consensus/XDPoS/engines/engine_v2/vote.go:281`.

**Check block miner field**

```bash
CANDIDATE_LOWER=$(echo "$CANDIDATE_ADDR" | tr '[:upper:]' '[:lower:]')
curl -s -X POST http://127.0.0.1:8658 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["latest",false],"id":1}' \
  | python3 -c "import sys,json; b=json.load(sys.stdin)['result']; print('miner:', b['miner'], 'number:', int(b['number'],16))"
```

**Explorer verification:** https://explorer.apothem.network — search CANDIDATE_ADDR for "Mined" blocks.

---

### Phase 6 — Mainnet validator (after apothem minting confirmed)

Do NOT attempt mainnet until apothem minting is verified end-to-end.

The mainnet dynamic run (:8654, deploy/a97v2-mainnet-dynamic @ 51fc026f9) must reach 8/8 validator-ready first. Current state: state-DL in progress (~15.7M entries at t+22min of it.28). Expected: AT_TIP in 12-24 hours from it.28.

Mainnet equivalent values:
- `MasternodeVotingSMC`: `0x0000000000000000000000000000000000000088` (same address)
- `minCandidateCap`: verify via `eth_call` on mainnet node (expected 10,000,000 XDC, 18-decimal)
- RPC for mainnet ops: use local :8654 once AT_TIP, or public `https://erpc.xinfin.network`
- Epoch = 900 blocks on mainnet (~2s/block = ~30 min/epoch)
- Resign after test: `resign(address)` + 5-epoch withdrawal delay

---

### Rollback procedures

| Scenario | Rollback |
|---|---|
| Validator node crashes before first block | `systemctl stop geth-a97-apothem-validator`; restart without `--mine`; investigate logs |
| BAD BLOCK after mining starts | Stop immediately; grep logs for `BAD BLOCK`/`invalid merkle root`; do NOT restart with --mine until root-caused (P0 per loop failure rules) |
| propose() tx reverts | Likely KYC or insufficient balance; do not retry; fix the root cause first |
| State corruption after syncmode fast→full transition | Restore from hardlinked clone or re-sync from canary snapshot |

---

## 5. IMMEDIATE NEXT ACTIONS (next 60 minutes)

Priority order for the orchestrator loop:

| # | Action | Owner | Time | Dependency |
|---|---|---|---|---|
| N1 | **Peer xdc02 :8654 (mainnet dynamic) with xdc01 snapfull2 node** via IPC `admin.addPeer`. Get xdc01 snapfull2 enode first: `ssh -p 12141 root@95.217.112.125; /root/workspace/go-ethereum/build/bin/geth attach nodes-local-mainnet/snapfull2/geth.ipc --exec 'admin.nodeInfo.enode'`; then add on xdc02 via `:8654`'s IPC. | Orchestrator (Sonnet) | 10 min | None |
| N2 | **Monitor mainnet :8654 state-DL** — check entry count, A94/BAD counters, peer count. Report if `failed with all peers` re-appears (trigger N1 immediately if so). | Orchestrator | 5 min | None |
| N3 | **Surface D1-D4 to user** — blocking items: EOA funding (10M TXDC), KYC status, candidate address choice. These have zero automation path; user must act. | Return this plan to user | 0 min | User reads this doc |
| N4 | **Dispatch Sprint A engineer task** — open code PR for M1-M5 block-production loop. File: `miner/xdpos_worker.go` (new). Canonical reference: `XDPoSChain/miner/worker.go:279-337`. Engineer must also handle M2 (channel alloc at `eth/backend.go:315`), M3 (Authorize wiring), M4 (HandleProposedBlock post-InsertChain), M5 (--mine re-activation). | Engineering | 1-2 days | Independent of OPS path |
| N5 | **Once D1-D4 resolved: execute Phase 2 (uploadKYC + propose)** on local :8652 node. Use cast or web3.py. Check receipt for Propose event. | User + Orchestrator | 30 min | D1-D4 |
| N6 | **xdc03 (65.21.27.213) A.97 deploy** — 267G free, AT_TIP nodes, clean candidate. Push deploy/a97v2-mainnet-dynamic to xdc03, native make geth, launch side-by-side apothem + mainnet instances. Expands fleet coverage and provides independent acceptance evidence. | Orchestrator (Sonnet) | 30 min | None |
| N7 | **xdc167 /mnt/data remediation** — request user auth to remove /work/xdcchain (dead node, releases volume). Then restart mainnet fast-sync. | User auth required | After user auth | User |

**Single most impactful immediate action:** N1 (peer :8654 to xdc01 path-scheme archive). The mainnet dynamic run is the next acceptance gate; ensuring it has a reliable state-serving peer accelerates it past the pruned-epoch wall risk.

**Single most impactful user action:** D3 (fund owner EOA with 10M TXDC from team faucet). Everything in Phase 2 is blocked on this.

---

## Summary

A.97 is the correct and complete path to real fast-sync aligned with pre-removal geth semantics. The five code gaps (M1-M5) blocking minting are all in the miner layer, not in the sync layer — they are a separate engineering sprint from the fast-sync work. The fast-sync work is proven complete on apothem (8/8 stage-6) and in-flight on mainnet. The validator election is an OPS/user path that runs in parallel. The critical path to first minted block is: Sprint A (M1-M5 code) + D3 (10M TXDC fund) + D4 (KYC) + propose() + 1-2 epoch wait — approximately 2-3 days from today assuming engineering starts immediately and funding is arranged today.
