# Integrating XDC Network onto any Ethereum client

**The single entry point for porting an Ethereum-family client (reth, erigon, besu,
nethermind, or another go-ethereum fork) into a consensus-faithful XDC Network node —
and keeping it there.**

A complete integration has **two pillars**:

| Pillar | Question it answers | Where it lives |
|---|---|---|
| **1. Port it** | *"How do I make client X produce byte-identical XDC blocks?"* | [`XDC_CROSS_CLIENT_CONFORMANCE_SPEC.md`](../XDC_CROSS_CLIENT_CONFORMANCE_SPEC.md) (16 parts) |
| **2. Keep it in sync** | *"How do I stay within one release of upstream without breaking consensus?"* | [`UPSTREAM_AUTO_SYNC.md`](./UPSTREAM_AUTO_SYNC.md) + the automation files |

This guide ties the two together, lists the reference implementations, and — at the
end — gives you a **copy-paste agent prompt + workflow** whose definition-of-done is
*"conformance gates green **and** upstream auto-sync live."* Use it to drive a fresh
client port to completion, hands-off.

> **Consensus is sacred.** Every rule below exists so that for XDC block `N` and its
> pre-state, your client produces an **identical** post-state root, receipts root, and
> block hash. A single differing byte forks you off the network. Treat any parity
> mismatch as P0.

---

## 0. What you are building

XDC Network is an EVM chain with a custom **XDPoS** consensus engine (Delegated Proof
of Stake with masternodes). Two consensus regimes exist on the same chain, switched by
fork block:

- **XDPoS v1** — round-robin Proof-of-Authority. Proposer = `signers[blockNumber % len(signers)]`; the header carries an M1/M2 double-validation field.
- **XDPoS v2** — HotStuff BFT. A proposer seals a block, ≥⅔+1 masternodes vote, votes aggregate into a **Quorum Certificate (QC)**; a failed round produces a **Timeout Certificate (TC)**. Rounds, not block numbers, drive epochs.

Networks you may target:

| Network | chainId | Notes |
|---|---|---|
| XDC mainnet | 50 (`0x32`) | production; v1→v2 already switched |
| Apothem testnet | 51 (`0x33`) | public testnet; v2.8 fork line |
| net5050 | 5050 (`0x13ba`) | mainnet-parity multi-client stress net (legacy + geth + erigon + reth + nethermind + besu). **Full genesis / bootnodes / hardforks: [issue #1019](https://github.com/XDCIndia/go-ethereum/issues/1019)** |
| devnet | 5551 | v2.8 canary |

> **net5050 network reference** — genesis (hash `0x3f63fc…`), the deliberate no‑London‑at‑genesis hardfork set, V2 switch @ block 2700, bootnode + full validator enode inventory, and the join recipe are all in **[issue #1019](https://github.com/XDCIndia/go-ethereum/issues/1019)**. It's the concrete target for the G9 cross‑client mint‑acceptance proof below.

**The consensus surface you must implement** (each maps to a spec Part):

| Surface | Spec Part | Consensus-critical? |
|---|---|---|
| Chain parameters & fork blocks | [Part 2](../XDC_CROSS_CLIENT_CONFORMANCE_SPEC.md) | ✅ |
| Genesis & system contracts (`0x…88/89/90`) | Part 3 | ✅ |
| Block header layout + RLP (`Validators`/`Validator`/`Penalties`) | Part 4 | ✅ **bit-exact** |
| XDPoS v1 round-robin + M1/M2 | Part 5 | ✅ |
| XDPoS v2 QC/TC/rounds/epoch-switch | Part 6 | ✅ |
| Rewards (HookReward) & penalties (HookPenalty) | Part 7 | ✅ |
| Special transactions & gas/fee rules | Part 8 | ✅ |
| RPC parity (`XDPoS_*`, XDC header fields) | Part 9 | value-parity |
| P2P / BFT vote-timeout-syncinfo protocol | Part 10 | ✅ (for validators) |
| Validator-readiness obligations | Part 11 | ✅ (for minting) |

---

## 1. Reference implementations (read these, don't guess)

| Role | Repo / path | Use it to learn… |
|---|---|---|
| **Canonical XDC behaviour** | `XDCIndia/go-ethereum` (this repo) | the source of truth for every hash/root/reward |
| XDPoS consensus engine | `XinFin/XDPoSChain` | the original engine (v1 + v2) reference |
| Node operator patterns | `XinFin-Node` | genesis, deployment, masternode ops |
| **reth port** (Rust) | `XDCIndia/reth` → `xdc/v2.2.0-port` | validator/minting parity in a non-Go client |
| **erigon port** (Go) | `XDCIndia/erigon-xdc` → `xdc/v3.4.2-replay` | staged-sync + IntraBlockState adaptation |
| **besu port** (Java) | `XDCIndia/besu` → `main` | JVM engine + BFT vote wiring |
| **nethermind port** (.NET) | `XDCIndia/xdc-nethermind-private` → `main` | `Nethermind.Xdc/*` module layout |

**Worked example — the live upstream-sync PRs** (each shows the real conflict surface a
port must own, i.e. exactly where XDC diverges from upstream):

| Client | Sync PR | Conflict surface |
|---|---|---|
| go-ethereum v1.17.4 | `XDCIndia/go-ethereum#1017` | core/state, miner, eth/protocols |
| reth v2.3.0 | `XDCIndia/reth#211` | eth-wire capability negotiation vs XDPoS wiring |
| nethermind 1.38.1 | `XDCIndia/xdc-nethermind-private#227` | `Nethermind.Xdc/*` reward/QC/RLP/header-validator |
| erigon v3.5.0 | `XDCIndia/erigon-xdc#311` | consensus/xdpos + core execution |

---

## 2. Pillar 1 — Port it (phases & gates)

Follow the phased order — the goal ladder in **[Conformance Spec Part 14 — Autonomous
Execution Harness](../XDC_CROSS_CLIENT_CONFORMANCE_SPEC.md)** (§14.2 maps each milestone
to its gate and drives it to green on every network). Condensed:

```
M1 Chain recognition   → node accepts --chain=xdc/apothem, genesis hash matches
M2 Engine compiles     → XDPoS v1+v2 types + RLP round-trip
M3 Block-validation    → header hash, signer/proposer, seal, QC verify  (Gate B, D)
M4 State-transition    → post-state root + receipts root bit-exact       (Gate E)  ← hardest
M5 Full sync           → genesis → head over p2p, head hash matches explorer
M6 RPC parity          → XDPoS_* + XDC header fields shape-and-value identical (Gate G)
M7 Validator readiness → vote/timeout/syncinfo accepted; can mint & be accepted (Gate E/H)
```

**Definition of "ported" = all conformance gates pass.** Gates (Spec Part 12,
run against a synced archive node + a private net):

| Gate | Checks | Runnable via |
|---|---|---|
| A | genesis + system contracts | read-only RPC |
| B | header RLP / block-hash | read-only RPC |
| C | v1 round-robin + M1/M2 | replay |
| D | v2 QC/TC/proposer/epoch-switch penalties | replay |
| **E** | **state-transition parity** | private net re-exec |
| F | rewards / penalties parity | replay |
| G | RPC parity | live diff vs geth-XDC |
| H | validator can mint & be accepted | private mixed-client net |
| I | fork-boundary correctness (v1→v2, v2.8) | replay across boundary |
| J | long-running stability at tip | live |

> **Top pitfalls** (full list in Spec Part 13):
> - Select v2 epoch-switch blocks by **round (`IsEpochSwitch`)**, never `number % 900` — the modulo selector is v1-only and silently skips v2 blocks.
> - Don't let `gen_header_json.go`-style JSON round-tripping zero `Validators`/`Validator`/`Penalties` — re-populate from raw RPC before hashing.
> - Never force upstream defaults (gas ceiling, sync mode, fork activation) onto XDC-customised paths.
> - Penalty/masternode-set derivation must be **bit-exact** — the #204 hazard that wedges v2 epoch switches.

Build a **parity harness first** (Spec Part 12 / `tools/parity-harness`): feed it XDC
block RLP + pre-state, execute, diff post-state root / receipts root / block hash / gas.
CI must run it on every consensus-touching commit.

---

## 3. Pillar 2 — Keep it in sync (automation)

A port is worthless if it rots three upstream releases behind. The **upstream auto-sync**
automation keeps every fork within one release, with a human + parity gate before
anything ships. Full design: **[`UPSTREAM_AUTO_SYNC.md`](./UPSTREAM_AUTO_SYNC.md)**.

Three files, dropped into any client (only env changes per client):

| File | Role |
|---|---|
| `scripts/upstream-sync.sh` | client-agnostic: detect newest upstream release tag → publish it as `upstream-sync/<tag>` so **GitHub computes the merge and shows conflicts natively** |
| `.github/workflows/upstream-tracker.yml` | every 15 min → run script → push tag branch → open Draft PR + validation issue (both embed the upstream release notes + conflict manifest), assigned to the reviewer |
| `.github/workflows/upstream-alpha-validate.yml` | on merge → build → **XDPoS parity gate (SACRED)** → alpha node smoke → Hive e2e → issue on failure |

### Per-client configuration

| Client repo | `UPSTREAM_REPO` | `STABLE_BRANCH` (= default branch) | `TAG_REGEX` |
|---|---|---|---|
| `XDCIndia/go-ethereum` | `ethereum/go-ethereum` | `main` | `^v[0-9]+\.[0-9]+\.[0-9]+$` |
| `XDCIndia/erigon-xdc` | `erigontech/erigon` | `xdc/v3.4.2-replay` | `^v[0-9]+\.[0-9]+\.[0-9]+$` |
| `XDCIndia/reth` | `paradigmxyz/reth` | `xdc/v2.2.0-port` | `^v[0-9]+\.[0-9]+\.[0-9]+$` |
| `XDCIndia/besu` | `hyperledger/besu` | `main` | `^[0-9]+\.[0-9]+\.[0-9]+$` (no `v`) |
| `XDCIndia/xdc-nethermind-private` | `NethermindEth/nethermind` | `main` | `^[0-9]+\.[0-9]+\.[0-9]+$` (no `v`) |

### Non-negotiable setup facts (learned the hard way)

1. **`UPSTREAM_SYNC_TOKEN` secret is mandatory** — a PAT with `repo` + `workflow` scope.
   The Actions `GITHUB_TOKEN` is blocked by org policy from creating PRs/issues, and
   pushing the upstream tag branch (which carries upstream's `.github/workflows`) needs
   the `workflow` scope. One **org-level** secret (visibility: all repos) covers the fleet.
2. **The workflow must live on the repo's DEFAULT branch** — GitHub fires `schedule`
   crons only from the default branch. If `main` is a bare upstream mirror (reth, erigon),
   make the **XDC production line** the default branch and set `STABLE_BRANCH` to it.
3. **Cron runs off-peak** (`7,22,37,52 * * * *`) — GitHub drops `:00/:15/:30/:45`
   schedules under load.
4. **Native-conflict model** — the sync branch head *is* the upstream tag, so GitHub's
   real "conflicts must be resolved" banner appears. Conflicts are resolved **locally**
   with the parity harness (never the web editor — consensus is sacred), never
   pre-committed as markers.
5. **GitHub Actions billing** — if the org's Actions is billing-blocked, jobs fail at
   startup ("recent account payments have failed…"). Fix in org **Settings → Billing &
   plans → Actions**. While blocked, the workflow's exact logic can be run manually:
   `git push origin refs/tags/<tag>:refs/heads/upstream-sync/<tag>` then `gh pr create`.

---

## 4. The completion prompt + workflow (drive a port to done, in sync)

Paste the prompt below to an autonomous coding agent (Claude Code or equivalent) to
port a **new** client — or complete an in-progress one — to the definition of done:
**all conformance gates green AND upstream auto-sync live.** It couples *port it* with
*keep it in sync* so the client can't be declared done while drifting from upstream.

````markdown
# XDC CLIENT INTEGRATION — autonomous completion prompt

You are porting `<CLIENT>` (repo `<XDCIndia/REPO>`, upstream `<UPSTREAM owner/repo>`,
language `<Go|Rust|Java|.NET>`) into a consensus-faithful XDC Network node that is
**validator-ready on every XDC network — mainnet(50), apothem(51), net5050(5050),
devnet(5551)**. You execute autonomously and never stop until BOTH pillars are complete
on ALL FOUR networks.

## Model routing (Fable + Opus plan · Sonnet executes)
- **Fable 5** (`claude-fable-5`) — orchestrator: decompose the goal ladder, sequence
  goals × the four networks, route each task, arm the `/loop`, decide what fans out.
- **Opus 4.8** (`claude-opus-4-8`) — architect: OWNS every block-hash / state-root /
  receipts-root / reward / penalty / masternode-set decision, state-model adaptation,
  first parity-mismatch root-cause, and the REFUTE verdict on any "gate passed" claim.
- **Sonnet** (`claude-sonnet-4-6`; latest Sonnet — e.g. **Sonnet 5** — when available) —
  executor: writes the code, runs the gates, applies the minimal fix, rebuilds, re-runs.
- **Never invert:** Sonnet escalates any consensus-affecting decision to Opus; Fable
  never hands consensus root-cause to Sonnet without an Opus plan. (Haiku: grep/lookup only.)

## References (read, never guess)
- Port rules + the autonomous harness: `docs/XDC_CROSS_CLIENT_CONFORMANCE_SPEC.md`
  (Parts 1–14; **Part 14 = the model-routed loop+workflow harness + network matrix**).
  Canonical behaviour: `XDCIndia/go-ethereum` + `XinFin/XDPoSChain`. Sync automation:
  `docs/port/UPSTREAM_AUTO_SYNC.md`.

## Hard constraints (P0 — consensus is sacred)
- For XDC block N + pre-state, your client MUST produce identical post-state root,
  receipts root, and block hash. Any mismatch stops everything until Opus root-causes it.
- Never "improve" XDC consensus, reward, penalty, masternode-selection, epoch, or RLP
  rules. Never force upstream defaults onto XDC paths. Document every divergence as an ADR.

## Pillar 1 — Port to green gates, on EVERY network (Fable sequences; Sonnet runs; Opus verifies)
1. Sonnet builds a parity harness FIRST (Spec Part 12): input = XDC block RLP + pre-state;
   output = PASS/FAIL with exact root/hash/gas diffs. Wire it into CI on every
   consensus-touching commit.
2. Drive the goal ladder G1→G10 (Spec Part 14 §14.2) to green **on each network**:
   run the harness over — for each of mainnet(50), apothem(51), net5050(5050),
   devnet(5551) — normal + epoch-checkpoint + special-tx + 1559 + cancun blocks, each
   fork boundary (v1→v2, v2.8), ≥3 v2 epoch switches, ≥1 round-timeout (TC). Fan the
   four networks out in parallel where independent.
3. Select v2 epoch-switch blocks by round (`IsEpochSwitch`), NOT `number % 900`.
   Keep `Validators`/`Validator`/`Penalties` populated through any JSON round-trip.
   Penalty-set derivation must be bit-exact (the #204 hazard) on all four nets.
4. G9 (mint+vote) proof per network: mainnet/apothem = validator dry-run/testnet
   masternode; **net5050 = your minted blocks accepted by the legacy+geth+erigon+reth+
   nethermind+besu producers** (cross-client QC ≥ threshold); devnet = v2.8 mint.
5. DONE-1 = conformance gates A–J PASS on **all four networks** (Opus-refuted, not just
   self-reported). Log each net's evidence in `docs/port/CONSENSUS_PARITY.md`.

## Pillar 2 — Wire upstream auto-sync (so the port can't rot) — Sonnet drops files; Fable verifies dispatch
6. Copy `scripts/upstream-sync.sh` VERBATIM from `XDCIndia/go-ethereum` (client-agnostic).
7. Copy `.github/workflows/upstream-tracker.yml` + `upstream-alpha-validate.yml`; change
   ONLY: workflow `name`, `concurrency.group`, `UPSTREAM_URL`, `UPSTREAM_REPO`,
   `STABLE_BRANCH` (= your XDC production line), `TAG_REGEX` (no `v` for CalVer clients),
   and the per-language build/test commands in the reviewer gate + alpha-validate.
8. Ensure the workflow lives on the repo's DEFAULT branch; if `main` is a bare mirror,
   set the XDC line as default. Cron = `7,22,37,52 * * * *`.
9. Have the org add the `UPSTREAM_SYNC_TOKEN` PAT secret (repo+workflow scope).
10. DONE-2 = a `workflow_dispatch` run detects the correct newest upstream tag and either
   opens a native-conflict Draft PR (assigned to reviewer, labels
   upstream-sync/conflicts/needs-manual-merge, body embeds release notes + manifest) or
   reports `uptodate`/`exists` idempotently. The alpha-validate workflow's XDPoS parity
   gate must guard every future merge.

## Standards
- Commit prefix per area (`consensus/xdpos:`, `chainspec:`, `ci(upstream-sync):`).
  Commit body = WHY + block range verified + upstream PR ref. ADR per divergence.
- Author every commit as the maintainer identity; never add a `Co-Authored-By` trailer.
- Keep sync PRs as Drafts until conflicts are resolved locally + the parity gate is green.

## Definition of done (mission complete only when ALL hold)
- ✓ Conformance gates A–J green **on all four networks** — mainnet(50), apothem(51),
  net5050(5050), devnet(5551) — Opus-refuted, not just self-reported (Pillar 1)
- ✓ net5050 cross-client mint accepted by the other producers (the validator-ready proof)
- ✓ Upstream auto-sync live + validated on the default branch (Pillar 2)
- ✓ Parity harness in CI; `CONSENSUS_PARITY.md` shows PARITY over a full sync per network
- ✓ Operator docs let an external user run a syncing node on each network
Never stop until every box is checked on every network. Sonnet fixes compile/test
failures and continues; escalate any consensus-affecting call to Opus; Fable re-sequences.
````

**The workflow half** (what makes "in sync" self-sustaining) is the same three files in
§3 — the prompt wires them, and thereafter the fork tracks upstream every 15 minutes
with the parity gate guarding every release. The prompt's *DONE-2* is exactly the
tracker's `workflow_dispatch` producing a correct native-conflict PR or an idempotent
`uptodate`/`exists`.

---

## 5. Quick-start checklist

```
[ ] Wire model routing: Fable plans/sequences · Opus owns consensus calls + REFUTE · Sonnet executes
[ ] Read Conformance Spec Parts 1–8 (the consensus surface) + Part 13 (pitfalls) + Part 14 (harness)
[ ] Stand up the parity harness (Spec Part 12) and wire it into CI
[ ] Drive the goal ladder G1→G10 (Spec §14.2) green ON EACH NETWORK:
      [ ] mainnet(50)   [ ] apothem(51)   [ ] net5050(5050)   [ ] devnet(5551)
[ ] G9 mint proof per net (net5050 = accepted by legacy+geth+erigon+reth+nethermind+besu) → Pillar 1 done
[ ] Drop the 3 sync files; set env + default branch + TAG_REGEX (§3 table)
[ ] Add the org UPSTREAM_SYNC_TOKEN PAT (repo+workflow)
[ ] workflow_dispatch the tracker → confirm a native-conflict PR or uptodate → Pillar 2 done
[ ] Ship operator docs per network; tag the release
```

When both pillars are green **on all four networks**, the client is a first-class XDC
validator **and** stays within one upstream release automatically — with consensus
correctness Opus-gated at every step.
