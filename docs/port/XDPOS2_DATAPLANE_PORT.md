# XDPOS2 Data-Plane Wire-Shape Port — Design Note

Open work item: closes blocker #1c in REMAINING_WORK.md.
Status: NOT STARTED. Estimated scope: 4-6 files, ~400 lines net.

## Why this exists

PR #757 fixed forkID divergence. PR #758 fixed the wire handshake
(`StatusPacket62` for XDPOS2). Reset nodes now complete the eth-protocol
handshake against the production geth-XDC fleet.

But the very next message fails:

```
err="invalid message: (code 3) (size 10) rlp: expected input list for
eth.GetBlockHeadersRequest, decoding into
(eth.GetBlockHeadersPacket).GetBlockHeadersRequest"
```

Production sends `GetBlockHeaders` (msg code 0x03) as the bare eth/63-style
`GetBlockHeadersRequest{Origin, Amount, Skip, Reverse}` — no RequestId
wrapper. Reset binary's upstream eth/69 packets all use
`(RequestId, *Request)` wrapping. Reset can't decode the payload, the peer
is dropped after 6 seconds.

The same pattern repeats for all data-plane messages:
GetBlockHeaders/BlockHeaders, GetBlockBodies/BlockBodies,
GetReceipts/Receipts, GetPooledTransactions/PooledTransactions.

## Why this is architectural, not a small patch

xdc-network solves this by:

1. **Dual-decode handlers**: every `handleXxx` first tries the modern
   wrapped form, then `bytes.Reader.Seek(0)` and tries the legacy form.
2. **bytes.Reader buffering**: `handleMessage` wraps the raw payload in
   `*bytes.Reader` for messages that need dual-decode (so the second
   `msg.Decode()` can re-read the same bytes).
3. **Bypass request-tracker for legacy responses**: legacy responses
   have no `RequestId`, so they can't be matched to an outstanding
   `Request` via `peer.dispatchResponse`. xdc-network instead calls
   `backend.Handle(peer, &legacyRes)` directly — pushing the response
   into the downloader's "untracked response" queue.
4. **`Backend.Handle` accepts legacy types**: xdc-network's `Backend`
   interface accepts `*BlockHeadersRequest`, `*BlockBodiesResponse`,
   `*ReceiptsRLPResponse`, `*PooledTransactionsResponse` in addition to
   the wrapped types.
5. **Outgoing legacy requests**: `peer.RequestHeadersByXxx` and
   `peer.RequestBodies` need to send the bare request shape when
   `p.version == XDPOS2`, **but** also need to track the outstanding
   request locally (since there's no RequestId to round-trip).
6. **`Reply*RLPLegacy` methods**: new peer methods that send the bare
   response list (no RequestId field).
7. **Downloader integration**: `eth/downloader/peer.go` and
   `eth/downloader/fetchers.go` need to handle responses arriving via
   `backend.Handle` instead of the dispatch channel.

## Files touched (incremental list)

| File | Change |
|---|---|
| `eth/protocols/eth/protocol.go` | Already has the request types; verify legacy shapes are decodable bare |
| `eth/protocols/eth/handlers.go` | Dual-decode in 8 handlers; `backend.Handle` calls for legacy responses |
| `eth/protocols/eth/handler.go` | `handleMessage` wraps payload as `*bytes.Reader` for dual-decode-eligible msg codes |
| `eth/protocols/eth/peer.go` | 4 new `Reply*RLPLegacy` methods + legacy-shape send in 3 `Request*` methods + "untracked outstanding request" tracking |
| `eth/handler.go` | `Backend.Handle` accepts legacy types (signature change) |
| `eth/downloader/peer.go` | Route legacy responses to the right fetcher queue |

## Discovered during 2026-05-23 surgical port attempt

After landing PR #763 (`handleGetBlockHeaders` legacy decode +
`ReplyBlockHeadersRLPLegacy`) and PR #764 (populate `lastRange` from
XDPOS2 status), the on-wire state is:

- Handshake against production fleet: succeeds
- Inbound `GetBlockHeaders` from prod: decoded correctly, replied legacy
- Peer's tip recorded: `lastRange.LatestBlock = 82,233,801`
- Reset's downloader: **still never triggers sync**

Root cause: **v1.17.3 removed the pre-merge auto-sync trigger**.
`eth/sync.go` no longer contains `chainSyncer.nextSyncOp` /
`pickSyncPeer`. The only Synchronise entry point is now
`Downloader.BeaconDevSync(targetHeader)`, called from
`eth/syncer/syncer.go` (which expects a consensus-layer to supply the
target via the `debug_chainSync` RPC).

XDC is pre-merge — there is no consensus-layer Engine API
`forkchoiceUpdated` event to trigger sync. Without restoring some form
of pre-merge auto-trigger, the reset binary can never start a sync run
regardless of how many legacy-shape data-plane handlers we port.

This blocks **all** downstream work in this design note. The full port
must include sync-trigger restoration. Options:

### Option A: pre-merge `chainSyncer` restoration

Add back the pre-merge sync trigger from upstream pre-v1.17.0 geth:

- `chainSyncer.loop()` polling peerSet for tip
- `chainSyncer.nextSyncOp()` picking the best peer
- Calling `Downloader.Synchronise(peer, td)` to kick off sync
- Hooking into `handlerStartCh` to start the loop after handler init

~100 lines, mirrors what xdc-network does. Must not interfere with
the post-merge `BeaconDevSync` path (gate via chain config).

### Option B: synthesize-and-call `BeaconDevSync`

In the eth handler, after `RegisterPeer` succeeds and the peer's
`lastRange.LatestBlock > local head`:

- Build a synthetic `types.Header` from `lastRange.LatestBlockHash` and
  `LatestBlock`
- Call `h.downloader.BeaconDevSync(syntheticHeader)` to trigger snap-sync

~30 lines. Hacky — synthetic headers may break invariants in the
downloader's beacon path (designed for real consensus-layer headers).

### Option C: keep XDPOS2 disabled, force ETH69 negotiation

Drop XDPOS2 from `ProtocolVersions`. Reset would advertise eth/69 and
eth/70 only, falling back to upstream wire shapes that the downloader
can drive natively.

Loses XDC V2 BFT message routing (Vote/Timeout/SyncInfo). M8 (block
production) becomes impossible. Not a viable end state, but useful
as a sanity-check that the rest of the wire works.

### Recommendation

Option A is the principled answer and matches xdc-network's behaviour.
Belongs in the paired wire bundle (PR #749 scope).

## Critical invariants

- Reset binary continues to handshake + sync correctly against UPSTREAM
  geth ≥1.13 peers (ETH69 / ETH70) — i.e. when they're not running
  XDC's customised wire. Modern-path must remain default for
  non-XDPOS2 versions.
- Legacy responses are still rate-limited and size-bounded the same
  way modern responses are (don't let an XDC peer DoS reset by flooding
  legacy responses).
- Outgoing legacy requests still time out cleanly if peer goes silent
  (no infinite-pending request leak).

## Test plan

1. Land all 6 files in one PR (or paired bundle as PR #749)
2. Deploy on xdc02
3. Restart `snapfullpath` with `SNAPFULL_GATEWAY=1`
4. Verify within 60s:
   - First successful handshake against a `Geth/v1.17.0-unstable-7f2da7e5`
     peer (already working post-#758)
   - First successful `BlockHeaders` response from prod peer
   - `eth.blockNumber` advances past 0 within 5 minutes
5. Soak 1 hour — verify head approaches prod tip at expected rate

## Why not just drop XDPOS2 advertisement?

Tempting fix: remove XDPOS2 from `ProtocolVersions` so reset only
advertises ETH69/ETH70 (upstream-shape). Two reasons not to:

1. Production peers ALSO have customised ETH69/ETH70 handlers (we
   haven't audited their wire shape for these versions yet — they may
   ALSO use legacy shapes there).
2. XDPOS2 carries the V2 BFT consensus messages
   (Vote/Timeout/SyncInfo). Reset wouldn't be able to participate in
   the V2 BFT round if it doesn't advertise XDPOS2. That's a deal-
   breaker for masternode interop (M8).

## Open question: Opus escalation

Per `CLAUDE.md`'s model-routing rules, this should escalate to the
Opus planner before implementation because:

- Item #2 in the "When to delegate to Opus" list applies: proposal to
  rewrite an existing module.
- Item #5 applies: block production model design (M8 depends on this).
- Item #6 applies: any decision that changes block hash output (this
  doesn't, but indirectly enables blocks to be transferred).

Open the escalation with this design note as the prompt body.
