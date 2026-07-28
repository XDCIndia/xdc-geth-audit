# Apothem validator 0x11c3637d — MINT CONFIRMED (2026-06-14)

Task #182 / #187 / #193. The Apothem validator (modern geth, host **135.181.117.109**,
datadir `/root/.xdc-validator-apothem`, RPC `127.0.0.1:8656`, coinbase
`0x11c3637d2848ebdc190d56d8b81348c21446e866`) is now an **active, block-producing V2
masternode** on the public Apothem testnet. The user proposed it into the validator set;
this doc records the verification.

## 1. Masternode-set comparison — IDENTICAL

`XDPoS_getMasternodesByNumber("latest")` at block **83,088,791**:

| Source | Count | Contains 0x11c3637d |
|---|---|---|
| Canonical Apothem (`erpc.apothem.network`) | 33 | YES |
| 109 validator (`127.0.0.1:8656`) | 33 | YES |

Programmatic set diff: **byte-identical** — `only-in-canonical = []`, `only-in-109 = []`.
The 109 node's view of the active masternode set equals the public network's exactly.

## 2. Minting — block 83,088,791 produced and accepted network-wide

`0x11c3637d` produced block **83,088,791** (the chain head at the time of check). Hash parity:

```
validator (127.0.0.1:8656) : 0xafe14b854f3722bef0c075bfd1a86156662f9267078332ff9ad11d6742626201  miner=0x11c3637d…e866
erpc.apothem.network        : 0xafe14b854f3722bef0c075bfd1a86156662f9267078332ff9ad11d6742626201  miner=0x11c3637d…e866
```

Byte-identical hash on the validator and on independent public infrastructure → the block our
node minted is **canonical on the public Apothem network**. This is the definitive
apothem-mint validation: modern geth now **syncs AND mints** accepted V2 blocks on Apothem.

The node just joined the active set (it expanded 32→33 with our node), so it has produced 1
block so far (its debut). It is rotating correctly: log shows `[yourturn] … curIndex=11
leaderIndex=N myaddr=0x11C3637D…` — it knows its slot (index 11 of 33) and waits its turn.

## 3. Observation — transient network-wide head stall at 83,088,791

At the time of verification, head was paused at 83,088,791 across **all three** endpoints
(erpc, earpc, the validator). V2 rounds were rotating (leaderIndex 0→4→5, proposers
0xEae878c4 / 0x59ebbff1 / 0x05A18817) but none produced 83,088,792. This is a **network-wide
liveness stall**, NOT our node:
- Our block 83,088,791 is canonical on independent public infra (accepted, not rejected).
- Our node is index 11, not the stalled leader; it waits its turn correctly.
- Timeout certs are forming (leaderIndex advances), implying a working ≥2/3 quorum.

Not a parity/safety issue (our node agrees with canonical). Coincides with the masternode-set
expansion to 33; most likely a transient testnet blip. **Monitoring for resume** — when the
network advances and our node's round (index 11) comes up, it should mint again. If the stall
persists, investigate whether the 32→33 set change caused a proposer-order / QC-threshold
hiccup.

## Status
- #187 SYNC blocker: RESOLVED earlier (full-body restore, crossed 83,049,946 clean).
- Apothem MINT precondition (staking/propose 0x11c3637d): SATISFIED by user → node is in the
  active set and has minted an accepted block.
- Apothem-mint validation: **COMPLETE** (set parity + hash parity on a self-minted block).
- Remaining watch item: confirm network resumes past 83,088,791 and node mints on later rounds.
