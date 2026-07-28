# Archive Node Disk Management Runbook

**When to use:** Your archive geth node is stuck at a block, peers look healthy, but `head` isn't advancing. This runbook covers the most common cause — root partition filled, freezer can't write — and the standard geth fix.

This document is derived from a real incident (xdc01 mainnet-archive-hash, stuck at block 59,240,971 on 2026-05-30). Issue: [#830](https://github.com/XDCIndia/go-ethereum/issues/830).

---

## TL;DR

```bash
# Diagnose
df -h /                                # Avail = 0?
grep "no space left on device" $LOG    # Confirms ENOSPC at freezer
ls /proc/$GETH_PID/exe                 # binary still alive?

# Phase 1 (emergency, ~10 min): truncate logs in-place, drop verbosity, restart
: > /path/to/archive-node.log          # truncates active log without disturbing geth fd
kill -TERM $GETH_PID && wait_for_exit && relaunch_with --verbosity 3

# Phase 2 (durable, ~5 min): split ancient/ to a separate disk
mv $DATADIR/geth/chaindata/ancient /mnt/other-disk/$NODE-ancient
relaunch_with --datadir.ancient /mnt/other-disk/$NODE-ancient --datadir.minfreedisk 5120
```

---

## Symptoms checklist

You're in this runbook if **all** of these are true:

- [ ] Archive geth node `head` block hasn't changed in ≥ 30 minutes
- [ ] `net.peerCount` ≥ 1 (so it's not a peer-loss problem)
- [ ] `eth.syncing` flapping between `true` and `false` in xdcSyncer heartbeats, or stuck `true` with no head advance
- [ ] `df -h /` (or whichever mount holds the chaindata) shows ≥ 95% used
- [ ] Hash at the stuck block matches a healthy reference node (rule out fork divergence)

If you see XDPOS2 protocol issues (`empty header batch`, `XDPOS2 bridge: empty response`), this is a different runbook — see issue #823 family.

---

## Diagnosis

### 1. Verify it's really a disk-full ENOSPC

```bash
# Identify the geth process + its log
PID=$(pgrep -f 'geth.*archive-hash' | head -1)   # adjust the grep for your node mode
LOG=$(ls -t /path/to/node/logs/*.log | head -1)

# Smoking gun pattern:
grep -E 'no space left on device|Error in block freeze' "$LOG" | tail -5
```

Expected hit if disk-full:

```
ERROR Error in block freeze operation
  err="can't write body to Freezer: write .../ancient/chain/bodies.NNNN.cdat:
       no space left on device"
```

### 2. Confirm chain data is not corrupt (rule out fork divergence)

Get the block-hash at the stuck height from your node, then compare against a known-good reference (another XDC node, public explorer):

```bash
geth attach --exec 'eth.getBlock(STUCK_BLOCK).hash' $DATADIR/geth.ipc
# expected: hash matches the canonical reference node at the same block
```

If hashes match → it's pure disk exhaustion, proceed with this runbook.
If hashes differ → fork divergence, **stop**, file a P0 issue, don't apply this fix.

### 3. Inventory the disk

```bash
df -hT /          # which filesystem, % used, Avail
du -sh $DATADIR/geth/chaindata $DATADIR/geth/chaindata/ancient
du -sh $DATADIR/logs/*.log     # log file sizes
journalctl --disk-usage
du -sh /root/.cache /root/go   # often-overlooked caches
```

Typical findings:
- `chaindata/`: many GB, can't move easily
- `chaindata/ancient/`: tens of GB, **can be relocated cleanly via geth flag**
- `logs/*.log`: huge if `--verbosity 4` was on; tens of GB possible

---

## Phase 1 — Emergency unstick (~10 min)

Goal: free enough disk for geth's freezer to flush the in-flight batch and resume importing.

### Step 1a: Truncate the active log in-place

Geth opens log files with `O_APPEND`. The kernel `write()` always seeks to current end-of-file *before* writing each line. So a `truncate()` to size 0 cleanly resets the file — geth's next write starts at offset 0, no sparse holes, no fd-reopen needed.

```bash
LOG=/path/to/active.log
ls -lh "$LOG"               # confirm size
: > "$LOG"                  # truncate to 0 bytes — INSTANT freeing
ls -lh "$LOG"               # should be ~0
df -h /                     # confirm disk reclamation
```

> ⚠️ **Caveat:** This trick only works when the writer uses `O_APPEND`. Verify with `lsof -p $PID | grep $LOG` — the open mode should show `w` and the file's offset behavior should be append-style. Geth's standard log writer does this by default.

### Step 1b: Delete rotated/old logs (regular files, not held by geth)

```bash
LOGDIR=$(dirname "$LOG")
ACTIVE=$(ls -t "$LOGDIR"/*.log | head -1)   # the active one (already truncated above)
for f in $(ls -t "$LOGDIR"/*.log | tail -n +2); do
    rm -v "$f"
done
```

### Step 1c: Restart geth with `--verbosity 3` (NOT 4)

`--verbosity 4` produces ~5 GB / day of `Bad discv5 packet`, `Removed dead node`, peer-churn debug noise. `--verbosity 3` (INFO) produces ~5 MB / hour and is fine for ongoing operation. Use 4 only during short debug sessions.

```bash
# Capture full cmdline of the current process for re-launch
tr '\0' ' ' < /proc/$PID/cmdline > /tmp/geth-cmdline.txt
# Edit /tmp/geth-cmdline.txt: change --verbosity 4 → --verbosity 3

# Stop geth
kill -TERM $PID
# Wait up to 60s for clean exit
for i in $(seq 1 30); do
    sleep 2
    kill -0 $PID 2>/dev/null || break
done
kill -0 $PID 2>/dev/null && kill -9 $PID

# Relaunch (replace cmdline tokens as captured)
nohup $GETH_BIN <captured args with --verbosity 3> > $LOGDIR/restart-$(date +%s).log 2>&1 &
disown
```

After 30 seconds, query the new geth via IPC and verify the head is advancing past the previously stuck block.

---

## Phase 2 — Safe filesystem cleanup (~5 min, ~6 GB recovered)

Buys you a few hours of headroom while you plan Phase 3.

```bash
# 1. journald — vacuum keeps recent, drops old
journalctl --vacuum-size=200M

# 2. Go / build / pip caches (~3 GB)
rm -rf /root/.cache/*
rm -rf /root/go/pkg/mod /root/go/pkg/sumdb        # regenerates on next `go build`

# 3. apt caches (~600 MB)
apt-get clean
rm -rf /var/lib/apt/lists/*

# 4. Old archived audit logs
rm -f /var/log/audit/audit.log.*

# 5. /tmp older than 1 day
find /tmp -type f -mtime +1 -delete
find /var/tmp -type f -mtime +1 -delete

# 6. Old devnet datadirs
rm -rf /root/.ethereum-*-devnet /root/.ethereum-*
```

Verify reclamation:

```bash
df -h /
```

**Honest estimate:** This buys days for a steady-state node, hours for a catching-up archive node. Move to Phase 3 within 24 hours.

---

## Phase 3 — Durable fix: split chaindata via `--datadir.ancient` (~5 min)

Geth supports putting the freezer (`chaindata/ancient/`) on a different mount than the active chaindata. This is the standard geth feature for multi-disk archive deployments.

### Flags

| Flag | Purpose |
|---|---|
| `--datadir.ancient PATH` | Ancient/freezer chunk files live here instead of `$DATADIR/geth/chaindata/ancient/` |
| `--datadir.minfreedisk N` | Auto-shutdown if any geth-managed disk drops below N MB free. **Always set this** (5120 = 5 GB is a sensible default) |
| `--datadir.era PATH` | Era1 history files (smaller; default `$ANCIENT/chain/era` works fine) |

### Migration procedure

```bash
PID=$(pgrep -f 'geth.*your-node-mode' | head -1)
SRC=$DATADIR/geth/chaindata/ancient
DST=/mnt/data/$NODE_NAME-ancient        # destination on a roomier disk

# 1. Sanity-check destination has room (~size of $SRC plus growth headroom)
du -sh $SRC
df -h $(dirname $DST)
[ -e $DST ] && { echo "abort: $DST already exists"; exit 1; }

# 2. Stop geth cleanly (let it checkpoint state to disk)
kill -TERM $PID
for i in $(seq 1 30); do sleep 2; kill -0 $PID 2>/dev/null || break; done
kill -0 $PID 2>/dev/null && kill -9 $PID

# 3. mv across mount points = copy + delete-source (atomic per-file)
#    Typical speed on intra-host NVMe-to-NVMe: ~300-500 MB/s
mv -v $SRC $DST

# 4. Relaunch with the split flag + safety net
nohup $GETH_BIN \
    --datadir $DATADIR \
    --datadir.ancient $DST \
    --datadir.minfreedisk 5120 \
    --verbosity 3 \
    <all your other usual flags> \
    > $LOGDIR/archive-$(date +%s).log 2>&1 &
disown
```

### Verification

```bash
NEW_PID=$(pgrep -f 'geth.*your-node-mode' | head -1)

# 1. The flag is in the live cmdline
tr '\0' ' ' < /proc/$NEW_PID/cmdline | grep -oE -- '--datadir\.ancient [^ ]+'

# 2. The ancient/ symlink does NOT exist at the old location (geth uses the flag, not a symlink)
ls -la $DATADIR/geth/chaindata/ | grep ancient        # should show nothing or empty

# 3. New chunk files land on the new path
ls -lh $DST/chain/ | head -5

# 4. Disk impact
df -h / /mnt/data

# 5. No ENOSPC recurrence
grep -E 'no space left|Error in block freeze' /path/to/latest.log | wc -l   # expect 0

# 6. Block advancing
geth attach --exec 'eth.blockNumber' $DATADIR/geth.ipc
```

### Real-world example (xdc01 mainnet-archive-hash, 2026-05-30)

```
                            BEFORE              AFTER
/dev/nvme0n1p3  (root)      871G 0B free        871G 51G free
/dev/nvme1n1p1  (mnt/data)  175G free           114G free      (+61G ancient)

mv duration:    2:44 for 61 GB (~380 MB/s NVMe-to-NVMe)
ENOSPC errors:  0 after restart
Block advance:  59,531,447 and rising at ~300 b/s
```

---

## Provisioning recommendations

For new archive nodes:

1. **Size the disk for chain tip + 100 GB headroom.** XDC mainnet at tip (~103M blocks) needs ~700 GB of chaindata in archive mode + grows ~1-2 GB/day.
2. **Use `--datadir.ancient` from day one** if the host has multiple mounts. Don't wait for ENOSPC to motivate it.
3. **Default `--verbosity 3` for archive nodes.** Use 4 only during interactive debugging sessions, and revert.
4. **Always set `--datadir.minfreedisk 5120`** as a safety net. A clean geth shutdown leaves chaindata recoverable; an OS-level ENOSPC during a freezer write can corrupt the last chunk.
5. **Monitor `df` via your existing alerting.** Page on `< 20 GB free` *and* `< 5% free`.

---

## Why the bind-mount / symlink alternatives are inferior

| Approach | Verdict |
|---|---|
| `--datadir.ancient $NEW_PATH` | ✅ **Use this.** Geth-native; geth knows the path; future `geth init`, `geth db inspect`, etc. all respect it. |
| `ln -s $OLD_ANCIENT $NEW_PATH` (symlink) | ⚠️ Works but easy to forget on the next host; can break with chown/permissions; geth utilities may not follow it. Don't use unless you can't restart geth. |
| `mount --bind /mnt/data/ancient $DATADIR/geth/chaindata/ancient` | ⚠️ Requires fstab; less portable; same drawbacks as symlink. |
| Move the **entire** `chaindata/` to another disk | ⚠️ Last resort. ~700 GB to move = 30-60 min downtime, no advantage over `--datadir.ancient` since the active chaindata fits and grows slower than the freezer. |

---

## Related runbooks / issues

- **Issue #830** — the incident that produced this runbook
- **Issue #823** family — XDPOS2 "empty header batch" stuck-at-tip (different cause, similar diagnosis flow)
- **PR #822** — apothem fleet peer/bootnode configuration (peer-side stuck-state)
- **PR #824 / #825 / #826** — apothem fleet enode + `EXTRA_STATIC_NODES` propagation (peer-side stuck-state)
- `scripts/local-sync-test.sh` — fleet harness (does not yet support `--datadir.ancient`; see #830 action items)

---

*Last updated: 2026-05-30. Tested on geth v1.17.3-stable / XDC mainnet archive-hash node.*
