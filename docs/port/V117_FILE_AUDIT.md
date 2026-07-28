# Audit of v1.17.3-only files for issue #798 bug

Detailed read-through of every NEW-in-v1.17.3 file that could plausibly affect the block 82,219,687 stateRoot divergence.

## Audit results

### `core/state/reader_stater.go` — RULED OUT
Pure statistics aggregator. Defines `ContractCodeReaderStats` and `StateReaderStats` with hit-rate calculation. No state mutation. No trie interaction.

### `core/state/reader_eip_7928.go` — RULED OUT
EIP-7928 Block Level Access List reader. Gated on `rules.IsAmsterdam`. XDC doesn't activate Amsterdam, so the wrapper layer is never installed.

Even when `stateReadList.AddAccount(addr)` is called unconditionally in `getStateObject`/`GetCommittedState`, the implementation is nil-safe: `if s == nil { return }` at the top of both `AddAccount` and `AddState`. So calls on uninitialized stateReadList do nothing.

### `core/state/database_mpt.go` — RULED OUT
```go
func (db *MPTDatabase) OpenTrie(root common.Hash) (Trie, error) {
    tr, err := trie.NewStateTrie(trie.StateTrieID(root), db.triedb)
    ...
}
```
Returns standard `trie.NewStateTrie`. The wrapping `MPTDatabase` is just a type-split from v1.17.0's `CachingDB`. Same trie instance, same path-scheme backend.

`MPTDatabase.StateReader` returns a `multiStateReader` combining flat reader (via pathdb.StateReader) + trie reader. For account reads, the flat reader wins (faster), but if both readers return the same account data (which they do for canonical-correct snapshot), no divergence.

`MPTDatabase.Commit` calls `EncodeMPTState` which uses `SlimAccountRLP` (snapshot format). But Commit runs AFTER the failing IntermediateRoot. Not in the failing path.

### `core/state/database_ubt.go` — RULED OUT
UBT (Unbounded Binary Trie / Verkle) database. XDC chains don't activate UBT. Inert.

### `core/state/database_iterator.go` — RULED OUT
State iterator implementation. Only used by `state.NewIterator()` which isn't called during block import.

### `core/state/database_code.go` — RULED OUT
`CodeDB` wraps ethdb access for contract code. The failing tx is an EOA-to-contract call; sender has no code, contract code reads are via `eth.getCode` which goes through this path BUT during the EVM execution (which we verified is identical between binaries).

### `core/types/bal/access_list.go` — RULED OUT
`StateAccessList` for EIP-7928. Nil-safe operations. XDC doesn't activate.

### `core/vm/eip7610.go` — RULED OUT
EIP-7610: revert account creation with non-empty storage. Gated on `rules.IsEIP7610` (Prague). XDC doesn't activate Prague.

### `core/vm/gascosts.go` — RULED OUT
Defines a `GasCosts` struct with `RegularGas` + breakdown fields. Used by `IntrinsicGas` callers in v1.17.3 code but the COMPUTED gas value is the same; just the return shape changed.

### `trie/inspect.go` — RULED OUT
Trie inspection helpers for `cmd/geth db-inspect`. Not in block-import code path.

### `trie/levelstats.go` — RULED OUT
Per-level node counter for diagnostics. Not in block-import code path.

### `triedb/generate.go` — RULED OUT
Generic trie generator helpers. Used by tests + bin-trie. Not in block-import code path.

### `triedb/pathdb/history_index_pruner.go` — INERT (configurable)
State-history pruner. Runs in background; gated on `config.TrienodeHistory >= 0`. Default config might enable, but it only PRUNES history, doesn't affect current state.

### `triedb/pathdb/history_indexer_state.go` — INERT (configurable)
State-history indexer state. Background indexer. Doesn't write to the active state trie.

### `crypto/keccak/*` — RULED OUT
Vendored keccak implementation replacing `golang.org/x/crypto/sha3`. Tested as fix — produced byte-identical hash output. The new vendored impl is correct.

## Conclusion

**All 14 NEW v1.17.3 files audited. None contain the bug** for issue #798.

The bug must be in:
1. A modification to an existing file we haven't deeply diffed (most likely `triedb/pathdb/database.go` — 190 lines diff)
2. The interaction between multiple modified files (e.g., lookup.go's tip resolution + reader.go's layer selection)
3. Some Go runtime non-determinism (concurrent hashing in `IntermediateRoot`'s `workers.Go` block)

## Next investigative steps

1. **Read `triedb/pathdb/database.go` diff line-by-line** — biggest single-file diff (190 lines)
2. **Investigate `triedb/pathdb/lookup.go`** + how `tree.lookupAccount`/`lookupStorage` interacts with the multiStateReader
3. **Check for goroutine race** — `IntermediateRoot` spawns concurrent workers via `errgroup.Group`. If they share mutable state through the trie's `t.root` pointer, race could produce non-deterministic results
4. **Run with `GOMAXPROCS=1`** — disable concurrency. If the bug disappears, it's a concurrency race
5. **Run with race detector**: `go build -race ./...` and re-run the failing import

The race-detector test is the most promising next step.
