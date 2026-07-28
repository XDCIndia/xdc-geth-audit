# LEGACY vs MODERN parity audit

**Audit date**: 2026-06-06
**Auditor**: AgentsOrchestrator (Opus 4.7 1M, autonomous run)
**Legacy ground truth**: `/Users/anilchinchawale/github/XDCNetwork/XDPoSChain/` @ `e4686012d` (v2.4.6-beta1-2384, the production XDPoSChain binary)
**Modern target**: `/Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/` @ `adc74c729` (v1.17.3-pristine-332, branch `feat/a11-fast-sync-peer-gate`)

**Why this exists**: Empirically proven (XDCIndia/go-ethereum#857 comment 4639501859) that modern v1.17.3 starting from genesis with identical bootnodes attaches ZERO peers, while legacy from the same IP attaches 60+. We hypothesise modern is missing one or more XDC-specific peer/handshake/wire behaviours. This file is the systematic file-by-file search for that gap and any other ports that have been overlooked.

---

## Critical findings (read first)

**SMOKING GUN for the empirical peer-attach failure (XDCIndia/go-ethereum#857
comment 4639501859):**

Modern `eth/backend.go:638-651` wraps the discv4 RandomNodes feed in
`enode.AsyncFilter(iter, RequestENR, …)`. The async-filter contract
(`p2p/enode/iter.go:214`) is: "**if check returns nil, the node is dropped**".
`UDPv4.RequestENR` (`p2p/discover/v4_udp.go:357-394`) sends a v4 `ENRRequest`
packet (type 5) and blocks until it receives an `ENRResponse` (type 6) with a
matching ReplyTok.

But legacy XDPoSChain (production mainnet binary) **does not implement
ENRRequest/ENRResponse at all**. Legacy `p2p/discover/udp.go:60-66` defines:

```go
const (
    pingPacket = iota + 1   // 1
    pongPacket              // 2
    findnodePacket          // 3
    neighborsPacket         // 4
    pingXDC                 // 5  -- ENRRequest slot!
)
```

Legacy's `decodePacket` (line 567-579) has no case for ptype 6 (ENRResponse)
and for ptype 5 instantiates `new(ping)` — i.e. legacy treats every type-5
packet on the wire as a Ping and tries to RLP-decode it as one. Modern's
ENRRequest payload is `{Expiration, Rest tail}` (one field). Legacy's
Ping shape is `{Version, From, To, Expiration, Rest tail}` (four fields).
The decode either fails ("rlp: too few elements") or returns garbage.
Either way legacy returns a Pong, not an ENRResponse. Modern's pending
matcher (`p2p/discover/v4_udp.go:371`) waits only for ENRResponsePacket so
it never matches, times out, and returns `nil`. AsyncFilter drops the node.

**Net effect**: every XDC peer that discovery v4 produces is discarded from
the dial pool. With zero candidates, the dial scheduler never opens an
outbound TCP, so no eth handshake ever runs, so net.peerCount stays at 0.
Static-nodes / explicit `--bootnodes` still work because they bypass discmix,
which matches what the empirical experiment shows: modern with explicit
static-nodes can attach, modern without (fresh-control mode) cannot.

**Recommended fix** (sketch — implementation is out of scope for this
audit): in `eth/backend.go:638-651`, replace the `AsyncFilter(iter,
RequestENR, …)` wrapper with a "best-effort ENR resolver" that returns the
**original** node unmodified when RequestENR fails, instead of nil. Concretely:

```go
resolverFunc := func(ctx context.Context, n *enode.Node) *enode.Node {
    nn, _ := s.p2pServer.DiscoveryV4().RequestENR(n)
    if nn == nil {
        return n   // <-- legacy XDC peers don't speak ENRRequest;
                   //     keep them in the pool, let the wire handshake decide
    }
    return nn
}
```

This restores parity with legacy's "no ENR; everyone is a candidate" behaviour.
The subsequent `enode.Filter(iter, eth.NewNodeFilter(s.blockchain))` already
has the XDC-aware fall-through (`discovery.go:75-86`) that accepts ENR-less
peers when `chain.Config().XDPoS != nil`.

(All other findings — full file-by-file table below.)

---

## Package: `p2p/` and `p2p/discover/`

| Legacy file | Modern file | Status | Severity | Notes |
|---|---|---|---|---|
| `p2p/peer.go` | `p2p/peer.go` | DIVERGED-OK | (none — base protocol parity) | Both define `baseProtocolVersion=5`, `baseProtocolLength=16`, `snappyProtocolVersion=5`. `protoHandshake` struct is identical except legacy `ID discover.NodeID` (64-byte) vs modern `ID []byte` (which holds 64 bytes of the raw uncompressed secp256k1 pubkey minus the leading 0x04). RLP wire shape is identical: `{Version, Name, Caps, ListenPort, ID, Rest tail}`. devp2p hello packet is interoperable. |
| `p2p/protocol.go` | `p2p/protocol.go` | DIVERGED-OK | (none) | Modern adds `DialCandidates enode.Iterator` and `Attributes []enr.Entry` fields to the `Protocol` struct; legacy lacks them. These are orchestration-only — don't change wire shape. |
| `p2p/server.go:setupLocalNode` | `p2p/server.go:setupLocalNode` | DIVERGED-OK | (none) | Modern sorts `ourHandshake.Caps` with `slices.SortFunc(..., Cap.Cmp)` (server.go:425). Legacy does not sort (server.go:539-542 just appends). Sorting changes presentation order but devp2p cap negotiation in both nodes scans the list and picks highest-common — no compatibility break. |
| `p2p/rlpx.go` | `p2p/transport.go` + `p2p/rlpx/*` | DIVERGED-OK | (none) | Same RLPx v4 handshake and frame format. Modern moved the actual RLPx primitives into `p2p/rlpx/` subpackage and renamed wrappers (`rlpxTransport`). No wire-level divergence. |
| `p2p/discover/udp.go` | `p2p/discover/v4_udp.go` + `v4wire/v4wire.go` | DIVERGED — interoperable for ping/pong/findnode/neighbors | **P0-peer-attach** | Modern split discovery v4 wire format into `v4wire/` and added EIP-868 ENRRequest/ENRResponse (types 5 & 6). **Both legacy and modern map "ping" to packet type 5** — legacy because that's its only ping kind (`pingXDC`), modern because of `UseXDCPing=true` (`v4wire.go:53`). So an outbound Ping from either side arrives correctly. **However**: modern's `RequestENR` (v4_udp.go:357) also encodes a separate packet structure (`ENRRequest{Expiration}`) with `Kind()==ENRRequestPacket==5`. The wire byte 5 + a 1-field RLP payload is a malformed Ping to a legacy peer (legacy decodes type 5 → `*ping{Version, From, To, Expiration, Rest}`). Combined with the AsyncFilter contract in `eth/backend.go` (see critical finding above) this drops every discv4-found peer. |
| (none — legacy has no ENR) | `p2p/enr/`, `p2p/enode/` | NEW IN MODERN | P0-peer-attach (indirect) | Legacy publishes no ENR (no ENRSeq on ping/pong, no `Record` data type, no `Seq()`). XDC mainnet peers all run the legacy protocol; they cannot answer ENRRequest. Any modern code that **requires** ENR resolution before treating a peer as dial-able will starve. (Concrete instance: `eth/backend.go:638-651`, fixed by treating RequestENR failure as "use original node".) |
| `p2p/discover/table.go` | `p2p/discover/table.go` + `table_reval.go` + `lookup.go` | DIVERGED-OK | P1-sync | Same Kademlia routing table semantics. Modern split out lookup and revalidation. Legacy lacks the live-node revalidation timer and may take longer to evict dead bootnodes, but this is a performance issue not a wire break. |
| `p2p/discover/database.go` | `p2p/enode/nodedb.go` | DIVERGED-OK | P3-ops | Both persist node bonds in LevelDB. Modern's nodedb is hash-keyed and stores ENRs; legacy stores `NodeID → lastPing/lastPong`. Schemas don't conflict since they use different data dirs. |
| `p2p/discover/node.go` | `p2p/enode/node.go` + `p2p/enode/iter.go` | DIVERGED-OK | (none) | Modern adopts enode URLs (with optional ENR appended) but parses bare `enode://pubkey@host:port` URLs identically. XDC bootnode strings in `params/bootnodes.go` work unchanged. |
| `p2p/discv5/` (legacy old-style discv5) | `p2p/discover/v5_udp.go`, `v5_talk.go`, `v5wire/` | DIVERGED — different protocols | P3-ops | Legacy's `discv5` predates EIP-868 ENR-based discv5 and is unused on XDC mainnet (default off). Modern implements canonical discv5 with ENR. XDC mainnet doesn't run discv5; no interop required. |
| `p2p/dial.go` | `p2p/dial.go` | DIVERGED-OK | P1-sync | Modern uses an iterator-driven `dialScheduler` consuming from `discmix`. Legacy uses a simpler ticker-driven `dialer`. Both honour StaticNodes & BootstrapNodes. Static-node dial path is still functional in modern (verified by the static-node attach experiment). |
| `p2p/nat/`, `p2p/netutil/` | `p2p/nat/`, `p2p/netutil/` | PORTED | (none) | Equivalent NAT traversal and netutil helpers. |
| `p2p/message.go`, `p2p/peer_error.go` | `p2p/message.go`, `p2p/peer_error.go` | PORTED | (none) | Equivalent. |

## Package: `consensus/XDPoS/`

| Legacy file | Modern file | Status | Severity | Notes |
|---|---|---|---|---|
| `XDPoS.go` | `xdpos.go` | DIVERGED-OK | (none) | Same entry-point dispatch (v1 / v2 by SwitchBlock). Naming difference cosmetic. |
| `api.go` | `api.go` + `xdpos_api.go` + `api_events.go` + `api_extended.go` | DIVERGED | P2-rpc | Modern split RPC into multiple files and added extra APIs (events, extended). Need to verify each XDC-specific RPC method (xdc_getMasternodes, xdc_getEpoch, xdc_getRewards) returns shape-identical responses. Not yet audited; flagged for RPC parity harness. |
| `verify_chain_reader.go` | (consolidated into engine.go and utils.go) | DIVERGED-OK | (none) | Function-equivalent. |
| `engines/engine_v2/engine.go` | `engines/engine_v2/engine.go` | DIVERGED-OK | (none for consensus, P1 for catchup) | Same Prepare/Finalize semantics. Constant difficulty=1. CalcDifficulty signature differs (modern adds time param to match newer consensus.Engine interface). |
| `engines/engine_v2/difficulty.go` | (moved into engine.go) | PORTED | (none) | calcDifficulty body identical: returns big.NewInt(1). |
| `engines/engine_v2/verifyHeader.go` | `engines/engine_v2/verifyHeader.go` | DIVERGED | **P0-consensus** (latent) + P1-sync | TWO divergences: (1) Modern adds `inCheckpointCatchup` QC-skip branch (lines 105-112). Legacy always calls verifyQC. Justification: checkpoint-trusted sync. Scope: only active when `GetTrustedCheckpointAnchor` is set. Severity P1-sync if confined to fast-sync bootstrap, but could mask malicious QCs if catchup window is reachable outside that mode. (2) Modern wraps `eip1559.VerifyEIP1559Header` in `if chain.Config().IsEIP1559(header.Number)` (line 127). Legacy calls unconditionally, relying on the function's internal pre-fork guard that rejects pre-fork blocks with non-nil BaseFee. Modern accepts pre-fork BaseFee!=nil silently — a *consensus-permissive* divergence (could accept malformed pre-fork blocks legacy would reject). P0-consensus by strict definition; practical impact zero on canonical chains since no pre-fork canonical block has BaseFee. |
| `engines/engine_v2/utils.go::sigHash` | `engines/engine_v2/engine.go::sigHash` | PORTED | (none) | **Byte-identical RLP encoding** of 17 fields (18 with BaseFee). Block-hash parity confirmed. |
| `engines/engine_v2/utils.go::ecrecover` | `engines/engine_v2/engine.go::ecrecover` | PORTED | (none) | Same secp256k1 signer recovery from `header.Validator` (65 bytes). |
| `engines/engine_v2/snapshot.go` | `engines/engine_v2/snapshot.go` | DIVERGED | **P0-consensus** (latent) | Modern's `SnapshotV2` adds `Version` (default 4) and `Penalties` ([]common.Address) fields. JSON tags use `omitempty` so on-disk format remains compatible with v2.6.8-written snapshots. However the *semantic* divergence is real: modern's `CandidatePool()` returns `masternodes ∪ penalties`, vs legacy which only exposes `NextEpochCandidates`. If HookPenalty (or any masternode-set selector at epoch boundary) consumes `CandidatePool()` on modern but only `NextEpochCandidates` on legacy, the two nodes could compute *different* masternode rotations at the same epoch switch — P0-consensus. Needs dedicated audit: trace every reader of `Snapshot.NextEpochCandidates` (legacy) vs `Snapshot.CandidatePool` (modern), confirm logical equivalence at each call site. |
| `engines/engine_v2/mining.go` | `engines/engine_v2/mining.go` | UNAUDITED | P0-consensus (potential, if running validator) | M8 territory: only matters if running as masternode. Out of M5/M6 scope. |
| `engines/engine_v2/epochSwitch.go` | `engines/engine_v2/epochSwitch.go` | UNAUDITED | P0-consensus (potential) | Critical to audit before declaring epoch-boundary parity. |
| `engines/engine_v2/forensics.go` | `engines/engine_v2/forensics.go` | PORTED | P2-rpc | Forensic detection of equivocating votes — operational, not consensus-critical for sync. |
| `engines/engine_v2/timeout.go` | `engines/engine_v2/timeout.go` | UNAUDITED | P1-sync | Timeout protocol — affects liveness, not safety. |
| `engines/engine_v2/vote.go` | `engines/engine_v2/vote.go` | UNAUDITED | P1-sync (only for masternode operation) | |
| `engines/engine_v2/testing_utils.go` | (none) | MISSING | (none) | Test-only helpers; absence is fine. |
| (none) | `engines/engine_v2/snap_pivot.go`, `snap_pivot_test.go` | NEW IN MODERN | (none — fast-sync addition) | Snapshot-based pivot for fast sync; XDC-port specific. |
| `engines/engine_v1/*` | `engines/engine_v1/*` | UNAUDITED | P0-consensus (pre-V2 blocks) | Engine v1 verifies the pre-XDPOSv2 chain (blocks 1..SwitchBlock). XDC Apothem and mainnet both already passed SwitchBlock, but a node syncing from genesis MUST validate v1 blocks correctly. Needs dedicated audit. |
| (none) | `network_constants.go`, `events.go`, `genesis_validate.go`, `penalty.go`, `reward.go`, `constants.go`, `contracts.go` | NEW IN MODERN | mixed | XDC-port additions providing event types, network IDs, hardcoded validation. Need to verify reward.go and penalty.go match legacy reward calculation byte-for-byte. **Reward calculation parity is P0-consensus** because it determines state-trie post-state. |
| `engines/engine_v2/utils.go` | `engines/engine_v2/utils.go` | DIVERGED-OK | P1-sync | Modern adds `inCheckpointCatchup` + extra helpers. Legacy code paths preserved. |

## Package: `params/`

| Legacy file | Modern file | Status | Severity | Notes |
|---|---|---|---|---|
| `config.go` + `config_networks.go` + `config_forks.go` | `config.go` | DIVERGED | **P0-consensus** | See detailed finding below. |
| `bootnodes.go` | `bootnodes.go` | PORTED | (none for attached operators) | Modern keeps Ethereum-mainnet bootnodes as fallback when no XDC flag is set. `XDCMainnetBootnodes` and `XDCApothemBootnodes` are populated and selected when `--xdc-mainnet` / `--xdc-apothem` / `--networkid 50` / `--networkid 51` is passed. Operators using the documented flags get correct bootnodes; bare `geth init && geth` with no flag would attempt Ethereum mainnet — flagged as P3-ops UX. |
| Legacy `MainnetGenesisHash`, `XDCMainnetGenesisHash`, etc. | Modern same | UNAUDITED | P0-consensus (if mismatch) | Need to confirm `XDCMainnetGenesisHash` in modern equals genesis block hash legacy produces. |

### `params/` P0-consensus finding: Constantinople/Petersburg activation block mismatch — *DEMOTED to P3-ops after deeper analysis*

**Re-audit conclusion**: this finding was initially flagged as P0-consensus
but a careful trace of all callers of `IsPetersburg` and `IsConstantinople`
shows the SSTORE gas decision (`core/vm/gas_table.go:109`) evaluates
**identically** on legacy and modern at every block range:

| Block range | Legacy IsConst / IsPet | Modern IsConst / IsPet | gas_table.go:109 result |
|---|---|---|---|
| 4..38M | false / false | false / false | `false ‖ !false = true` → legacy gas |
| 38M..76M | false / true | false / false | both: `true ‖ true = true` → legacy gas (same) |
| 76M+ | false / true | true / true | both: `true ‖ ... = true` → legacy gas (same) |

So the SSTORE gas pricing is identical in practice. The structural
difference (different `IsConstantinople(num)` return at 76M+) is observable
via the `Rules` struct, but no consensus-touching call site we inspected
distinguishes these cases beyond the SSTORE pricing one above.

**Demoted to P3-ops** (the comment / implementation mismatch is still worth
fixing for code clarity, but it doesn't fork the chain in practice).

The original analysis is retained below as it documents the data, but the
severity downgrade applies.

---

**Modern `params/config.go:209-211`** sets:
```go
ConstantinopleBlock: big.NewInt(76321000)
PetersburgBlock:     big.NewInt(76321000)
IstanbulBlock:       big.NewInt(76321000)
```

The accompanying comment (line 197-199) states: "Constantinople/Petersburg/Istanbul
are set to BerlinBlock so they pass `CheckConfigForkOrder` while the XDC-specific
TIPXDCXCancellationFee fallback in IsIstanbul/IsPetersburg activates them earlier
(at block 38,383,838 on mainnet) — matching canonical behaviour."

**The implementation does not match the comment** for `IsPetersburg`:

```go
// modern config.go:2524
func (c *ChainConfig) IsPetersburg(num *big.Int) bool {
    return isBlockForked(c.PetersburgBlock, num) ||
           c.PetersburgBlock == nil && isBlockForked(c.ConstantinopleBlock, num)
}
```

There is **no** `TIPXDCXCancellationFee` fallback in `IsPetersburg`. Only
`IsIstanbul` (line 2529-2531) has the fallback.

**Legacy `params/config_forks.go:66-68`** by contrast:
```go
func (c *ChainConfig) IsPetersburg(num *big.Int) bool {
    return isForked(c.TIPXDCXCancellationFeeBlock, num) || isForked(c.PetersburgBlock, num)
}
```

→ Returns true for any block ≥ 38,383,838.

**Consequence**: at block range 38,383,838 .. 76,320,999, legacy says
`IsPetersburg == true` but modern says `IsPetersburg == false`. Petersburg's
delta vs Constantinople is the EIP-1283 net-gas-metering rollback for SSTORE.
**Any block in this 37.9-million-block window that contains an SSTORE
operation will produce a different state root on modern vs legacy.** That is
direct P0-consensus failure — modern will fork off mainnet at the first
SSTORE-touching block after 38,383,838.

Additionally `IsConstantinople(num)` returns false on modern for blocks
38383838..76320999, while legacy with `ConstantinopleBlock=nil` also returns
false. So this specific helper agrees. The break is in Petersburg only.

**Recommended fix sketch**: in modern `params/config.go:2521-2526`, change
IsPetersburg to mirror legacy's logic:

```go
func (c *ChainConfig) IsPetersburg(num *big.Int) bool {
    return isBlockForked(common.TIPXDCXCancellationFee, num) ||
           isBlockForked(c.PetersburgBlock, num) ||
           (c.PetersburgBlock == nil && isBlockForked(c.ConstantinopleBlock, num))
}
```

(The third clause preserves upstream Ethereum semantics for non-XDC configs.)

**Verification**: parity harness on a block 38M..76M containing SSTORE
should produce matching state roots. Without this fix, M4/M5 acceptance
(Apothem/mainnet sync) **cannot pass** beyond block 38,383,838.

**Also recommended audit**: cross-check every other `Is*` helper in modern
config.go against legacy config_forks.go for similar XDC-specific fallbacks
that may have been missed:
- `IsConstantinople` — legacy returns true at 38M? Currently legacy returns
  false at 38M (since ConstantinopleBlock=nil and no fallback). But the EVM
  pricing pre-Petersburg is "Constantinople" (with EIP-1283). Verify
  semantics: does legacy actually use Constantinople EVM at block 38M+?
  Need to grep `IsByzantium` / pricing tables to be sure.
- `IsTIPRandomize`, `IsTIPSigning`, `IsTIPNoHalvingMNReward`, etc. —
  every TIP* fork needs cross-check.
- `MergeBlock`, `ShanghaiBlock`: legacy sets both to 76321000 (Berlin
  block); modern's XDC config does not set them at all in the snippet read.
  Could affect post-Berlin behaviour.

---

### `consensus/XDPoS/` follow-on audits required (not P0 for peer-attach, but blocking M5/M6)

1. **Snapshot semantic audit**: enumerate every reader of `SnapshotV2.NextEpochCandidates` (legacy) and every reader of `SnapshotV2.CandidatePool` (modern). At each site, confirm legacy semantics ≡ modern semantics when `Penalties` is empty (which is the v2.6.8-written case).
2. **Reward / penalty parity**: diff legacy `XDPoSChain/consensus/XDPoS/engines/engine_v2/utils.go` (reward functions) against modern `consensus/XDPoS/reward.go` / `penalty.go`. Any difference is P0-consensus.
3. **Engine v1 parity**: verify pre-SwitchBlock validation produces identical state-trie / receipts. Use parity harness on blocks 1..SwitchBlock.
4. **EIP1559 pre-fork strict mode**: align modern's `verifyHeader` to call `eip1559.VerifyEIP1559Header` unconditionally (relying on its internal pre-fork BaseFee==nil check), matching legacy. Removes the permissive divergence.

---

## Package: `eth/protocols/eth/`

| Legacy file | Modern file | Status | Severity | Notes |
|---|---|---|---|---|
| `eth/protocol.go` (combined) | `eth/protocols/eth/protocol.go` | DIVERGED-OK | (none) | Both declare `ProtocolName="eth"`, `XDPOS2=100`, `eth/63=63`. Modern adds `ETH69`, `ETH70`. Modern's `ProtocolVersions=[XDPOS2, ETH70, ETH69, ETH63]` advertises ETH63 (refs A.59) so XDC mesh peers accept the connection. Devp2p cap negotiation picks highest-common = XDPOS2/100 with any legacy peer. **Wire-protocol parity confirmed.** |
| (in `eth/protocol.go`) | `eth/protocols/eth/protocol.go` | DIVERGED-OK | (none) | Message codes 0x00..0x10 identical between legacy and modern XDPOS2 path. XDC trading-tx codes (0x08 OrderTxMsg / 0x09 LendingTxMsg) handled as no-ops on modern (refs A.59). VoteMsg=0xe0, TimeoutMsg=0xe1, SyncInfoMsg=0xe2 identical. |
| (in `eth/handler.go`) | `eth/protocols/eth/handshake.go` | DIVERGED-OK | (none) | Modern's `handshake62` reproduces the legacy 5-field StatusPacket62 `{ProtocolVersion, NetworkID, TD, Head, Genesis}` exactly. No ForkID on XDPOS2. localXDCCaps trailer is **explicitly not sent** on the wire to preserve legacy decoder compat. Read confirmed at handshake.go:132-136. |
| (in `eth/handler.go`) | `eth/protocols/eth/handler.go` | DIVERGED-OK | (none) | Modern preserves XDPOS2 message routing via the `xdpos2` handlers map (handler.go:207-247). NewBlockHashes/NewBlock/OrderTx/LendingTx XDC routes all wired. |
| (in `eth/handler.go`) | `eth/protocols/eth/discovery.go` | NEW IN MODERN | (none, see fix above) | `NewNodeFilter` already has the XDC pass-through for ENR-less peers; the gap is one layer up in `eth/backend.go`'s `AsyncFilter(RequestENR, …)` (see Critical Findings). |
| `eth/protocol.go::StatusPacket` | `eth/protocols/eth/protocol.go::StatusPacket62` | PORTED | (none) | RLP shape identical. |

## Package: `eth/` (handler + sync glue)

| Legacy file | Modern file | Status | Severity | Notes |
|---|---|---|---|---|
| `eth/handler.go` | `eth/handler.go` + `handler_eth.go` + `handler_snap.go` + `handler_xdc.go` | DIVERGED-OK | (none for protocol) | Modern split handler into eth/snap/xdc submodules. peer registration semantics preserved. |
| `eth/peer.go` | `eth/peer.go` + `peerset.go` | DIVERGED-OK | (none) | Equivalent peer-management abstractions. |
| `eth/sync.go` | `eth/syncer/`, `eth/handler_eth.go` | DIVERGED | P1-sync | Modern uses BeaconSync + Snap sync paradigms; legacy uses simpler chainSyncer. XDC-port adds `sync_xdc.go` to glue XDC's pre-merge sync onto modern's beacon-style downloader. Operator-facing impact is bigger but no consensus risk. |
| `eth/protocol.go` | (split, see above) | DIVERGED-OK | (none) | |
| `eth/backend.go` | `eth/backend.go` | DIVERGED | **P0-peer-attach** (the AsyncFilter wrapper at backend.go:638-651 — see Critical Findings) | |
| `eth/api*.go` | `eth/api*.go` (split) | DIVERGED | P1-rpc / P2-rpc | Equivalent RPC methods; need per-method audit. |

## Package: `eth/downloader/`

| Legacy file | Modern file | Status | Severity | Notes |
|---|---|---|---|---|
| `downloader.go` | `downloader.go` + many new files | DIVERGED-MAJOR | P1-sync | Modern is fully merged-era downloader (BeaconSync) with substantial XDC adaptations (gap_pivot_xdc.go, BeaconSync pivot pinning for XDPOS2). Out of scope for peer-attach audit. Heavy ongoing work in this area (issue #857 thread). |
| `peer.go`, `queue.go`, `statesync.go`, `resultstore.go`, `metrics.go`, `events.go`, `api.go`, `modes.go`, `types.go` | same names | DIVERGED-OK | P1-sync | Function-equivalent at API surface; internals modernised. |
| (none) | `beaconsync.go`, `skeleton.go`, `fetchers*.go`, `gap_pivot_xdc.go`, etc. | NEW IN MODERN | P1-sync | Upstream beacon-era sync infrastructure + XDC adaptations. |

## Package: `core/`

Not deeply audited in this pass (orthogonal to peer-attach). Modern's `core/` is substantially rebased on upstream go-ethereum v1.17.3 with XDC-specific shims for state/receipts. Critical follow-on audit items:

| Concern | Legacy | Modern | Severity | Notes |
|---|---|---|---|---|
| `core/types/block.go::Header.Validators / Penalties / Validator` | XDC-specific fields preserved | XDC-specific fields preserved | P0-consensus | Confirmed via sigHash inspection (both include header.Validators and header.Penalties in RLP-for-signing). Block-hash parity. |
| `core/types/receipt.go` | Legacy XDC receipt fields | Modern receipt fields | P0-consensus | UNAUDITED. Receipt-trie-root mismatch noted as next gap in `MEMORY.md` (A.60 handoff). |
| `core/tx_pool.go` | Legacy txpool | Modern txpool | P1-sync | Heavy upstream changes. |
| `core/genesis.go` / `core/genesis_alloc.go` | Legacy XDC genesis allocation | Modern XDC genesis allocation | P0-consensus | UNAUDITED. Need to confirm allocation maps byte-identical for XDC mainnet, otherwise GenesisHash mismatches. (GenesisHash field is asserted equal — so probably OK.) |

## Package: `cmd/`

| Legacy | Modern | Status | Severity | Notes |
|---|---|---|---|---|
| `cmd/XDC/` | `cmd/geth/` | DIVERGED-OK | P3-ops | Modern renamed binary to `geth` but kept `clientIdentifier = "XDC"` (cmd/geth/main.go:50). Both expose `--xdc-mainnet` / `--xdc-apothem` chain flags. |
| `cmd/XDC/main.go` | `cmd/geth/main.go` | DIVERGED-OK | P3-ops | clientIdentifier="XDC" preserved. |
| `cmd/utils/flags.go` | `cmd/utils/flags.go` | DIVERGED-OK | P3-ops | Modern adds the XDC chain flags and routes them to `XDCMainnetBootnodes` / `XDCApothemBootnodes`. Network-id 50/51 also routes correctly. |

## Package: other

| Concern | Legacy | Modern | Severity | Notes |
|---|---|---|---|---|
| `XDCxlending/` and `XDCx/` (XDC DEX / lending protocol) | Full implementation | NOT-PORTED (handled as no-op) | P1-rpc | Modern intentionally accepts OrderTxMsg/LendingTxMsg from wire but does not maintain the order book or matching engine. Sufficient for sync-only operation; would block running as a DEX-serving validator. |
| `internal/ethapi/`, `accounts/abi/bind` | Standard go-ethereum | Standard go-ethereum | (none) | Largely upstream. |

---

### `p2p/` package: P0 fix sketch (peer-attach)

**Path**: `eth/backend.go:638-651` (function: `setupDiscovery` or equivalent; the
block that builds the discv4 iterator chain).

**Fix sketch**: change the `AsyncFilter`'s resolver function so that
`RequestENR` failure returns the **original** node instead of nil. This restores
parity with the legacy "no ENR; everyone is a candidate" model and only relies
on the existing eth-handshake / `eth.NewNodeFilter` filter for compatibility.
The XDC fall-through in `discovery.go:77` already accepts ENR-less peers when
`chain.Config().XDPoS != nil`, so the downstream filter chain is already
prepared for ENR-less candidates.

**Secondary fix candidate**: in `eth/backend.go`, consider gating the
`AsyncFilter(RequestENR)` wrapper entirely on a non-XDC chain config — the
upstream-Ethereum-style ENR resolution is cosmetic for XDC, since XDC's
handshake never reads the ENR's `eth` entry anyway (it uses StatusPacket62 with
no ForkID — `eth/protocols/eth/handshake.go:126`).

**Verification**: after the fix, fresh-control node should attract peers from
discv4 alone (no explicit static-nodes), matching the legacy XDPoSChain baseline.

---

## Methodology

For each legacy file we identify the modern equivalent (rename-aware), compare functions/types/constants/behaviours, and record:

- **Status**: PORTED / PARTIAL / MISSING / DIVERGED
- **Severity**:
  - **P0-consensus** — affects block/state hash output
  - **P0-peer-attach** — affects whether nodes connect at all (current showstopper)
  - **P1-sync** — affects sync correctness or speed after attach
  - **P1-rpc** — affects RPC parity for consumers
  - **P2-rpc** — affects ops/debug RPCs
  - **P3-ops** — operator UX / docs / CLI

Findings grouped by package, in priority order: `p2p/`, `consensus/XDPoS/`, `eth/protocols/eth/`, `eth/handler*.go`, `eth/downloader/`, `core/`, `params/`, `cmd/`, others.

---

## Executive Summary

### Counts by severity

(Counts are findings explicitly recorded above. Several packages have UNAUDITED entries that may surface additional findings on deeper inspection.)

- **P0-consensus** (latent or confirmed): 3 (after demoting one finding on re-audit)
  1. ~~`params/config.go::IsPetersburg` missing TIPXDCXCancellationFee fallback~~ — **DEMOTED to P3-ops** on re-audit: while the implementation does not match the comment, the only consensus-touching caller (SSTORE gas pricing) evaluates identically because of the `IsPetersburg || !IsConstantinople` clause structure. See body for details. Still worth fixing for code clarity.
  2. `consensus/XDPoS/engines/engine_v2/snapshot.go::SnapshotV2.Penalties` semantic divergence — modern's `CandidatePool()` returns masternodes ∪ penalties; legacy returns only masternodes. Could change validator set at epoch boundary if any caller uses CandidatePool (LATENT — needs call-site audit)
  3. `consensus/XDPoS/engines/engine_v2/verifyHeader.go` EIP1559 pre-fork permissiveness — modern silently accepts pre-fork BaseFee≠nil that legacy would reject (LATENT — practical impact zero on canonical chains but consensus surface area divergence)
  4. `consensus/XDPoS/engines/engine_v2/verifyHeader.go` inCheckpointCatchup branch — modern can accept blocks legacy would reject during catchup window. Scope-bounded by checkpoint-trusted sync mode (LATENT — only reachable in that mode)
- **P0-peer-attach**: 1 (the AsyncFilter / RequestENR drop — see Critical Findings)
- **P1-sync**: ~10 (downloader rewrite, BeaconSync, fast-sync code paths)
- **P1-rpc**: 2 (XDC DEX endpoints not ported; per-method audit pending)
- **P2-rpc**: 1 (debug/forensics RPC variance)
- **P3-ops**: 3 (default bootnodes fall through to Ethereum mainnet without XDC flag; CLI naming `geth` vs `XDC`; etc.)

### Top 10 P0 findings ranked by impact on the empirical peer-attach failure

1. **AsyncFilter / RequestENR drops every discv4-found peer** (`eth/backend.go:638-651` → `p2p/enode/iter.go:214` → `p2p/discover/v4_udp.go:357`). Legacy XDC peers don't speak ENRRequest/ENRResponse so RequestENR always times out and returns nil; AsyncFilter then **drops the node**. With zero candidates, the dial scheduler never dials, no eth handshake runs, net.peerCount stays 0. **This is THE smoking gun.** Fix sketch in body. Static-nodes / explicit `--bootnodes` bypass the filter and still attach, which matches the experimental observation.

The next 9 are not directly blocking peer-attach but block subsequent acceptance gates (M5/M6/M9). Listed in roughly decreasing severity for full sync:

2. **~~`IsPetersburg` activation block divergence~~** — DEMOTED. On re-audit, the SSTORE gas-table expression evaluates identically in both implementations. See body for the corrected analysis. Replacing this slot:
   **`consensus/XDPoS/reward.go` / `penalty.go` parity vs legacy reward calculation** — UNAUDITED. Any divergence here is P0-consensus because it changes post-state balances at epoch reward distribution.
3. **`SnapshotV2.Penalties` semantic divergence** — potential validator-set fork at epoch boundaries. Needs call-site audit to confirm severity.
4. **`eth/backend.go` fall-through to upstream Ethereum behaviour for non-XDC chains** — operators who omit `--xdc-mainnet` and don't pass `--networkid 50` get Ethereum bootnodes, fail to join the XDC mesh. Easy operator footgun.
5. (slot taken by #2) — `core/types/Header.UncleHash` constant — UNAUDITED. Both define `UncleHash` in `utils/utils.go` (or equivalent). Worth confirming both compute the same RLP-empty-list keccak (`0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347`) and that legacy and modern blocks all use this constant.
6. **Engine v1 (pre-V2 switch) verifyHeader and seal recovery** — UNAUDITED. Required for syncing the chain segment 0..80,370,000.
7. **`core/types/receipt.go` XDC-specific receipt fields** — MEMORY.md A.60 handoff cites receipt-trie-root mismatch as the next gap. Not directly peer-attach but P0-consensus for full sync.
8. **`core/genesis_alloc.go` XDC mainnet genesis allocation** — UNAUDITED. GenesisHash equality is checked numerically (matches legacy), so allocation is likely byte-equivalent, but worth direct diff for confidence.
9. **`consensus/XDPoS/engines/engine_v2/verifyHeader.go::inCheckpointCatchup`** — bounded but still a deliberate consensus-rule divergence. Document as ADR.
10. **`consensus/XDPoS/engines/engine_v2/verifyHeader.go` EIP1559 unconditional VerifyEIP1559Header call** — trivial fix to align with legacy's permissive-rejection semantics. Cosmetic but tightens the consensus surface.

### Confidence in the peer-attach fix

**High.** The AsyncFilter/RequestENR drop is a code-traceable, deterministic
filter chain that drops 100% of discv4 candidates given a legacy-only peer
network. The empirical observation (static-nodes attach; discovery-only does
not) is exactly what this code path predicts. The recommended fix is a 5-line
change in `eth/backend.go` and the supporting infrastructure
(`NewNodeFilter` XDC fall-through) is already in place.

After applying the fix, fresh-control mode should attract peers from discv4
alone within a few minutes (matching legacy's behaviour). If it still doesn't
attach, the next candidates to investigate are (in order):

- Modern's `enode.NewBufferIter(iter, discoveryPrefetchBuffer)` wrapper — does it pre-buffer so aggressively that XDC peers (which are infrequently rotated in the DHT) starve?
- Modern's `enode.Filter(iter, NewNodeFilter(...))` — confirm the XDC fall-through (`xdcChain := chain.Config().XDPoS != nil`) is actually setting `chain.Config().XDPoS` at the moment this filter is constructed (i.e. before chain config is loaded?).
- Modern's discv4 bond timer / pingInterval — are we evicting bootnodes too aggressively because legacy bonds are not periodically re-pinged by them?

### Cross-cutting observations

- Modern is much further along than naive inspection would suggest. The XDC-specific peer-attach work (UseXDCPing, ETH63 cap advertisement, StatusPacket62, localXDCCaps, NewNodeFilter XDC fall-through) is all already in place. The peer-attach gap is one filter wrapper deep, which is why empirical testing was needed to find it.
- The catalyst-era / beacon-era / merge-era go-ethereum refactor has pulled the modern codebase a long way from legacy. Many "DIVERGED" findings above are upstream-mandated, not XDC issues. Re-merging vs upstream is M9 territory and out of scope here.
- Test coverage for the XDC-specific paths is uneven. The parity harness (Phase 3 of CLAUDE.md) is the right vehicle to lock down the open P0-consensus items.

---

**End of audit. Authored autonomously without modifying either codebase.**


