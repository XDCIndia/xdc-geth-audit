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
| Upstream base | `ethereum/go-ethereum` **v1.17.5** = `9621c6ad10934a01b5514886fb6fbd87640b6c05` |
| Upstream tree SHA | `ce7dded85f82c2bd3998aa8a8a51932d7c6682b2` |
| Upstream commits below the base | 17,223, with their **original SHAs** |
| XDC source reviewed | `XDCIndia/go-ethereum` @ `f39bb0769d0c142a1d50d34476055c4b9b1caaf3` |
| Corresponding release | **none** — the source is 113 commits past `v-xdc-geth-mint-20260727` |
| Published binary sha256 | **n/a** — no binary has been published from this source |

### Integrity check

The base is unmodified upstream. Verify it yourself — these use explicit SHAs rather
than relative refs like `~11`, which drift whenever the review history is re-cut:

```sh
UP=9621c6ad10934a01b5514886fb6fbd87640b6c05

git rev-parse $UP^{tree}                    # -> ce7dded85f82c2bd3998aa8a8a51932d7c6682b2
git rev-list --count $UP                    # -> 17223
git merge-base --is-ancestor $UP audit/v2-1175         # succeeds
git merge-base --is-ancestor $UP audit/code-only-1175  # succeeds

# confirm that SHA is genuinely upstream's, not ours:
git ls-remote https://github.com/ethereum/go-ethereum refs/tags/v1.17.5
```

Everything at `$UP` and below is byte-identical to upstream v1.17.5, with upstream's
original commit SHAs. Immediately above it sits one commit removing 3 upstream GitHub
Actions workflow files — the token used to publish this repository lacks the `workflow`
scope, so those files cannot be pushed — and above that the XDC delta, split across the
review commits below.

To confirm the split changed no content, compare against the audited source directly.
Only the 3 workflow paths, this file, `.gitattributes`, and the dropped detritus listed
under [Disclosure](#disclosure-status--partial-stated-honestly) may differ:

```sh
git fetch <source-remote> f39bb0769d0c142a1d50d34476055c4b9b1caaf3
git diff --name-only FETCH_HEAD audit/v2-1175
```

### Reproducibility limit — read this

You get **tree equality, not binary reproduction.** `internal/version/vcs_buildinfo.go`
calls `debug.ReadBuildInfo()` and stamps the building repository's VCS SHA into the
binary, so a build from *this* repository will not byte-match the published binary built
from `XDCIndia/go-ethereum`. The source trees can be compared; the binaries cannot be
bit-compared. Treat the sha256 above as identifying what shipped, not as something you
can regenerate here.

Two further limits on independent verification:

- `XDCIndia/go-ethereum` is **private**, so `f39bb0769` cannot be resolved externally
  without being granted read access.
- This source carries **no release and no published binary at all**. The last tagged
  release, `v-xdc-geth-mint-20260727`, is 113 commits behind it and itself carried no
  attached assets — its binary lived at an unversioned, mutable, unsigned URL. There is
  therefore nothing to bit-compare against for the tree reviewed here.

---

## Two refs, and why

| Ref | Contents | Use |
|---|---|---|
| **`audit/code-only-1175`** | code, tests, contracts, genesis fixtures, this guide | **read this one** |
| **`audit/v2-1175`** | the same history plus one final commit adding `docs/`, `scripts/`, `docker/`, `build/`, `tools/` | provenance anchor |
| `1175` | the pristine upstream v1.17.5 base alone, no XDC content | base integrity check |

`audit/code-only-1175` is a strict **ancestor** of `audit/v2-1175` — the operator and
documentation material is never introduced on it, rather than deleted from it. The
difference is therefore provable by construction:

```sh
git merge-base --is-ancestor audit/code-only-1175 audit/v2-1175  # succeeds
git diff --name-only audit/code-only-1175 audit/v2-1175          # docs/, scripts/, docker/, build/, tools/
```

This gives a clean review surface without weakening provenance: no history was
rewritten and no audited file was deleted to produce it.

---

## The v1.17.5 uplift

These refs are the **same review stack re-cut onto a newer upstream base**. The
previously reviewed refs — `audit/code-only` and `audit/v2` on upstream **v1.17.4**
(`36a7dc72e`) — are untouched and still published. Nothing was force-pushed.

Both cuts are a presentation of the same fork, so the honest way to state what changed
is to compare each cut against *its own* source and check that the leftover is the same.
It is:

| | 1.17.4 cut | 1.17.5 cut |
|---|---|---|
| source | `adf46fc9c` | `f39bb0769` |
| upstream base | v1.17.4 `36a7dc72e` | v1.17.5 `9621c6ad1` |
| paths differing from source, `code-only` | 172 | **172** |
| paths differing from source, `v2` | 34 | **34** |

Same path sets, not merely the same counts — no path gained, none lost. The leftover is
exactly the disclosed set: this file, `.gitattributes`, the 7 workflow files, and the 25
dropped paths under [Disclosure](#disclosure-status--partial-stated-honestly).

**If you already reviewed the v1.17.4 stack, one commit is new to you:**
`xdc: v1.17.5 merge adaptation`. Everything the uplift required is in it — nothing was
folded into the slices you have already signed off. It carries 6 paths:

- 3 XDC-authored tests: `core/gate6_merge_1175_test.go`,
  `eth/downloader/item_b_wiring_xdc_test.go`,
  `eth/protocols/eth/protocol_xdc_versions_test.go`
- 3 upstream test files that are **new in v1.17.5** and do not compile against XDC's
  `newStateTransition`, which carries an extra coinbase argument (see the core
  integration slice): `core/eip2780_test.go`, `core/eip8037_test.go`,
  `core/eip8038_test.go`. Added argument only, no behavioural change.

Every number in [Review tiers](#review-tiers--the-mandatory-read-is-far-smaller-than-the-diff)
moves by exactly those 6 paths: modified 92 -> 95, added 178 -> 181, tests 68 -> 74.

The 10 XDC slices themselves were replayed, not re-authored. Where upstream v1.17.5 had
touched the same file (47 files did collide), the conflict was resolved by taking the
fork's own content verbatim — never by re-deciding the merge here. Each slice is then
asserted byte-identical to `f39bb0769` for every path in it, which is what makes the
172/34 figures above provable rather than asserted.

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
| 10 | `xdc: v1.17.5 merge adaptation` | the whole v1.17.4 -> v1.17.5 uplift, isolated — see [The v1.17.5 uplift](#the-v1175-uplift) |
| 11 | `ops+docs` | *`audit/v2-1175` only* |

### Review tiers — the mandatory read is far smaller than the diff

Measured on this branch against upstream v1.17.5:

| Tier | Files | Lines | Read it? |
|---|---|---|---|
| **Modified upstream files** | **95** | **11,438** | **yes — this is the real audit surface** |
| Added XDC files | 181 | 62,209 | yes, but new code in isolation |
| ↳ of which generated bindings | 13 | 18,339 | no — verify provenance only |
| ↳ of which tests | 74 | 13,885 | as coverage evidence |
| ops+docs (`audit/v2-1175` only) | 138 | 29,085 | no |

The 95 modified files are the point. They are surgical edits to battle-tested upstream
logic, and that is where fork bugs live. Highest-deletion files, i.e. where upstream
behaviour was most altered:

```
core/blockchain.go             -49
params/config.go               -47
eth/downloader/skeleton.go     -43
core/types/block.go            -43
eth/downloader/downloader.go   -28
eth/protocols/eth/peer.go      -24
eth/downloader/beaconsync.go   -22
core/types/gen_header_rlp.go   -22
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
