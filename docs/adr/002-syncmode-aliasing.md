# ADR 002 — Drop `fast` and `fastpath` sync modes from local-sync-test

Status: ACCEPTED (PR #757, 2026-05-23)
Context: Reset baseline onto upstream v1.17.3 dropped `--syncmode fast`.

## Context

Upstream geth v1.17.3 removed the `fast` sync mode entirely. The only
remaining `--syncmode` values are `full` and `snap`.

`scripts/local-sync-test.sh`, pulled verbatim from xdc-network, has
historically maintained 5 syncer cohorts each with hash and path-scheme
variants:

| Mode | Syncmode | Notes |
|---|---|---|
| `full` / `fullpath` | snap (default) | Bootstrap front-line |
| `snap` / `snappath` | snap | State-snapshot pivot |
| `fast` / `fastpath` | fast (legacy) | Header-then-state pivot |
| `archive` / `archivepath` | full + gcmode=archive | Full execute, retain all state |
| `snapfull` / `snapfullpath` | full | Execute from genesis to tip |

After the baseline reset:
- `--syncmode fast` is no longer accepted by the binary
- An early PR (#755) aliased fast→snap in the script so the binary
  would accept the flag
- This left `fast`/`snap` as duplicate modes with identical
  syncmode+gcmode but different ethstats labels and ports

Operators reading the ethstats dashboard saw both `xdc02-apothem-fast-*`
and `xdc02-apothem-snap-*` rows that behaved identically, causing
confusion when triaging sync state.

## Decision

Drop `fast` and `fastpath` modes entirely from the script.

After this change, each cohort name maps to exactly one
`(syncmode, gcmode)` tuple:

| Mode | Syncmode | gcmode |
|---|---|---|
| `full` / `fullpath` | snap (default) | full |
| `snapfull` / `snapfullpath` | full | full |
| `snap` / `snappath` | snap | full |
| `archive` / `archivepath` | full | archive |

8 distinct cohorts (4 hash + 4 path-scheme).

## Alternatives considered

### A. Keep fast→snap aliasing for backwards compatibility

Rejected. The aliasing produced two distinct ethstats labels for
identical underlying nodes. Operators couldn't tell at a glance whether
they were looking at a snap node or a fast-aliased-as-snap node. The
aliasing also meant `wipe-stateful` had to delete BOTH `fast` and `snap`
chaindata even though they were redundant.

### B. Rename `fast` to a distinct configuration

Rejected. There's no third distinct sync configuration available in
v1.17.3. Inventing one (e.g. fast = snap + extra prune setting) would
add complexity without operational benefit.

### C. Keep `fast` as an explicit alias of `snap` with a warning log

Rejected. Same downside as A plus the warning log noise.

## Consequences

Positive:
- Each ethstats label corresponds to a unique syncmode invocation
- `wipe-stateful` deletes 3 cohorts (archive/snapfull/snap) instead of 4
- Script is 23 lines shorter
- New operators don't have to learn the historical `fast` vs `snap`
  distinction (which has been a duplicate since v1.17.3)

Negative:
- Anyone with `start fast` in their muscle memory or scripts gets a
  "ERROR: unknown target 'fast'" error
- 2 ethstats rows disappear from the dashboard (previously `*-fast-*`
  and `*-fastpath-*`)

Neutral:
- The chaindata directories for any previously-started `fast` /
  `fastpath` nodes remain on disk; operators can delete manually if
  needed

## Migration

Operators with running `fast` or `fastpath` nodes should:

```bash
# Stop the stale node manually (it won't be tracked by the new script)
PID=$(ps -ef | grep "datadir.*fastpath" | grep -v grep | awk '{print $2}')
kill -9 "$PID"
rm -rf nodes-local/fastpath nodes-local/fastpath.*
```

(Already done for xdc02-reset on 2026-05-23.)

## References

- PR #757 — implementation (rolled into the same PR as the genesis-merge fix)
- `scripts/local-sync-test.sh` — current state
