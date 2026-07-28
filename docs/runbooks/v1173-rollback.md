# Runbook — Rollback xdc04 archivepath (or any v1.17.3 node) to v1.17.0

Use this when an xdc04 node running v1.17.3 starts misbehaving and you need to revert without losing chaindata.

## When to use this runbook

PAGE-NOW signatures in logs:
- `BAD BLOCK` repeated at the same block number across multiple restarts
- `missing trie node` accompanied by `invalid merkle root` (#804 family)
- Sustained `peers=0` with peer-pool warmup time exceeded 5 minutes
- Sync drift > 1000 blocks behind canonical sustained > 10 minutes
- Process repeatedly crashing within seconds of restart

SAFE-TO-IGNORE (don't roll back for these):
- Transient `count=0` from peers — normal at-tip cycle
- gap=1 to gap=5 drift fluctuation — natural P2P propagation
- Brief disconnect/reconnect cycles
- Single `XDPoS2 BlockHeaders received count=0` log lines

## Pre-flight (always do first)

```bash
ssh xdc04

# Confirm which node and which binary is failing
ps -ef | grep "build/bin/geth" | grep -v grep

# Check current head and peer state on the affected node, e.g. archivepath:
/root/workspace/go-ethereum/build/bin/geth attach \
  --exec "JSON.stringify({head:eth.blockNumber, peers:net.peerCount, syncing:eth.syncing})" \
  /mnt/data/workspace/go-ethereum/nodes-local/archivepath/geth.ipc

# Tail recent log
tail -100 /root/workspace/go-ethereum/logs/xdc04-archive-*.log | less
```

## Rollback — archivepath

```bash
# 1. STOP the v1.17.3 binary gracefully — DO NOT kill -9 if avoidable
pid=$(pidof /root/workspace/go-ethereum/build/bin/geth | tr ' ' '\n' | \
      while read p; do
        if grep -q "archivepath" /proc/$p/cmdline 2>/dev/null; then echo $p; break; fi
      done)
echo "killing $pid"
kill $pid

# 2. Wait up to 90s for graceful flush (archive chaindata is ~147GB)
for i in $(seq 1 30); do
  if ! pgrep -f "datadir.*archivepath.*--gcmode archive" >/dev/null; then
    echo "stopped after ${i}x3s"; break
  fi
  sleep 3
done

# 3. If still alive, force-kill (last resort — risks corruption)
pgrep -f "datadir.*archivepath.*--gcmode archive" >/dev/null && \
  kill -9 $(pgrep -f "datadir.*archivepath.*--gcmode archive")

# 4. Restart with the OLD v1.17.0 binary, same datadir, same cmdline as before upgrade
nohup /mnt/data/workspace/go-ethereum/build/bin/geth \
  --datadir /mnt/data/workspace/go-ethereum/nodes-local/archivepath \
  --config /mnt/data/workspace/go-ethereum/nodes-local/archivepath/geth/config.toml \
  --networkid 51 --apothem --port 30405 \
  --gcmode archive --syncmode full --nodiscover \
  --authrpc.port 8655 --http.port 8649 \
  --authrpc.addr 127.0.0.1 --http.addr 127.0.0.1 \
  --http --http.api eth,net,web3 \
  --verbosity 4 --state.scheme=path \
  --ethstats xdc04-apothem-archivepath-path-linux-x86_64-1.17.0-unstable-7f2da7e5:xdc_openscan_stats_2026@stats.xdcindia.com:443 \
  > /root/workspace/go-ethereum/logs/xdc04-archive-reverted-$(date +%s).log 2>&1 < /dev/null &

# 5. Verify
sleep 12
pgrep -f "datadir.*archivepath.*--gcmode archive" | head -1
/mnt/data/workspace/go-ethereum/build/bin/geth attach \
  --exec "eth.blockNumber" \
  /mnt/data/workspace/go-ethereum/nodes-local/archivepath/geth.ipc

# 6. Restart any downstream nodes that depend on this archive (RPC consumers, etc.)
```

## Rollback — other v1.17.3 nodes

Generalised: replace `archivepath` with the relevant datadir name. The binary paths are:

| Binary | Path |
|---|---|
| v1.17.0 (stable production) | `/mnt/data/workspace/go-ethereum/build/bin/geth` |
| v1.17.3 (this session) | `/root/workspace/go-ethereum/build/bin/geth` |

`/root/workspace/go-ethereum/` is the v1.17.3 checkout (branch `fix/pathdb-rlp-import`).
`/mnt/data/workspace/go-ethereum/` is the v1.17.0 checkout.

## Confirm rollback succeeded

```bash
# Binary commit should be 7f2da7e5 (v1.17.0)
/mnt/data/workspace/go-ethereum/build/bin/geth version | grep "Git Commit"

# Head should be at or near where the v1.17.3 was when it broke (no data loss)
# Should be importing blocks again within 30s

# No BAD BLOCK errors in new log
tail -50 /root/workspace/go-ethereum/logs/xdc04-*reverted*.log | grep -i "BAD BLOCK"
```

## Known limitations

- **Cannot roll back state**: if v1.17.3 wrote chaindata in a format v1.17.0 can't read, this runbook FAILS. Mitigation: nightly chaindata snapshots (see `docs/runbooks/snapshot-cron.md` when written).
- **Watchdog may interfere**: archive auto-restarts via an unknown watchdog (TBD inspection). The watchdog will use whatever binary is in the canonical path. If you want it to keep using v1.17.0 after rollback, ensure the canonical path points there.

## Post-rollback actions

1. Open a comment on the relevant GitHub issue (#804 if it's the chaindata-incompat family) with the exact error lines from the v1.17.3 log + rollback timestamp.
2. Restart the drift watcher (`Monitor` tool, gap > 50 threshold) on the reverted node.
3. Do NOT retry the upgrade until the underlying issue (#804 or root cause) has a fix.

## Decision tree

```
v1.17.3 node misbehaving?
├── Symptom: BAD BLOCK + missing trie node → rollback NOW (this runbook)
├── Symptom: drift > 1000 blocks for 10min → check #802 fix; consider rollback
├── Symptom: peers = 0 sustained → check trusted peers connectivity first
├── Symptom: high memory / OOM → not chaindata issue; profile first
└── Symptom: unknown → tail log, file issue, ask before destructive action
```
