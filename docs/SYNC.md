# XDC Network Synchronization Guide

## Overview

This guide covers the various synchronization modes available for the XDC Network and how to optimize sync performance.

## Sync Modes

### 1. Full Sync
Downloads and verifies every block from genesis.

```bash
./XDC --syncmode full
```

**Pros:**
- Complete blockchain history
- Can serve historical data
- Most secure

**Cons:**
- Longest sync time
- Highest disk usage

### 2. Fast Sync (Default)
Downloads blocks and state at a recent checkpoint.

```bash
./XDC --syncmode fast
```

**Pros:**
- Faster initial sync
- Lower disk usage during sync
- Good for most users

**Cons:**
- No historical state
- Depends on checkpoint validity

### 3. Snap Sync — **NOT IMPLEMENTED** on this fork

`--syncmode snap` is parsed but **silently downgrades to full-sync**.
The XDC fork's `xdcSyncer` (`eth/sync_xdc.go`) drives all sync regardless
of the flag, doing block-by-block full import. The standard Geth
`eth/downloader/snap.go` is wired in but never invoked because xdcSyncer
takes ownership ahead of it.

Starting v1.17.3 the node emits a WARN at startup if `--syncmode snap`
is passed, pointing at the snapshot-restore alternative below.

Why this is the case: snap-sync's pivot-block model assumes a clear
finality boundary (PoW total-difficulty or post-merge CL signature).
XDPoS V2's 3-chain commit rule doesn't map cleanly onto that, and an
incorrect pivot would land the node on a non-canonical fork.

Tracker: [#807 — Re-enable real snap-sync on XDC v1.17.3](https://github.com/XDCIndia/go-ethereum/issues/807)

### 4. Snapshot Restore (the **actual** fast-sync path)

For fast initial sync today, download a pre-built chaindata snapshot
from `https://xdc.network/snapshots/` and extract it into the datadir
before starting `geth`. This is what the XDC fleet operators use.

```bash
# Apothem snapfullpath snapshot, ~97G compressed → ~132G extracted
mkdir -p $DATADIR
cd $DATADIR
curl -L -o snap.tar.zst \
  https://xdc.network/snapshots/xdc04-apothem-snapfullpath-<commit>-<block>.tar.zst
zstd -dc snap.tar.zst | tar -xf -
# now $DATADIR/geth/chaindata exists and geth can boot directly
```

Expected wall-clock: 15-25 min download + 2-5 min extract + minutes-to-hour
catch-up to tip via xdcSyncer. Total: under 1 hour on a fresh server with
good peers.

**Cons:**
- Trusts the snapshot publisher (xdc.network)
- Snapshot is N hours old; catch-up needed (handled by xdcSyncer automatically once geth starts)
- No pre-snapshot history (use `gcmode=archive` + full-sync if you need that)

## XDC-Specific Sync Features

### Checkpoint Verification
XDC uses checkpoints for additional security.

```bash
./XDC --xdc.checkpoint.interval 900
```

### Validator Set Sync
Synchronizes the current validator set.

```go
// Automatically syncs validator set at epoch boundaries
```

### Trading State Sync (XDCx)
Syncs order book state if XDCx is enabled.

```bash
./XDC --xdcx
```

### Lending State Sync
Syncs lending state if XDCxLending is enabled.

```bash
./XDC --xdcxlending
```

## Performance Optimization

### Hardware Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| CPU | 4 cores | 8+ cores |
| RAM | 16 GB | 32+ GB |
| Disk | 500 GB SSD | 1+ TB NVMe |
| Network | 25 Mbps | 100+ Mbps |

### Configuration Tuning

#### Cache Settings
```bash
./XDC --cache 4096 --cache.gc 50
```

#### Database Settings
```bash
./XDC --db.engine pebble
```

#### Network Settings
```bash
./XDC --maxpeers 50 --maxpendpeers 25
```

## Sync Progress Monitoring

### RPC Methods
```javascript
// Get sync status
eth.syncing

// Get current block
eth.blockNumber

// Get peer count
net.peerCount
```

### Log Monitoring
```bash
tail -f /var/log/xdc/node.log | grep -i sync
```

### Expected Sync Times (measured on v1.17.3 reset baseline, 2026-05)

| Mode | Mainnet | Apothem (testnet) | Notes |
|------|---------|-------------------|-------|
| Full from genesis | days-to-weeks | 30-50 h | block-by-block via xdcSyncer; ~430 bps sustained on NVMe + 3-5 peers |
| Snap | ⚠ not implemented (full-sync fallback) | ⚠ same | see [#807](https://github.com/XDCIndia/go-ethereum/issues/807) |
| **Snapshot restore** | ~30-60 min total | **15-25 min total** | download tarball + extract + catch-up; see Section 4 above |
| Fresh-sync from snapshot (catch-up only) | n/a | 5-30 min for typical 0-60k-block gap | measured: 75k blocks / 62 min ≈ 1,200 bps with good peer pool |

The "Fast" mode column was removed: `--syncmode fast` was deprecated by
upstream Geth years ago and is silently treated as snap on this fork
(which itself is silently treated as full).

## Troubleshooting

### Sync Stuck

1. Check peer connections
```bash
./XDC attach --exec "admin.peers.length"
```

2. Add bootnodes
```bash
./XDC --bootnodes "enode://..."
```

3. Clear bad peers
```bash
./XDC attach --exec "admin.peers"
```

### Low Peer Count

1. Check firewall rules
2. Open P2P ports (30303)
3. Enable UPnP

```bash
./XDC --nat extip:YOUR_IP
```

### Disk Space Issues

1. Enable pruning
```bash
./XDC --gcmode archive
```

2. Use snap sync
3. Resize disk

### Memory Issues

1. Reduce cache
```bash
./XDC --cache 2048
```

2. Limit peers
```bash
./XDC --maxpeers 25
```

## Network-Specific Settings

### Mainnet
```bash
./XDC --xdc.mainnet \
  --datadir /data/xdc/mainnet \
  --syncmode fast
```

### Testnet (Apothem)
```bash
./XDC --xdc.testnet \
  --datadir /data/xdc/testnet \
  --syncmode fast
```

### Devnet
```bash
./XDC --xdc.devnet \
  --datadir /data/xdc/devnet \
  --syncmode full
```

## State Pruning

### Enable Pruning
```bash
./XDC --gcmode full --state.gc.percent 25
```

### Manual Pruning
```bash
./XDC snapshot prune-state --datadir /data/xdc
```

## Backup and Recovery

### Create Backup
```bash
# Stop node first
./XDC copydb /data/xdc /backup/xdc
```

### Restore Backup
```bash
cp -r /backup/xdc /data/xdc
./XDC --datadir /data/xdc
```

## Monitoring Tools

### Prometheus Metrics
```bash
./XDC --metrics --metrics.addr 0.0.0.0 --metrics.port 6060
```

### Key Metrics
- `chain_head_block`: Current block number
- `p2p_peers`: Connected peer count
- `chain_sync_mode`: Current sync mode

## Best Practices

1. **Use SSDs**: Much faster than HDDs
2. **Adequate RAM**: Prevents swap usage
3. **Stable Network**: Reduces peer churn
4. **Regular Backups**: Protect against corruption
5. **Monitor Disk**: Prevent space exhaustion

## Further Reading

- [Node Deployment Guide](./DEPLOYMENT.md)
- [Network Configuration](./NETWORK.md)
- [Performance Tuning](./PERFORMANCE.md)
