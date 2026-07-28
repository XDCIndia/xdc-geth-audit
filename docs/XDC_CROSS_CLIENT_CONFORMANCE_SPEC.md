# XDC Network — Cross‑Client Conformance Specification & Validator‑Readiness Test Suite

> **New here? Start with the [XDC Client Integration Guide](./port/XDC_CLIENT_INTEGRATION_GUIDE.md)** — a two-pillar overview (*port it* + *keep it in sync*), the reference-implementation map, and a copy‑paste agent completion prompt. This spec is the detailed reference that guide points into.

**Purpose:** This is a self‑contained specification *and* an executable validation prompt. Give it to an engineering team or an AI coding agent to (a) implement XDC consensus on top of **any** Ethereum execution client (reth, erigon, besu, nethermind, geth), and (b) **prove** the implementation is bit‑for‑bit consensus‑compatible with the live XDC network and ready to run as a validator/masternode.

**Reference implementation:** XDC on top of **go‑ethereum v1.17.3** (the "modern XDC fork"). Every consensus rule below is what that fork does; the canonical behavior originates from XDPoSChain (the original XDC client). When this doc and the live network disagree, **the live network wins** — re‑derive the value from a synced archive node and file a correction.

**How to use this as a prompt (read first):**
> You are porting XDC to `<CLIENT>`. XDC = standard Ethereum execution (EVM, state, RLP, EIP‑1559/Cancun) **plus** the XDPoS proof‑of‑stake consensus engine, a non‑standard block header, an epoch reward/penalty system, two system precompiled contracts, special (gasless) transactions, and a BFT p2p protocol. Implement each numbered section, then run every test in **Part 12** against a mainnet/apothem archive node. The implementation is "XDC validator‑ready" only when **all** Part‑12 gates pass, including G‑live (sync genesis→head with zero state‑root mismatches) and I (mint + BFT participation on a private net accepted by reference nodes).

> ⚠️ **Consensus correctness is sacred.** Any deviation that changes a block hash, state root, receipts root, or the masternode/penalty set is a P0 failure. Do not "improve" or "simplify" any rule. Match exactly, including quirks flagged as 🪤 TRAP.

---

## Part 1 — Architecture

XDC is a delegated‑proof‑of‑stake (DPoS) EVM chain with **two sequential consensus engines** selected by block number:

| Engine | Range | Style | Finality |
|---|---|---|---|
| **XDPoS v1** | genesis → `V2SwitchBlock` (inclusive) | PoA‑style round‑robin masternode sealing (Clique‑derived) | probabilistic (TD/longest‑chain) |
| **XDPoS v2** | `V2SwitchBlock + 1` → head | HotStuff‑style BFT with QuorumCerts | deterministic (QC chain) |

A client MUST route every block to the correct engine: a header `H` is governed by **v2 iff `H.Number > config.V2.SwitchBlock`**, else **v1**. The switch block itself is v1‑mined.

Everything else (EVM opcodes, gas, state trie, tx types, EIP‑1559 base fee, Cancun blobs) is **stock Ethereum at the activated fork level** — reuse your client's existing machinery. The XDC‑specific surface is: header shape, sealing/verification, epoch/masternode logic, reward/penalty crediting, two system contracts, special transactions, and the BFT p2p messages.

---

## Part 2 — Chain Parameters

### 2.1 Networks

| Network | Chain ID | CLI flag (ref impl) | Genesis hash | V2 switch block |
|---|---|---|---|---|
| **Mainnet** | `50` | `--xdcmainnet` | `0x4a9d748bd78a8d0385b67788c2435dcdb914f98a96250b68863a1f8b7642d6b1` | `80,370,000` |
| **Apothem (testnet)** | `51` | `--apothem` | `0xbdea512b4f12ff1135ec92c00dc047ffb93890c2ea1aa0eefe9b013d80640075` | `56,828,700` |
| **Devnet 5551** | `5551` | `--xdcdevnet` | `0xb8be003946a9c2688e9f1e255a5567c3d144293ccde2ffb38452a5840081b402` | `2,700` |

> 🪤 TRAP: chain id is **50**, not 0x32‑as‑decimal confusion; addresses are commonly shown with an `xdc` prefix instead of `0x` (same 20 bytes). EIP‑155 replay protection uses chain id 50.

### 2.2 EVM fork schedule (block‑number gated)

XDC pins standard EVM forks at **block numbers** (not timestamps, except Cancun/Prague which some configs express as blocks on XDC). Exhaustive table — **the validation suite must confirm every one**:

| Fork | Mainnet (50) | Apothem (51) | Devnet (5551) |
|---|---|---|---|
| Homestead | 1 | 1 | 0 |
| EIP‑150 | 2 | 2 | 0 |
| EIP‑155/158 | 3 | 3 | 0 |
| Byzantium | 4 | 4 | 0 |
| Constantinople | 76,321,000 | 61,290,000 | 0 |
| Petersburg | 76,321,000 | 61,290,000 | 0 |
| Istanbul | 76,321,000 | 61,290,000 | 0 |
| Berlin | 76,321,000 | 61,290,000 | 0 |
| London | 76,321,000 | 61,290,000 | 0 |
| EIP‑1559 (base fee) | 98,800,200 | 71,550,000 | 25,000 |
| Cancun | 98,802,000 | 71,551,800 | 25,000 |
| Prague (EIP‑2935) | (not yet) | 83,600,000 *(v2.8, ~2026‑06‑26)* | 50,000 |

> 🪤 TRAP — **Istanbul double gate:** in the reference impl, Istanbul EVM semantics effectively activate via the XDC‑specific `TIPXDCXCancellationFee` block (mainnet `38,383,838`, apothem `23,779,191`) **as well as** the `IstanbulBlock`. When validating historical execution you must reproduce whichever gate the canonical client used. Verify EVM behavior at both boundaries.

> 🪤 TRAP — **EIP‑1559 base fee semantics on XDC:** base fee exists post‑1559 block but XDC does **not** burn it to the protocol; see Part 7.

### 2.3 XDC‑specific "TIP" fork blocks (consensus‑ and execution‑relevant)

These gate XDC behaviors. Per‑network values (`big.Int` block numbers; `0` = active from genesis on devnet):

| TIP | Mainnet | Apothem | Devnet 5551 | Effect |
|---|---|---|---|---|
| `TIP2019` | 1 | 1 | 0 | reward formula update |
| `TIPSigning` | 3,000,000 | 3,000,000 | 0 | signing‑tx handling; BlockSigners contract self‑destructs |
| `TIPRandomize` | 3,464,000 | 3,464,000 | 0 | M1/M2 randomization via RandomizeSMC |
| `TIPNoHalvingMNReward` | 38,383,838 | 23,779,191 | 0 | stops reward halving |
| `TIPXDCX*` (trading/lending/fees) | 38,383,838 | 23,779,191 | 0 | DEX/lending precompiles |
| `TIPXDCXMinerDisable` | 80,370,000 | 61,290,000 | 0 | disable XDCX mining |
| `BlackListHF` | 38,383,838 | 23,779,191 | 0 | address blacklist enforcement |
| `TIPUpgradeReward` (v2.8) | nil | 83,600,000 | 50,000 | tiered rewards (Part 7.2) |
| `TIPUpgradePenalty` (v2.8) | nil | 83,600,000 | nil on devnet | upgraded penalty (Part 7.3) |

> A port that only targets **post‑v2 sync** (the common case) still must implement the EVM forks active at the sync range and the v2.8 forks (Prague/TIPUpgradeReward) for apothem after block 83.6M.

### 2.4 XDPoS engine parameters

| Param | Mainnet | Apothem | Devnet | Meaning |
|---|---|---|---|---|
| `Period` (block time) | 2 s | 2 s | 2 s | min seconds between blocks |
| `Epoch` | 900 | 900 | 900 | blocks per epoch |
| `Gap` | 450 | 450 | 450 | gap window before epoch switch |
| `RewardCheckpoint` | 900 | 900 | 900 | reward accounting window |
| `Reward` | 5000 | 5000 | (10 on legacy 551) | base XDC reward per epoch (×1e18) |
| `FoundationWallet` | `0x92a289fe95a85c53b8d0d113cbaef0c1ec98ac65` | `0x746249c61f5832c5eed53172776b460491bdcd5c` | per‑net | 10% reward sink |
| V2 `CertThreshold` | 0.667 | 0.667 (0.45 at round 0) | 0.667 | QC/TC quorum fraction |
| V2 `MaxMasternodes` | 108 | 15→108 | 108 | active set cap (round‑gated; **verify vs live**) |

> 🪤 TRAP — note the **misspelled** field `FoudationWalletAddr` in the reference source; the *value* is what matters for the reward sink.

---

## Part 3 — Genesis & System Contracts

### 3.1 System (pre‑deployed) contracts

Pre‑deployed in genesis alloc with fixed bytecode at fixed addresses:

| Address | Name | Role |
|---|---|---|
| `0x0000…0088` | **MasternodeVotingSMC** | candidate registry: propose/vote/unvote/resign + stake. Source of the candidate pool. |
| `0x0000…0089` | **BlockSigners** | records which masternodes signed which blocks (via signing‑txs). Self‑destructs at `TIPSigning`. |
| `0x0000…0090` | **RandomizeSMC** | secret/opening reveal for M2 selection order (v1). |
| `0x0000…0068` | Foundation wallet | reward share recipient. |

Method selectors on `0x88`: `propose=0x01267951`, `vote=0x6dd7d8ea`, `unvote=0x02aa9be2`, `resign=0xae6e43f5`. Sign selector on `0x89`: `0xe341eaa4`.

A port MUST pre‑deploy these with the exact bytecode from the reference genesis (the bytecode hash is part of the genesis state root). **Do not** reimplement them as host precompiles unless you also reproduce their exact storage layout and gas.

### 3.2 Genesis block

Genesis `extraData` layout encodes the initial validator set:
```
extraData = [ 32 bytes vanity ][ N × 20‑byte validator address ][ 65 bytes signature ]
```
`coinbase = 0x0`, `mixHash = 0x0`, `difficulty = 1`, uncles forbidden. The computed genesis block hash MUST equal the value in Part 2.1.

---

## Part 4 — Block Header & RLP (CONSENSUS‑CRITICAL)

XDC adds **three** fields to the standard Ethereum header. They are **always present in the RLP** (not optional), positioned after `Nonce` and before the post‑London optional fields:

```
Header := RLP([
  ParentHash, UncleHash, Coinbase, Root, TxHash, ReceiptHash,
  Bloom, Difficulty, Number, GasLimit, GasUsed, Time, Extra,
  MixDigest, Nonce,
  Validators,        # []byte  — XDC: packed 20‑byte addresses (epoch‑switch); else empty
  Validator,         # []byte  — XDC: 65‑byte proposer signature (v2) / seal
  Penalties,         # []byte  — XDC: packed 20‑byte penalised addresses (epoch‑switch); else empty
  # then, only-if-present (post‑London / Cancun), in this order:
  BaseFee?, WithdrawalsHash?, BlobGasUsed?, ExcessBlobGas?, ParentBeaconRoot?
])
```
- `UncleHash` MUST be `keccak(RLP([])) = 0x1dcc4de8…` (uncles forbidden). `MixDigest` MUST be zero.
- **Block hash** = `keccak256(RLP(Header))` over the full field list above. Getting the field order or the presence of `Validators/Validator/Penalties` wrong → every block hash diverges.

### 4.1 Seal hash (the message that gets signed)

**v1** `sigHash` = `keccak256(RLP([...header fields..., Extra[:len-65], MixDigest, Nonce, <post‑London optionals>]))` — i.e. standard fields with the **trailing 65‑byte signature stripped from `Extra`**, and the v1 seal lives in the last 65 bytes of `Extra`.

**v2** `sigHash` = `keccak256(RLP([ParentHash, UncleHash, Coinbase, Root, TxHash, ReceiptHash, Bloom, Difficulty, Number, GasLimit, GasUsed, Time, Extra, MixDigest, Nonce, Validators, Penalties, BaseFee?]))` — note v2 includes the full `Extra` (which carries the version byte + RLP(round, QC)) and **includes `Validators` and `Penalties`** in the signed payload, but **excludes `Validator`** (that field *holds* the resulting signature).

Signer recovery: `secp256k1 ecrecover(sigHash, signature) → pubkey → keccak256(pubkey[1:])[12:] → 20‑byte address`.

### 4.2 `Extra` field

- **v1:** `[32 vanity][ (checkpoint only) N×20 masternode addrs ][65 seal]`. Constants: `extraVanity=32`, `extraSeal=65`.
- **v2:** `Extra[0] = 0x02` (version), `Extra[1:] = RLP(ExtraFields_v2)` where
  ```
  ExtraFields_v2 := { Round: uint64, QuorumCert: QuorumCert }
  ```

---

## Part 5 — XDPoS v1 (round‑robin PoA)

### 5.1 Epoch / checkpoint / gap
- `epoch = 900`. **Checkpoint block**: `number % 900 == 0` — its `Extra` carries the next epoch's masternode list; `Coinbase` MUST be `0x0`; nonce MUST be `0x00…0`.
- **Gap block**: `number % 900 == 450` region drives next‑epoch prep; **no block may be sealed where `number % 900 == 899`** (the last block before checkpoint is reserved — verify against canonical: ref says `epoch-1`).

### 5.2 In‑turn selection & difficulty
Given masternode list `M` (from the most recent checkpoint header) and block `N`:
```
inTurnIndex = N % len(M)
proposer    = M[inTurnIndex]
```
Difficulty encodes "distance" from the previous signer:
```
preIndex = index_of(ecrecover(parent))      # signer of parent
curIndex = index_of(signer)
hop = (preIndex<curIndex) ? curIndex-preIndex-1
    : (preIndex>curIndex) ? (len-preIndex)+(curIndex-1)
    : len-1                                   # same signer consecutively
difficulty = len(M) - hop                     # in‑turn ⇒ difficulty == len(M); out‑of‑turn smaller
```
A verifier recomputes this and rejects on mismatch. Constants `DiffInTurn=2 / DiffNoTurn=1` exist but the live rule is the `len-hop` formula above — **verify against canonical block difficulties**.

### 5.3 Verification (v1)
1. nonce ∈ {all‑zero, all‑ones}; checkpoint ⇒ all‑zero.
2. `len(Extra) ≥ 97` (32 + 65); checkpoint ⇒ `(len(Extra)-97) % 20 == 0`.
3. `parent.Time + Period ≤ Time`.
4. recover signer from last 65 bytes of `Extra`; must be in the authorized set and not have signed within the recent‑signer limit (anti‑spam: no repeat within `len/2`‑ish; ref uses limit derived from masternode count).
5. recomputed difficulty == header difficulty.
6. checkpoint ⇒ extracted masternode list == computed `(snapshot candidates − penalties over last 4 epochs)`.

---

## Part 6 — XDPoS v2 (HotStuff BFT)

### 6.1 Data structures (RLP‑encoded on the wire and in `Extra`)
```
BlockInfo   := { Hash: 32B, Round: uint64, Number: bigint }
QuorumCert  := { ProposedBlockInfo: BlockInfo, Signatures: [][]byte(65B each), GapNumber: uint64 }
TimeoutCert := { Round: uint64, Signatures: [][]byte, GapNumber: uint64 }
Vote        := { ProposedBlockInfo: BlockInfo, Signature: 65B, GapNumber: uint64 }   # signer recovered
Timeout     := { Round: uint64, Signature: 65B, GapNumber: uint64 }
SyncInfo    := { HighestQuorumCert: QuorumCert, HighestTimeoutCert: TimeoutCert }
```

### 6.2 Round → proposer
```
proposer = masternodes[ round % epochLen % len(masternodes) ]    # verify exact modulo vs live
```
Each new block's round = parent round + 1 (or jumps on timeout/TC). Difficulty is **always 1** in v2.

> 🪤 TRAP — **v2 epoch switches are ROUND‑indexed, not block‑number‑indexed.** A v2 epoch is `Epoch` *rounds* (900), counted from `V2.SwitchEpoch` (mainnet 89,300; apothem 63,143). So the `number % 900 == 0` selector finds **v1** checkpoints only — in the v2 range it does **not** land on epoch‑switch blocks (the ones that carry populated `Validators`/`Penalties`). Detect a v2 epoch switch by round (`IsEpochSwitch`), never by block number, or you will mis‑sample Gate D and never observe a populated `Validators` block. *(Live‑confirmed 2026‑06‑21.)*

### 6.3 QC verification (the heart of v2 validity)
A v2 header is valid iff:
1. `len(header.Validator) == 65` and ecrecover(v2 sigHash, Validator) == the round's proposer.
2. decode `Extra → {round, qc}`; require `round > qc.ProposedBlockInfo.Round`.
3. **verifyQC:** for each sig in `qc.Signatures`, recover signer over the **vote sig‑hash of the certified block** (`VoteSigHash({ProposedBlockInfo, GapNumber})`); count distinct signers that are in the masternode set for `qc.GapNumber`'s epoch; require `count ≥ ceil(CertThreshold × len(masternodes))` (≈ 2/3 + 1).
4. epoch‑switch block ⇒ `Validators` populated and equal to computed set; `Penalties` equal to computed penalties; nonce all‑zero.
5. gas‑limit delta ≤ parent/1024; `parent.Time + minePeriod ≤ Time`.

### 6.4 Vote / Timeout signing
```
VoteSigHash    = keccak256(RLP({ ProposedBlockInfo, GapNumber }))
TimeoutSigHash = keccak256(RLP({ Round, GapNumber }))
```
Signer of a Vote/Timeout MUST be in `snapshot.NextEpochCandidates`. `GapNumber` = the gap block of the epoch (`epochStart - Gap`); it binds a vote to a specific epoch's validator set — **get this wrong and quorum counting silently fails (0 valid sigs)**.

---

## Part 7 — Rewards & Penalties

> XDC has **no per‑block coinbase reward.** All issuance is paid at **epoch checkpoints** (`number % 900 == 0`, first payout at block **1800**), computed over the *previous* epoch window.

### 7.1 Legacy reward (current mainnet/apothem)
For checkpoint block `N`, window = `[N-1800+1 .. N-900]` (the epoch before last):
```
chainReward = Reward × 1e18                      # 5000 XDC
halving: if N≥2·blocksPerYear: /2; if N≥5·blocksPerYear: /4   # blocksPerYear=15,768,000
         (suppressed after TIPNoHalvingMNReward)
```
Count, per masternode, how many "counting blocks" they signed in the window. A **counting block** is one where `blockHeight % MergeSignRange == 0` (`MergeSignRange = 15`); signatures are read from **signing‑txs** to `0x89` (see 8.1). Then:
```
signerReward(addr)   = floor(chainReward / totalSignCount) × signCount(addr)
split per signer:  owner 90%  |  voters 0% (reserved)  |  foundation 10%
owner = MasternodeVotingSMC.getCandidateOwner(signer)
credit: state.AddBalance(owner, 90%) ; state.AddBalance(FoundationWallet, 10%)
```
> 🪤 TRAP (#186): if `owner == FoundationWallet`, **skip** the 90% owner credit (only the 10% is added) — else the foundation is double‑credited and the state root diverges at the first such checkpoint.

> 🪤 TRAP: reward queries read **parent (pre‑transaction) state** — snapshot the state *before* applying the block's txs and use that snapshot for `getCandidateOwner`.

### 7.2 v2.8 tiered reward (`TIPUpgradeReward`)
After the per‑network `TIPUpgradeRewardBlock`, replace the proportional split with **fixed per‑node‑type** rewards: each signer in a tier receives the full tier amount (not divided): `MasternodeReward`, `ProtectorReward`, `ObserverReward` (e.g. 63.42 / 50.27 / 25.13 XDC — **read live values**), inflated by `RewardInflation(...)`, then the same 90/0/10 owner/voter/foundation split. Tiers are the top‑N candidates by stake, capped by `MaxMasternodes / MaxProtectorNodes / MaxObserverNodes`.

### 7.3 Penalties (`HookPenalty` → masternode set)
At each epoch switch the active set is `candidates − penalties`, capped at `MaxMasternodes`:
```
walk back over the epoch, tally blocks-signed per coinbase (statMiners)
penalize any masternode that signed < MinimumMinerBlockPerEpoch  (=1 pre‑v2.8; =5 post‑TIPUpgradePenalty)
comeback (pre‑v2.8): a penalised node that has signed ≥1 counting block in the last 150 blocks rejoins next epoch
penalties accumulate over the last LimitPenaltyEpoch (=4) epochs
```
> 🪤 TRAP (#204/#206): the penalty *count* must match canonical exactly — counting "every block from the epoch walk‑back" vs "blocks where the miner was the round proposer / in the QC committee" gives different penalty sets (observed 18 vs 17 on devnet), which changes the masternode set → "validators not legit" at the next epoch switch. This is an **open parity hazard**; validate the penalty set block‑by‑block across an epoch boundary.

> v2.8 caveat: on devnet 5551 the canonical legacy producers run with `TIPUpgradePenaltyBlock = nil`, so a port targeting that chain must also leave it nil or it over‑counts penalties.

---

## Part 8 — Special Transactions & Gas/Fee Rules

### 8.1 Special transactions
A tx is **special** iff `tx.To() ∈ { 0x…89 (BlockSigners), 0x…90 (RandomizeSMC) }`.
- **Signing‑tx** (to `0x89`, selector `0xe341eaa4`): payload is **exactly 68 bytes** = 4‑byte selector + 32‑byte blockNumber + 32‑byte blockHash. Masternodes emit one per block they witness; the reward logic reads these. 🪤 TRAP: the canonical guard is **exactly 68 bytes**, not `≥68`.
- **Randomize‑txs** (to `0x90`): `setSecret=0x34d38600` (~block 800 of epoch), `setOpening=0xe11f5ba2` (~block 850).
- Special txs **bypass the min‑gas‑price / min‑tip rule** in the txpool and may be sent with `gasPrice = 0`. A port MUST exempt them from min‑tip validation and from being dropped as underpriced, or masternodes can't record signatures.

### 8.2 Fee handling
- **No base‑fee burn**: post‑1559 base fee exists but is not sent to a burn address; fees route per `TIPTRC21Fee` rules to the masternode owner (or signer pre‑TIP).
- TRC21 (XRC21) "zero‑gas for end user, fee paid by token issuer" semantics apply post‑`TIPTRC21Fee`.

---

## Part 9 — RPC API (parity surface)

### 9.1 `XDPoS_*` namespace (must return shape‑and‑value identical)
`getSnapshot`, `getSnapshotAtHash`, `getSigners[AtHash]`, `getMasternodes(number)`, `getMasternodesByNumber(number)→{Number,Masternodes[]}`, `getCandidates(number)`, `getV2BlockByNumber/Hash→{Hash,Number,Round,Masternodes,EncodedQC,Error}`, `getEpochNumbersBetween(begin,end)`, `getBlockInfoByEpochNum(epoch)`, `getMissedRoundsInEpochByBlockNum(number)`, `getLatestPoolStatus→{VotePool,TimeoutPool}`, `networkInformation→{NetworkId,XDCValidators,Epoch,V2SwitchBlock}`, `getRewardByAccount(account,begin,end)`, `proposals/propose/discard`.

### 9.2 `eth_*` XDC extensions
`eth_getBlockSignersByHash/Number→[]address`, `eth_getBlockFinalityByHash/Number→uint(%)`, `eth_getCandidateStatus(coinbase,epoch)→{status∈{MASTERNODE,PROPOSED,SLASHED,PENALTY},capacity,...}`, `eth_getCandidates(epoch)`, `eth_getRewardByHash(hash)`, `eth_getStakerROI[Masternode]`. Standard `eth_getBlockBy*` responses include extra fields **`validators`, `validator`, `penalties`** (hex bytes) when non‑nil.

---

## Part 10 — P2P / BFT Protocol

- Protocol negotiated over devp2p as **`XDPOS2/100`** (advertised alongside eth/63/69/70 for mesh acceptance). Message‑code space length = 229.
- Consensus message codes (RLP body = the struct from 6.1):
  - `VoteMsg = 0xe0 (224)`, `TimeoutMsg = 0xe1 (225)`, `SyncInfoMsg = 0xe2 (226)`.
- Blocks propagate via standard `NewBlockMsg (0x07)`; the `TD` field is ignored by v2 (QC‑chain decides canonicality).
- 🪤 TRAP (#971): a server caps `GetBlockHeaders` at **`MaxHeaderFetch = 192`**; legacy XDPOS2 peers reply **empty (count=0)** to over‑cap requests. A syncing client MUST request ≤192 headers per round or it will stall against the live fleet.

---

## Part 11 — Validator‑Readiness Obligations

To act as a masternode/validator a node MUST:
1. **Be in the active set** — staked + proposed via `0x88`, selected into the epoch set (top‑N by stake − penalties). Verify with `XDPoS_getMasternodes`.
2. **Mint when scheduled** — v1: when `M[N % len(M)] == self`; v2: when `masternodes[round % … ] == self` and not already mined that round (track `highestSelfMinedRound`). Never mint gap blocks.
3. **Emit signing‑txs** to `0x89` (gasless) for blocks it witnesses, so it earns rewards and avoids penalty.
4. **Participate in BFT** — verify each proposed block, broadcast a **Vote** (signed `VoteSigHash`), broadcast **Timeout** on round expiry, and **SyncInfo** for liveness; aggregate ≥`CertThreshold` votes into a QC to drive the next block.
5. **Sign with the masternode key** loaded via the client's `Authorize(signer, signFn)` equivalent.

---

## Part 12 — END‑TO‑END CONFORMANCE TEST SUITE

Run against a **synced XDC archive node** of the target network (mainnet recommended for breadth; apothem for v2.8). Reference RPC endpoints: a trusted archive node you control. Each gate has explicit **PASS** criteria; the port is validator‑ready only when **all pass**.

> **Live validation status (2026‑06‑21):** Gates **A** (genesis + system contracts) and **B** (header RLP / block‑hash — 7/7 sampled blocks across v1, v1‑checkpoint, v2, and apothem‑v2‑with‑baseFee re‑hashed to canonical) are **CONFIRMED** against live mainnet + apothem with zero consensus‑claim discrepancies. Gates **C–J** require a re‑execution harness and/or a private net (read‑only RPC cannot run them). **Tooling caveat:** the reference repo's `core/types/gen_header_json.go` omits `Validators`/`Validator`/`Penalties`, so an RPC‑fed harness that round‑trips headers through `types.Header` JSON will zero them and fail B for the *wrong* reason — re‑populate those three fields from the raw RPC JSON before hashing (consensus unaffected; the RLP encoder is correct).

> Recommended harness: a small driver that, for a block `N`, pulls `(pre‑state at N‑1, block N RLP)` from the reference node, feeds them to `<CLIENT>`, and diffs outputs. Use `debug_*`/`eth_*` to fetch state and `XDPoS_*` for sets.

### A — Chain recognition & genesis parity
- Build genesis for chain id 50/51/5551. **PASS:** computed genesis block hash == Part 2.1 value exactly (this proves alloc, system‑contract bytecode, extraData, and header‑RLP field order are all correct).
- Direct system‑contract presence: public XDC RPCs **do** serve `eth_getCode(addr, "0x0")` for the genesis/early window (only deep mid‑history ~3M is pruned, returning "missing trie node"). Confirm `0x…88` (~14.4 KB) and `0x…90` (~0.8 KB) at block 0 on both nets, and `0x…89` present at genesis (~0.8 KB, contains sign selector `0xe341eaa4`) but **empty at latest** (self‑destructed at `TIPSigning`). *(Live‑confirmed 2026‑06‑21 on mainnet + apothem.)*

### B — Header RLP / block‑hash parity
- Fetch 1,000 random headers spanning v1 and v2 ranges. Re‑encode each with your header RLP and hash it. **PASS:** `keccak(RLP(header)) == canonical block hash` for 100%. *(Catches Validators/Validator/Penalties placement and optional‑field ordering.)*

### C — v1 block validation parity
- Replay validation over a contiguous v1 range (e.g. mainnet `0 … 1,000,000` incl. ≥3 epoch checkpoints). For each block verify: signer recovery, in‑turn/difficulty rule, checkpoint masternode list, anti‑spam. **PASS:** every block accepted with the same signer/difficulty the canonical chain has; checkpoint masternode lists match `XDPoS_getMasternodes`.

### D — v2 block validation parity
- Replay validation over a v2 range incl. ≥3 epoch switches and ≥1 round‑timeout (TC) occurrence. **Select epoch‑switch blocks by round (`IsEpochSwitch`), not `number % 900`** — that selector is v1‑only (see 6.2 TRAP); in the v2 range it yields empty `Validators`/`Penalties` and silently skips the blocks this gate must check. Verify QC signature counting, proposer derivation, epoch‑switch `Validators`/`Penalties`, `GapNumber` binding. **PASS:** all blocks valid; QC quorum recomputed ≥ threshold; proposer matches; **derived penalty set == canonical** (the #204 hazard).

### E — State‑transition parity (the hard gate)
- For each block `N` in chosen ranges (must include: a normal block, a block with special signing‑txs, an EIP‑1559 block, a Cancun block, and an epoch‑checkpoint block): load pre‑state, execute, compare. **PASS:** `post‑stateRoot`, `receiptsRoot`, `gasUsed`, and every receipt status/logs match canonical **exactly**. Special‑tx blocks must show the gasless accounting; checkpoint blocks must show the exact reward credits (owner 90 / foundation 10, with the #186 skip).

### F — Reward & penalty parity
- At ≥5 epoch checkpoints (mix of pre‑ and post‑`TIPUpgradeReward` on apothem): diff the balance deltas of every credited account and the next‑epoch masternode/penalty set. **PASS:** reward amounts per owner + foundation match to the wei; penalty set and active set match `XDPoS_getMasternodes`/`getCandidates`.

### G — Sync parity (live integration)
- **G‑sync:** from a clean datadir, sync the chain genesis→head over p2p against the live fleet. **PASS:** head hash matches a public explorer; zero `BAD BLOCK` / `invalid merkle root` / state‑root‑mismatch events; respects the 192‑header cap (no stall).
- **G‑rpc:** run the Part‑9 method catalog against your node and a reference node for 50 sampled blocks. **PASS:** shape‑and‑value identical (allow documented ordering differences only).

### H — BFT wire parity
- Connect your node to a reference v2 node on a private net. **PASS:** your Vote/Timeout/SyncInfo messages are accepted (signatures verify on the reference side) and you correctly accept/aggregate the reference node's messages into QCs; `XDPoS_getLatestPoolStatus` shows growing pools.

### I — Validator readiness (definition of done)
- On a **private XDPoS net** with ≥4 nodes mixing `<CLIENT>` and reference nodes: stake + propose your node via `0x88`, wait for epoch inclusion, then **mint blocks accepted by the reference nodes** and **participate in QC formation** across ≥2 epoch switches without forking or being penalized. **PASS:** reference nodes build on your minted blocks; your node appears in `getMasternodes`; finality (`eth_getBlockFinalityByNumber`) reaches 100% on your blocks; no "validators not legit" at the epoch boundary.

### J — v2.8 fork parity (apothem, time‑boxed)
- Across apothem block `83,600,000` (Prague/EIP‑2935 + TIPUpgradeReward/Penalty): verify the history‑storage contract (EIP‑2935) behavior, the tiered reward switch, and **forkid** (EIP‑2124) equals the live fleet so the node peers. **PASS:** node stays canonical across the fork; tiered rewards credited correctly; node peers with the live apothem fleet (matching forkid).

> 🪤 TRAP (forkid): two nodes with different fork schedules compute different EIP‑2124 forkids and **refuse to peer even with identical genesis** — confirm your full fork list (incl. `pragueBlock`, `tipUpgradeRewardBlock`) matches the live network before expecting peers.

---

## Part 13 — Implementation Order & Pitfall Checklist

1. Header type + RLP (Part 4) → pass **B** on a header dump offline (no sync needed).
2. Genesis + system contract bytecode (Part 3) → pass **A**.
3. v1 engine (Part 5) → pass **C**.
4. EVM fork wiring + special‑tx gas exemption (Parts 2.2, 8) → pass **E** on v1 blocks.
5. v2 engine + QC verify (Part 6) → pass **D**, then **E** on v2 blocks.
6. Rewards/penalties (Part 7) → pass **F**.
7. p2p XDPOS2 + 192‑cap (Part 10) → pass **G‑sync**.
8. RPC (Part 9) → pass **G‑rpc**.
9. BFT minting/voting (Part 11) → pass **H**, **I**.
10. v2.8 (Parts 2.3/7.2) → pass **J**.

**Top pitfalls that silently corrupt consensus:** header field order/presence (B); v2 sigHash including `Validators`+`Penalties` but excluding `Validator` (4.1); `GapNumber` binding in QC/votes (6.4); penalty‑count algorithm (#204, 7.3); foundation double‑credit (#186, 7.1); special‑tx exactly‑68‑byte guard (8.1); 192‑header cap (#971, 10); forkid fork‑list completeness (J).

---

## Part 14 — Autonomous Execution Harness (model routing + loop + workflow)

This document is meant to be **executed by an autonomous multi‑agent system**, not just read. Three model tiers drive the port, two primitives (`/loop`, `Workflow`) sequence the work, and one goal ladder is driven to green on **every** XDC network (mainnet, apothem, net5050, devnet).

### 14.0 Model routing — **Fable + Opus plan · Sonnet executes**

| Tier | Model | Role | Owns |
|---|---|---|---|
| **Orchestrator** | **Fable 5** (`claude-fable-5`) | fast strategic planning | decompose the goal ladder, sequence goals × networks, route each task to the right tier, arm the `/loop`, decide what fans out in parallel |
| **Architect** | **Opus 4.8** (`claude-opus-4-8`) | deep / irreversible planning + adversarial review | any decision that changes a **block hash / state root / receipts root / reward / penalty / masternode set**; state‑model adaptation; first parity‑mismatch root‑cause; the **REFUTE** verdict in a validation workflow |
| **Executor** | **Sonnet** (`claude-sonnet-4-6`; use the latest Sonnet — e.g. **Sonnet 5** — when available) | implementation + gate runs | write the code, run each Part‑12 gate, apply the *minimal* fix, rebuild, re‑run, record evidence, save the handoff doc |

**Routing rules (never invert):** Fable plans and dispatches → Sonnet implements and validates → Opus is the scalpel for consensus‑critical / irreversible calls and **signs off or refutes every "gate passed" claim** before it is trusted. Sonnet MUST escalate any block‑hash‑affecting decision to Opus rather than guess; Fable MUST NOT hand a consensus root‑cause to Sonnet without an Opus plan. (Haiku may do pure grep/lookup only.) Cost discipline: keep Opus for the irreversible scalpel work; Fable for cheap high‑level sequencing; Sonnet for the bulk execution.

### 14.1 The two primitives
- **`/loop` — self‑paced iteration ("go ahead until the goal is achieved").** *Fable* arms it; *Sonnet* runs each iteration: run → validate → if failing, root‑cause (escalate to *Opus* on any consensus mismatch) + apply the *minimal* fix + rebuild + re‑run → reschedule a wake‑up → repeat. **Never stop until the goal's gate PASSES.** On PASS, *Fable* advances to the next goal (or next network) and re‑arms. Only interrupt the human (PushNotification) for a genuine blocker — a canonical value the live chain disagrees with, or a decision that changes scope.
- **`Workflow` — parallel multi‑agent validation/implementation.** *Fable* authors the fan‑out; *Sonnet* subagents do the parallel replay/implementation; ***Opus* subagents adversarially verify** every "gate passed" claim (default REFUTE on any field mismatch) before declaring green. Fan out naturally‑parallel work: per‑block parity replay, the independent Part‑12 gates, multi‑subsystem code mapping — **and each of the four networks in parallel.**

### 14.2 Goal ladder × network matrix (validator‑ready on EVERY network)
Each goal Gk drives one gate to PASS. The port is **not done** until the whole ladder is green **on every target network** — Fable sequences goals *and* networks; run the four networks' gates in parallel where independent.
```
                            mainnet(50)  apothem(51)  net5050(5050)  devnet(5551)
G1  header/hash ....... B       ▢            ▢             ▢              ▢
G2  genesis .......... A        ▢            ▢             ▢              ▢
G3  v1 validation .... C        ▢            ▢             ▢              ▢
G4  EVM+special-tx  E(v1)       ▢            ▢             ▢              ▢
G5  v2 + QC ....... D,E(v2)     ▢            ▢             ▢              ▢
G6  rewards+penalty . F         ▢            ▢             ▢              ▢
G7  p2p sync ..... G-sync       ▢            ▢             ▢              ▢
G8  RPC parity ... G-rpc        ▢            ▢             ▢              ▢
G9  BFT mint+vote . H,I         ▢            ▢             ▢              ▢
G10 fork parity ... J         n/a¹         v2.8          all-forks       v2.8
```
¹ mainnet activates v2.8 later — run G10 on apothem/devnet/net5050 now, mainnet when it forks.

**Per‑network intent (what "validator‑ready" concretely means on each):**
- **mainnet (50)** — sync genesis→head (head hash == public explorer); mainnet validator dry‑run (gated) — the production bar.
- **apothem (51)** — sync + testnet masternode; carries the **v2.8 fork** (Prague + DynamicGasLimit + TIPUpgradeReward/Penalty).
- **net5050 (5050)** — the mainnet‑parity **multi‑client stress net**; the real cross‑client proof: your node's blocks must be **minted and accepted by the legacy + geth + erigon + reth + nethermind + besu producers** (QC formed ≥ threshold across clients), and your node must accept theirs.
- **devnet (5551)** — v2.8 canary; smallest surface to shake out fork‑activation parity before apothem/mainnet.

`IsEpochSwitch`‑by‑round selection, **penalty‑set bit‑exactness** (the #204 hazard), and **mint‑acceptance** must hold on **all four**.

**Definition of validator‑ready = the full G1…G10 × {mainnet, apothem, net5050, devnet} matrix is green.** When the matrix is green, Fable **sets a new top‑level goal** and repeats the same loop+workflow cycle: `7‑day soak per network` → `mainnet validator dry‑run` → `100% RPC parity` → `within 1 minor version of upstream <CLIENT>` (via the upstream auto‑sync automation) → … indefinitely.

### 14.3 The loop prompt (re‑arm per goal × network) — model‑routed
```
/loop Port XDC to <CLIENT> — drive goal G<k> (<gate>) to PASS on <network ∈ {mainnet-50, apothem-51, net5050-5050, devnet-5551}>.
 ROUTING: Fable plans + sequences goals×networks + arms this loop; Sonnet executes
 each iteration; escalate ANY block-hash / state-root / receipts-root / reward /
 penalty / masternode-set decision to Opus (never let Sonnet guess consensus).
 Each wake (Sonnet): run the gate's validation workflow against a synced <network>
 archive node — and for G9, a mixed-client private net / net5050 — and read results.
 If a check fails: Opus root-causes the failing block/diff and hands Sonnet a MINIMAL
 fix; Sonnet rebuilds + re-runs. An Opus subagent must REFUTE-test any "gate passed"
 claim before it counts. If the gate PASSES on this network: record evidence; Fable
 advances to G<k+1> (or the next network) and re-arms. Else reschedule ~1200s —
 NEVER stop until PASS. Save a handoff doc (current goal, network, last failure,
 next step) every iteration so another agent/session resumes. PING the human only on
 a real blocker (a canonical value the live chain disagrees with, or a scope change).
 DONE only when G1…G10 are green on ALL FOUR networks.
```

### 14.4 The validation workflow (per gate, parallel + adversarial)
Example — gate E (state‑transition parity), pipelined over a block list so each block verifies as soon as it's replayed:
```js
export const meta = { name: 'xdc-gate-E-stateparity',
  description: 'Replay blocks on <CLIENT>; diff post-state/receipts vs canonical archive',
  phases: [{title:'Replay'},{title:'Verify'}] }
const blocks = args.blocks  // [normal, epoch-checkpoint, special-tx, 1559, cancun, ...]
const out = await pipeline(blocks,
  b => agent(`Load pre-state @${b-1}, execute block ${b} on <CLIENT>; return {stateRoot,receiptsRoot,gasUsed,receipts}`, {schema: PARITY}),
  r => agent(`Adversarially diff this vs the canonical archive node; REFUTE on ANY field mismatch`, {schema: VERDICT}))
return out.flat().filter(v => !v.confirmed)   // empty ⇒ gate E PASS
```
Run independent gates (B, C, D, F, G‑rpc) as a single `parallel([...])` fan‑out; ordered gates (A→C→E→F) as sequential loop goals. For exhaustive ranges, shard the block list across many `pipeline` items (cap ~16 concurrent).

### 14.5 Repeat
Every gate PASS triggers: record evidence → set next goal → re‑arm the loop. When the ladder completes, set a fresh top‑level goal and repeat — the loop never idles while a goal is open, and the workflow gives each goal parallel, adversarially‑checked validation. This is the unattended "implement → validate → advance → repeat" engine that takes a `<CLIENT>` port from zero to XDC‑validator‑ready and keeps it in parity over time.

---

*Generated from the go‑ethereum v1.17.3 XDC reference fork. Every numeric constant should be re‑confirmed against a live archive node before relying on it for a production validator — file corrections where the chain disagrees.*
