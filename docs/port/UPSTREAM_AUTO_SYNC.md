# Automated Upstream Release Tracking

Keeps each XDC client fork within one release of its upstream, automatically, with
a human + parity gate before anything ships.

## Pipeline

```
every 15 min ─▶ upstream-tracker.yml ─▶ scripts/upstream-sync.sh
                     │  detect newest upstream stable release tag
                     │  already in stable branch?  ─▶ stop
                     │  throwaway trial-merge to CLASSIFY clean/conflicts + build
                     │      a file+line manifest (aborted, never pushed)
                     │  publish the upstream TAG itself as upstream-sync/<tag>
                     ▼
              open PR ──▶ target = stable branch, assignee/reviewer = @AnilChinchawale
                     │   GitHub computes the merge, so CONFLICTS SHOW NATIVELY
                     │   ("conflicts must be resolved" banner + file list).
                     │   conflicts ─▶ Draft + labels conflicts/needs-manual-merge
                     │   clean ─────▶ mergeable directly
                     │
   @AnilChinchawale resolves conflicts LOCALLY (never the web editor — consensus
   is sacred), runs the parity harness, merges (brings full upstream history in)
                     ▼
        upstream-alpha-validate.yml (on merge)
                     │  build ─▶ XDPoS parity/conformance gate (SACRED)
                     │  alpha node smoke on a throwaway devnet
                     │  Hive e2e (self-hosted + clients/xdc)
                     ▼
        all green ─▶ tag release  |  any step fails ─▶ open issue @AnilChinchawale
```

**Why publish the tag instead of a pre-merged branch?** A pre-merged branch is
`stable + one merge commit`, so GitHub reports it *mergeable* even when the merge
committed `<<<<<<<` markers — the green merge box hides the work and is a footgun.
Publishing the tag makes GitHub do the merge, so its real conflict banner appears.
The reviewer resolves locally with the full toolchain + parity harness, which is
exactly where consensus code must be resolved.

**Requires a PAT.** The tag branch carries upstream's `.github/workflows` changes,
and org policy blocks the Actions `GITHUB_TOKEN` from creating PRs/issues. Add a
`UPSTREAM_SYNC_TOKEN` secret (PAT with `repo` + `workflow` scope); the workflow
falls back to `GITHUB_TOKEN` but push + PR/issue creation will fail without it.

## Files
- `scripts/upstream-sync.sh` — client-agnostic detect + merge-branch engine (env-driven).
- `.github/workflows/upstream-tracker.yml` — 15-min watcher → auto-merge → PR → issue-on-failure.
- `.github/workflows/upstream-alpha-validate.yml` — post-merge build → parity gate → alpha node → Hive → issue-on-failure.

## Per-client configuration (drop the same 3 files, change only these env vars)

| Client repo | `UPSTREAM_URL` | `STABLE_BRANCH` | `TAG_REGEX` |
|---|---|---|---|
| **XDCIndia/go-ethereum** | `ethereum/go-ethereum` | `main` (seeded from reset/integration-1.17.3) | `^v[0-9]+\.[0-9]+\.[0-9]+$` |
| XDCIndia/erigon (erigon-xdc) | `erigontech/erigon` | *(fork stable)* | `^v[0-9]+\.[0-9]+\.[0-9]+$` |
| XDCIndia/reth | `paradigmxyz/reth` | *(fork stable)* | `^v[0-9]+\.[0-9]+\.[0-9]+$` |
| XDCIndia/besu | `hyperledger/besu` | `xdc/besu-phase2-v1-validation` | `^[0-9]+\.[0-9]+\.[0-9]+$` |
| XDCIndia/nethermind | `NethermindEth/nethermind` | *(fork stable)* | `^[0-9]+\.[0-9]+\.[0-9]+$` |

The build/parity/hive steps in `upstream-alpha-validate.yml` are per-language
(Go / Rust / Java / .NET) — swap the build + test commands accordingly; the sync,
PR, assignment, and issue-on-failure logic is identical everywhere.

## Testing before rollout
1. **Script, locally:** `DRY_RUN=1 STABLE_BRANCH=main bash scripts/upstream-sync.sh`
   — must print `latest_tag=v1.17.4` and `status=would-sync` without creating a branch.
2. **Workflow, manually:** Actions ▸ *Upstream Release Tracker* ▸ *Run workflow* with `dry_run=true`.
   Then run once with `dry_run=false` and confirm the PR opens, is assigned to the reviewer, and
   is labelled `upstream-sync` (+ `conflicts` when applicable).
3. **Validation, manually:** Actions ▸ *Alpha Deploy + Hive E2E* ▸ *Run workflow* on the stable branch.

## Important: scheduling caveat
GitHub fires `schedule` (cron) triggers **only from the repository's DEFAULT branch.**
Today the default is `xdc-network`. To make the 15-min watcher run, either
(a) also merge `upstream-tracker.yml` onto `xdc-network`, or (b) set
`main` as the default branch. `workflow_dispatch` works from any
branch, so manual runs work immediately.

## Guarantees / guardrails
- **Consensus is sacred:** no upstream merge reaches a release until the XDPoS parity/conformance
  gate is green (header hash, state root, receipts root, QC verification bit-exact on net5050 + apothem).
- **Human in the loop:** every PR is assigned to @AnilChinchawale; conflicts are never auto-guessed
  for consensus code — they are committed with markers and flagged `needs-manual-merge`.
- **Self-healing visibility:** any failed step opens an issue tagging the reviewer; alpha/hive
  failures are `release-blocker`.
