# Handoff — #952 apothem halt recovery + Track A open question (2026-06-14)

## Track B — Apothem (#952): HALT root-caused + code-fixed; RECOVERY PENDING OPERATOR
- Our modern validator (0x11c3637d) HALTED the public Apothem testnet at block 83,088,791 by
  minting with the upstream GasCeil default (60M) instead of XDC's 420M genesis gas limit.
  Full analysis: issue **#952**. Network-wide wedge verified (block 83,088,792 = null on erpc + earpc).
- **Code fix committed + pushed: `629b8d02b`** — `miner/xdpos_worker.go` `xdcGasCeil()` targets the
  genesis gas limit at both mint sites. Builds clean.
- **RECOVERY (operator action — harness classifier blocks the agent from restarting a public
  validator):** restart the validator with `--miner.gaslimit 420000000`:
  ```
  ssh -p 12141 root@135.181.117.109 'PID=$(pgrep -f "datadir /root/.xdc-validator-apothem"|head -1); CMD=$(tr "\0" " " < /proc/$PID/cmdline); printf "exec %s --miner.gaslimit 420000000\n" "$CMD" > /root/.xdc-validator-apothem/relaunch-gaslimit.sh; kill $PID; sleep 10; systemd-run --unit=geth-validator-apothem --setenv=HOME=/root /bin/bash /root/.xdc-validator-apothem/relaunch-gaslimit.sh'
  ```
  When the node next proposes it builds 83,088,792 @ 419,999,599 (in-bound) → QC → chain advances →
  returns to 420M within ~2 blocks. Status at this iteration: still wedged at 83,088,791.

## Track A — devnet empty-Validator gate-relax: HELD, premise needs verification
The pending task was: relax the empty-Validator reject at `consensus/XDPoS/xdpos.go:662`
(`verifyHeaderLightV1`) for devnet, mirroring legacy `IsTestnet → fullVerify=false`.

**Blocker found (do NOT implement blind):** in legacy XDPoSChain, `common.IsTestnet` is set
**only for Apothem (chainId 51)** — `common/constants.all.go CopyConstants` sets it for chainID 51;
the flag path sets it for `--testnet`/NetworkId 51. **Devnet (551/5151) and mainnet (50) keep
`fullVerify=true`** → legacy *enforces* the empty-Validator check on devnet. That contradicts the
Track A premise. If legacy devnet enforces but modern relaxes, modern accepts blocks legacy rejects
→ devnet state divergence.

**Required before coding:** empirically confirm legacy devnet-5151 block 1962's `Validator` field —
is an empty-Validator >epoch block actually canonical on the running legacy devnet? If YES (legacy
devnet runs with the check relaxed, e.g. launched `--testnet`), relax modern for devnet too. If NO,
the real fix is different (modern must co-sign / fetch the co-signed sibling, not relax). Needs a
legacy devnet RPC. Also reconcile with #951 Defect 1c (xdc04 minted 295 >900 blocks cross-client-
matched) and why the full-verify path (xdpos.go:1039) only LOGS while the light path (662) hard-rejects.

Decision: not implementing a second consensus change blind right after the #952 halt. Verify first.
