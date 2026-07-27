# Bug hunt — block 82,219,687 stateRoot divergence

## Conclusive findings (8 hypotheses tested experimentally)

| # | Hypothesis | Method | Result |
|---|---|---|---|
| H1 | Hash cache returns wrong/stale values | Disabled `nodeFlag.hash` cache in `trie/hasher.go`, forced fresh computation | **RULED OUT** — preHash still matches canonical 0xf2810fe5 |
| H3 | Trie walker is non-deterministic | Called `trie.Hash()` twice in a row, compared | **RULED OUT** — preHash1 == preHash2 always |
| H11 | Database returns wrong bytes during Update walk | Logged every `resolveAndTrack` call with hash check | **RULED OUT** — 580 calls, all match=true |
| H2 | `*nodeLoc → nodeLoc` semantic change matters | Inspected callers | RULED OUT — pure logging, no consensus impact |
| size>=hashLen | The 32-byte boundary check change | Reverted to `>` (v1.17.0 behavior) | RULED OUT — no effect on divergence |
| H8 | Sender's leaf encoded differently | Verified via eth_getProof + instrumented logging | RULED OUT — bytes byte-identical |
| H9 | Storage root reference encoded differently | Inspected: sender Root = EmptyRootHash | RULED OUT — both versions use same constant |
| H10 | Hidden second mutation | `mutationsCount=1` confirmed in IntermediateRoot log | RULED OUT — only sender |

## Cryptographically proven invariants

- Pre-state trie hash MATCHES canonical (0xf2810fe5) — verified with cache enabled AND disabled
- Single mutation: sender F3CF505896... balance + nonce
- Account RLP bytes IDENTICAL between binaries: `f8508306109a891f4c3110e59f6a5e42a056e8...`
- 580 trie node loads during import — every blob hashes to its expected hash
- All accessed accounts' balances identical between binaries
- `trie/trie.go`, `trie/hasher.go`, `trie/encoding.go`, `trie/committer.go` byte-identical between v1.17.0 and v1.17.3

## The contradiction

Same trie content + same mutation MUST produce the same result hash under standard MPT semantics. The fact that our v1.17.3 binary produces a different result is mathematically impossible UNLESS:

- A v1.17.3-specific code path mutates additional state during the Update walk that we haven't yet instrumented
- The `s.trie` field is wrapped/intercepted by v1.17.3-only logic
- Some Go runtime non-determinism (map iteration, goroutine scheduling) leaks into the hash computation

## What we've verified is NOT the cause

- ❌ EVM execution differs (identical 1153-op traces on both binaries)
- ❌ State diff differs (identical prestateTracer output)
- ❌ Account RLP encoding differs (byte-identical bytes written to trie)
- ❌ Database returns different bytes (every load verified hash-matches)
- ❌ Hash cache is lying (cache-disabled test still matches canonical)
- ❌ Walker is non-deterministic (preHash1 == preHash2)
- ❌ trie/node.go `>=` vs `>` boundary check (reverted, no effect)
- ❌ The XDC overlay (BigBalance, EVM instruction sets, fee handling)
- ❌ Snapshot data corruption (xdc-network commit 7f2da7e5 imports same block correctly from same chaindata)

## What remains to investigate

The bug is in v1.17.3 PBSS-specific code that runs during the Update→Hash chain WITHOUT going through the instrumented points (resolveAndTrack, updateStateObject, IntermediateRoot). Candidates:

1. **`triedb/pathdb/database.go`** — 190 lines diff. May affect what trie reader is wired to.
2. **`triedb/pathdb/verifier.go`** — 305 lines diff. NEW component. May run during commit/import.
3. **`triedb/pathdb/history_indexer.go`** — 247 lines diff. NEW indexer that may write state history.
4. **`core/state/database_mpt.go`** — NEW v1.17.3 file. Replaces v1.17.0's CachingDB. May open trie with different wrapper.
5. **`witness.AddState` mid-walk** — though gated on `s.witness != nil`, double-check.
6. **`stateReadList` collection** — new in v1.17.3 for EIP-7928, may have side effects.

## Recommended next session approach

1. **Add tracer logs in `triedb/pathdb/buffer.go`** — every `buffer.put` / `buffer.commit` call.
2. **Instrument `triedb/pathdb/database.go` Update method** — log every account/storage write.
3. **Instrument any v1.17.3-only goroutine** — concurrent writes may race with the trie hash computation.
4. **Run v1.17.0 binary against the same chaindata side-by-side** — capture every node access via strace, compare disk access patterns.

## Production deployment block

Deployment to mainnet is **blocked** until this bug is resolved. The reset/integration-1.17.3 binary cannot extend the XDC Apothem chain past block 82,219,686 even though all our consensus-overlay changes are demonstrably correct on a per-tx basis.

The parallel fresh syncs (xdc02 + xdc04) syncing from genesis will independently reach this block in ~18-22h and confirm whether the bug is reproducible from a genesis-built state (i.e., deterministic in our binary) or specific to snapshot loading (i.e., something about how PBSS deserializes the snapshot diverges from how v1.17.0 wrote it).

## Branch state

After extensive debug iterations, branch `fix/pathdb-rlp-import` is reset to commit `44cf31cec` (clean state, no debug instrumentation). All debug commits force-pushed away.
