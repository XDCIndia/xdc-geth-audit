# A.57 — HBSS Archive Fleet Operational Manifest

**Owner**: Ops team (XDCNetwork infra)
**Status**: REQUIREMENTS — awaiting deployment
**Refs**: #857, Sprint 2 deliverable

## Why A.57 exists

XDPOS2 classical fast sync downloads ~9-13M state trie nodes via the
NodeData wire path. Each node lookup must reach a peer running the
**hash-based state scheme** (HBSS — pre-PBSS Merkle Patricia layout). After
go-ethereum v1.13+, every fresh archive node defaults to **path-based state
scheme** (PBSS) and CANNOT answer GetNodeData requests with non-empty
responses. The A.37 + A.40 gates filter these peers out of the dispatch
pool, but the public XDC peer mesh today is overwhelmingly PBSS — at the
A.14c rate cap (~13K nodes/min on xdc04) a full state sync still takes
~12-15 hours, vs. ~2-3 hours achievable against a dedicated HBSS fleet.

A.57 documents the requirements for ops to spin **3-5 modern v1.17.3 HBSS
archive nodes** that the downloader can pin via `--bootnodes` /
`static-nodes.json` so fast sync escapes the public-mesh PBSS bottleneck.

## Fleet sizing

**Minimum**: 3 nodes (geographic redundancy)
**Recommended**: 5 nodes (load distribution + maintenance headroom)

| Role | Count | Notes |
|------|-------|-------|
| Primary HBSS archive | 3 | Always-on, three distinct cloud regions |
| Spare HBSS archive | 2 | Hot standby; rolled into primary set during maintenance |

Going below 3 risks a single-region outage stalling all sync. Going above
5 yields diminishing returns — at the wire protocol's per-peer NodeData
serving budget (typically 768 hashes / batch / RTT), 5 healthy peers
already saturate a syncing node's parallel-inflight headroom (16 slots
via A.53).

## Per-node hardware

| Resource | Spec | Reason |
|----------|------|--------|
| CPU | 8+ vCPU (AMD EPYC or Intel Xeon, post-2020) | Trie node decode + signature verification |
| RAM | 32 GB | Archive working set + LDB block cache |
| Disk | NVMe SSD, 4 TB usable | XDC mainnet archive footprint ~1.8 TB (Q2 2026) + room for growth |
| Disk IOPS | 50K+ random read | NodeData serving is read-heavy on cold trie regions |
| Network | 1 Gbps symmetric, unmetered | A typical fast-sync peer pulls 50-150 GB of NodeData per sync |

**Disk type matters**: HDD or older SATA SSDs will starve under concurrent
NodeData read load and the trackers in msgrate will down-rank the node,
which defeats the purpose of pinning it.

## Cloud regions (geographic spread)

| Region | Provider examples | Why |
|--------|-------------------|-----|
| EU-West (Frankfurt/Helsinki) | Hetzner / OVH | Closest to current xdc04 mainnet bootnode at 65.21.71.4 |
| US-East (Ashburn/Newark) | Vultr / DigitalOcean | Coverage for North American operators |
| AP-Southeast (Singapore/Tokyo) | Vultr / Linode | Coverage for SEA mainnet operators (largest XDC validator concentration) |
| US-West (San Francisco/Los Angeles) | *spare* | Latency hedge for west-coast operators |
| EU-North (Stockholm) | *spare* | Latency hedge for Nordic operators |

Avoid putting all 3 primary nodes inside the same cloud provider — a
single-provider outage (AWS Frankfurt 2017, OVH SBG 2021) would take the
entire fleet offline.

## Geth build + flags

**Binary**: v1.17.3 of this fork (the erigon-xdc / go-ethereum-xdc port)
once A.57's other Sprint 2 work has stabilised on mainnet.

**Required flags**:
```
geth \
  --datadir=/data/xdc-archive \
  --syncmode=full \
  --gcmode=archive \
  --state.scheme=hash          # HBSS — non-negotiable for A.57
  --txlookuplimit=0 \
  --history.transactions=0 \
  --networkid=50 \
  --port=30303 \
  --maxpeers=200 \             # Serve as many syncing peers as possible
  --light.maxpeers=0 \
  --nat=extip:<PUBLIC_IP> \
  --metrics --metrics.expensive \
  --metrics.addr=0.0.0.0 --metrics.port=6060 \
  --pprof --pprof.addr=127.0.0.1 --pprof.port=6061 \
  --log.file=/var/log/geth/xdc-archive.log
```

**Critical**: `--state.scheme=hash` (HBSS). The default switched to PBSS in
upstream and our v1.17.3 carries the upstream default forward. If this flag
is missing, the node is functionally useless for A.57's purpose — the A.37
proactive gate will skip it on every connection, and the A.40 reactive
blacklist will sticky-skip it after 10 empty NodeData responses.

**Do NOT use**:
- `--syncmode=snap` (snap-only nodes can't serve NodeData)
- `--state.scheme=path` (PBSS)
- `--cache.preimages=false` if planning to serve eth_call against the archive

## Verification that a deployed node is actually HBSS

After bringing a node up to head, run from a separate machine:

```bash
geth attach http://<node-ip>:8545
> admin.nodeInfo.protocols.eth.config
# Look for XDCCaps trailer; CommitPrefix should be non-zero
# scheme should report "hash"
```

Or query the rate limiter for incoming GetNodeData responses:

```bash
curl -s http://<node-ip>:6060/debug/metrics | grep nodedata_serve_empty_count
# Should be ~0; nonzero means the node is responding [] to NodeData (PBSS!)
```

If `nodedata_serve_empty_count` rises monotonically, the node is PBSS —
re-provision with the correct `--state.scheme=hash` flag.

## Peer-publishing manifest

The downloader on a syncing node finds these peers via the bootnode list.
**Each node's static enode URL** (with public IP + port + node ID) must be
published to a manifest the syncing operators can ingest.

Recommended publishing locations:
1. `https://archive.xinfin.network/peers.json` (canonical, ops-team-controlled)
2. Pinned IPFS hash, rotated quarterly
3. Read-only S3 / R2 bucket with HTTPS + CORS

Manifest schema (JSON):
```json
{
  "version": "a57-v1",
  "scheme": "hbss",
  "updated": "2026-06-05T00:00:00Z",
  "primary": [
    {"region": "eu-west", "enode": "enode://abc...@1.2.3.4:30303"},
    {"region": "us-east", "enode": "enode://def...@5.6.7.8:30303"},
    {"region": "ap-southeast", "enode": "enode://...@9.10.11.12:30303"}
  ],
  "spare": [
    {"region": "us-west", "enode": "enode://...@..."},
    {"region": "eu-north", "enode": "enode://...@..."}
  ]
}
```

A syncing operator then bootstraps with:
```bash
curl -fsSL https://archive.xinfin.network/peers.json | jq -r '.primary[].enode' > /etc/geth/bootnodes.txt
geth --bootnodes=$(paste -sd, /etc/geth/bootnodes.txt) --syncmode=fast ...
```

## Monitoring requirements

Each fleet node must export:

| Metric | Threshold | Action if breached |
|--------|-----------|--------------------|
| `eth/downloader/states/in/count` (responses served) | should rise monotonically while peers sync | page if flat for >10 min — likely peer-stall |
| `chain/inserts` head height vs apothem-info.xinfin.network | lag < 5 blocks | page if lag > 50 blocks — node is unhealthy |
| Disk space remaining | > 500 GB | page at < 200 GB; rotate / compact / replace disk at < 100 GB |
| Disk read IOPS p99 | < 30K | page if sustained > 40K — fleet is undersized |
| OOM kills (kernel ring buffer) | 0 | page on any — RAM is undersized or there's a leak |
| Open file descriptors | < 80% of ulimit | page at 90% |

Dashboards: Grafana + Prometheus stack co-deployed with each node. Public
read-only mirror at `https://archive.xinfin.network/grafana/` so syncing
operators can verify fleet health before kicking off a long sync.

Alertmanager destinations:
- Primary: ops-team-rotation@xinfin.org (PagerDuty)
- Secondary: #xdc-archive-ops Slack channel

## Maintenance procedures

**Rolling restart** (for v1.17.x point releases): drain one node at a time,
upgrade, restart, wait until head-lag < 5 blocks, repeat. Never restart >1
primary simultaneously.

**Disk migration** (NVMe replacement / region move): bring up a spare in
the same region first, let it catch up to head, then drain the node being
migrated. Update the peer manifest atomically (single PR + CDN purge).

**State scheme migration**: NEVER migrate an A.57 node to PBSS. If
operationally required (e.g. PBSS becomes mandatory upstream), provision a
fresh HBSS node from genesis in parallel — switching scheme on a running
archive corrupts the trie.

## Security

- SSH key-only access, public key rotation every 90 days.
- Geth RPC (HTTP/8545) bound to 127.0.0.1 only; expose via reverse-proxy
  + auth token if external RPC is needed.
- WS endpoint similarly bound; do not expose `admin_*` namespace publicly.
- Firewall: only TCP/30303 + UDP/30303 open to internet; metrics/pprof
  bound to localhost or VPN.

## Acceptance criteria (when A.57 is "done")

1. 3 primary HBSS archive nodes online for ≥ 7 days, head-lag < 5 blocks.
2. Peer manifest published at the canonical URL with HTTPS + correct CORS.
3. Grafana dashboard public, showing `nodedata_serve_empty_count == 0` on
   every primary node.
4. A fresh syncing node bootstrapped against the manifest completes XDC
   mainnet fast-sync in < 6 hours (target: 3-4 hours).
5. Runbook for rolling restarts + disk migration tested in staging.

## Out of scope for A.57

- Building the manifest CDN (assumed: existing xinfin.network infra).
- DNS / TLS certificate provisioning (assumed: existing process).
- Geth code changes (none required — the gates already exist in A.37/A.40/
  A.53; A.57 is purely an ops investment in supply-side HBSS capacity).
- Snap-sync alternative path (out of Sprint 2 scope; addressed by future
  M.7 / snap-sync re-enablement work).

---

**Status reporting**: ops team should comment on #857 when each
primary node comes online + when the manifest URL goes live. Sprint 2's
A.57 deliverable is this manifest; deploying the fleet itself is a
separate ops-team task tracked under their own backlog.
