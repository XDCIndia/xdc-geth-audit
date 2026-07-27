# A.47 — Recover late-delivery NodeData responses (Opus, 2026-06-05)

## Problem (proven via A.46-xdposchain-peers run)

In `eth/downloader/faststatesync.go` runFastStateSync loop at line 204–210:

```go
case pack := <-d.fastStateCh:
    // Discard any data not requested (or previously timed out)
    req := active[pack.PeerId()]
    if req == nil {
        log.Debug("Unrequested node data", "peer", pack.PeerId(), "len", pack.Items())
        continue  // ← DROPS valid state data
    }
```

**Empirically measured on A.46-xdposchain-peers (port 30400, PID 4081263):**
- processed = 1858 state nodes (in 30 min)
- unexpected = 4,440,300 state nodes dropped
- Ratio: **0.026% delivered / 99.974% dropped**

The "Busy peer assigned new fast state fetch" path (line 255-261) moves
in-flight reqs to `finished[]` with `dropped=true`. Late NodeData arrivals
for that peer then find `active[peer] == nil` → log "Unrequested" → discard
valid state data the trie scheduler would happily accept.

## Concurrency constraint (CRITICAL)

Three goroutines touch the fastStateSync data:
1. **fastStateFetcher** → calls `runFastStateSync(s)` — this is the goroutine
   that hits the "Unrequested" branch
2. **s.run()** (spawned line 167) → calls `process(req)` for each finished
   request received via `s.deliver` channel — this mutates `s.sched`,
   `s.trieTasks`, `s.codeTasks`, `s.inflightHashes`, `s.keccak`
3. **timers** → fire `timeout <- req` into runFastStateSync's select

**`s.sched`, `s.keccak`, `s.trieTasks`, `s.codeTasks`, `s.inflightHashes`
are NOT thread-safe.** They're touched by both runFastStateSync (via fillTasks
and the "Busy peer" branch) AND process() in s.run().

A naïve A.47 patch that calls `s.sched.ProcessCode(...)` from runFastStateSync
would race with process()'s ProcessCode/ProcessNode calls.

## Safe design

### Option A — channel-based late-delivery dispatch (recommended)

Add a new channel `s.lateDeliveryCh chan [][]byte` to fastStateSync. In the
"Unrequested" branch, send the blobs to that channel. In `process()`, drain
the channel as part of normal request handling — but instead of building a
synthetic req, simply iterate the blobs and call processNodeData against a
**synthesized "ghost" req** that has `trieByHash`/`codeTasks` populated from
the **current finished reqs being processed**.

Cleaner alternative: process() runs in s.run(). Add a new case to s.run()'s
select that handles late deliveries directly — it has exclusive access to
s.sched, s.keccak in that goroutine.

### Option B — global inflightTrieByHash with mutex (also viable)

Add `s.inflightTrieByHash map[common.Hash]string` + `s.inflightMu sync.RWMutex`.
Maintain it in fillTasks (write under lock) and on req completion (write under
lock). In the "Unrequested" branch, RLock + look up + call s.sched.ProcessNode.
But s.sched still needs synchronization with process().

### Option C — defer to process() via synthetic queued req (simplest semantics)

In the "Unrequested" branch, create a synthetic stateReq with:
- `req.peer = nil` (sentinel)
- `req.response = pack.(*statePack).states`
- `req.trieTasks` = empty (process() will try-but-fail, marking all unexpected)
- `req.codeTasks` = empty
- `req.dropped = true`

Append to `finished[]`. process() iterates blobs, processNodeData returns
ErrNotRequested for each → unexpected++, none successful. **Net effect: same
as dropping today, just routed through the normal path.** This doesn't help.

To make Option C work, we need `process()` to additionally look up each blob
hash against `s.trieByHash`/`s.codeTasks` (the pool that has NOT been assigned
to any in-flight req). Tasks that were assigned but their req was dropped DO
get returned to s.trieTasks (process() lines 720-752). So if a late delivery
arrives AFTER its req was dropped AND process() ran on the dropped req, the
task IS back in s.trieTasks and a hash-lookup would find it.

Cleanest Option C variant: extend `process()` to ALSO accept a synthetic
req with `req.peer = nil` and (for nil peer) match blobs against the GLOBAL
`s.trieTasks`/`s.codeTasks` pools using a hash→path scan. Linear scan is
acceptable here because late deliveries are rare relative to normal flow.

## Recommended implementation order (next session)

1. **Add a regression test** mirroring `TestNodeDataDropWhenNoFetcher` (A.36)
   — `TestLateNodeDataRecoveredViaGlobalPool` that:
   - Creates a fastStateSync with a known root + a couple trie tasks
   - Simulates an in-flight req that gets dropped via the busy-peer path
   - Sends a `*statePack` with blobs matching the dropped req's hashes via
     `d.fastStateCh`
   - Asserts that `s.sched.Pending()` decreases (data accepted)
2. **Implement Option C variant** (~80 LOC in faststatesync.go).
3. **Build + deploy as A.47** on xdcscan port 30410, same XDPoSChain peers
   as A.46-xdpc.
4. **Acceptance**: within 30 min, processed grows from <2000 to >50,000;
   unexpected ratio drops from 99.97% to <50%.

## File-level changes summary

- `eth/downloader/faststatesync.go`:
  - Modify runFastStateSync "Unrequested" branch (lines 204-210) to route
    via finished[] with synthetic req
  - Modify `process()` to handle req.peer == nil by scanning s.trieTasks/
    s.codeTasks instead of req.trieTasks/req.codeTasks
- `eth/downloader/downloader_test.go`: add regression test
- Estimated diff: ~80-100 LOC + ~60 LOC of test = ~150 LOC total

## Risk surface

- Hash collisions: trie spec says different paths can hash same blob during
  sync (per A.26 Q3 comment line 326). Scanning s.trieTasks for matching
  hash could match the WRONG path. Mitigation: prefer the first match;
  scheduler will reject via ErrAlreadyProcessed if duplicate.
- Late-delivery DoS: malicious peer floods unrequested data. Mitigation:
  cap recovery attempts per packet at maxNodeDataServe (already enforced
  on the sender side).
- Memory: synthetic req lives in finished[] briefly. No leak.

## Why this fix matters

Without A.47, A.46-xdposchain-peers will take **~3 weeks of wall-clock** to
finish state-trie sync (10M nodes / 50 nodes/min). With A.47, expected rate
is **2000+ nodes/min** → finish in **~3 days**. Even better with parallelism
across the 3 peers in pool.
