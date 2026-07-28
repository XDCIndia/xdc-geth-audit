# A.60 — Autonomous orchestrator /loop for XDC 100% wire compatibility

## Goal

Drive modern v1.17.3 go-ethereum to **100% wire-protocol compatible** with
the XDC mainnet peer mesh (XDPoSChain v2.7.0-devnet) under negotiated
XDPOS2/100 — **without any bypass, fallback, or runtime polyfill**. Every
packet code modern sends and accepts must match the legacy XDPoSChain wire
shape byte-for-byte when the peer version is XDPOS2.

## Root cause already isolated (do not re-investigate)

Side-by-side experiment 2026-06-06 proved:
- Modern's protocol/handshake code is fine — XDPOS2/100 negotiates cleanly.
- Modern's StatusPacket62 path is fine — handshake completes.
- The **first request packet** modern sends (GetReceiptsPacket70, msg code 0xf)
  carries an eth/66+ `RequestId` field that legacy peers cannot decode:
  ```
  err="invalid message: (code f) rlp: input string too long for uint64,
       decoding into (eth.GetReceiptsPacket70).RequestId"
  ```
- Same problem applies to every other request packet under XDPOS2: headers,
  bodies, receipts, NodeData. All need legacy non-RequestId shapes when the
  negotiated version is XDPOS2.

Reference evidence in `docs/port/PORT_LOG.md` under `[2026-06-06]`.

## Orchestrator /loop prompt

```
/loop agents-orchestrator opus drive A.60 to completion: make modern v1.17.3
go-ethereum 100% wire-compatible with XDC mainnet (XDPoSChain v2.7.0) under
XDPOS2/100. NO bypass, NO runtime polyfill, NO "if peer.version == XDPOS2
skip this" shortcuts — every request and response packet body modern sends
or accepts under XDPOS2 MUST match the legacy XDPoSChain wire shape exactly.

Working tree: /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum
Active branch: feat/a11-fast-sync-peer-gate (HEAD currently 1c8e4a4b4)
Test bench:    xdcscan (95.217.106.210, ssh port 12141), datadir
               /root/workspace/go-ethereum/nodes-local-mainnet/m1
Control:       fresh XDPoSChain at port 30310 (PID 1356536) — its
               admin.peers output is the ground-truth wire shape.

Workflow per cycle:
  1. Sonnet implementation subagent picks the next packet (in order:
     GetReceipts → GetBlockBodies → GetBlockHeaders → NodeData →
     Receipts → BlockBodies → BlockHeaders → NodeDataResponse →
     NewBlockHashes → NewBlock → Transactions → NewPooledTransactionHashes
     → GetPooledTransactions → PooledTransactions → BlockRangeUpdate).
  2. Adds a `legacy*Packet` (non-RequestId) variant alongside the existing
     RequestId-wrapped Packet70 type in eth/protocols/eth/protocol.go.
  3. Modifies the encode/decode site under
     eth/protocols/eth/{handlers,peer,broadcast}.go so that
     when p.version == XDPOS2 we send the legacy shape and accept the legacy
     shape on receipt. No code path for XDPOS2 may RequestId-wrap or expect
     a RequestId field.
  4. Adds a unit test in
     eth/protocols/eth/protocol_test.go that encodes the legacy shape and
     decodes back, asserting byte-for-byte equivalence with the
     reference encoding produced by XDPoSChain v2.7.0 for the same message.
  5. Commits as one commit per packet with subject
     "eth/protocols/eth: A.60.N — <PacketName> legacy XDPOS2 shape (refs #857)".
     Body explains the exact byte-level shape difference fixed.
  6. Sonnet builds geth on xdcscan via ssh -p 12141 root@95.217.106.210
     (cd /root/workspace/go-ethereum && git pull && go build -o
     build/bin/geth-a60-N ./cmd/geth).
  7. Sonnet hot-restarts the m1 testbench with the new binary, verifies via
     net.peerCount + admin.peers that peers stay attached longer than the
     previous build (steady-state target: ≥ 3 external peers for ≥ 5 minutes
     without RLPx disconnect).
  8. Sonnet greps the modern log for the next "invalid message: (code N)..."
     error and routes that to the next loop cycle.
  9. If a cycle produces no improvement (peer attachment duration does not
     increase), Sonnet escalates to Opus planner via Task with
     subagent_type=planner; advice goes verbatim into PORT_LOG.md as
     [PLANNER-ADVICE] before the next cycle.

Hard rules — orchestrator must enforce:
  • No commits with the word "bypass" in code or commit message.
  • No flags like --xdpos2-legacy-mode — every XDPOS2 connection uses the
    legacy shape, always.
  • No `if peer.version == XDPOS2 && time.Now() > ...` runtime gating —
    the version check IS the gate, period.
  • Every consensus-touching commit triggers the parity harness against
    Apothem blocks 80000..80100 + hardfork boundary blocks; non-green
    rolls back the commit and re-escalates.
  • All commits push to origin/feat/a11-fast-sync-peer-gate immediately
    so the test bench can pull.

Acceptance — orchestrator stops when ALL of:
  • m1 testbench has held ≥ 5 external peers for ≥ 30 minutes
    contiguously without "invalid message" disconnect events.
  • Fast-sync state import processed counter advances at ≥ 100 nodes/sec
    sustained over a 10-minute window.
  • Parity harness PASS on Apothem 80000..80100 with the modern binary
    decoding all legacy XDPOS2 messages.
  • #857 has a closing comment with the bench transcript + the commit
    list (A.60.1 .. A.60.N).

Notes:
  • Disk on xdcscan is healthy (45+ GB free on /, 233+ GB free on /mnt/data).
  • Fresh nodekey is at /root/workspace/go-ethereum/nodes-local-mainnet/m1/geth/nodekey.
  • XDC peers' caps are ["xdpos2/100","eth/63"]; never add or remove caps
    on modern's side as part of A.60 work — that's a separate concern
    handled in A.59v2.
  • The 4 prior fixes (wire-fix b643922f8, A.47 b9cf801b7, A.53 768d59488,
    ENR-filter a50580a81, A.58 Day 2 aa9694801, A.59 + A.59v2) stay intact.
    A.60 is on top, not replacing them.

Cadence: 20 minutes between full status reports. Push notifications on
peer-attachment-cycle improvements or consensus-parity green/red flips.
Stop the loop only at the acceptance criteria above or on user-typed
interrupt. ```

## How to start

From a terminal in this repo:

```bash
cd /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum
claude
```

Then paste the prompt block above (everything between the triple-backticks)
as a single `/loop` command in the Claude Code prompt.

The orchestrator should be authorized for full autonomy on this branch
(`feat/a11-fast-sync-peer-gate`) — every commit is reversible via `git
revert` and the test bench is isolated.

## Live test bench reference

| Component | Host / port | Path / PID |
|---|---|---|
| Modern testbench `m1` | 95.217.106.210 port 12141 (SSH) | `/root/workspace/go-ethereum/nodes-local-mainnet/m1` (fresh nodekey) |
| Modern RPC | 95.217.106.210 port 8569 (HTTP) | `admin_nodeInfo`, `admin_peers`, `net_peerCount` |
| XDPoSChain control (established) | 95.217.106.210 port 30304 (P2P), 8548 (RPC) | PID 537625 |
| XDPoSChain control (fresh) | 95.217.106.210 port 30310 (P2P), 8550 (RPC) | PID 1356536 |
| xdc05 mainnet (modern + works) | 185.180.220.183 port 52316 (SSH) | `/root/workspace/go-ethereum/nodes-local-mainnet/snapfull` |
| Build tree | `/root/workspace/go-ethereum` | branch `feat/a11-fast-sync-peer-gate` |

Test the loop is making progress by:

```bash
ssh -p 12141 root@95.217.106.210 \
  'curl -s -X POST -H "content-type: application/json" \
     --data "{\"jsonrpc\":\"2.0\",\"method\":\"net_peerCount\",\"params\":[],\"id\":1}" \
     http://127.0.0.1:8569/'
```

Expected progression as the loop adds packet shapes:
- After A.60.1 (GetReceipts legacy): peer attachment lasts > 5s before disconnect.
- After A.60.3 (GetBlockHeaders legacy): peer attachment lasts > 1 minute.
- After A.60.N (full set): peer attachment is permanent, state import begins.
