> **This view: `audit/main-uplift-1179-xdc19`: code only, no tests.**
> Source: XDCIndia/go-ethereum `24d7192e8c00` (v1.17.9-xdc.19 plus later fixes).
> Every `_test.go` file and every `testdata/` path is excluded (1,230 files), together
> with the non-code paths (docs, scripts, ops, CI, reports: 182 paths). Every remaining
> file is byte-identical to the source. `go build ./...` and `go vet ./...` pass on
> this tree. The tier tables and notes below describe the earlier 1178 cut and are kept
> for history; tests listed there are not part of this view.

# XDC go-ethereum fork — audit guide

This repository exists to give a reviewer **one provable diff** of the entire XDC
divergence from pristine upstream go-ethereum, arranged so it can be read in a
sensible order rather than as a single 120k-line wall.

Start with [Where to look first](#where-to-look-first). Read
[Invariants that look like bugs](#invariants-that-look-like-bugs-and-are-not) before
filing anything — four XDC behaviours are deliberate and each has already caused an
incident when "corrected".

---

## Provenance

| | |
|---|---|
| Upstream base | `ethereum/go-ethereum` **v1.17.8** = `a579077007b98217c3e253a66e4b452ca0c32b96` |
| Upstream tree SHA | `41025bb76ac5e943973403358dafa0a72dde68fc` |
| Upstream commits below the base | 17,412, with their **original SHAs** |
| XDC source reviewed | `XDCIndia/go-ethereum` @ `9cc06cf85d812d27b3f8d49900310a5b2dca080a` |
| Corresponding release | **`v1.17.8-xdc.13`** — the source is exactly that tag |
| Published binary sha256 | linux-amd64 `927f02f03dc51006429a2e1190d8f00f772878534180c47bd3863f7a1094f6b8`, linux-arm64 `31c4028c18a39e9c0549720f7fdb87c3e3e0e199bb518955800f447e2125a6b7` |

### Integrity check

The base is unmodified upstream. Verify it yourself — these use explicit SHAs rather
than relative refs like `~11`, which drift whenever the review history is re-cut:

```sh
UP=a579077007b98217c3e253a66e4b452ca0c32b96

git rev-parse $UP^{tree}                    # -> 41025bb76ac5e943973403358dafa0a72dde68fc
git rev-list --count $UP                    # -> 17412
git merge-base --is-ancestor $UP audit/v2-1178         # succeeds
git merge-base --is-ancestor $UP audit/code-only-1178  # succeeds

# confirm that SHA is genuinely upstream's, not ours:
git ls-remote https://github.com/ethereum/go-ethereum refs/tags/v1.17.8
```

Everything at `$UP` and below is byte-identical to upstream v1.17.8, with upstream's
original commit SHAs. Immediately above it sits one commit removing 3 upstream GitHub
Actions workflow files — the token used to publish this repository lacks the `workflow`
scope, so those files cannot be pushed — and above that the XDC delta, split across the
review commits below.

To confirm the split changed no content, compare against the audited source directly.
Only the 3 workflow paths, this file, `.gitattributes`, `CHANGELOG.md` (on
`audit/code-only-1178` only), and the dropped detritus listed under [Disclosure](#disclosure-status--partial-stated-honestly) may differ:

```sh
git fetch <source-remote> 9cc06cf85d812d27b3f8d49900310a5b2dca080a
git diff --name-only FETCH_HEAD audit/v2-1178
```

### Reproducibility limit — read this

You get **tree equality, not binary reproduction.** `internal/version/vcs_buildinfo.go`
calls `debug.ReadBuildInfo()` and stamps the building repository's VCS SHA into the
binary, so a build from *this* repository will not byte-match the published binary built
from `XDCIndia/go-ethereum`. The source trees can be compared; the binaries cannot be
bit-compared. Treat the sha256 above as identifying what shipped, not as something you
can regenerate here.

Two further limits on independent verification:

- `XDCIndia/go-ethereum` is **private**, so `9cc06cf85` cannot be resolved externally
  without being granted read access.
- The release `v1.17.8-xdc.13` has **no assets attached on GitHub**. Its binaries
  are served from `https://xdc.network/snapshots/bin/` (`geth-linux-amd64`,
  `geth-linux-arm64`), an unversioned, mutable, unsigned URL. The sha256 values above
  were taken when those files were published on 2026-10-14; a later download may differ.

---

## Two refs, and why

| Ref | Contents | Use |
|---|---|---|
| **`audit/code-only-1178`** | code, tests, contracts, genesis fixtures, this guide | **read this one** |
| **`audit/v2-1178`** | the same history plus one final commit adding `docs/`, `scripts/`, `docker/`, `build/`, `tools/`, `CHANGELOG.md` | provenance anchor |
| `1178` | the pristine upstream v1.17.8 base alone, no XDC content | base integrity check |

`audit/code-only-1178` is a strict **ancestor** of `audit/v2-1178` — the operator and
documentation material is never introduced on it, rather than deleted from it. The
difference is therefore provable by construction:

```sh
git merge-base --is-ancestor audit/code-only-1178 audit/v2-1178  # succeeds
git diff --name-only audit/code-only-1178 audit/v2-1178          # docs/, scripts/, docker/, build/, tools/, CHANGELOG.md
```

This gives a clean review surface without weakening provenance: no history was
rewritten and no audited file was deleted to produce it.

---

## The v1.17.8 uplift

These refs are the **same review stack re-cut onto a newer upstream base**. Every earlier
cut is untouched and still published: `audit/code-only` / `audit/v2` on upstream
v1.17.4 (`36a7dc72e`) and `audit/code-only-1175` / `audit/v2-1175` on v1.17.5
(`9621c6ad1`). Nothing was force-pushed.

Both cuts are a presentation of the same fork, so the honest way to state what changed
is to compare each cut against *its own* source and check that the leftover is the same:

| | 1.17.4 cut | 1.17.5 cut | 1.17.8 cut |
|---|---|---|---|
| source | `adf46fc9c` | `f39bb0769` | `9cc06cf85` (`v1.17.8-xdc.13`) |
| upstream base | v1.17.4 `36a7dc72e` | v1.17.5 `9621c6ad1` | v1.17.8 `a57907700` |
| paths differing from source, `code-only` | 172 | 172 | **173** |
| paths differing from source, `v2` | 34 | 34 | **34** |

The `v2` leftover is the same 34-path set as before. The `code-only` leftover is the
same 172-path set plus `CHANGELOG.md`, which is new in the fork since the v1.17.5 cut.
It is documentation, so like `docs/` it is introduced only on `audit/v2-1178`. No other
path was gained or lost.

**If you already reviewed the v1.17.5 stack, two commits are new to you.** Both sit
directly after `xdc: v1.17.5 merge adaptation`:

- `xdc: v1.17.6-v1.17.8 merge adaptation` (2 paths): upstream files changed in this
  range that the fork had to adapt. They are `core/state_processor_parallel.go`
  (upstream's parallel state processor) and `core/gas_tracing_test.go` (an upstream test
  adapted to XDC's `newStateTransition`, which takes an extra coinbase argument).
- `xdc: XDPoSChain v2.9.x parity ports since the v1.17.5 cut` (17 paths): XDC paths
  that are new since the v1.17.5 cut. They cover TRC21 token-fee capacity, v2.9.1
  parity (Gas2500x), the XDC `0x0a` precompile sets, the special-transaction fee
  credit, and txpool same-nonce protection for special transactions.

The existing slices also changed, because the fork changed: 225 commits separate
`f39bb0769` from `9cc06cf85`. Each slice was replayed, not re-authored, and is
byte-identical to `9cc06cf85` for every path it owns. To see what moved inside the
slices you already signed off, compare the two stacks commit by commit:

```sh
git range-diff 1175..audit/v2-1175 1178..audit/v2-1178
```

61 paths in the XDC delta were also changed upstream between v1.17.5 and v1.17.8. As
before, each slice takes the fork's own content verbatim; no merge was re-decided here.

Every number in [Review tiers](#review-tiers--the-mandatory-read-is-far-smaller-than-the-diff)
was re-measured on this cut. Modified files went from 95 to 98 and added files from 181
to 197.

---

## Where to look first

Review commits are ordered **riskiest first**; the commit order is the suggested
reading order.

| # | Commit | What it is |
|---|---|---|
| 1 | `xdc: block types, header extras and chain params` | header shape + chain config — everything depends on it |
| 2 | `xdc: XDPoS consensus engine` | **highest risk** — V1 clique-style, V2 HotStuff BFT |
| 3 | `xdc: XDPoS engine tests` | coverage for 2, separated for independent sign-off |
| 4 | `xdc: core integration` | where the engine meets upstream's core |
| 5 | `xdc: sync and networking` | XDC fast sync, downloader, eth protocols |
| 6 | `xdc: miner, node plumbing and CLI` | producer loop, the gas plateau |
| 7 | `xdc: system-contract wrappers (hand-written)` | hand-written contract glue |
| 8 | `xdc: generated abigen bindings` | **skip — verify provenance, do not line-read** |
| 9 | `xdc: genesis fixtures` | operator fixtures + embedded genesis |
| 9b | `xdc: XDCx / XDCxlending, version stamp, XDPoS integration test` | DEX/lending services, version stamp |
| 10 | `xdc: v1.17.5 merge adaptation` | the v1.17.4 -> v1.17.5 uplift, isolated |
| 10b | `xdc: v1.17.6-v1.17.8 merge adaptation` | the v1.17.5 -> v1.17.8 uplift, isolated — see [The v1.17.8 uplift](#the-v1178-uplift) |
| 10c | `xdc: XDPoSChain v2.9.x parity ports since the v1.17.5 cut` | XDC paths new since the v1.17.5 cut |
| 11 | `ops+docs` | *`audit/v2-1178` only* |

### Review tiers — the mandatory read is far smaller than the diff

Measured on this branch against upstream v1.17.8:

| Tier | Files | Lines | Read it? |
|---|---|---|---|
| **Modified upstream files** | **98** | **11,618** | **yes — this is the real audit surface** |
| Added XDC files | 197 | 63,660 | yes, but new code in isolation |
| ↳ of which generated bindings | 13 | 18,339 | no — verify provenance only |
| ↳ of which tests | 87 | 14,975 | as coverage evidence |
| ops+docs (`audit/v2-1178` only) | 139 | 29,314 | no |

The 98 modified files are the point. They are surgical edits to battle-tested upstream
logic, and that is where fork bugs live. Highest-deletion files, i.e. where upstream
behaviour was most altered:

```
params/config.go               -46
core/types/block.go            -43
eth/downloader/skeleton.go     -43
eth/downloader/downloader.go   -32
core/state_transition.go       -28
eth/protocols/eth/peer.go      -24
core/types/gen_header_rlp.go   -22
core/blockchain.go             -20
```

---

## Invariants that look like bugs and are not

Each of these has already caused a real incident when "fixed". Please read before
filing.

**1. The 420,000,000 gas plateau deliberately ignores genesis.**
`params.XDCBlockGasLimit = 420_000_000`, and `miner/xdpos_worker.go`'s `xdcGasCeil`
returns it while ignoring both the genesis `gasLimit` **and** `--miner.gaslimit`. The
plateau is a **per-network policy constant**, not a derived value. XDC mainnet's own
genesis is `4,700,000`; deriving the target from genesis makes producers hone the limit
*downward*, which **wedged public apothem at block 83,088,791**. Two pull requests
"correcting" this were closed as incorrect. Health signature: gasLimit monotonic
non-decreasing, then flat at the plateau — a **DOWN-step is the bug**.

**2. `ProtocolVersions` must retain `XDPOS2` and legacy `ETH63`.**
In `eth/protocols/eth/protocol.go`. `ETH71`/`ETH72` are defined but deliberately **not
advertised**. Without `ETH63` a node sat at **0 peers for 50+ minutes** on the same
bootnodes. Adopting upstream's list yields a node that builds, boots, and then never
peers or reaches consensus — with no error.

**3. V2 epoch switches are ROUND-based, not number-based.**
`leaderIndex = currentRound % epoch % len(masternodes)`. Because rounds and block
numbers diverge, epoch-switch **block numbers drift below** the 900-multiples. Any tool
computing switch positions from block-number arithmetic will be wrong. This has
repeatedly misled operators, including during this fork's own testing.

**4. The gasless `0x89` BlockSigners signing transaction must stay gasless.**
It must also not be dropped by txpool validation. If it is dropped, `totalSigners`
becomes 0, the post-state equals the parent, and the node hits a permanent
`invalid merkle root`. See `ApplySigningTransaction` and the `IsSpecialTransaction`
exemption.

---

## Things you would otherwise reasonably flag

- **`tests/testdata` and `tests/evm-benchmarks` are submodule gitlinks.** They point at
  upstream's fixture repositories and are intentionally not vendored (they include
  ~60 MB files). `git submodule update --init` populates them; `go test ./tests/...`
  then runs the Ethereum consensus suite.
- **No XDC `.sol` sources exist in-tree.** The 18,339 lines of abigen bindings therefore
  cannot be regenerated or diffed against contract source here. They must be checked
  against the deployed `0x88` (XDCValidator) and `0x89` (BlockSigner) bytecode on the
  live chains. The 7 `.sol` files present are upstream's own.
- **`docs/audits/*.pdf` are upstream go-ethereum's third-party audits, not XDC's.**
  Present on `audit/v2` only. Do not read them as an XDC audit trail.
- **Genesis fixtures include several mainnet candidates.** The files embedded by
  `core/genesis.go` via `go:embed` are authoritative; the rest are operator fixtures.
  All candidates agree on chainId 50 and gasLimit 4,700,000 — and note 4,700,000 is the
  *genesis* value, not the 420,000,000 runtime plateau (invariant 1).
- **GitHub Actions cannot run on this repository or on the source repository** — the
  organisation's Actions billing is disabled, so jobs report FAILURE within ~4 seconds
  without executing. A red check is *not* a test result. All verification behind the
  reviewed release was local.
- **One test fails in the source as well as here.** `TestParallelAbortsOnWorkerFailure`
  (`core/eip8037_test.go`) fails with "test block has no access list, the parallel
  processor would not run". It fails the same way on `v1.17.8-xdc.13` itself, so it is
  a known fork issue, not an artefact of this cut.

---

## Disclosure status — partial, stated honestly

**What was checked and found clean.** An independent scan of the delta found **no**
private keys, node keys, mnemonics, PEM or keystore files, and no API tokens (GitHub
PAT, AWS, Slack).

**What is still present, and is NOT claimed to be sanitised:**

- `cmd/utils/flags.go` contains a **compiled-in default ethstats secret**. Because it is
  a Go constant it is present in any binary built from this tree, including the
  published one. Rotation is tracked outside this repository.
- Operator documentation on `audit/v2` contains **root-SSH host inventories** (public
  IPv4 addresses and non-standard ports). Absent from `audit/code-only`.

**Dropped while building this history** (25 paths, ~18k lines, no information loss):
a stray agent session-transcript file at the repository root; `.claude/` and
`.codebase-domination/` tooling scaffolding; a `CLAUDE.md` symlink (`AGENTS.md` is
retained); six `*.conflicts.txt` merge-conflict dumps still containing live conflict
markers; nine `docs/**/*.html` renders each duplicating a sibling `.md`; two
byte-identical duplicates of the apothem genesis; and two **executable** operator
scripts carrying root-SSH host inventories. No other scrubbing was performed —
partial redaction would invite a false "sanitised" impression.

**Also absent for the same token-scope reason as the 3 upstream workflows:** four
XDC-authored workflow files — `.github/workflows/xdc-build.yml`, `xdc-test.yml`,
`upstream-tracker.yml` and `upstream-alpha-validate.yml`. They are CI definitions, not
client code, but they *are* part of the XDC delta and are not presented here. Read them
in the source repository if CI configuration is in scope for you.

---

## Out of scope

- Upstream go-ethereum itself, including the EVM. The base is unmodified upstream at a
  known tag; upstream code is upstream's to audit.
- Live network operations, deployment, and key management.
- The XDC system contracts as deployed (only the Go bindings are here).
