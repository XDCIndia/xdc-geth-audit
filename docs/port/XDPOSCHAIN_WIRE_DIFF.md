# XDPoSChain vs modern go-ethereum v1.17.3 — fast-sync wire-protocol diff

## Executive summary

`XDPoSChain` (PID 537625, version `XDPoS/2.7.0-devnet-ac324a8a`, uptime 2d14h)
is fully synced to block 103,401,694 against the *exact same* public XDC peer
mesh on which modern `v1.17.3` cannot get past the skeleton phase. Both
binaries speak `xdpos2/100`. Both are launched with `--syncmode fast` against
the same bootnodes. They differ **structurally** in how the downloader asks
peers for headers:

| Dimension | XDPoSChain (works) | Modern v1.17.3 (stalls) |
|---|---|---|
| Header request **direction** | `reverse=false` (forward) | `reverse=true` (backward from head) |
| Header request **count** per packet | `192` (or `128` skeleton) | `512` |
| **Skeleton step size** | `skip=191` (XDC peers cap at 192) | `skip=0` (contiguous) |
| Response **size requirement** | "deliver what you have" | strict `len(headers) == 512` |
| **Pivot selection** | self-derived `head - 64` from peer | operator-pinned `--fastsyncpivotnumber` |
| Common-ancestor discovery | `findAncestor` binary search (1 hdr at a time) | inherited from BeaconSync, requires pinned head |
| **State retrieval** | `RequestNodeData(hashes []common.Hash)` (eth/63, no RequestId) | same eth/63 shape (correct) |
| Sync **orchestrator** | classical `Downloader.synchronise` | BeaconSync `skeleton.Sync` |

The peers themselves are not the problem. The XDPoSChain peer fleet caps
`GetBlockHeaders` responses at `MaxHeaderFetch = 192` (XDPoSChain
`eth/handler.go:445`). When modern v1.17.3 asks for `amount=512, reverse=true,
skip=0`, the peer can only return ≤192 headers. Modern's skeleton then
rejects the response with **"Invalid non-genesis header count" — `have 192,
want 512`** (modern `eth/downloader/skeleton.go:986-989`) and reschedules.
Every honest response from every honest peer is dropped on the floor.

**The fix is not a workaround.** The fix is to teach modern's skeleton path
to accept the 192-cap when the responding peer is `XDPOS2` — and ideally to
issue `amount=192` (rather than 512) for XDPOS2 peers, so the request matches
the peer's serve cap. A complementary fix is a small classical fallback that
mirrors XDPoSChain's `fetchHeaders` (forward, count=192, skip=0 fill +
skip=191 skeleton) when the only available peers are XDPOS2.

---

## Section 1 — XDPoSChain sync mechanism (the working reference)

### 1.1 Live process inspection

```
$ ssh xdcscan 'ps -fp 537625'
/root/workspace/XDPoSChain/build/bin/XDC
  --datadir /root/workspace/XDPoSChain/nodes-local/fast --mainnet
  --port 30304 --verbosity 3 --syncmode fast --gcmode full
  --fastsyncpivotnumber 103309269
  --fastsyncpivothash 0x32a2aeb22957a7526498a2c2b447762ea2708c42fa43b03ac83d2e3be3d9eea9
  --fastsyncpivotroot 0xa9e706f9ca9ec8b58a47740ee43cf860954b7051b09a5653d60cf3de37d9111e
  --cache 2048 --maxpeers 50 --bootnodes <58 enodes>
```

```
$ XDC version
Version: 2.7.0-devnet
Git Commit: ac324a8a225057112cb4fffe6f22ccd27c3a71fe
Protocol Versions: [100 63 62]
```

```
$ curl ... eth_blockNumber  →  0x629e8de   (103,401,694)
$ curl ... eth_syncing      →  false
$ curl ... net_peerCount    →  0x10        (16 peers)
```

**Important finding**: the `--fastsyncpivot*` flags were copy-pasted from the
modern v1.17.3 fork's flag set. They do **not exist** in XDPoSChain's
`cmd/utils/flags.go` (verified by `grep -rn "fastsyncpivot"
XDPoSChain/` — zero hits). They are silently ignored by `cli`. XDPoSChain
is syncing against the public peer mesh **with no operator pivot at all**,
relying only on its in-built `findAncestor + pivot = peerHead - 64` flow.

### 1.2 Default sync mode

- `XDPoSChain/eth/ethconfig/config.go:50` — `SyncMode: downloader.FullSync`
- The live process forces `--syncmode fast` via CLI → `FastSync` mode.

### 1.3 Protocol advertisement

- `XDPoSChain/eth/protocol.go:42` — `ProtocolVersions = []uint{xdpos2, eth63, eth62}`
- `xdpos2 = 100`. Adds `VoteMsg=0xe0`, `TimeoutMsg=0xe1`, `SyncInfoMsg=0xe2`
  on top of eth/63's data-plane messages. **No RequestId wrapper anywhere**
  (the data-plane shapes are bare eth/63).

### 1.4 Synchronise entrypoint

`XDPoSChain/eth/downloader/downloader.go:346  synchronise(id, hash, td, mode)`:

1. `fetchHeight(p, hash)` — single-header probe of the peer's tip.
2. `findAncestor(p, latest)` — binary search (line 655). Issues
   `RequestHeadersByNumber(check, 1, 0, false)` per probe (line 770) — one
   header at a time, forward direction.
3. `pivot = height - uint64(fsMinFullBlocks)` (line 456) — peer-derived,
   `fsMinFullBlocks` typically 64. **No operator override**.
4. Five concurrent fetchers (line 472-479):
   `fetchHeaders → fetchBodies → fetchReceipts → processHeaders → processFastSyncContent`.

### 1.5 Header fetch wire shape — the critical path

`XDPoSChain/eth/downloader/downloader.go:844  fetchHeaders(p, from, pivot)`:

```go
const (
    MaxHeaderFetch  = 192   // line 45
    MaxSkeletonSize = 128   // line 46
)

getHeaders := func(from uint64) {
    if skeleton {
        // SKELETON: request 128 sparse headers, one per 192-block window
        go p.peer.RequestHeadersByNumber(
            from + uint64(MaxHeaderFetch) - 1,   // origin = from + 191
            MaxSkeletonSize,                      // amount = 128
            MaxHeaderFetch - 1,                   // skip   = 191
            false,                                // reverse = false  ← FORWARD
        )                                         // — line 864
    } else {
        // FILL: contiguous 192-header batch from `from`
        go p.peer.RequestHeadersByNumber(
            from, MaxHeaderFetch, 0, false,       // — line 867
        )
    }
}
```

`fillHeaderSkeleton` (line 1011) then spawns concurrent fillers that each
fetch a 192-header gap between skeleton anchors via
`RequestHeadersByNumber(from, 192, 0, false)` on **other** peers
(line 1024 → `peer.FetchHeaders(req.From, MaxHeaderFetch)`).

### 1.6 GetBlockHeaders server-side cap

`XDPoSChain/eth/handler.go:445`:

```go
for !unknown && len(headers) < int(query.Amount)
              && bytes < softResponseLimit
              && len(headers) < downloader.MaxHeaderFetch {   // ← 192 ceiling
    ...
}
```

This is the **hard ceiling**. Every XDC mainnet peer on the wire today caps
its `BlockHeaders` reply at 192 headers regardless of what `query.Amount`
asked for. A request with `amount=512` returns at most 192 headers — the
peer is healthy and responding correctly; it's just enforcing its serve cap.

### 1.7 Body / state wire shapes

- `XDPoSChain/eth/peer.go:410  RequestBodies(hashes []common.Hash)` →
  `p2p.Send(rw, GetBlockBodiesMsg, hashes)` (bare hash list, no RequestId).
- `XDPoSChain/eth/peer.go:421  RequestNodeData(hashes []common.Hash)` →
  `p2p.Send(rw, GetNodeDataMsg, hashes)` (bare hash list, no RequestId).
- State sync (`statesync.go:437`): `req.peer.FetchNodeData(append(nodes,
  codes...))` — single eth/63 GetNodeData per batch.

---

## Section 2 — Modern go-ethereum v1.17.3 sync mechanism (the failing path)

### 2.1 Default sync entrypoint

Modern uses BeaconSync (`eth/downloader/skeleton.go`) which was introduced
for the merge. The skeleton's `Sync(head)` (line 386) walks **backwards**
from a pinned head, accumulating header batches into `scratchSpace` until
either genesis or a previously-known anchor is hit. No `findAncestor` binary
search; no per-peer common-ancestor probe.

### 2.2 Request constants

`go-ethereum/eth/downloader/skeleton.go`:

```go
const requestHeaders = 512    // line 59 — packet size, hard-coded
const scratchHeaders = 131072 // line 52 — 256 batches of 512 each
```

### 2.3 Header fetch wire shape

`go-ethereum/eth/downloader/skeleton.go:909-914`:

```go
requestCount := requestHeaders        // = 512
if req.head < requestHeaders {
    requestCount = int(req.head)
}
netreq, err := peer.peer.RequestHeadersByNumber(
    req.head,         // origin (a block number, walking backward)
    requestCount,     // amount = 512
    0,                // skip   = 0      ← CONTIGUOUS
    true,             // reverse = true  ← BACKWARD
    resCh,
)
```

`peer.go:518  RequestHeadersByNumber` correctly handles the XDPOS2 wire
shape (no RequestId wrapper) via `newHeadersRequestData(p.version, ...)` at
line 549-562. **Wire encoding to the peer is correct.**

### 2.4 Response validation — where the rejection happens

`go-ethereum/eth/downloader/skeleton.go:951-996`:

```go
case res := <-resCh:
    headers := *res.Res.(*eth.BlockHeadersRequest)
    switch {
    case len(headers) == 0:
        // ... empty-response counter, drop peer after 5
        res.Done <- errors.New("no headers delivered")
        s.scheduleRevertRequest(req)

    case headers[0].Number.Uint64() != req.head:
        // anchor mismatch → reject
        res.Done <- errors.New("invalid header batch anchor")
        s.scheduleRevertRequest(req)

    case req.head >= requestHeaders && len(headers) != requestHeaders:
        // ───────────────────────────────────────────────────────────────
        //  THIS IS THE BUG: rejects 192-header replies from XDPOS2 peers
        // ───────────────────────────────────────────────────────────────
        peer.log.Debug("Invalid non-genesis header count",
            "have", len(headers), "want", requestHeaders)   // want=512
        res.Done <- errors.New("not enough non-genesis headers delivered")
        s.scheduleRevertRequest(req)
    ...
```

When `requestHeaders=512` and the peer returns 192 (because XDPoSChain caps
at `MaxHeaderFetch`), `len(headers) != requestHeaders` is true and the
batch is rejected wholesale. The downloader reschedules to another peer; that
peer also returns 192; rejected again; rinse and repeat forever.

### 2.5 GetBlockHeaders server-side cap (modern, for comparison)

`go-ethereum/eth/protocols/eth/handler.go:44` — `maxHeadersServe = 1024`.
Modern peers respect up to 1024 per request. But modern peers are not what
v1.17.3 is talking to — it's talking to XDPoSChain peers with cap=192.

### 2.6 Existing XDPOS2 bridge work (already merged)

The repo already has solid XDPOS2 plumbing on the send/receive side:

- `eth/protocols/eth/peer.go:549  newHeadersRequestData` — strips RequestId
  for XDPOS2 peers (legacy wire shape).
- `eth/protocols/eth/handlers.go:387-503  handleBlockHeaders` (XDPOS2 path) —
  decodes bare `[]*types.Header` (no RequestId), re-anchors the response,
  truncates, and dispatches via the in-flight FIFO matched by per-peer
  request stash.
- `eth/protocols/eth/peer.go:407  ReplyBlockHeadersRLPLegacy` — replies with
  the bare list when serving an XDPOS2 peer.

So the wire layer is already correct. The **breakage is at the higher layer**:
`skeleton.executeTask`'s strict `len(headers) == 512` check on the
**incoming** validated response.

### 2.7 Pivot path (gap_pivot_xdc.go)

`eth/downloader/gap_pivot_xdc.go` and `eth/sync_xdc.go` add operator-pinned
pivot + gap-block snapshot logic. These mitigate the *post-skeleton*
masternode-snapshot problem (consensus engine needs gap-block state for
post-pivot epoch crossings). But they do **not** help the skeleton get past
the 512-vs-192 mismatch in the first place.

---

## Section 3 — The diff, line by line

| # | What | XDPoSChain (works) | Modern v1.17.3 (broken) | Effect |
|---|---|---|---|---|
| 1 | Skeleton direction | `reverse=false` | `reverse=true` | Both legal on the wire. Modern walking back from a pinned head means the very first request hits a number that may be many blocks *past* any peer's head — but more importantly the validator below rejects the size. |
| 2 | Skeleton packet size | `amount=128` (sparse) + `192` (fill) | `amount=512` | XDPoSChain peers cap at 192. Modern's 512 returns ≤192 headers → rejected at `skeleton.go:986`. |
| 3 | Skeleton step (skip) | `skip=191` (sparse), then `skip=0` for fillers | `skip=0` (contiguous) | XDPoSChain serves both shapes, but only up to its cap. |
| 4 | Response size policy | tolerates short replies (line 915-994 in `downloader.go`) — uses `packet.Items()` to drive state machine, processes whatever comes back | strict equality (`skeleton.go:986`) | XDPoSChain accepts under-fills; modern rejects them. |
| 5 | Common-ancestor probe | `findAncestor` binary search via single-header forward requests (`RequestHeadersByNumber(check, 1, 0, false)`) | not used; BeaconSync assumes a pinned head | When the head is unknown / operator pivot is stale, modern has no fallback. XDPoSChain re-binds to whatever the peer claims as head. |
| 6 | Pivot derivation | `pivot = peerHead - 64` (peer-driven, fresh every cycle) | operator-pinned via CLI flags (`--fastsyncpivotnumber/...`) | XDPoSChain re-derives pivot each cycle. Modern uses a static operator value that goes stale (peer fleet has already moved tens of thousands of blocks beyond). |
| 7 | Body request shape | `RequestBodies(hashes)` — bare hash list (eth/63) | identical XDPOS2 path in `peer.go:RequestBodies` — bare list (eth/63), via `XDCBodyResp` and dispatcher bridge | matches — not the bug |
| 8 | State request shape | `RequestNodeData(hashes)` — bare hash list (eth/63) | identical bare-list path in `peer.go:428  RequestNodeData` (refs #844/#845) | matches — not the bug |
| 9 | Server-side cap (peer we talk to) | `MaxHeaderFetch=192` in `eth/handler.go:445` | n/a (modern's own serving cap is 1024, but it's the *receiver*) | The cap is enforced by the peer fleet; modern just has to live with it. |
| 10 | Empty-response handling | "if 0 and skeleton phase, switch to fill mode and retry from `from`" (line 889-891) | drop peer after 5 consecutive empties (line 967-973) | XDPoSChain treats empty as a phase signal. Modern treats it as peer misbehavior. |

**Root cause in one sentence**: Modern v1.17.3's skeleton fetcher asks for
512-header batches and **strictly** requires 512-header responses; XDPoSChain
peers cap at 192 and return 192; modern rejects every response and never
makes progress.

---

## Section 4 — Backport recommendation

### 4.1 Minimal fix (low-risk, ~30 LoC)

Make modern's skeleton size adaptive to the responding peer's protocol
version. When the peer is `XDPOS2`, send `amount=192` and validate against
`192` instead of `512`.

#### File: `eth/downloader/skeleton.go`

Define a per-peer batch size resolver:

```go
// requestHeadersFor returns the per-packet header count this skeleton
// cycle should request from `peerVersion`. XDPOS2 (pre-merge XDC) peers
// cap their GetBlockHeaders reply at MaxHeaderFetch (192). Asking for
// more results in a 192-header reply that fails our strict-equality
// validator, stalling the skeleton indefinitely. Refs #857.
func requestHeadersFor(peerVersion uint) int {
    if peerVersion == eth.XDPOS2 {
        return eth.MaxXDPOS2HeaderBatch   // = 192 (alias for MaxHeaderFetch)
    }
    return requestHeaders                 // = 512 (eth/66+)
}
```

Use it inside `executeTask` (line 905-914) **and** in the validator
(line 986-996). Also propagate through `scratchHeaders` math at lines
856-885 (the scratch space is partitioned in `requestHeaders`-sized
chunks — for XDPOS2 we want 192-sized chunks).

Pseudo-diff:

```go
// In executeTask:
batch := requestHeadersFor(peer.peer.Version())   // 192 for XDPOS2, 512 else
requestCount := batch
if req.head < uint64(batch) {
    requestCount = int(req.head)
}
netreq, err := peer.peer.RequestHeadersByNumber(req.head, requestCount, 0, true, resCh)
...
// In the response validator:
case req.head >= uint64(batch) && len(headers) != batch:
    peer.log.Debug("Invalid non-genesis header count", "have", len(headers), "want", batch)
    res.Done <- errors.New("not enough non-genesis headers delivered")
    s.scheduleRevertRequest(req)

case req.head < uint64(batch) && uint64(len(headers)) != req.head:
    ...
```

Caveat: `scratchSpace` is allocated as `make([]byte, scratchHeaders)` with
`scratchHeaders % requestHeaders == 0` enforced at startup (line 103-105).
For a mixed-version peer set, the scratch chunking has to be done at
*request-issue time* using the issuing peer's batch size. Today it
pre-allocates with the wrong stride. Simplest fix: assert `scratchHeaders %
192 == 0` AND `scratchHeaders % 512 == 0` (both true for 131072), then
remove the panic and make the scratch chunking variable.

### 4.2 Belt-and-suspenders (medium-risk, ~150 LoC)

Add a classical-downloader fallback that mirrors XDPoSChain's
`fetchHeaders` exactly for cycles where the only available peers are
XDPOS2 and the skeleton has stalled (e.g., no progress for 60s).

- Drop into `Downloader.synchronise` (legacy path already partly present in
  `downloader.go`) with `fetchHeaders` issuing
  `RequestHeadersByNumber(from+191, 128, 191, false)` skeleton +
  `RequestHeadersByNumber(from, 192, 0, false)` fill — both `reverse=false`.
- This bypasses BeaconSync entirely when *every connected peer* is XDPOS2.
- Re-using the existing `findAncestor` binary-search code in
  `XDPoSChain/eth/downloader/downloader.go:655` as a reference; the algorithm
  is simple and well-tested.

### 4.3 Pivot strategy (separate concern, already half-implemented)

The operator-pivot flags (`--fastsyncpivotnumber/hash/root`) wired through
`eth/sync_xdc.go` are useful as a manual override but are **not** what makes
XDPoSChain work. XDPoSChain re-derives the pivot from each peer's head on
every sync cycle. Modern should do the same when no operator value is set:

```go
// In sync_xdc.go pinned-head logic:
if !operatorPivotSet {
    // Probe top peer for its head, derive pivot as peerHead - fsMinFullBlocks
    // identical to XDPoSChain/eth/downloader/downloader.go:456.
    peerHead, err := topPeer.fetchHeight(...)
    pivot = peerHead.Number.Uint64() - fsMinFullBlocks
}
```

### 4.4 Logging / observability

Add wire-pass-through metrics at the boundary:

```go
xdpos2HeaderBatchRejected.WithLabelValues("size_mismatch").Inc()
xdpos2HeaderBatchRejected.WithLabelValues("anchor_mismatch").Inc()
xdpos2HeaderBatchAccepted.Inc()
```

So future debugging doesn't require reading process logs to discover that
every batch is being rejected.

---

## Section 5 — Consensus-parity risk analysis

The backport is **strictly request-shape and acceptance-policy** — it does
not touch:

- Block validation / sealing / signer recovery (`consensus/XDPoS/`)
- State-trie execution semantics (`core/state`, `core/vm`)
- Receipt encoding / bloom assembly (`core/types/receipt.go`)
- Gap-block masternode-snapshot materialisation
  (`eth/downloader/gap_pivot_xdc.go`)
- Block-hash composition or RLP field order anywhere

**What it DOES change**: how many headers per `BlockHeaders` packet the
local node accepts as "valid" before queueing them for hash-chain
verification. The downstream `case` for hash-progression validation at
`skeleton.go:998-1008` is unchanged — every accepted batch still goes
through `headers[i].ParentHash == headers[i+1].Hash()` linkage. Bad headers
are still rejected; we just stop rejecting *good headers that happen to be
batched smaller*.

### Specific gates that MUST pass after the change

1. **Parity harness**: re-run on Apothem blocks 80000..80100 and any
   hardfork boundary (TIP2019, TIPSigning, TIPRandomize, TIPV2, TIPUniform,
   Berlin, London, Merge, Cancun). Header-hash, receipts-root, state-root
   must match the canonical XDPoSChain post-state.
2. **Skeleton regression tests**: `skeleton_test.go` (with `requestHeaders=
   512`) must still pass for non-XDPOS2 peers. The existing
   `skeleton_xdpos2_test.go` already asserts XDPOS2 peers are eligible idle
   peers; the new tests must additionally assert that a 192-header response
   from an XDPOS2 peer is **accepted** (not rejected with "Invalid
   non-genesis header count").
3. **Fast-sync end-to-end**: rerun A.13/A.14 fast-sync harness against
   public peers and verify the skeleton makes monotonic progress (head
   strictly increases over a 30-minute window).
4. **No header poisoning**: the per-peer XDPOS2 batch size cap is set
   server-side at 192 — a malicious peer cannot trick us into accepting
   larger batches than legitimate. (Asymmetric concern: a malicious peer
   could send `<192` headers to slow us down, but that's already a DoS
   vector handled by the empty-response peer-drop logic at lines 962-978.)

### What this fix does NOT solve

- The masternode-snapshot gap-block enumeration (`gap_pivot_xdc.go`) is
  still needed for post-pivot epoch crossings.
- Operator pivot validation (header/state-root sanity) is still useful for
  cold starts.
- State retrieval via `NodeData` still needs the eth/63-shape work already
  merged in #844/#845.

The fix unblocks the **skeleton phase** — every other XDC fast-sync
component is already correct.

---

## References

| Path | Significance |
|---|---|
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/XDPoSChain/eth/downloader/downloader.go:844-1034` | XDPoSChain `fetchHeaders` — the working reference |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/XDPoSChain/eth/downloader/downloader.go:45-46` | `MaxHeaderFetch=192`, `MaxSkeletonSize=128` |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/XDPoSChain/eth/handler.go:431-507` | Server-side `GetBlockHeaders` handler with the 192 cap on line 445 |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/XDPoSChain/eth/peer.go:399-410` | `RequestHeadersByNumber` wire shape (bare eth/63, no RequestId) |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/XDPoSChain/eth/protocol.go:42-72` | `xdpos2=100`, protocol message codes |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/downloader/skeleton.go:59` | `requestHeaders = 512` — the wrong constant for XDPOS2 peers |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/downloader/skeleton.go:909-914` | The 512-amount, reverse=true outbound request |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/downloader/skeleton.go:986-989` | The strict-equality validator that rejects 192-header responses (**the bug**) |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/eth/peer.go:518-563` | Correct XDPOS2-aware outbound wire encoding |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/protocols/eth/handlers.go:377-505` | XDPOS2-aware inbound response decoding + dispatch bridge |
| `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/eth/downloader/gap_pivot_xdc.go` | Gap-block snapshot logic (orthogonal to this fix) |

Live evidence: `xdc04:537625` syncing at block `0x629e8de` (103,401,694)
against the same peer mesh as v1.17.3, no operator pivot in effect,
`eth_syncing=false`, 16 peers, protocol `[100 63 62]`.
