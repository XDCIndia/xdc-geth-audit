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
| Upstream base | `ethereum/go-ethereum` **v1.17.4** = `36a7dc72e96b3f42846be925cfeb2fad18489917` |
| Upstream tree SHA | `3ce5a3428afb5961dd1c7bb3b4cc6bfb4c718770` |
| Upstream commits below the base | 17,113, with their **original SHAs** |
| XDC source reviewed | `XDCIndia/go-ethereum` @ `adf46fc9c0de63a49880476e2f7338ea359e56b2` |
| Corresponding release | `v-xdc-geth-mint-20260727` |
| Published binary sha256 | `705406e8c5b7339b0f429d841a7ba6d5568f2956a5d46d12fa3fcd09798f8d31` |

### Integrity check

The base is unmodified upstream. Verify it yourself — these use explicit SHAs rather
than relative refs like `~11`, which drift whenever the review history is re-cut:

```sh
UP=36a7dc72e96b3f42846be925cfeb2fad18489917

git rev-parse $UP^{tree}                    # -> 3ce5a3428afb5961dd1c7bb3b4cc6bfb4c718770
git rev-list --count $UP                    # -> 17113
git merge-base --is-ancestor $UP audit/v2           # succeeds
git merge-base --is-ancestor $UP audit/code-only    # succeeds

# confirm that SHA is genuinely upstream's, not ours:
git ls-remote https://github.com/ethereum/go-ethereum refs/tags/v1.17.4
```

Everything at `$UP` and below is byte-identical to upstream v1.17.4, with upstream's
original commit SHAs. Immediately above it sits one commit removing 3 upstream GitHub
Actions workflow files — the token used to publish this repository lacks the `workflow`
scope, so those files cannot be pushed — and above that the XDC delta, split across the
review commits below.

To confirm the split changed no content, compare against the audited source directly.
Only the 3 workflow paths, this file, `.gitattributes`, and the dropped detritus listed
under [Disclosure](#disclosure-status--partial-stated-honestly) may differ:

```sh
git fetch <source-remote> adf46fc9c0de63a49880476e2f7338ea359e56b2
git diff --name-only FETCH_HEAD audit/v2
```

### Reproducibility limit — read this

You get **tree equality, not binary reproduction.** `internal/version/vcs_buildinfo.go`
calls `debug.ReadBuildInfo()` and stamps the building repository's VCS SHA into the
binary, so a build from *this* repository will not byte-match the published binary built
from `XDCIndia/go-ethereum`. The source trees can be compared; the binaries cannot be
bit-compared. Treat the sha256 above as identifying what shipped, not as something you
can regenerate here.

Two further limits on independent verification:

- `XDCIndia/go-ethereum` is **private**, so `adf46fc9c` cannot be resolved externally
  without being granted read access.
- Release `v-xdc-geth-mint-20260727` carries **no attached assets**; the binary lives at
  an unversioned, mutable, unsigned URL.

---

## Two refs, and why

| Ref | Contents | Use |
|---|---|---|
| **`audit/code-only`** | code, tests, contracts, genesis fixtures, this guide | **read this one** |
| **`audit/v2`** | the same history plus one final commit adding `docs/`, `scripts/`, `docker/`, `build/`, `tools/` | provenance anchor |

`audit/code-only` is a strict **ancestor** of `audit/v2` — the operator and
documentation material is never introduced on it, rather than deleted from it. The
difference is therefore provable by construction:

```sh
git merge-base --is-ancestor audit/code-only audit/v2   # succeeds
git diff --name-only audit/code-only audit/v2           # docs/, scripts/, docker/, build/, tools/
```

This gives a clean review surface without weakening provenance: no history was
rewritten and no audited file was deleted to produce it.

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
| 10 | `ops+docs` | *`audit/v2` only* |

### Review tiers — the mandatory read is far smaller than the diff

Measured on this branch against upstream v1.17.4:

| Tier | Files | Lines | Read it? |
|---|---|---|---|
| **Modified upstream files** | **92** | **11,315** | **yes — this is the real audit surface** |
| Added XDC files | 178 | 61,759 | yes, but new code in isolation |
| ↳ of which generated bindings | 13 | 18,339 | no — verify provenance only |
| ↳ of which tests | 68 | 13,425 | as coverage evidence |
| ops+docs (`audit/v2` only) | 138 | 29,085 | no |

The 92 modified files are the point. They are surgical edits to battle-tested upstream
logic, and that is where fork bugs live. Highest-deletion files, i.e. where upstream
behaviour was most altered:

```
core/blockchain.go             -49
params/config.go               -47
eth/downloader/skeleton.go     -43
core/types/block.go            -43
eth/protocols/eth/peer.go      -29
eth/downloader/downloader.go   -28
core/rawdb/accessors_chain.go  -17
core/state_transition.go       -14
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

---

## Out of scope

- Upstream go-ethereum itself, including the EVM. The base is unmodified upstream at a
  known tag; upstream code is upstream's to audit.
- Live network operations, deployment, and key management.
- The XDC system contracts as deployed (only the Go bindings are here).
