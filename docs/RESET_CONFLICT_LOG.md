# Baseline reset conflict log (refs #740)

One line per conflict resolved during baseline reset bundle work.

| # | Bundle | Commit | File | Resolution mode | Notes |
|---|---|---|---|---|---|
| 1 | B0 | `40f00e878` feat(params): Add XDC mainnet and Apothem chain configurations | `params/config.go` | XDC-verbatim (40f00e878 side is a superset) | XDC adds `omitempty` JSON tags + V2/V2Config types to XDPoSConfig. Pure XDC additions, no upstream involvement. Non-F-bucket file. |
| 2 | B1 | -X theirs (intra-XDC: later wins) for f5749c631..e40a3414c | many | bulk strategy | 10 commits in Phase A continuation. Conflicts auto-resolved by giving precedence to the later (= more-recent XDC) version. |
| 3 | B1 | cleanup commit `25dbd93c6` (decision-Option-B) | `cmd/XDC/**`, `build/ci_xdc.go`, `Makefile`, `params/config.go` | drop cmd/XDC + dedupe | Per issue #740 comment-4524720763: drop XDC binary fork; vanilla cmd/geth from v1.17.3 is the supported binary. Also removes duplicate XDCMainnetChainConfig/XDCApothemChainConfig (-X theirs kept both versions). |
| 4 | B3 | (synthetic — F-bucket policy) | `eth/protocols/eth/protocol.go`, `eth/protocols/eth/handler.go`, `eth/protocols/eth/handlers.go` | Option C minimal wire | Add `XDPOS2 = 100` + `VoteMsg/TimeoutMsg/SyncInfoMsg` + `VotePacket/TimeoutPacket/SyncInfoPacket` + `xdpos2` handler map mirroring eth/70 + switch case. Drop legacy ETH/62/63/68 entirely. Per `[PLANNER-ADVICE 2026-05-23 eth-protocols]`. |
| 5 | B4 | -X theirs (intra-XDC) | `eth/handler_eth.go`, `eth/protocols/eth/handlers.go`, scripts | misplacement fixed by hand | abe54fdb4's XDPoS2 cases ended up inside handleTransactions; moved into Handle() switch + restored upstream handleTransactions tail. |
