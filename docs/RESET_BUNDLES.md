# Baseline reset bundle catalog (refs #740)

Working document for grouping the 677-commit XDC overlay
(`54a91b3a..xdc-network`) into merge-commit bundles for replay onto
`upstream/v1.17.3` (`117e067f0`).

> **Status:** Opus planner advice applied 2026-05-23 — see `docs/PORT_LOG.md`.
> Bundle merge order was inverted; F-bucket bundles run before V2 engine.

## Merge order (after planner advice)

**B0 → B1 → B3 → B4 → B5 → B7 → B2 → B6 → B8 → B9 → B10 → B11**

V2 engine (B2) was moved from #2 to #7 so it lands on top of validated header
plumbing + P1 + bypass cleanup. Otherwise V2 consensus tests would fail for
plumbing reasons and obscure whether V2 itself was ported correctly.

## Bundle execution mechanic

Per bundle (per planner advice — NOT linear rebase across 677 commits):
```bash
git checkout -b bundle/Bn reset/baseline-1.17.3
git cherry-pick <bundle commits>       # resolve conflicts per F-bucket policy
go build ./... && go vet ./... && go test ./<touched>/...
# (F-bucket bundles only):
scripts/local-sync-test.sh wipe-stateful && scripts/local-sync-test.sh start all
# Grep set must return zero hits:
#   BAD BLOCK | invalid merkle root | XDC TIPSigning state-root bypass |
#   XDC: Head state found via | XDC: Setting head to block with valid cached state |
#   XDC: Found state via PBSS disk layer root | XDC P1: forced trie commit at checkpoint
git checkout reset/baseline-1.17.3
git merge --no-ff bundle/Bn -m "merge: <title> (refs #740) — absorbs XDC PRs #x, #y, #z"
git push origin reset/baseline-1.17.3
```

For B4, B5, B7 (F-bucket-collision): escalate to Opus before the merge into
`reset/baseline-1.17.3` with the diff + grep evidence attached.

## Overlay profile (2026-05-23)

- Total overlay commits: 677
- Touching `consensus/XDPoS/`: 190
- Touching `core/blockchain.go`: 75  ← F-bucket collision hot zone
- Touching `eth/downloader/`: 115     ← F-bucket collision hot zone
- Touching `scripts/`: 26
- Touching `docs/` only: 22
- Subjects mentioning `P1`/`checkpoint`: 115 (overlaps with downloader)
- Subjects mentioning `snap`/`sync-test`/`local-sync`: 70

## Proposed bundles

| ID | Title | Approx commits | Key paths | F-bucket collision |
|---|---|---|---|---|
| B0 | Chain config + genesis + validator/blocksigner contracts | ~10 | `params/`, `core/genesis*.go`, `contracts/` | low |
| B1 | XDPoS V1 engine | ~50 | `consensus/XDPoS/engines/engine_v1/` | low |
| B2 | XDPoS V2 engine + epoch / masternode wiring | ~90 | `consensus/XDPoS/engines/engine_v2/`, `consensus/XDPoS/utils/`, `XDCxlending/`, `XDCx/` | medium |
| B3 | TIPSigning + BlockSigners precompiles + signer state | ~25 | `core/vm/`, `accounts/abi/bind/`, `XDCxDAO/` | low |
| B4 | Header/finality plumbing + 'XDC:' marker family | ~50 | `core/blockchain.go`, `core/state_processor.go`, `core/types/`, `eth/handler.go` | **HIGH** |
| B5 | P1 bulk-sync + checkpoint trie commit | ~80 | `eth/downloader/`, `core/blockchain.go`, `core/state/`, `triedb/` | **HIGH** |
| B6 | Snap-sync XDC adaptations (path scheme, pivot stability) | ~50 | `eth/downloader/`, `core/rawdb/`, `triedb/pathdb/` | medium |
| B7 | Bypass cleanup #651 / #682 (state-divergence guards) | ~16 | `core/blockchain.go`, `core/state_processor.go` | **HIGH** |
| B8 | sync-test scripts + canonical parity validator (#689, #690, #732, #735, #736) | ~26 | `scripts/` | none |
| B9 | Recent fix PRs (#722, #724, #726, #728, #735, #736) and one-off bug fixes | ~30 | mixed | low–medium |
| B10 | Docs / port log / ADRs | ~22 | `docs/` | none |
| B11 | Anything left over (build, CI, devops) | balance | various | low |

Total: ~449 + balance ≈ 677.

## Bundling rules

1. Within a bundle, preserve original commit order (chronological).
2. Each bundle merges as a single merge commit; the merge commit's body lists the absorbed XDC PR numbers and the bundle's purpose. Original XDC SHAs remain reachable via `git log --all`.
3. F-bucket-collision bundles (B4, B5, B7) run the **full** `scripts/local-sync-test.sh wipe-stateful && start all` gate after their merge. Other bundles only need `go build ./... && go vet ./... && go test ./<touched-packages>/...`.
4. On any conflict where `XDC:` marker is present in either side: XDC behaviour wins. Document each such resolution as a line in `docs/RESET_CONFLICT_LOG.md` (file path + before/after + 1-line why).

## Open questions for Opus planner (Phase 2 entry)

1. Should B4 + B5 + B7 (the three F-bucket-collision bundles) merge before or after B2 (V2 engine)? V2 depends on header plumbing in B4, but B4's conflict surface is the post-merge upstream `core/blockchain.go` refactors.
2. Are B6 (snap-sync adaptations) and B5 (P1 bulk-sync) cleanly separable, or are they intertwined enough to merge as one bundle?
3. Conflict-resolution policy on upstream files that are pure refactors (no semantic change) where XDC has overlaid additions: take upstream skeleton + re-apply XDC additions, or take XDC version verbatim?
4. Should the bundle merge commits use `--no-ff` (preserving the bundle's commit list) or squash (collapsing to a single commit per bundle)? Issue #740 says merge-commit; confirm.

## Verification before Phase 2 starts

- [x] `xdc-network-pre-reset` rollback branch exists on origin (at `15fa78e1f`)
- [x] PR #739 (triage doc) merged to `xdc-network`
- [x] `reset/baseline-1.17.3` is pristine `upstream/v1.17.3` (`117e067f0`)
- [x] Pristine v1.17.3 builds clean on the local toolchain
- [x] Overlay enumerated to `/tmp/reset-740/overlay.txt` (677 lines)
- [ ] Opus planner advice logged in `docs/PORT_LOG.md`
- [ ] Bundle list above refined per planner advice
- [ ] `docs/RESET_CONFLICT_LOG.md` skeleton created (empty, ready for entries)
