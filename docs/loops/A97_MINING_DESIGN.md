# A.97-M: XDPoS V2 Block-Production (Minting) Design — Opus brief (escalation 5)

I have everything needed. Here is the complete design brief.

---

# DESIGN: XDPoS V2 Block-Production (Minting) for geth-XDC fork

## Executive correction to the gap model — TWO additional gaps found

The fleet-review M1-M5 model is correct but **incomplete**. M4's premise ("the existing handler's vote/timeout/syncInfo message paths are already working — this node validates today") is **only half true**. This node validates *inbound, inserted* blocks (header verification works), but it does **not participate in the BFT round-trip in either direction**. Two more gaps must be closed or no minted block will ever be committed:

- **M4b (BFT dispatch unwired) — FATAL.** `eth/bft/handler.go` (the `Bfter`) is fully ported but **never constructed** — there is no `bft.New(` call anywhere in non-test code. Inbound `VotePacket`/`TimeoutPacket`/`SyncInfoPacket` are explicitly "accepted, no dispatch" no-ops at `eth/handler_eth.go:90-98`. The engine's outbound `engine_v2.BroadcastCh` (produced at `engine.go:2249` via `broadcastToBftChannel`) **has no consumer**. So: this node neither casts votes nor processes peer votes. A minted block would sit at the proposer with no QC ever forming.
- **M4c (no outbound block gossip) — FATAL.** There is **no `NewBlockMsg` send** anywhere (grep: only the message-code definition and the *inbound* handler at `handler_eth.go:118` exist). Peers only learn of new tips via `BlockRangeUpdatePacket` (every 32 blocks, `eth/handler.go:746`) or by directly receiving a `NewBlock` someone else sent. If our proposer never gossips its sealed block via `NewBlockMsg`, other validators never see it to vote on it.

**Both M4b and M4c are pre-existing latent gaps independent of minting** — but they are on the critical path to a *committed* minted block. They were invisible because a pure sync/validator node receives already-committed blocks (with QCs embedded in child headers' Extra) and never needs to vote.

**Good news that shrinks M1/M3:** the consensus engine layer is already ported and far more complete than the gap table implies. `engine_v2` already has `New(...minePeriodCh, newRoundCh)`, `Authorize`, `Seal` (synchronous), `Prepare`, `Finalize`, `YourTurn`, `FindParentBlockToAssign`, `setNewRound`, `UpdateParams`, `ProposedBlockHandler`. The `XDPoS` wrapper already has `Authorize` with 3-arg→2-arg SignerFn adaptation (`xdpos.go:1477-1494`) and a sync→async `Seal` adapter (`xdpos.go:1306-1328`). So M3 is mostly "call the existing Authorize," and the worker can reuse the engine as-is.

---

## Q1. ARCHITECTURE — what ports, what adapts (old→new API map)

The fork's `miner/` is upstream post-merge geth: `Miner` is a payload-builder (`miner/miner.go:69-91`, `miner/worker.go:133 generateWork`). It has **no `Start()`, no `update()` loop, no `wait()`, no mux, no engine-channel consumption, no `Seal` call.** It is driven only on-demand by `getPending()`/`BuildPayload` (the Engine API path). This confirms M1.

**Decision: do NOT rewrite the payload-builder `Miner`. Add a parallel XDPoS production loop that coexists with it.** The payload-builder is harmless when idle (only invoked by `engine_*` RPC, which XDC doesn't drive). Porting the XDPoSChain `worker` wholesale would collide with the fork's newer txpool/state/1559 APIs. Instead, build a thin `miner/xdpos_worker.go` that reuses the fork's *existing* `prepareWork`/`fillTransactions`/`AssembleBlock` primitives for block assembly, and adds only the XDPoS loop + seal + insert + gossip + BFT-self-vote.

### Component-by-component port map

| XDPoSChain component | file:line (XDPoSChain) | Disposition in fork | fork file:line / new API |
|---|---|---|---|
| `worker.update()` round/period loop | `miner/worker.go:270-382` | **PORT** (core of M1) — the timer + `MinePeriodCh`/`NewRoundCh`/`chainHeadCh` select | new `miner/xdpos_worker.go`; consumes channels via new engine accessors (Q2) |
| `getResetTime` | `miner/worker.go:384-395` | **PORT verbatim** (pure timing fn) | new file; uses `chain.CurrentBlock().Time` (fork: `Header.Time uint64`, not `*big.Int`) |
| `worker.commitNewWork()` | `miner/worker.go:600-...` | **ADAPT** — keep the gating (FindParentBlockToAssign, YourTurn, Prepare), replace block assembly with fork primitives | see below |
| `FindParentBlockToAssign(chain, block)` | XDPoSChain engine | **API CHANGE** → fork takes `FindParentBlockToAssign(chain)` only (`engine_v2/engine.go:2311`); parent derived from `highestQuorumCert`. Call `xdposEngine.EngineV2.FindParentBlockToAssign(cr)` — needs to be exposed on the wrapper |
| `YourTurn(chain, parent, coinbase)` | XDPoSChain | **REUSE** — wrapper has it: `xdpos.go:1515` returns `(int,int,int,bool,error)`; the bool (4th) is "isMyTurn" |
| `engine.Prepare(chain, header)` | — | **REUSE** — wrapper `xdpos.go:1003` dispatches to `EngineV2.Prepare` which sets Extra (Round+QC), Difficulty, Validators/Penalties at epoch switch, Time, and checks `header.Coinbase == signer`. **Returns `utils.ErrNotReadyToPropose`/`ErrNotReadyToMine` — must be treated as "skip this round," not fatal** (mirrors XDPoSChain's `consensus.ErrNotReadyToPropose` handling at `worker.go:669`) |
| block body fill / `commitTransactions` | `worker.go:commitTransactions` + TRC21 fee capacity + special txs + XDCx/lending | **ADAPT to fork** — fork's `fillTransactions`/`commitTransactions` (`miner/worker.go:430-616`) handle 1559/4844/blob/prio. **NON-GOAL: XDCx/XDCxLending/order-pool matching** (the fork has no `GetXDCX`/`OrderPool` — those are XDC-DEX features out of scope for mainnet validator minting). Use plain `fillTransactions`. |
| `Finalize` (rewards) | XDPoSChain `Finalize` | **REUSE, do NOT re-apply rewards** — see Q7. The fork applies rewards inside `state_processor.go Process` (via `SetParentState` at `state_processor.go:220` + `HookReward` in wrapper `Finalize` at `xdpos.go:1200`). `engine_v2.Finalize` (`engine.go:630`) fires `HookReward` only at epoch switch and computes `header.Root`. The block-assembly path must call `FinalizeAndAssemble`-equivalent exactly once. |
| `engine.Seal(chain, block, results, stop)` (async push) | XDPoSChain | **API CHANGE** → fork wrapper `Seal(chain, block, results, stop)` exists (`xdpos.go:1306`) and internally adapts to V2's **synchronous** `EngineV2.Seal(cr, block, stop) (*types.Block, error)` (`engine_v2/engine.go:660`). The worker can either (a) call wrapper `Seal` with a `results` channel, or (b) call `EngineV2.Seal` directly for a synchronous return. **Recommend (b) synchronous** — simpler, no CpuAgent, matches V2 semantics. The seal writes `header.Validator = signature` (`engine_v2/engine.go:683`). |
| `worker.wait()` — write block + post NewMinedBlockEvent + HandleProposedBlock + tx-sign | `worker.go:397-504` | **REPLACE** with: `blockchain.InsertChain([block])` → gossip via `NewBlockMsg` (M4c) → `HandleProposedBlock` (M4) → BFT self-vote path. The fork has no `WriteBlockWithState` mux path; use `InsertChain`. |
| `worker.update()` mux downloader gate | `miner/miner.go:85-106` | **ADAPT** — fork uses `eth.Synced()` / `downloader` state, not mux `StartEvent/DoneEvent`. See Q5. |
| CpuAgent (`agent.go`, `remote_agent.go`) | — | **DROP** — V2 seal is synchronous; no PoW agent abstraction needed. |

### Field/type API deltas the porter will hit
- `types.Header.Time` is `uint64` in fork (was `*big.Int` in XDPoSChain). `header.Number` still `*big.Int`.
- `eip1559.CalcBaseFee(config, parent)` (fork, `miner/worker.go:295`) takes parent header, not the new header.
- txpool: `txpool.Pending(filter txpool.PendingFilter)` (fork) vs `TxPool().Pending(true)` (XDPoSChain). Use the fork's `fillTransactions`.
- State: `chain.StateAtForkBoundary(parent, header)` (fork, `miner/worker.go:344`) vs `chain.StateAt(parent.Root())` (XDPoSChain).
- `core.AssembleBlock(engine, chain, header, state, body, receipts)` (fork, `miner/worker.go:233`) is the canonical assembler — reuse it.

---

## Q2. CHANNELS — minePeriodCh / newRoundCh lifecycle

**Current state (the M2 bug):**
- `eth/backend.go:315`: `engine_v2.New(chainConfig, chainDb, nil, nil)` — both channels nil.
- Producers, all unconditional: `setNewRound` does `select { case x.newRoundCh <- round: default: }` (non-blocking, `engine.go:2269` — safe with nil, just drops). `minePeriodCh` sends are `go func(){ x.minePeriodCh <- ... }()` at `engine.go:233, 397, 441, 478` — **blocking sends; with nil channel each leaks a goroutine forever** (the "leak now"). With M1 wired to a real channel that the worker drains, they're fine; with a real channel nobody drains, each leaks (the "deadlock when M1 lands" — really a leak, but `UpdateParams`'s goroutine accumulation under round churn is the practical hazard).

**Design:**
- Allocate both channels in `eth/backend.go` *before* `engine_v2.New`:
  - `newRoundCh := make(chan types.Round, 1)` — buffer 1 matches XDPoSChain's `worker.go:281` usage; producer is non-blocking-select so buffer 1 is enough to avoid drops between worker iterations.
  - `minePeriodCh := make(chan int, 1)` — buffer 1; producers are goroutine sends so even buffer 0 wouldn't deadlock, but buffer 1 avoids goroutine pileup.
- Pass them to `engine_v2.New(chainConfig, chainDb, minePeriodCh, newRoundCh)`.
- **Owner = `eth.Ethereum`** (store on the struct), not the engine and not the miner — because the engine is a singleton (`engineOnce`, `engine.go:124-134`) and the miner can start/stop. The XDPoS worker reads them via **new accessors on the wrapper** (the wrapper does NOT currently expose them — unlike XDPoSChain where the worker did `w.engine.(*XDPoS.XDPoS).MinePeriodCh`). Add `func (c *XDPoS) MinePeriodCh() chan int` and `func (c *XDPoS) NewRoundCh() chan types.Round` to the wrapper, delegating to a new `EngineV2` accessor, OR (cleaner) have backend pass the two channels directly into the new xdpos-worker constructor. **Recommend: backend passes channels to the worker constructor** — avoids widening the consensus interface and keeps ownership in `eth`.
- **Drain rule:** the xdpos-worker's `update()` loop is the *sole consumer* of both. It must consume even when not mining (drain-and-ignore) so producers never pile up. XDPoSChain's worker `defer close(MinePeriodCh)` / `defer close(NewRoundCh)` on loop exit — **do NOT close** in the fork because the engine is a singleton that outlives miner restarts; closing would panic the engine's next send. Instead: on worker stop, leave channels open; the buffered channel + non-blocking producer tolerate a stopped consumer briefly. (This is a deliberate divergence from XDPoSChain forced by the singleton engine.)
- **Shutdown:** worker `stop()` exits its goroutines via a `quit chan struct{}`; channels are owned by `eth.Ethereum` and GC'd at node close. The engine singleton's periodicJob continues but its sends are buffered/non-blocking.

**M2 minimal fix if M1 is staged separately:** guard the producers with `if x.minePeriodCh != nil`. But since M1 lands the real channels, the cleaner fix is allocation. Add the nil-guard anyway as belt-and-suspenders (cheap, prevents regression if a future caller passes nil).

---

## Q3. SIGNING — Authorize wiring

**What the engine expects:** wrapper `XDPoS.Authorize(signer common.Address, signFn SignerFn)` where `SignerFn = func(accounts.Account, string, []byte) ([]byte, error)` (3-arg, `xdpos.go:181`). It internally adapts to V2's 2-arg `engine_v2.SignerFn` via closure (`xdpos.go:1488-1492`). V2 `Seal` calls `signFn(accounts.Account{Address: signer}, sigHash(header).Bytes())` and writes `header.Validator` (`engine_v2/engine.go:679-683`).

**Canonical wiring (XDPoSChain `StartStaking`, `eth/backend.go:494-516`):**
```
eb := etherbase
wallet := accountManager.Find(accounts.Account{Address: eb})
XDPoS.Authorize(eb, wallet.SignHash)   // wallet.SignHash is the 3-arg SignerFn
go miner.Start(eb)
```

**Design for the fork:** add `StartMining()` / `StopMining()` to `eth.Ethereum` (neither exists today — only `Miner()` at `backend.go:549`). `StartMining`:
1. Resolve etherbase from `config.Miner.PendingFeeRecipient` / `--miner.etherbase` (see Q6). This is the **candidate address** = node coinbase = the address that must be in the masternode set.
2. `wallet, err := s.accountManager.Find(accounts.Account{Address: eb})` — must be **unlocked** (via `--unlock` + `--password`). The keystore wallet's `SignHash(account, hash) ([]byte, error)` is a 2-arg method; **the wrapper's `SignerFn` is 3-arg**. XDPoSChain passed `wallet.SignHash` directly because its `SignerFn` is 2-arg. **API delta:** in the fork wrap it: `signFn := func(a accounts.Account, _ string, h []byte) ([]byte, error) { return wallet.SignHash(a, h) }`. (Verify the fork's keystore `SignHash` signature — it is `SignHash(account, hash)` in go-ethereum.)
3. `xdposEngine.Authorize(eb, signFn)`.
4. Set the worker's coinbase to `eb` and start the worker loop.

**Coinbase/etherbase/candidate relationship (consensus-critical):** V2 `Prepare` enforces `header.Coinbase == signer` (`engine_v2/engine.go:621-624`) and the worker must set `header.Coinbase = w.coinbase`. The signer (from Authorize) MUST equal the etherbase MUST equal the candidate address that was `propose()`d on the MasternodeVotingSMC. A mismatch → `Prepare` returns "coinbase mismatch with signer" and no block is produced. Validate this at `StartMining` startup and fail loudly.

---

## Q4. BFT HOOKUP — where HandleProposedBlock goes + M4b/M4c

**The mine→commit sequence the worker must drive (per block):**
```
1. commitNewWork: FindParentBlockToAssign → YourTurn gate → build header → engine.Prepare
   (Prepare sets Round+QC in Extra, Difficulty, epoch Validators/Penalties)
2. fill txs (fork fillTransactions) → assemble → state root via Finalize-equivalent
3. block, err := EngineV2.Seal(cr, assembled, stop)   // synchronous; writes header.Validator
4. n, err := blockchain.InsertChain(types.Blocks{block})   // local canonical insert + full validation
5. if err == nil && n == 1:
   a. h.BroadcastBlock(block)         // M4c — NEW: gossip NewBlockMsg to peers so they can vote
   b. xdposEngine.HandleProposedBlock(chain, block.Header())   // M4 — proposer self-processes:
        processQC(own QC), then if allowedToSend+verifyVotingRule → sendVote → BroadcastCh → peers
6. loop: wait for NewRoundCh (advanced by incoming votes forming a QC → setNewRound) or timer
```

**M4 (the 10-LOC hookup) has a hidden prerequisite — the wrapper stub.** `xdpos_api.go:94 HandleProposedBlock` currently **returns nil without delegating**. It must call `c.EngineV2.ProposedBlockHandler(cr, header)` (the real impl at `engine_v2/utils.go:505`). Add `ProposedBlockHandler` to `EngineV2Iface` (`xdpos.go:321-354`) and implement the wrapper delegation. Then call it from step 5b. `ProposedBlockHandler` does: processQC → `allowedToSend(chain, header, "vote")` → `verifyVotingRule` → `sendVote` → `broadcastToBftChannel(voteMsg)`.

**M4b (BFT dispatch) — must be wired or step 5b's vote goes nowhere and peers' votes are dropped:**
1. Construct the `Bfter` in `eth/backend.go` (or `newHandler`): `bft.New(BroadcastFns{Vote: h.BroadcastVote, Timeout: h.BroadcastTimeout, SyncInfo: h.BroadcastSyncInfo}, chainReader, chainHeightFn)`.
2. `bfter.SetConsensusFns(v2Engine)` (engine satisfies the narrow interface — all 6 methods exist: `vote.go:22/61`, `timeout.go:22/65`, `utils.go:459/491`).
3. `bfter.SetBroadcastCh(v2Engine.BroadcastCh)` — connects engine outbound → peer gossip.
4. `bfter.SetEpoch(epoch)` and `bfter.Start()` (launches the forwarding `loop()`).
5. Replace the no-op cases at `handler_eth.go:90-98` with `h.bfter.Vote(peer.ID(), decodedVote)` etc. (decode the raw RLP first, mirroring XDPoSChain's bft_handler call sites). Gate on `h.bfter.Engine() != nil`.

**M4c (block gossip):** add `func (h *handler) BroadcastBlock(block *types.Block)` to `eth/handler_xdc.go` mirroring the existing `BroadcastVote` pattern — RLP-encode a `eth.NewBlockPacket{Block: block, TD: ...}` and `p.Peer.Send(eth.NewBlockMsg, pkt)` to all peers. Peers receive it at `handler_eth.go:118` (direct-extension insert) and then their own BFT dispatch votes on it.

**Interaction with existing (already-working-inbound-verify) paths:** wiring M4b makes inbound votes actually drive `VoteHandler` → on reaching QC threshold the engine calls `setNewRound` → `newRoundCh` → wakes the worker for the next slot, and `HookCommitBlock` (`backend.go:318`) marks finality. This closes the loop. The forensics/equivocation path (`forensics.go`) is unaffected.

---

## Q5. SAFETY GATES

**Never mint while syncing — three independent gates (all required):**
1. **`eth.Synced()`** (`backend.go:559`, reads `handler.synced`) — `StartMining` must refuse / the worker must not seal until true. Mirror XDPoSChain's `canStart`/`shouldStart` (`miner/miner.go:85-106`) but driven by `Synced()` + a downloader-event subscription instead of the mux `StartEvent/DoneEvent` (the fork's downloader publishes `downloader.SyncEvent` on a feed — `handler.go:684 SubscribeSyncEvents`; subscribe and pause sealing on `SyncStarted`, resume on `SyncCompleted`).
2. **`core.XdcBulkSyncMode`** (`core/xdc_sync_opts.go:17`) — must be `false`. It's set true during bulk sync (`sync_xdc.go:332`) and false at tip (`sync_xdc.go:338,388`). The worker's `commitNewWork` must early-return if `core.XdcBulkSyncMode.Load()`. This is the same flag the A.97 heal/A.94 gate keys on — reusing it guarantees minting and bulk-state-import are mutually exclusive.
3. **`YourTurn` + `Prepare`** — `commitNewWork` calls `YourTurn(chain, parent, coinbase)` and bails if not my turn (`xdpos.go:1515`); `Prepare` re-checks `yourturn` (`engine_v2/engine.go:582-589`) and returns `ErrNotReadyToMine`. Double gate prevents minting out of turn.

**Never double-mint a round:** the engine tracks `highestSelfMinedRound` (set in `Seal`, `engine.go:691`) and `yourturnAligned` refuses if "Already mined on this round" (`mining.go:27`). Additionally keep XDPoSChain's `lastParentBlockCommit` guard (`worker.go:619-621`) in the ported `commitNewWork` to dedupe rapid `chainHeadCh`+`newRoundCh` double-fires on the same parent.

**Respect YourTurn at epoch switches:** the masternode set at epoch boundaries is computed in `Prepare` via `calcMasternodes` (`engine.go:601`) and seeded from snapshots (already done by A.97 work). `IsEpochSwitch` is round-based in V2 (`engine.go:595`). The worker must NOT pull txs on epoch-switch blocks (mirror `worker.go:723` — epoch-switch blocks carry only Validators/Penalties, no txs). No special masternode-set handling needed in the worker; the engine owns it.

---

## Q6. FLAGS / UX

**M5 — reactivate `--mine` (do NOT invent a new flag).** Operator docs, XinFin-Node systemd units, and the A97 plan already use `--mine --miner.etherbase --unlock --password`. Reuse them:
- `setMiner` (`flags.go:1786`): remove the deprecation-only branch; set a `cfg.Mining = true` (add a bool to `miner.Config`) when `--mine` is set. (`--miner.etherbase` is currently "deprecated" too — `flags.go:1534 setEtherbase` reads it into `cfg.Miner.PendingFeeRecipient`; that read still works, keep it as the candidate-address source.)
- In `cmd/geth` node startup (where the backend is registered and started), after the stack starts, if mining is enabled call `ethBackend.StartMining()`. Mirror upstream's old `geth.go` `startNode → ethBackend.StartMining()`; the fork removed it, re-add gated on the flag.

**Etherbase = candidate address.** `--miner.etherbase` MUST equal the `propose()`d candidate and an **unlocked** keystore account. `StartMining` validates: account found + unlocked + (warn if) not currently in masternode set (`ValidateMasternode` pattern, XDPoSChain `backend.go:474`).

**Fast-synced node minting without restart?** **Restart required.** The node was launched `--syncmode fast` without `--mine`/`--unlock`. To mint: restart with `--syncmode full --mine --miner.etherbase <C> --unlock <C> --password <file> --gcmode full --state.scheme hash --snapshot=false` (per A97 plan Phase 4). Exact delta: add `--mine --miner.etherbase --unlock --password`, change `--syncmode fast`→`full`, drop `--no-auto-pivot`. Rationale: Authorize needs an unlocked wallet resolved at startup; the account manager unlock happens during node init, not hot. (A hot-enable RPC `miner_start`/`personal_unlock`+Authorize is possible as a follow-up but out of scope.)

---

## Q7. CONSENSUS-SAFETY INVARIANTS (bit-identical-or-reject)

A geth-XDC or XDPoSChain peer runs `VerifyHeaderWithParents` → `EngineV2.VerifyHeader` (`verifyHeader.go`). Any deviation in these header fields → rejected block → at best ignored, at worst the proposer is penalized. The worker must produce headers byte-identical to what `Prepare`+`Finalize`+`Seal` generate — which it will **if and only if it calls the engine's own Prepare/Finalize/Seal and does not post-mutate the header.** Enumerated invariants:

1. **Extra field (Round + QuorumCert):** set ONLY by `engine_v2.Prepare` via `types.ExtraFields_v2{Round, QuorumCert}.EncodeToBytes()` (`engine.go:558-566`). The worker MUST NOT touch `header.Extra` (no DAO-fork override, no `w.extra` append — that XDPoSChain V1 code at `worker.go:657,677-688` must be **dropped** for V2). `sigHash` includes Extra inline (`engine.go:238`), so any extra byte breaks the signature.
2. **Validator (seal signature):** 65 bytes, set ONLY by `Seal` (`engine.go:683`); verified at `verifyHeader.go:39-42,298`. Worker must not pre-size or touch `header.Validator`.
3. **Validators / Penalties:** populated ONLY at epoch-switch by `Prepare` from `calcMasternodes` (`engine.go:601-610`). Order and content must match what verifiers recompute. Owned by engine — worker must not set.
4. **Difficulty:** `calcDifficulty` (`engine.go:592`); for V2 it's QC-window-based. Owned by `Prepare`.
5. **Time:** `parent.Time + Period`, floored to now (`engine.go:616-619`). Owned by `Prepare`. Worker's own `tstamp` computation (XDPoSChain `worker.go:641-650`) is V1-only and must be dropped — let `Prepare` set Time.
6. **Coinbase == signer** (`engine.go:621`). Worker sets `header.Coinbase = w.coinbase` before Prepare; signer is from Authorize; they must match.
7. **Nonce = {} , MixDigest = {} , UncleHash = CalcUncleHash(nil)** — set by Prepare/Finalize. No uncles in XDPoS.
8. **Root (state):** computed by `Finalize` via `state.IntermediateRoot` (`engine.go:653`) AFTER rewards. Must be the single source of the root.
9. **REWARDS — NOT duplicated (verified):** Rewards are applied during normal block *processing* via `state_processor.go Process` → `SetParentState` (`state_processor.go:220`) → wrapper `Finalize`'s `HookReward` (`xdpos.go:1200`). During *mining*, `engine_v2.Finalize` (`engine.go:637-645`) fires `HookReward` at epoch switch on the mining state. **The risk:** rewards get applied once during mining-Finalize AND again when the block is re-processed on InsertChain. **This is the single most dangerous part of the implementation.** `InsertChain` re-runs `Process` on the inserted block; if `Process`+`Finalize` apply rewards on top of an already-rewarded state, the root won't match the mined root → BAD BLOCK on the proposer's own insert, or a fork. **Verify before implementing:** confirm the assembled block's state root (from mining `Finalize`) equals the root `Process` produces on re-execution. The fork's `InsertChain` of a self-mined block re-executes from the parent state, so as long as mining used `Finalize` exactly once on a fresh parent state (not double-applying), the roots match. The XDPoSChain worker sidestepped this by `WriteBlockWithState` (writing the already-computed state, no re-execution). **The fork uses `InsertChain` (re-executes)** — so the worker must produce a block whose txs+coinbase deterministically reproduce the same rewards on re-execution. They will, because `HookReward` is deterministic given (state, parentState, header). The porter must NOT pre-apply rewards and then also leave them for Process. **Recommended verification gate: in devnet, mine one epoch-switch block and confirm `InsertChain` returns no error and the inserted root == mined root.**

**Net invariant rule for the porter: the worker assembles txs, then calls engine Prepare → (fill) → Finalize → Seal and touches no header field directly except Coinbase (pre-Prepare). Everything else is engine-owned.**

---

# IMPLEMENTATION-PLAN (ordered; the Sonnet brief)

**Sequencing:** M4b/M4c BFT+gossip wiring FIRST (independently testable: two non-mining nodes should exchange votes once one *receives* a block — actually only testable once one mints, so co-stage). Realistic order: PR-A (channels+BFT plumbing, no behavior change to sync), PR-B (worker+seal+insert+gossip+Authorize+flag), PR-C (HandleProposedBlock delegation + self-vote), then devnet.

### Task 1 — Channels (M2). Size: S (~20 LOC)
- `eth/backend.go:315`: allocate `minePeriodCh := make(chan int, 1)`, `newRoundCh := make(chan types.Round, 1)`; store on `eth.Ethereum`; pass to `engine_v2.New`.
- `consensus/XDPoS/engines/engine_v2/engine.go:233,397,441,478`: add `if x.minePeriodCh != nil` guard (defense).

### Task 2 — BFT dispatch wiring (M4b). Size: M (~80 LOC)
- `eth/backend.go` (after `SetEngineV2`): construct `bft.New`, `SetConsensusFns(v2Engine)`, `SetBroadcastCh(v2Engine.BroadcastCh)`, `SetEpoch`, `Start()`. Store `bfter` on handler.
- `eth/handler.go`: add `bfter *bft.Bfter` field; pass through `newHandler`.
- `eth/handler_eth.go:90-98`: replace no-op cases — decode raw RLP (`rlp.DecodeBytes(packet.Vote, &vote)`) then `h.bfter.Vote(peer.ID(), &vote)` (and Timeout/SyncInfo). Gate on `h.bfter != nil && h.bfter.Engine() != nil`.
- Expose `BroadcastCh` accessor on engine if needed (it's already a public field).

### Task 3 — Outbound block gossip (M4c). Size: S (~40 LOC)
- `eth/handler_xdc.go`: add `BroadcastBlock(block)` mirroring `BroadcastVote` — `p.Peer.Send(eth.NewBlockMsg, &eth.NewBlockPacket{Block: block, TD: td})`. Compute TD from `blockchain.GetTd`.

### Task 4 — HandleProposedBlock delegation (M4). Size: S (~15 LOC)
- `consensus/XDPoS/xdpos.go`: add `ProposedBlockHandler(chain consensus.ChainReader, header *types.Header) error` to `EngineV2Iface`.
- `consensus/XDPoS/xdpos_api.go:94`: replace the stub body with `if c.EngineV2 != nil { if cr, ok := chain.(consensus.ChainReader); ok { return c.EngineV2.ProposedBlockHandler(cr, header) } }; return nil`.

### Task 5 — XDPoS production worker (M1). Size: L (~300-400 LOC)
- New file `miner/xdpos_worker.go`: `xdcWorker` struct {engine, chain, txpool, coinbase, minePeriodCh, newRoundCh, chainHeadCh sub, quit, mining atomic.Bool}.
- `update()` loop: port `worker.go:270-382` timer logic; consume `minePeriodCh`/`newRoundCh`/`chainHeadCh`; on tick/round/head → `commitNewWork()` if mining && synced && !XdcBulkSyncMode.
- `commitNewWork()`: `parent = EngineV2.FindParentBlockToAssign(cr)`; `lastParentBlockCommit` dedupe; `YourTurn` gate; build header (Coinbase only); `engine.Prepare` (treat `ErrNotReadyToPropose`/`ErrNotReadyToMine` as skip); reuse fork `prepareWork`/`fillTransactions` for body (skip txs on epoch-switch); `Finalize`; `EngineV2.Seal(cr, block, quit)`; then `commitMinedBlock(block)`.
- `commitMinedBlock(block)`: `InsertChain([block])`; on success → `handler.BroadcastBlock(block)` → `xdposEngine.HandleProposedBlock(chain, block.Header())`.
- Reuse fork primitives; **drop** V1/XDCx/DAO/uncle/tstamp code.

### Task 6 — Authorize + StartMining (M3). Size: M (~70 LOC)
- `eth/backend.go`: add `StartMining() error` / `StopMining()`. StartMining: resolve etherbase; `Find` wallet; build 3-arg `signFn` wrapping `wallet.SignHash`; `xdposEngine.Authorize(eb, signFn)`; validate coinbase==signer; start `xdcWorker`. Gate on `Synced()`.
- Construct `xdcWorker` in `New` (with the channels) but don't start it until `StartMining`.

### Task 7 — Flag (M5). Size: S (~25 LOC)
- `cmd/utils/flags.go:1786 setMiner`: set `cfg.Mining = true` on `--mine` (add `Mining bool` to `miner.Config`).
- `cmd/geth` startup: after stack start, `if cfg.Mining { ethBackend.StartMining() }`.

### Old→New API map (consolidated for the porter)
| XDPoSChain call | Fork replacement |
|---|---|
| `c.FindParentBlockToAssign(chain, currentBlock)` | `EngineV2.FindParentBlockToAssign(cr)` |
| `engine.Seal(chain, block, results, stop)` push | `EngineV2.Seal(cr, block, quit) (*Block,error)` sync |
| `WriteBlockWithState(block, receipts, state, ...)` | `blockchain.InsertChain(Blocks{block})` |
| `mux.Post(NewMinedBlockEvent{block})` + minedBroadcastLoop | `handler.BroadcastBlock(block)` (NEW, NewBlockMsg) |
| `TxPool().Pending(true)` + TRC21 fee cap + special txs | `miner.fillTransactions(ctx, interrupt, env)` |
| `chain.StateAt(parent.Root())` | `chain.StateAtForkBoundary(parent, header)` |
| `header.Time *big.Int` | `header.Time uint64` |
| `wallet.SignHash` (2-arg, direct) | `func(a,_string,h){return wallet.SignHash(a,h)}` (3-arg) |
| mux `StartEvent/DoneEvent` sync gate | `eth.Synced()` + `downloader.SubscribeSyncEvents` |

### Test plan
- **Unit:** worker `commitNewWork` gating (not-my-turn skip, ErrNotReadyToMine skip, XdcBulkSyncMode skip, not-synced skip); `getResetTime` timing; channel drain (no goroutine leak under round churn — run `-race`); `HandleProposedBlock` delegation returns engine result; BFT decode/dispatch round-trip.
- **Integration (devnet, 3 masternodes):** the only way to prove a minted block is accepted. Spin 3 nodes (mix: ≥1 this-geth + ≥1 canonical XDPoSChain for cross-client parity) on a private V2 genesis with all 3 in the masternode set. Assert: `[yourturn] Yes`, block sealed, **`InsertChain` returns nil AND inserted root == mined root** (the reward-duplication gate), peers receive via NewBlockMsg, votes flow (`VoteHandler` fires on peers), `Successfully created QC`, `HookCommitBlock`/SetFinalized advances, chain progresses ≥2 epochs (exercises epoch-switch Validators/Penalties + rewards). Cross-client acceptance = XDPoSChain node must accept this-geth's blocks and vice-versa.
- **Apothem dry-run** only after devnet green and `propose()`+KYC+epoch-wait done (Phase 2 of A97 plan). NEVER mainnet before Apothem mints end-to-end.

### Non-goals (explicit)
- XDCx / XDCxLending / order-pool / lending-matching txs in mined blocks.
- Hot mining enable without restart (RPC `miner_start`).
- Rewriting the upstream payload-builder `Miner` (it coexists, idle).
- Changing reward math, RLP encoding, or any consensus rule (failure-rules forbid).
- V1 (pre-V2-switch) mining — mainnet/Apothem are long past V2 switch.

---

# RISKS (top failure modes + detection before public network)

1. **Reward double-application → root mismatch / BAD BLOCK (P0).** If mining-Finalize and InsertChain-reprocess both apply epoch rewards, the proposer's own InsertChain fails or forks. **Detect:** devnet epoch-switch block; assert inserted root == mined root; grep `invalid merkle root`/`BAD BLOCK`. This is THE gate before any public network.
2. **Header byte-divergence → peers reject (P0).** Any worker-side mutation of Extra/Validator/Time/Difficulty makes `verifyHeader` reject. **Detect:** cross-client devnet (XDPoSChain peer must accept). Single-client devnet hides this.
3. **BFT not wired → block never commits (P0).** If M4b is skipped, mined blocks insert locally but never get a QC; chain stalls at the proposer. **Detect:** absence of `Successfully created QC` and `VoteHandler` logs on peers; chain height frozen.
4. **Minting while syncing → invalid/forked blocks (P0).** Missing any of the three gates (Synced/XdcBulkSyncMode/YourTurn). **Detect:** start `--mine` on a mid-sync node; it must log "Network syncing, will start miner afterwards" and produce zero blocks until tip.
5. **Goroutine leak / deadlock on minePeriodCh (P1).** Wrong buffer/no-drain under round churn. **Detect:** `-race` + goroutine-count assertion over 1000 simulated rounds.
6. **Coinbase≠signer / locked account → no blocks, silent (P1).** **Detect:** `StartMining` must fail loudly at startup, not silently produce nothing.
7. **Double-mint a round → equivocation, slashing (P1).** Missing `highestSelfMinedRound`/`lastParentBlockCommit` dedupe. **Detect:** forensics equivocation log; assert one block per round in devnet.
8. **Engine singleton + channel close panic (P1).** If the worker closes the channels on stop (XDPoSChain did), the singleton engine's next send panics. **Detect:** stop+start mining twice in a test; must not panic.

---

# ESTIMATED-SCOPE

**~550 LOC production + ~250 LOC tests + ~150 LOC devnet harness.**

| PR | Scope | LOC | Risk |
|---|---|---|---|
| **#902** | M2 channels + M4b BFT dispatch wiring + M4c BroadcastBlock + M4 HandleProposedBlock delegation. No minting yet; makes the node BFT-participant-capable. Independently reviewable, low risk to sync. | ~220 | Low-Med |
| **#903** | M1 `miner/xdpos_worker.go` + M3 Authorize/StartMining + M5 `--mine` flag. The minting engine. Depends on #902. | ~330 + tests | High (consensus) |
| **#904** | Devnet harness + integration tests + Opus design-review gate + Apothem dry-run runbook. | ~150 | Med |

Stack #903 on #902. #902 can merge alone (it only enables BFT participation, which a validator needs regardless). Require an Opus consensus re-review of #903 before merge (per loop escalation rules — reward path + header parity are exactly the "state-root/consensus-adjacent decisions" that mandate escalation). Do NOT un-draft #903 until cross-client devnet shows inserted-root==mined-root across an epoch switch.

---

### Critical Files for Implementation
- /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/.claude/worktrees/heuristic-margulis-5e085e/eth/backend.go (channels at :315, miner.New at :457, add StartMining/Authorize/Bfter construction)
- /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/.claude/worktrees/heuristic-margulis-5e085e/miner/xdpos_worker.go (NEW — the M1 production loop)
- /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/.claude/worktrees/heuristic-margulis-5e085e/eth/handler_eth.go (BFT dispatch no-op cases at :90-98 → real Bfter.Vote/Timeout/SyncInfo)
- /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/.claude/worktrees/heuristic-margulis-5e085e/consensus/XDPoS/xdpos_api.go (HandleProposedBlock stub at :94 → delegate to EngineV2.ProposedBlockHandler)
- /Users/anilchinchawale/github/XDCNetwork/deVTest/go-ethereum/.claude/worktrees/heuristic-margulis-5e085e/eth/handler_xdc.go (add BroadcastBlock mirroring BroadcastVote at :20)