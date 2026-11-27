# Hacken audit remediation: XDC Network (XinFin L1)

Report: Hacken final report, 2026-10-13. Audited commit: `45e2b6ee`.

## Eighth hand-over (27 November 2026): Hacken final remediation, branch `hacken/final-remediation`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Branch | `hacken/final-remediation` |
| Base | `main` at `56436982f82e641df0d9e4e8d8e0373a50dd6661` |
| Code head | **`b054a3002b9db2547a494343cda283eeb54e0aa9`** (the last commit before the docs commits, which only change this file) |
| Range | `56436982f82e..b054a3002b9d` (45 commits without docs: 44 with a finding id and one that drops non-essential tests; no merges) |
| Source | XDCIndia/go-ethereum; every commit is a port of existing source commits (`Source:` lines in the commit body), with their tests; no new design |

Only Hacken findings are in the range. Each commit carries its source commit(s), the three trailers and the source tests. A finding whose fix was later repaired in the source has the repair as a second commit that says so (F-2026-19463, F-2026-19608, F-2026-19609, F-2026-19612, F-2026-19459 and F-2026-19602 with F-2026-19462). Each fix keeps one focused regression test (more only where a finding has separate fix parts), the edits to existing tests that the new behaviour needs, and the helpers those tests use. Where a source test sits in a file that is not in the audit tree, the test and the helpers it needs are in a new finding-named file, as the commit body says.

### Pending Fix findings

| Finding | Status | Commit(s) on this branch | Files | Tests |
|---|---|---|---|---|
| F-2026-20648 | Fixed | `dbde1f02336f89120520cbf018b9062cfdce5d7e` | `cmd/geth/main.go`, `cmd/utils/flags.go`, `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/verifyHeader.go`, `consensus/XDPoS/utils/errors.go`, `consensus/XDPoS/utils/utils.go`, `core/blockchain.go`, `core/state/statedb_utils.go` and 7 more | `TestF202620648ForgedValidatorListRefusedWithoutState`, `TestProveGapStateRefusesBadProofs`, `TestProvenWitnessNegatives` |
| F-2026-20645 | Fixed | `1c437ee020e59ae5a52ad58539dac6704974de44` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/utils.go`, `consensus/XDPoS/engines/engine_v2/verifyHeader.go` | `TestF202620645OneSignatureBranchAboveAnchorRejected`, `TestF202620645SideBranchBelowAnchorRejected` |
| F-2026-20605 | Fixed | `bfb5f3bfcd8c1497b0977b93c03231d3aa6f1aca` | `eth/handler.go`, `eth/handler_eth.go` | `TestF202620605KnownHeadReplayDropped`, `TestNewBlockParkingNeedsKnownParentAndByteBudget` |
| F-2026-20604 | Fixed | `d5863b945269d2f89675d8c00d1fb09d53b63283` | `consensus/XDPoS/engines/engine_v2/engine.go` | `TestF202620604InitialNoReaderNoGoroutineLeak` |
| F-2026-20644 | Fixed | `0f441d7380c84200089d6d52755d447781cacb78` | `eth/sync_xdc.go` | `TestPeerAdvertisesTip`, `TestPeerAdvertisesTipXDPOS2Peers` |

**F-2026-20648 (Fixed).** A fast-synced node holds no state below its snap pivot. The header of an epoch switch carried the validator list that then authenticated its own seal, so one key could install its own list. Two source changes close it. First, on a node without state, above the range its anchor authenticates, the header's list must keep more than two thirds of the set that signed the parent (taken from verified epoch info or the locally computed list); a rotation passes, a list chosen by the header does not, and a switch whose set cannot be derived is refused with ErrEpochSwitchSetNotDerivable and retried. Second, the below-pivot gap snapshots are rebuilt from the 0x88 candidate storage fetched with eth_getProof from --snap.gap-rpc and checked node by node against the gap header's state root (a forged node is not found, a withheld one is a read error); the result is stored as a version 7 gap snapshot that is never replaced by a header-seeded one. This replaces the fast-sync pivot rule suggested in the report (pick the pivot at or below the next switch's gap block). That rule cannot be applied as stated: epoch switches drift below multiples of 900, so the gap block that a given pivot must sit at or below is not a fixed height that can be named in advance, and a pivot rule would have to be re-derived against every peer-reported switch. The proven gap snapshot needs no particular pivot, leaves the pivot choice alone, and removes the header from the trust path for every switch above the pivot. --snap.gap-rpc defaults to a built-in list on XDC mainnet.

**F-2026-20645 (Fixed).** The catch-up window skipped verifyQC for every block up to the anchor plus two epochs, so one masternode signature extended the chain of a fast-synced node. A header now skips the certificate checks only when it is on the anchor's parent-hash chain (tracked with a bounded walk of the anchor's parents), the leader-mismatch tolerance ends at the anchor, the round-skip timeout certificate exception follows the same test, and the 3-chain commit is skipped only while the parent or grandparent is missing.

**F-2026-20605 (Fixed).** Future-timestamp NewBlock packets are parked on a timer. This commit bounds them: one block per peer, 16 in total, only a block at head or head plus one, a byte bound of 32 MiB for all parked blocks, a known parent header, no hash that another peer has already parked, and an early return for a block already in the chain before header verification and import. An invalid NewBlock is now verified first and a peer that sends five definitively invalid blocks is disconnected.

**F-2026-20604 (Fixed).** setNewRound, UpdateParams and the three sends in initial() ran go func() { ch <- p }(). A node that does not mine has no reader on the mine-period channel, so each call parked a goroutine forever. All of them now use a non-blocking send that replaces the stale buffered value. No consensus rule changes.

**F-2026-20644 (Fixed).** The at-tip check skipped XDPOS2 peers and let the highest eth/69 advertised head decide, so one peer advertising a low head ended bulk sync. XDPOS2 peer heads (verified tip and header-batch tip) now count; while XDPOS2 peers are connected and no tip is known the check reports not at tip; an advertised head at or below the local head is ignored.

### Other findings in this range

| Finding | Status | Commit(s) on this branch | Files | Tests |
|---|---|---|---|---|
| F-2026-19457 | Mitigated | `0978257f3bb1e98459955d26a5db59c8cec6d9dd` | `core/txpool/legacypool/legacypool.go`, `core/txpool/legacypool/list.go`, `core/txpool/txorder/ordering.go`, `core/txpool/validation.go`, `core/types/transaction.go`, `miner/worker.go` | `TestF202619457OtherCalldataPaysTheTip`, `TestNewTxWithMinerFeeSpecialExemptionNeedsKnownSelector` |
| F-2026-19459 | Mitigated | `5b554306a45828b7c8dd24de7a87a603f2eba5cf`, `9419c4bbf2ce05d7a5d9028788df4e6de2245f8c` | `common/types.go`, `core/state_processor.go` | `TestF202619459EmptyRouteNonceOnDevnetChains` |
| F-2026-19460 | Fixed | `489de49bcd20d8204ea16c41056a6c34cce89a26` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/xdpos.go`, `eth/backend.go`, `eth/bft/handler.go` | `TestF202619460OwnSendAfterStopDoesNotBlock` |
| F-2026-19462 | Mitigated | `4e6c35b90f5078bfe2a65eac3098d1c244070df2`, `2eaf10f70a90c2c8f4a4658ae4a6d5a2bb0bdbc4` | `cmd/utils/cmd.go`, `cmd/utils/flags.go`, `consensus/XDPoS/xdpos.go`, `core/block_validator.go`, `eth/hooks/hooks.go` | `TestF202619602OutsiderExtraRejectedNothingWritten`, `TestImportChainStartsABatchAtEachCheckpoint`, `TestV1HookRecheckWithoutParentStateIsAnError`, `TestValidateBodyReplaysDeferredV1Validators` |
| F-2026-19463 | Mitigated | `6febe087a100ebc5feaf4493c1a66abb45cece5d`, `d84346d7efa5be92516201d31ac901a81b70bf01` | `consensus/XDPoS/engines/engine_v2/verifyHeader.go`, `consensus/XDPoS/utils/errors.go`, `consensus/XDPoS/xdpos.go`, `core/blockchain.go`, `eth/sync_xdc.go` | `TestF202619463ReorderedListRefusedAtTip`, `TestVerifyHeaderLeaderTurn_CheckpointEpochSwitchKeepsLocalList` |
| F-2026-19468 | Fixed | `248117d3aba4b0271be710d21269dee7a8567117` | `consensus/XDPoS/engines/engine_v2/verifyHeader.go` | `TestF202619468Sync11DoesNotTrustHeaderListUnderFullVerify` |
| F-2026-19469 | Fixed | `3b145b3be9742e6137f780540d55fe3b8af3f6a8` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/forensics.go`, `consensus/XDPoS/engines/engine_v2/timeout.go`, `consensus/XDPoS/engines/engine_v2/utils.go`, `consensus/XDPoS/engines/engine_v2/vote.go` | `TestF202619469TimeoutEquivocationSendsAProof` |
| F-2026-19471 | Fixed | `ca4042205d494601b45423f1d216f2e5870016ab` | `cmd/utils/flags.go`, `eth/ethconfig/auto_pivot_xdc.go` | `TestAutoCheckpointPivotRefusesUnverifiedPin` |
| F-2026-19596 | Fixed | `662cbe06588ccfefac66910921b1cc303ee6d3cb` | `consensus/XDPoS/engines/engine_v2/timeout.go` | `TestF202619596VerifyTCIgnoresStandbySigners` |
| F-2026-19601 | Fixed | `d2d3543aba93e3d55c6f994f6ef170795a7bfa96` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/verifyHeader.go`, `params/config.go` | `TestF202619601BlockAccessListHashIsInTheSeal` |
| F-2026-19602 | Accepted | `9f3b5f20a8c0ff9634e511a0427d3c8baba862e7`, `2eaf10f70a90c2c8f4a4658ae4a6d5a2bb0bdbc4` | `cmd/utils/cmd.go`, `cmd/utils/flags.go`, `consensus/XDPoS/xdpos.go`, `eth/hooks/hooks.go` | `TestF202619602ForgedExtraRejectedOnFullMainnet`, `TestF202619602OutsiderExtraRejectedNothingWritten`, `TestImportChainStartsABatchAtEachCheckpoint`, `TestV1HookRecheckWithoutParentStateIsAnError` |
| F-2026-19608 | Accepted | `5966fb533782d6fb2b14505ad539176cbc998983`, `93559643966e13597966474e31ed4fb3f36f9cc3` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/timeout.go`, `consensus/XDPoS/engines/engine_v2/verifyHeader.go`, `consensus/XDPoS/engines/engine_v2/vote.go`, `consensus/XDPoS/utils/errors.go`, `core/types/consensus_v2.go`, `params/config.go` | `TestF202619608ActiveGateRejectsSkipWithoutCertificate` |
| F-2026-19609 | Accepted | `213bd0ef5e2d15f72e5178e0a895d60805a8c843`, `132e4f9753afc15e6b694319cab66dea43adf385` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/utils.go` | `TestF202619609LockFollowsTheCertifiedBlock`, `TestF202619609RestartRaisesHighestQCToStoredLock` |
| F-2026-19612 | Fixed | `8e72af187119ffcba029822c017b3c9313d081a7`, `c6bbae16e72055b322b1db4963d79ff6dd393629` | `consensus/XDPoS/engines/engine_v2/timeout.go`, `consensus/XDPoS/engines/engine_v2/vote.go` | `TestF202619612TimeoutGapFollowsRoundEpoch`, `TestF202619612VerifyTCGapCheckedBeforeSnapshot` |
| F-2026-19614 | Fixed | `8866a463e7f89d484609b654a9340e79981c75ca` | `consensus/XDPoS/xdpos.go` | `TestV1LightPathUnassignedM2AfterRandomize` |
| F-2026-19615 | Accepted | `a8fc1afecaf8abc7aa6960efb1c683ff39949b26` | `consensus/XDPoS/xdpos.go` | `TestF202619615V1GasLimitUnbounded` |
| F-2026-19621 | Accepted | `b0b52302de7efd15e05a611dd3cc6c93230e1b0f` | `consensus/XDPoS/xdpos.go` | `TestF202619621PenaltiesOutsideSeal` |
| F-2026-19636 | Fixed | `e6b0308d4e5d623c8a8590c1b5eb2fc12856c9ed` | `eth/sync_xdc.go` | `TestF202619636UnverifiedHeadersDoNotRollBack` |
| F-2026-19637 | Fixed | `d9378e385bfbb41af8b7446d087e593b8076d19b` | `consensus/XDPoS/engines/engine_v2/engine.go`, `eth/hooks/engine_v2_hooks.go` | `TestHookPenaltyV2PostUpgradeComeback` |
| F-2026-19638 | Mitigated | `0049213c8c4d8a1643393ae6f81cd4cafe30137a` | `consensus/XDPoS/engines/engine_v2/engine.go`, `internal/ethapi/api_xdc.go` | `api_xdc_finality_19638_test.go` |
| F-2026-19639 | Mitigated | `221fab689e1bd55f7e1cfad3b2079c56aff25cfd` | `consensus/XDPoS/api_extended.go` | `TestF19639V1BlockInfoMatchesCheckpoint` |
| F-2026-19643 | Fixed | `500db5575ed50a3afddc02e5910206c3afea50e2` | `miner/worker.go` | `TestF202619643MinerSkipsBlacklistedAddress` |
| F-2026-19644 | Fixed | `22062c33bbd8a792367e119d091939af88bc2cbf` | `consensus/XDPoS/api_extended.go`, `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/xdpos.go`, `eth/hooks/engine_v2_hooks.go` | `TestGetStandbynodes` |
| F-2026-19723 | Accepted | `ea621dc76a66f31bd8e4e190645211534cce4c2f` | `core/blockchain.go` | `TestBulkSyncRejectsMissingTrieNode` |
| F-2026-19725 | Fixed | `22062c33bbd8a792367e119d091939af88bc2cbf` | `consensus/XDPoS/api_extended.go`, `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/xdpos.go`, `eth/hooks/engine_v2_hooks.go` | `TestGetStandbynodes` |
| F-2026-19726 | Fixed | `2795e0ae3ecb3e369790dec4cc601d4a745d373b` | `common/countdown/countdown.go`, `consensus/XDPoS/engines/engine_v2/engine.go` | `TestF202619726InitialUsesRoundTimeout` |
| F-2026-19773 | Fixed | `eb76546fc7d63bc4c4f56ecfaaeb0d5a21a1b9e5` | `consensus/XDPoS/engines/engine_v2/engine.go`, `consensus/XDPoS/engines/engine_v2/utils.go`, `core/blockchain.go`, `core/rawdb/accessors_xdc.go` | `TestF202619773CommittedBlockSurvivesRestart`, `TestReorgRefusedWhenNewChainReplacesCommittedHash`, `TestSetHeadRefusedBelowCommittedBlock` |
| F-2026-19805 | Mitigated | `ea523d9470baeb773838bab8edb8915eaf36fcc3` | `consensus/XDPoS/reward_file.go`, `eth/api_backend.go` | `TestF19805CreditedAmountNotGrossSignerReward` |
| F-2026-19815 | Accepted | `192a617a9ec0cca60120b612833e636f830f9c7b` | `consensus/XDPoS/engines/engine_v2/verifyHeader.go`, `consensus/XDPoS/utils/utils.go`, `params/config.go` | `TestF202619815ShippedGatesLeaveReorderedListsValid` |
| F-2026-20591 | Fixed | `4752879f544d509a0463b653017c4af0daa80f9b` | `core/vm/contracts.go`, `core/vm/contracts_xdc.go`, `core/vm/privacy/bulletproof.go`, `internal/ethapi/privacy_window.go` | `TestF202620591RPCBulletproofCommitmentCap` |
| F-2026-20597 | Fixed | `4de16802acff66c450ac0d5bbc3e40e55c391552` | `eth/handler.go`, `eth/protocols/eth/handlers.go`, `eth/protocols/eth/peer.go` | `TestXDPOS2RequiredBlockForeignBatch`, `TestXDPOS2RequiredBlockWithSyncerReading` |
| F-2026-20610 | Fixed | `0e94ca80eb6d632552803a66ff1f83fbb534dc48` | `core/rawdb/accessors_indexes.go`, `eth/downloader/downloader.go`, `eth/sync_xdc.go` | `TestF202620610ReceiptRootRequired` |
| F-2026-20612 | Fixed | `b1c823e752ea5c0c6a000a9adcfccf67d8eb0388` | `eth/sync_xdc.go` | `TestF202620612MismatchedBodiesTaintRaceWinner` |
| F-2026-20641 | Fixed | `6d174ded21a6674aaed105567ba95407a2667fb5` | `core/state_processor.go`, `miner/worker.go` | `TestFeeOwnerReadPoint` |

**F-2026-19457 (Mitigated).** A zero-tip transaction to BlockSigners or RandomizeSMC is fee-exempt only when its calldata is a sign, setSecret, or setOpening call. Other calldata pays the tip and does not take the signer fast path. txorder exempted any transaction to 0x89 / 0x90 from the fee-cap check. Use KnownSpecialCall, as the pool admission, miner filter and special-tx prefix already do, so an unknown-selector call below the base fee gets ErrGasFeeCapTooLow. The sender check keeps the candidate pool on purpose (canonical parity, see the issue).

**F-2026-19459 (Mitigated).** Chain IDs 5151, 5050 and 5551 still accept the empty route. A repeated payload is rejected there. Mainnet and Apothem keep the historical receipt with no nonce change. Canonical v2.7.1 common/constants.local.go (and v3 params/config_networks.go:538,:566) sets TIPXDCXReceiverDisable to 0 for the local chain, so IsTIPXDCXReceiver is false on every block and a tx to 0x91-0x94 runs through the EVM and pays gas. Chain 5151 had the 1e17 sentinel, which kept the zero-gas empty-tx route open. Chains 5050 and 5551 have no canonical value and keep the sentinel. The 5050 comment no longer claims canonical evidence.

**F-2026-19460 (Fixed).** Select on the broadcast stop signal when sending this node's own BFT message, so a send that starts after the dispatcher stops does not block.

**F-2026-19462 (Mitigated).** Header batches are verified before their blocks are stored, so a missing parent defers the signer list instead of accepting the checkpoint. Body validation runs the check once the parent is written. Apothem chain 51 still skips every V1 checkpoint through the V2 switch at block 56,828,700, and a node with no parent state still skips, because rejecting those checkpoints would drop canonical history. evictPoisonedGapSnap treated any checkpoint whose Extra held an address absent from the gap snapshot as proof of a poisoned snapshot, rebuilt the gap snapshot from that same header and stored it. checkSignersOnCheckpoint then compared the header list with itself, so a checkpoint that added an outsider passed and the forged set was persisted. Canonical (v3.0.0-testnet-2 engine_v1/engine.go:255-268) does a snapshot check, then the contract fallback, and rejects otherwise. The pre-check call and the header-seeding function are removed. When the snapshot check fails and the contract state of the gap block passes the checkpoint, that contract set is stored as the gap snapshot (repair); otherwise the checkpoint is rejected and nothing is written. getSignersFromContract now also returns the gap header.

**F-2026-19463 (Mitigated).** An epoch-switch header whose validator list differs from the local list is not allowed to name its own leader while CheckpointSyncNoStateActive is set and the node is not in bulk sync. The in-batch bulk-sync tolerance stays. When calcMasternodes fails on a node without state (noStateSwitch) and the node is not in bulk sync, verifyHeader returns the error instead of taking the leader list from the header being verified, so a reordered Validators cannot pick the slot leader. The two-thirds overlap gate (F-2026-20648) stays for bulk sync above the anchor. ErrPenaltyDataUnavailable moves to consensus/XDPoS/utils (XDPoS keeps the same value) so core and eth can match it. Like ErrV2SnapshotMissing it writes no bad-block record and does not taint the peer, and the block is retried.

**F-2026-19468 (Fixed).** A missing gap snapshot or penalty ancestor no longer makes full verification use the header's own validator and penalty lists, and that list is not stored as the gap snapshot.

**F-2026-19469 (Fixed).** The monitoring and vote-equivocation checks run after a commit or a pooled vote. A failure from either call is written to the node log. The engine subscribes to its forensics feed and logs each proof. The loop ends when broadcast stops. Conflicting timeouts stay unchecked.

**F-2026-19471 (Fixed).** --auto-checkpoint-pivot pinned the highest compiled TrustedSyncCheckpoint on the binary's word. A checkpoint carries only number, hash, root and masternodes, so there is no quorum certificate to verify against the epoch masternodes. AutoPinPivotFromCheckpoints now logs a warning and does not pin; the operator uses --fastsyncpivot* or the dynamic pivot. Adding a QC to TrustedSyncCheckpoint and verifying it in that function re-enables the pin. Tests that asserted a pin now assert the refusal, and the selection predicate is tested directly. The ValidateBody bypass, the operatorPivotRoot initial target and the genesis auto-fill of the issue are not part of this change.

**F-2026-19596 (Fixed).** Canonical XDPoSChain builds a TC from every pooled timeout and verifies it against the candidate list, so one standby candidate signature made a canonical-built TC unusable here. Recover signers with no allowed set, count and verify only the round epoch's active masternodes toward the threshold, and ignore the rest. Candidates are not counted toward the threshold. The early abort on the first foreign signer is gone, so the signature count is bounded at 4x the gap snapshot's candidate list. The comment in onTimeoutPoolThresholdReached no longer claims canonical rejects such a TC.

**F-2026-19601 (Fixed).** The field is nil on every block before Amsterdam, so those seals stay the same. When the field is set, a different value no longer verifies under the same signature. v1.17.10-xdc.22 bound header.BlockAccessListHash into the V2 proposer seal whenever the field is set, with no activation block. On chains that already carry the field (net2040 after Amsterdam) every block sealed by an earlier release or by another client then recovers a wrong signer and is rejected ("validator address not within the masternode list"). An xdc.22 canary on net2040 stopped at 999,690 for that reason.

**F-2026-19602 (Accepted).** Chain 50 full verification already rejects a checkpoint extra that replaces the signer set. The seal fallback remains for Apothem, the light path, ignore-listed checkpoints and a damaged snapshot. Every V1 block is historical, so limiting the fallback would reject blocks current-main still imports. evictPoisonedGapSnap treated any checkpoint whose Extra held an address absent from the gap snapshot as proof of a poisoned snapshot, rebuilt the gap snapshot from that same header and stored it. checkSignersOnCheckpoint then compared the header list with itself, so a checkpoint that added an outsider passed and the forged set was persisted. Canonical (v3.0.0-testnet-2 engine_v1/engine.go:255-268) does a snapshot check, then the contract fallback, and rejects otherwise. The pre-check call and the header-seeding function are removed. When the snapshot check fails and the contract state of the gap block passes the checkpoint, that contract set is stored as the gap snapshot (repair); otherwise the checkpoint is rejected and nothing is written. getSignersFromContract now also returns the gap header.

**F-2026-19608 (Accepted).** Header verification only checked that the round was above the parent certificate, so a proposer could seal a block many rounds ahead without a timeout certificate. The check is gated by xdpos.v2.roundSkipTCBlock, which is unset on every network. With the gate unset the extra encoding and the accepted headers stay the same as today. verifyRoundSkip (F-2026-19608, RoundSkipTCBlock) called verifyTC, which finds the epoch of the timeout certificate from chain.CurrentHeader() and applies the gap rule against the head. With the gate set, two nodes could give a different verdict for the same header, or one node a different verdict at another time, depending on where their head was (for example a VerifyHeaders batch that runs before the head moves). Add verifyTCAt, which takes the parent of the header and the in-batch parents. The epoch of the certificate round is found by walking the epoch-switch parent links from the epoch of that parent (getTCEpochInfoAt), and the next-epoch gap exception uses the parent instead of the head (timeoutGapMatchesEpochAt). verifyRoundSkip uses it. verifyTC and the timeout message handlers keep the head, as canonical does (engine_v2/timeout.go in XDPoSChain v3.0.0-testnet-2); canonical has no RoundSkipTCBlock, so the gated path has no canonical counterpart. The checkCompatible item of the issue is in the change.

**F-2026-19609 (Accepted).** processQC stored the certificate carried in the certified block's header, which names the parent. A sibling of the certified block then passed the vote rule. The lock is now that certificate itself. Voting is local, so header validity is unchanged, and the next honest proposal still extends the certified block so a current-main node and this node vote for it. processQC locks on the certificate for the block just certified (head B). The head header carries only the certificate for B's parent P, so after a restart highestQuorumCert was QC(P), one block below the stored lock. Leaders built a sibling of B on P and verifyVotingRule refused it on every node that held the lock. A restart of the whole committee then stalled. initial() now raises highestQuorumCert to the loaded lock when the lock round is higher, its block is local and verifyQC passes (via processQC, so the round advances too). ReinitBFT keeps the higher in-memory certificate instead of falling back to the head header certificate.

**F-2026-19612 (Fixed).** A certificate or timeout message that names an older gap snapshot is rejected once the epoch switch block of that round is known. sendTimeout signs the gap of head+1 when the round opens a new epoch, before that epoch's switch block exists. The F-2026-19612 check compared it with the gap of the last known switch block, so every honest timeout at an epoch-switch round was rejected and no timeout certificate could form. This halted net 2040 at round 999000 (block 1,004,681). An older gap is still rejected. verifyTCAt loaded the gap snapshot for the certificate's unchecked GapNumber before the gap rule ran, and timeoutGapMatchesEpochAt passes when the epoch cannot be resolved. Resolve the TC round epoch first, return an error when the epoch info or its switch block is nil, run the gap check, and only then load the snapshot. A wrong-gap TC now fails with the gap error.

**F-2026-19614 (Fixed).** A nil chain config fails closed. Chain 51 still skips the check through the V2 switch, and a missing M2 assignment before TIPRandomize or on an ignore-listed checkpoint stays accepted.

**F-2026-19615 (Accepted).** V1 header verification does not bound the gas limit against the parent, matching canonical XDPoSChain. Every V1 block on mainnet, Apothem and net 2040 is historical, so a new bound would reject blocks current-main still imports. V2 verification already applies the 1/1024 bound and is unchanged. The source change to xdpos.go is a comment; the test locks the existing behaviour.

**F-2026-19621 (Accepted).** The penalty list is compared with the hook under full verification on non-Apothem chains. It stays out of the V1 seal hash: covering it would invalidate every historical seal on mainnet, Apothem and net 2040. The compare stays skipped on Apothem, on the light path and when penalty state is missing, because those paths still have to import canonical history. The source change to xdpos.go is a comment; the tests lock the existing behaviour. checkpoint_penalties_helpers_test.go holds a chain stub copied from checkpoint_penalties_test.go, which is not in the audit tree.

**F-2026-19636 (Fixed).** A peer that reports a different hash at the local height is not enough to move the head. The competing header has to pass consensus verification.

**F-2026-19637 (Fixed).** When a block in the penalty window has no body, the hook returns ErrPenaltyDataUnavailable instead of treating that block as zero signatures. TestApothemPenaltyReplay is opt-in (XDC_APOTHEM_CHAINDATA, a copy of an Apothem chaindata directory; XDC_APOTHEM_REPLAY_FROM raises the start above a snap pivot). It sets core.XdcForceFullVerify, picks stored epoch-switch headers after 83,604,500 where a signer sits in 2-4 consecutive Penalties lists plus one release, recomputes Penalties with the penalty hook and re-verifies each header with the V2 engine. A LimitPenaltyEpoch=1 negative control must diverge and be rejected.

**F-2026-19638 (Mitigated).** Signer RPCs use Author for a V2 block. A canonical block at or below the finalized head is reported as final, including a V1 block.

**F-2026-19639 (Mitigated).** GetCheckpointFromEpoch (canonical XDPoSChain v2.7.1) maps a V1 epoch to 0 below epoch 2, else Epoch*(epoch-1), and the candidate RPCs read that block. XDPoS_getBlockInfoByEpochNum used epoch*Epoch on V1, one epoch apart. Canonical has no V1 path for this RPC, so the V1 branch now follows the canonical checkpoint convention. RPC output only.

**F-2026-19643 (Fixed).** After the blacklist fork block, the miner skips a transaction whose sender or recipient is blacklisted, so it is not sealed into a block that other nodes would reject.

**F-2026-19644 (Fixed).** A failed gap snapshot is no longer an empty protector pool. Chain 5551 keeps the parent-state pool because that devnet's reward history was produced that way. The tier comment matches the snapshot path used on the other chains.

**F-2026-19723 (Accepted).** A bulk-sync follower that hit a missing trie node during execution treated the state-root mismatch as recoverable: it latched no-state mode, swapped in an empty trie and wrote the block. That block is now rejected and the flag is not set. A block whose execution matches the header root is still imported. This is local import policy and does not change which blocks are valid.

**F-2026-19725 (Fixed).** A failed gap snapshot is no longer an empty protector pool. Chain 5551 keeps the parent-state pool because that devnet's reward history was produced that way. The tier comment matches the snapshot path used on the other chains.

**F-2026-19726 (Fixed).** The first Reset in initial no longer keeps the round-zero period when the head is already in a later config and processQC has not run yet.

**F-2026-19773 (Fixed).** Write the three-chain committed block to the database when it changes and load it with the other BFT safety state. SetHead refuses to move below it. core: apply the XDPoS committed-block rewind guard from the stored marker

**F-2026-19805 (Mitigated).** GetRewardByAccount returned RewardLog.Reward, the masternode's gross epoch reward, for a signer and skipped the holder tables once a signer matched. The amount is now the address's entries in the rewards tables summed over every signer, so an address that is both signer and holder gets its holder shares. GetRewardByHash returns empty when --store-reward is unset instead of reading the file name relative to the working directory. The existing signer test expected the gross 100 and now expects the credited 60. RPC output only.

**F-2026-19815 (Accepted).** The order check already rejects an epoch-switch header once validatorOrderBlock is active. Mainnet and Apothem leave that block at the far-future placeholder, and every other shipped network leaves it nil, so a reordered list is still valid. Moving the block earlier on this client alone would reject headers current-main and legacy still accept. An edit to the comments of certik_1722_validator_order_test.go is not carried because that file is not in the audit tree.

**F-2026-20591 (Fixed).** RPC only. internal/ethapi swaps precompile 0x28 for a copy capped at 256 commitments (before the no-override early return), so eth_call, eth_estimateGas, eth_simulateV1 and debug_traceCall reject a larger proof from its 4-byte count prefix. The consensus instance has maxComms 0 and is unchanged (canonical has no cap). MRPVerify now checks abort in the commitment, tmp1, HPrime, tmp2 and inner-product loops and waits for ecMu with TryLock, so a cancelled call neither runs on nor sits queued behind another verification.

**F-2026-20597 (Fixed).** The version-100 required-block challenge returned, keeping the peer unchecked, when it read a header batch for another number. It now puts the batch back and keeps waiting for the reply at the challenge number until the timeout. The syncer-side draining of the challenge reply is not changed. validate XDPOS2 header reply before dropping the request

**F-2026-20610 (Fixed).** snapBlockComplete accepted a stored receipt list that did not hash to header.ReceiptHash when its length equalled the transaction count. It now returns true only when DeriveSha(receipts) == header.ReceiptHash. The PrimeAncestorBodies tx-lookup point of the issue is not part of this change. PrimeAncestorBodies replaced an anchor placeholder body without indexing its transactions, and when no receipts came with it the placeholder receipts stayed behind. It now writes the tx lookups with every body it writes, and keeps the placeholder when a body would replace it without receipts (the anchor-gap repair fills it).

**F-2026-20612 (Fixed).** fetchBodiesHedged reports hedgeFailed=false for any non-empty reply, so the taint after buildBlocks returned nothing was dead code. Taint the winner unconditionally on that exit, and the peer that supplied the bodies when it was the hedge. fetchBodiesHedged now also returns the supplier ID. Hedge retry on a mismatched primary reply is not changed. fetchBodiesHedged returned any non-empty reply, so a race winner that answered with bodies that do not belong to the headers got no hedge: the hedge only ran on a timeout or an empty reply. It now takes the headers and treats a reply whose first body fails verifyBodyAgainstHeader as no reply: the sender is tainted and the hedge starts at once.

**F-2026-20641 (Fixed).** The earlier per-transaction fee-owner read made a block that mainnet nodes accept (owner read once per block before the transaction loop, canonical v2.7.1) a block that nodes on current main would reject. The per-transaction read is now gated on IsTIPUpgradePenalty (nil on mainnet and the legacy-profile nets, Apothem 83,600,000 and later); core.Process and the miner follow the same rule, so a block built and imported gets the same fee routing. It mirrors the source change exactly.

### Findings with no commit in this range

**F-2026-20606 (Accepted).** No code change. The XDCx price precompiles only executed before TIPXDCXMinerDisable, which is active on all networks; changing the result would alter historical execution.

**F-2026-19467 (Mitigated).** The mitigation (cap on signing transactions per block, skip of malformed and future-block signing transactions) is already in the base of this range; no further change.

### Verify

```sh
git log --reverse --format='%h %s' 56436982f82e..b054a3002b9d
go build ./... && go vet ./consensus/XDPoS/... ./core/... ./eth/... ./cmd/utils ./params ./internal/ethapi ./miner
go test -count=1 -timeout 150m ./consensus/XDPoS/... ./core/... ./eth/... ./internal/ethapi ./miner ./cmd/utils ./params
```

Results on the code head `b054a3002b9db2547a494343cda283eeb54e0aa9`: gofmt and goimports report no file among the modified Go files; `go build ./...` passes; `go vet` passes on the packages above; `go test` passes in all 47 packages (`core` 581 s, `eth` 45 s).


## Seventh hand-over (22 November 2026): Hacken report 3 remediation on `main` at `56436982f82e641df0d9e4e8d8e0373a50dd6661`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Final commit (`main`) | **`56436982f82e641df0d9e4e8d8e0373a50dd6661`**: merge of the branch below into `main`; its tree equals the branch head |
| Branch / code head | `hacken/r3-remediation` / **`f7fa344c3061`** |
| Range | `23a5bb5bb..f7fa344c3061` (18 commits, no merges) |
| Base | `23a5bb5bb` (r14, source XDCIndia/go-ethereum `6fc8e8c2088b`, v1.17.10-xdc.21) |
| Findings | the 21 new findings of the updated preliminary report (1 High, 12 Medium, 8 Low) |

### Structure: one commit per finding

Each commit fixes one finding; a change that fixes several findings with one root cause is one commit that lists every ID. Every commit has a test, its source commit (`Source: XDCIndia/go-ethereum <sha>`) and the original message. No other changes are in the range: no upstream updates, features or other audit fixes.

| Commit | Finding(s) | Test file(s) | Source commit |
|---|---|---|---|
| `4426cbc4615e5070d5111f08b81f21596c96546e` | F-2026-20477: reject non-zero-cost signing tx in pool promotion | `core/txpool/legacypool/legacypool_20477_test.go` | `2225c42e4737d42230a567c7f76922cf582e20bc` |
| `b0feb28eae5b0911beda6f6e697503db956a8baf` | F-2026-20608: reject high-S signatures before pooling | `consensus/XDPoS/engines/engine_v2/high_s_20608_test.go` | `3796012cc4260d9c41d9a765bebe04df5bfdff77` |
| `3f22c16d0981f27a98c1e384b79e9d1155d788fd` | F-2026-20607: store a certificate epoch only after verifyQC accepts | `consensus/XDPoS/engines/engine_v2/qc_epoch_cache_20607_test.go` | `0193207a41dbb8c3de138e0f83c517ec039f9dd8` |
| `21cbf1a71a1b5473426ec8f7358b73807dbc1220` | F-2026-20636, F-2026-19774: keep the persisted vote lock across a restart | `consensus/XDPoS/engines/engine_v2/lock_restart_20636_test.go` `core/rawdb/bft_safety_test.go` | `1c693e17a46e3013a02f7566393eaabedf61b5d8` |
| `1cb5ddddb0725b0eb05361efb69bf0a02aec97cc` | F-2026-20590: keep one vote per signer per round in the vote pool | `consensus/XDPoS/engines/engine_v2/vote_pool_20590_test.go` `consensus/XDPoS/utils/pool_20590_test.go` | `0bcaff1825578a22b9123073629c61f2acfcabf6` |
| `892ad214bd90c6d2791d7205ed0d08da4aae07d6` | F-2026-19460, F-2026-20592: bound per-peer bft sends | `eth/handler_xdc_20592_test.go` | `83a08c922675a07808333e1c3d33256b92be154c` |
| `8ed39e02f56b8038ab7c9eb9de7441d15e2ecf96` | F-2026-20612: taint the header-race winner when bodies do not extend the head | `eth/sync_xdc_20612_test.go` | `8ff96db5519418586356158a239626f8e8afa1b3` |
| `aa8f95f8fe27abc39bd7d36a03b46dfa2af1c324` | F-2026-20637, F-2026-20613: check prefetched bodies and receipts against the header before the write | `eth/downloader/prime_ancestor_bodies_test.go` | `1a13ca126d58b39fbf7ee1b96d8ebfa77a4cac8c` |
| `cdfd3198939df957b962008de2425971085aeb64` | F-2026-20610: do not count fast-sync anchor placeholders as a finished block | `eth/sync_xdc_20610_test.go` | `38f5ea7038209047e5d61ca27a0d68819d78d85c` |
| `633e6df0a969233f29906e7179d2bfd4371ee438` | F-2026-20639, F-2026-19805: report signer and holder rewards separately | `consensus/XDPoS/api_helpers_20639_test.go` `consensus/XDPoS/reward_file_20639_test.go` `internal/ethapi/api_test.go` `internal/ethapi/transaction_args_test.go` | `174eb0b7d34d8a21e6b2cddc21db23a69e54da37` |
| `d7058d6daf6f7d410654f1100bab3563357a6c1e` | F-2026-20603: include TRC21 fee-capacity accounts in the scoped pool reset | `core/txpool/legacypool/legacypool_20603_test.go` | `8dc4dd851d0dd7ff47874a8efa46513b0251d7cd` |
| `409803b6f8a5085e989756693786f443a48a001e` | F-2026-20640, F-2026-19639: resolve candidate RPCs with the epoch checkpoint | `internal/ethapi/api_xdc_test.go` | `b830d7010126de062c5aad894fddf324d2b147aa` |
| `5067335b86c4594e0a1ecb8bde6ace26e02e4e9c` | F-2026-20597, F-2026-20609: check the required-block challenge without taking the syncer's header | `eth/sync_xdc_20609_test.go` | `ab9d7bba2ce58d380331779e794340fedef36bd3` |
| `75c149452881d58420c1bc6772078882bd8b0698` | F-2026-20596: run checkBodyItemCounts on version-100 bodies before hashing | `eth/protocols/eth/handler_test.go` | `179f642c72ae55b0dcb209e8e1a3c219407cbc41` |
| `cbe108bcbcd669a998ea98a4ba1f99b6d6d3b947` | F-2026-20638: treat a stale signing-transaction nonce as ErrNonceTooLow | `core/signing_nonce_20638_test.go` | `ccdbc194f9f14763b38c09ca7d4268a07a8e7600` |
| `f74c44a4a16627765c25e1cf50ef0f392a0a4dc5` | F-2026-20611: cache a fork-guard verdict only when the reply number matches | `eth/sync_xdc_20609_test.go` | `6ae00fe7ebec9750b005d60b412941cafe8e9492` |
| `78328eb4e80544c6abd84cb7d77a47e40b9f5725` | F-2026-20591: keep privacy precompiles inside the loaded state's window | `core/vm/contracts_fuzz_test.go` `core/vm/contracts_test.go` `core/vm/precompile_cache_test.go` `core/vm/privacy/bulletproof_20591_test.go` `internal/ethapi/privacy_window_test.go` | `9969d925e64f3f4c1acab471ce886955568d2deb` |
| `f7fa344c306162605b8555cdee2dbaf25aca45e8` | F-2026-20641: read the fee owner for each transaction | `core/hunt35_fee_owner_per_tx_test.go` | `f6cce2146a57c08db8674fa75f5273e27598bdb5` |

### Decisions

- **F-2026-20606:** Accepted, no commit. The XDCx price precompiles only executed before TIPXDCXMinerDisable, which is active on all networks; changing the result would alter historical execution.
- **F-2026-20641:** fixed (previously Accepted as needing a hard fork). Canonical XDPoSChain v3.0.0-testnet-2 already reads the fee owner per transaction (`core/state_processor.go:442-443`, `:652`). The only validator-contract paths that change a candidate's owner are `propose` (never the block's own validator) and the `voteInvalidKYC` delete, so no fork block is needed.

### Test files

The audit tree leaves out new XDC test files. For this range each fix brings its test. Where the source test was in a file that is not in the audit tree, the test and the helpers it needs are in a new finding-named file, as the commit body says.

### Verify

```sh
git log --reverse --format='%h %s' 23a5bb5bb..f7fa344c3061
go build ./... && go vet ./...
go test -count=1 -timeout 120m ./consensus/XDPoS/... ./core/... ./eth/... ./internal/ethapi ./miner
```

All 45 packages pass on `f7fa344c3061` (`core` 970 s).

## Sixth hand-over (28 October 2026): revalidate `main` at `6dc4de6202a19ecd672acd74e1552f0476fd88a6`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Final commit (`main`) | **`6dc4de6202a19ecd672acd74e1552f0476fd88a6`**: merge of the branch below into `main`; its tree equals the branch head |
| Branch / code head | `audit/main-uplift-11710-xdc20-r13` / **`14eb294ba8b8147dc99ca6fa2e4d02aba284257a`** |
| Previously revalidated commit (base) | `5d31c76662306c68627d3cb2b41a4bf0b521d440` (fifth hand-over, r12; `main` `2eb1332f3eda` has the same tree) |
| Source | XDCIndia/go-ethereum `08d25cafd425d249cf213ea68e05866434399f0a` (tag `v1.17.10-xdc.20`) |
| Upstream | unchanged: go-ethereum v1.17.10 |
| Out of scope | docs, scripts, CI and audit reports |

### Structure: one commit per fix

**12 commits on top of `5d31c7666230`, one per source fix**, in source order, no merges. Each has the fix's original message and author, its code, and a `Source: XDCIndia/go-ethereum <sha>` line.

Summary of the changes:

- Transaction pool: the reorg opens the new head state before it takes the pool lock, and reset demote/promote is limited to the senders in the new block.
- Consensus engine (no rule change): votes are pooled up to 5 rounds ahead. An epoch switch after fast sync is not rejected when the gap state is missing below the sync pivot. Engine start-up does not walk the full chain under the engine lock.
- Sync: bulk-mode lock fix, snap/2 advertisement, fetcher back-off on XDPoS, and full block history after fast and snap sync. A node synced with a history cutoff can restart and rewind.
- Consensus engine (no rule change): a gap snapshot seeded from an epoch-switch header below the snap pivot is not used for the penalty check; the header penalties are trusted and rechecked later.
- **New fork (Apothem only):** TIPProportionalReward at block 84,733,000, ported from XDPoSChain v3.0.0-testnet-2. From that checkpoint each masternode, protector and observer gets `perNodeReward × min(sign, signSlots) / signSlots`; the unearned share is not minted. Mainnet is not scheduled. `tipUpgradeRewardBlock` is renamed `tipFixedRewardBlock` (legacy key still read).

**XDC test files.** 13 new XDC test files are not included. Tests already in the base follow the source. The suite compiles (`go vet ./...` passes).

Excluded files:

- `consensus/XDPoS/engines/engine_v2/gap_snapshot_below_pivot_test.go`
- `consensus/XDPoS/engines/engine_v2/penalty_ancestor_missing_test.go`
- `consensus/XDPoS/engines/engine_v2/reinitbft_walk_1929_test.go`
- `consensus/XDPoS/engines/engine_v2/vote_lookahead_1665_test.go`
- `core/blockchain_trusted_anchor_test.go`
- `core/txpool/legacypool/legacypool_1665_scoped_demote_test.go`
- `eth/fetcher/tx_fetcher_xdc_1665_test.go`
- `eth/snap_advertise_1922_test.go`
- `eth/sync_xdc_1665_test.go`
- `eth/sync_xdc_1927_test.go`
- `consensus/XDPoS/engines/engine_v2/snapshot_below_pivot_1931_test.go`
- `consensus/XDPoS/reward_proportional_test.go`
- `core/blockchain_xdc_gapfill_test.go`

### How to verify

```
git log --first-parent --oneline 5d31c7666230..14eb294ba8b8   # 12 fix commits
git rev-parse 6dc4de6202a1^{tree} 14eb294ba8b8^{tree}           # equal trees
git checkout 6dc4de6202a1 && go build ./... && go vet ./...
go test -timeout 60m ./consensus/XDPoS/... ./core/... ./eth/...
```

### Commits (audit commit → source commit)

| Audit commit | Source | Change |
|---|---|---|
| `7324162d75c073c242e69755ab5942026fffd914` | `7bfee6b49c37` | core/txpool/legacypool: open the new head's state before taking pool.mu in runReorg |
| `5280c04cdc2208b18cc129bad493dee003e04480` | `e686e8fc9e25` | core/txpool/legacypool: scope reset demote and promote to the new block's senders |
| `7f8ecf807a33728570a3068155eee66e8748d977` | `b966e2d522e4` | eth: fix xdcSyncer bulk-mode lock and stuck-peer cluster |
| `93997f84facce7e2dfa7c16ff42dd0851f7024d9` | `0ae7f6389369` | consensus/XDPoS: pool votes up to 5 rounds ahead |
| `fefbca182c973fa256510c56b0e373336035f1b0` | `87f9b81b4e4c` | eth/fetcher: skip the refused-batch sleep on XDPoS chains |
| `fab69d04ade7505c308500b0c71699f6ba19bbb2` | `a64fdd477664` | eth: do not advertise snap/2 on fast-sync nodes healing over snap/1 |
| `dd23befcac67b48513d268fe89a96bfafd75a79c` | `0b5bc3c3bd85` | consensus/XDPoS, eth: do not reject epoch switch after fast sync on missing gap state |
| `966eba64d1e7b5dd84a61dff60cf44db3c0a07cc` | `965b95df7ee1` | consensus/XDPoS: drop O(chain) header walk under engine lock in initial |
| `619e08a835929e671d7078de301d24e3147ab55f` | `66176966e305` | core, eth: backfill full block history after fast and snap sync |
| `82edabec667aa41b51205eeb596f3c99325c5c22` | `8a2d545621f3` | consensus/XDPoS: keep header-seeded gap snapshots below the snap pivot from penalty checks |
| `e92a644d393827478c7cc8a303b4a2cd04088d58` | `ab1119ac00b2` | core, eth: repair the history tail after fast and snap sync with a history cutoff |
| `14eb294ba8b8147dc99ca6fa2e4d02aba284257a` | `08d25cafd425` | params, eth/hooks, consensus/XDPoS: add the proportional reward fork (Apothem 84,733,000) |

## Fifth hand-over (26 October 2026): revalidate `main` at `2eb1332f3eda9f0262e2561d69ef8287eaa92dad`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Final commit (`main`) | **`2eb1332f3eda9f0262e2561d69ef8287eaa92dad`**: merge of the branch below into `main`; its tree equals the branch head |
| Branch / code head | `audit/main-uplift-11710-xdc20-r12` / **`5d31c76662306c68627d3cb2b41a4bf0b521d440`** |
| Previously revalidated commit (base) | `7520ece168f792f39835882d5f13b74c4491f9b0` (fourth hand-over, r11) |
| Source | XDCIndia/go-ethereum `aa0b36dec0be43f17a0f6b102ef13ad50747db67` |
| Upstream | unchanged: go-ethereum v1.17.10 |
| Out of scope | docs, scripts, CI and audit reports |

### Structure: one commit per fix

**6 commits on top of `7520ece168f7`, one per source fix**, in source order, no merges. Each has the fix's original message and author, its code, and a `Source: XDCIndia/go-ethereum <sha>` line.

The last commit (`5d31c7666230`) is a correctness fix for the fourth hand-over. The gap-snapshot repair added there could rewrite a real gap snapshot as a header seed. That happens when the candidate pool equals the validator list (small networks) and the node restarts between the gap block and the epoch switch. It produced a different state root at that switch. The repair now runs only for gaps below a recorded snap/fast-sync pivot, the only place header seeds exist. Validated with a full sync from genesis on a private test network: the previous code reproduces the divergence; this code matches the canonical root.

**XDC test files.** 2 new XDC test files are not included. Tests already in the base follow the source. The suite compiles (`go vet ./...` passes).

Excluded files:

- `consensus/XDPoS/finalize_hooked_state_test.go`
- `eth/sync_xdc_1886_test.go`

### How to verify

```
git log --first-parent --oneline 7520ece168f7..5d31c7666230   # 6 fix commits
git rev-parse 2eb1332f3eda^{tree} 5d31c7666230^{tree}           # equal trees
git checkout 2eb1332f3eda && go build ./... && go vet ./...
go test -timeout 60m ./consensus/XDPoS/... ./core/... ./eth/...
```

### Commits (audit commit → source commit)

| Audit commit | Source | Change |
|---|---|---|
| `a8d2b97c9198e13e59be7ae25bedfe77429dd533` | `d9e9590dfc91` | eth: drop empty-strike counts on peer disconnect and stop the gap repair on quit |
| `6ea6fb61d44d65ae8e4ada8d9b25712af54ee3bd` | `e8f9f307a111` | consensus/XDPoS: unwrap the tracing hooked state in every Finalize path |
| `c2cf53e50d084c08b2391e0ff7e1168165928383` | `de6424a0bdd9` | eth: prefer history-serving peers in snap backfill, batch receipts, log progress |
| `ce185d3094127be7f74f0fd7666b8a2fbd9867dd` | `9dfc8684da94` | eth: keep the log index paused for the whole xdc bulk sync |
| `2bfb4e07170b12adad2fc829679982f032259fa4` | `2bdae9c064af` | core/rawdb: read backfilled canonical hashes behind zero freezer placeholders |
| `5d31c76662306c68627d3cb2b41a4bf0b521d440` | `aa0b36dec0be` | consensus/XDPoS, eth: repair header-seeded gap snapshots only below the sync pivot |

## Fourth hand-over (25 October 2026): revalidate `7520ece168f792f39835882d5f13b74c4491f9b0`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Branch | `audit/main-uplift-11710-xdc20-r11` |
| Commit to revalidate | **`7520ece168f792f39835882d5f13b74c4491f9b0`** |
| Previously revalidated commit (base) | `08d60e39096625b798a901e91a2470ed994c6be8` (third hand-over, branch `audit/main-uplift-11710-xdc20-r10`) |
| Source | XDCIndia/go-ethereum `504a353c31c11f055e448010242b69563be9f82e` (main after v1.17.10-xdc.20) |
| Upstream | unchanged: go-ethereum v1.17.10 (no upstream merge in this hand-over) |
| Out of scope | docs, scripts, CI and audit reports |

### Structure: one commit per fix

The branch is a fast-forward of the third hand-over: **18 commits on top of `08d60e390966`, one per source fix**, in source order, no merges.
- Each commit has the fix's original message and author, its code (and changes to tests already in the base), and a `Source: XDCIndia/go-ethereum <sha>` line.
- No commit changes block, vote or QC validity rules. Two commits restore parity with the canonical client on nodes that start from fast/snap sync or from an empty data directory:
  - `core: patch missing fork blocks in the fresh-datadir chain config`: the consensus hooks of a node started from an empty data directory used the fork schedule without the missing-fork patch until the first restart (Apothem paid flat rewards after `TIPUpgradeRewardBlock`).
  - `consensus/XDPoS, eth: replace header-seeded gap snapshots with the contract pool`: a gap snapshot seeded from an epoch-switch header during fast/snap sync is replaced with the stake-sorted 0x88 candidate pool, as a fully synced node stores it.
- The other commits are sync, downloader, RPC-shape and storage fixes.

**XDC test files.** 12 XDC test files that would be new in this hand-over are not included, to keep the review diff small. Tests already in the base follow the source. The suite compiles (`go vet ./...` passes).

Excluded files:

- `consensus/XDPoS/api_masternodes_1900_test.go`
- `consensus/XDPoS/engines/engine_v2/repair_seeded_pool_1884_test.go`
- `consensus/XDPoS/snapshot_inbatch_parents_1888_test.go`
- `core/blockchain_pathdb_archive_test.go`
- `core/genesis_fresh_fork_patch_test.go`
- `core/rawdb/chain_freezer_test.go`
- `eth/downloader/fetchers_concurrent_bals_xdc_test.go`
- `eth/protocols/eth/receipts_xdpos2_1889_test.go`
- `eth/sync_xdc_1876_test.go`
- `eth/sync_xdc_1884_test.go`
- `eth/sync_xdc_buildblocks_test.go`
- `eth/sync_xdc_stopwait_test.go`

### How to verify

```
git log --first-parent --oneline 08d60e390966..7520ece168f7   # 18 fix commits
git checkout 7520ece168f7 && go build ./... && go vet ./...
go test -timeout 60m ./consensus/XDPoS/... ./core/... ./eth/...
```

### Commits (audit commit → source commit)

| Audit commit | Source | Change |
|---|---|---|
| `65f377504a2e8aa8f9830f51447cd80e3af632b7` | `d5caa7dc4dac` | eth: bulk-sync lookahead window, hedged ranges and partial-import resume (review SYNC-12) |
| `320f8e451275b76e0b63d572e9d5cc836ce7665d` | `c2af9504159a` | eth, eth/downloader: keep peers through fresh fast/snap sync on xdpos2 v101 |
| `781bb043b93dd1a386414a006c78612ddd609428` | `a828ed43248f` | eth: skip snap peers that are still syncing when choosing the snap pivot |
| `db4d7934d5487f2fc5e8a6edf13ec5a767f47d3b` | `221cc08a8a53` | core/rawdb/eradb: convert the upstream era fixtures from raw rlp in TestEreDatabase |
| `e43b0aa94b88d2436474ba4cc5d7ca33f6762777` | `dce9d3394954` | eth: wait for xdc syncer background goroutines before the database closes |
| `73ba0ff6099b7b6a9eb4041a60409ba2d8951b48` | `a6ec713f3ec3` | eth/protocols/eth: accept the eth/69 receipt shape on xdpos2 receive |
| `928ec10578f704897c874b0174a0fa7326d63141` | `4e21daea06f4` | core: back off the xdc td backfill while history below the pivot is missing |
| `2c7f58d40077e8a5eff1d1e679380df1e7a0f998` | `32e68433b9d7` | eth: snap sync on apothem — fork-guarded tip, anchor from the head's peer, keep progress across re-pivots |
| `f9a17739d69183138c6d1a04be9825b57ada740e` | `78406034afa3` | core/rawdb: write the bals placeholder in the freezer's fast-sync gap fill |
| `0b7f63669536cca65ad26a61b029eebdf5b4a5be` | `90afae1cc817` | consensus/XDPoS: consume each in-batch parent in the v1 snapshot walk-back |
| `283c7c180aaff5caf23e895746bdb60d207c89d4` | `256871988906` | eth: report the network tip, not the last header batch, in eth_syncing |
| `8990c1812d12eebfa25453e0e381acf5f87f4852` | `0466dd5708f8` | eth: walk a peer's announced head forward to its real tip |
| `07defb3cd315f34cd70aa5f938399d4eaaf1cb18` | `67ae1e12aec0` | eth: keep peers on an abandoned branch out of bulk sync |
| `e80a43d2b7ca8018aed583160d6b3046b1e40060` | `219a3bb0d090` | core: patch missing fork blocks in the fresh-datadir chain config |
| `9ce73cad1e9f2a9c2e514f53fbae0b073f920971` | `2d81f8f903b0` | consensus/XDPoS, eth: replace header-seeded gap snapshots with the contract pool |
| `1d21b439f17c35ab9ac04b51ed2a84b856b3d4ed` | `7b52d831d67c` | core: index state history immediately for path-scheme archive nodes |
| `66ab40aec31a1312a67a7f2ad76b0bda98eac73a` | `377dfbcd6273` | eth/downloader: keep xdpos2 peers out of 512-batch skeleton cycles |
| `7520ece168f792f39835882d5f13b74c4491f9b0` | `504a353c31c1` | consensus/XDPoS: match canonical XDPoS_getMasternodesByNumber response |

## Third hand-over (24 October 2026): revalidate `08d60e39096625b798a901e91a2470ed994c6be8`

### Scope

| Item | Value |
|---|---|
| Repository | XDCIndia/xdc-geth-audit |
| Branch | `audit/main-uplift-11710-xdc20-r10` |
| Commit to revalidate | **`08d60e39096625b798a901e91a2470ed994c6be8`** |
| Last fix commit | `71a581f0079cd7cae87397a9a9859a00920a83f7` (the 79th fix; the head commit only removes test files) |
| Previously revalidated commit (base) | `4bf67046816d5d2731c4f2bf78d9f327d7886618` |
| Source | XDCIndia/go-ethereum `d0eda1964`: release v1.17.10-xdc.20 plus the sync fix for peers that cannot serve a range |
| Upstream | go-ethereum v1.17.9 `9dffe6e976cb` (branch `1179`) and v1.17.10 `1df30b18d9ed` (branch `11710`) |
| Out of scope | docs, scripts, CI and audit reports. Pure-upstream code arrives through the two upstream merges with their own history. |

### Structure: one commit per fix

From the base, the branch holds **79 commits, one per source fix**, in source order, then one commit that removes the XDPoS test files added after the original audit:
- Each commit has the fix's original message and author, its code (and changes to existing tests), and a `Source: XDCIndia/go-ethereum <sha>` line.
- Commits that fix a finding name it in the subject (CertiK, F-2026-…, or an internal review ID).
- **Upstream go-ethereum v1.17.9** enters as a real merge, `cf0dbb99f35de1df765bc6b7d8b735e388118e04` (second parent `9dffe6e976cb`, 38 upstream commits), at the point it was merged in the source.
- **Upstream go-ethereum v1.17.10** enters as a real merge, **`0076aa0ddcb4f9693205f7e0209b1db7a967cf89`** (second parent `1df30b18d9ed`, 9 upstream commits). XDC decisions in it:
  - the new transaction field validation is taken as-is (XDC special transactions are ordinary signed transactions);
  - the DUP/SWAP interpreter fold is taken (parity vectors pass);
  - snap.v2 auto-enable is gated off on XDPoS chains unless `--snap.v2` is set;
  - `cmd/geth/default.pgo` is upstream's.

**XDC test files.** 66 XDC test files that would be new in this hand-over are not included, to keep the review diff small. Upstream tests and XDC tests already in `4bf67046816d` are kept and follow the source. The suite compiles (`go vet ./...` passes).

**XDPoS test files removed.** The head commit `08d60e390966` (`consensus/XDPoS: remove test files added after the original audit`) deletes the 43 Go test files under `consensus/XDPoS` (18 in the package root, 24 in `engines/engine_v2`, 1 in `utils`) that were added after the originally audited commit `45e2b6ee`. The 21 test files present at `45e2b6ee` and the `testvectors` fixtures are kept, as is `unsealed_fields_test.go`, which holds a helper a kept test uses. The commit changes no non-test code, so it can be skipped when reviewing fixes.

Excluded files:

- `consensus/XDPoS/api_epoch_range_1756_test.go`
- `consensus/XDPoS/api_network_info_1757_test.go`
- `consensus/XDPoS/api_proposals_1755_test.go`
- `consensus/XDPoS/api_reward_scan_limit_1787_test.go`
- `consensus/XDPoS/engines/engine_v2/backfill_qc_1847_test.go`
- `consensus/XDPoS/engines/engine_v2/backfill_qc_1859_test.go`
- `consensus/XDPoS/engines/engine_v2/calcdiff_signer_1785_test.go`
- `consensus/XDPoS/engines/engine_v2/certik_1719_deferred_switch_recheck_test.go`
- `consensus/XDPoS/engines/engine_v2/certik_1722_validator_order_test.go`
- `consensus/XDPoS/engines/engine_v2/epochswitch_between_begineqend_test.go`
- `consensus/XDPoS/engines/engine_v2/forensics_guards_1854_test.go`
- `consensus/XDPoS/engines/engine_v2/forensics_mutex_1784_test.go`
- `consensus/XDPoS/engines/engine_v2/processforensics_guard_1786_test.go`
- `consensus/XDPoS/engines/engine_v2/selfmined_lock_1837_test.go`
- `consensus/XDPoS/engines/engine_v2/sigbomb_1707_test.go`
- `consensus/XDPoS/engines/engine_v2/snap_gap_seed_1863_test.go`
- `consensus/XDPoS/engines/engine_v2/snapshot_empty_test.go`
- `consensus/XDPoS/engines/engine_v2/verify_currentround_race_test.go`
- `consensus/XDPoS/engines/engine_v2/verifyHeader_sync11_1863_test.go`
- `consensus/XDPoS/engines/engine_v2/verifysyncinfo_race_test.go`
- `consensus/XDPoS/engines/engine_v2/yourturn_init_race_test.go`
- `consensus/XDPoS/pre_tx_state_reward_test.go`
- `consensus/XDPoS/reward_missing_header_test.go`
- `consensus/XDPoS/signer_lock_1783_test.go`
- `consensus/XDPoS/utils/pool_signer_cap_test.go`
- `core/blockchain_fastsync_pathdb_test.go`
- `core/blockchain_stale_canonicals_test.go`
- `core/blockchain_xdc_nilblock_test.go`
- `core/blockchain_xdc_sidechaincap_test.go`
- `core/certik_1721_signing_tx_bound_test.go`
- `core/rawdb/accessors_xdc_schema_test.go`
- `core/txpool/legacypool/legacypool_certik15_test.go`
- `core/txpool/legacypool/legacypool_certik31_test.go`
- `core/txpool/legacypool/legacypool_certik36_test.go`
- `core/vm/interpreter_xdc_tableloop_test.go`
- `core/vm/privacy/bulletproof_1790_test.go`
- `core/vm/privacy/bulletproof_1791_test.go`
- `core/vm/privacy/bulletproof_1824_test.go`
- `core/vm/privacy/bulletproof_1834_test.go`
- `core/vm/privacy/ringct_1771_test.go`
- `core/vm/privacy/ringct_1792_test.go`
- `eth/downloader/fast_closeonerr_sync14_test.go`
- `eth/downloader/fast_pathdb_snap_xdc_test.go`
- `eth/downloader/fast_snap_fallback_xdc_test.go`
- `eth/downloader/fast_stalepivot_sync16_test.go`
- `eth/downloader/faststatesync_nodedatacap_test.go`
- `eth/handler_eth_1799_test.go`
- `eth/handler_eth_1844_test.go`
- `eth/handler_xdc_test.go`
- `eth/hooks/certik_1723_parole_dedup_test.go`
- `eth/hooks/penalty_missing_header_test.go`
- `eth/protocols/eth/receipts_xdpos2_1839_test.go`
- `eth/sync_xdc_1761_test.go`
- `eth/sync_xdc_1762_test.go`
- `eth/sync_xdc_1798_test.go`
- `eth/sync_xdc_1823_test.go`
- `eth/sync_xdc_1848_test.go`
- `eth/sync_xdc_1857_test.go`
- `eth/sync_xdc_1861_test.go`
- `eth/sync_xdc_1869_test.go`
- `eth/sync_xdc_1872_test.go`
- `eth/sync_xdc_progress_test.go`
- `eth/sync_xdc_snap_gap_1863_test.go`
- `internal/ethapi/api_proof_delete_1758_test.go`
- `p2p/discover/table_reseed_test.go`
- `params/certik_placeholder_test.go`

### How to verify

```
git log --first-parent --oneline 4bf67046816d..08d60e390966       # 79 fix commits + 1 XDPoS test-file removal
git log -1 --format=%P cf0dbb99f35d                                 # <previous> 9dffe6e976cb… (v1.17.9)
git log -1 --format=%P 0076aa0ddcb4                                # <previous> 1df30b18d9ed… (v1.17.10)
git checkout 08d60e390966 && go build ./... && go vet ./...
go test -timeout 60m ./consensus/XDPoS/... ./core/... ./eth/...
# known: core/rawdb/eradb TestEreDatabase fails in the source too (upstream era fixture
# holds Ethereum headers without the XDC header fields); being fixed in the source
```

### Finding → commit

#### Hacken findings

| Finding | Commit | Change |
|---|---|---|
| F-2026-19525 | `62cdaf95709d052e713f080a8daf595e96969cad` | consensus/XDPoS/utils: cap vote-pool entries to one per signer per round (CertiK XDC-07) |

#### CertiK findings and internal review items

| ID | Commit | Change |
|---|---|---|
| CertiK P3 | `a2dd99f5c68e286e55a62fc197e455ef560edb07` | consensus/XDPoS: stop qc/tc recovery at the first non-masternode signer (CertiK P3) |
| CertiK XDC-07 | `62cdaf95709d052e713f080a8daf595e96969cad` | consensus/XDPoS/utils: cap vote-pool entries to one per signer per round (CertiK XDC-07) |
| CertiK XDC-30 | `87a97c84d24f989fda4908f6091184263bfab9a7` | consensus/XDPoS: include epoch-switch block in GetEpochSwitchInfoBetween when begin equals end (CertiK XDC-30) |
| CertiK XDC-35 | `c4614d3f0b7b360d01891a04247b0e4fbca4caf6` | consensus/XDPoS: reject empty persisted v2 gap snapshots (CertiK XDC-35) |
| CertiK XDC-13 | `5caa4f0d4efc68c48702bc1144241aec3f02a563` | eth: send bft broadcasts to peers without blocking on slow peers (CertiK XDC-13) |
| CertiK XDC-16 | `abc445c3585aa51dbf1fd3c5236feddd70b156d3` | eth/downloader: cap nodedata responses to the requested item count (CertiK XDC-16) |
| CertiK XDC-33 | `698fdd910caecbead52483afcb72e92fbc4f6c74` | p2p/discover: re-seed from bootnodes when the discovery table empties (CertiK XDC-33) |
| CertiK XDC-31 | `84cf44b40258ac00ffa7f707602a2c8ea8bd36b4` | core/txpool/legacypool: wait for tx events in the eviction test (CertiK XDC-31) |
| CertiK XDC-36 | `53be39b1edce5245f915a44406a4a3ac836dc2b3` | core/txpool/legacypool: skip reset post-processing when pool reset fails (CertiK XDC-36) |
| CertiK XDC-15 | `46cc367576ba6b9c13b543f038dc0fa6415a50c0` | core/txpool/legacypool: decouple pool reset from txFeed subscriber delivery (CertiK XDC-15) |
| CertiK XDC-22 | `61591fb2449cb07e3de7dcfc36ad49604c77bebd` | core: nil-check GetBlock in insertSideChain pruned-ancestor loop (CertiK XDC-22) |
| CertiK XDC-19 | `0761f4800796d67ec44e44dccb2bccf9cd45736f` | core: bound sidechain children per pruned parent (CertiK XDC-19) |
| CertiK XDC-26 | `03650a9ac4ef35326dc98fa6d27d1c401b542413` | core: emit reorg log events after the canonical indexes are written (CertiK XDC-26) |
| review R1-A01 | `88384097cfab2a29740770b064b30da2c324e18e` | consensus/XDPoS: initialise the v2 engine under the write lock in yourturn (review R1-A01) |
| review R1-A02 | `e26a9f0c3121a6b498f50aa8b97d05d14f513166` | consensus/XDPoS: read currentround under the lock in vote and timeout verification (review R1-A02) |
| review R1-A03 | `5e558f2fff1221c9577885dc09c4dde819cdc31f` | consensus/XDPoS: read the highest qc and tc under the lock in syncinfo verification (review R1-A03) |
| review R1-F01 | `6f40506857e55ad1f789d48ee28fcf06cb68d6df` | eth/hooks: return the reward-signers error instead of exiting (review R1-F01) |
| review R1-F02 | `652fc37f7307f0ca72dfce9a299b05ee1d936e20` | eth/hooks: return an error when a parent header is missing in the v2 penalty walk (review R1-F02) |
| review R1-F03 | `626cf89a54450acf74dd21070f0f93abcb0a28ec` | consensus/XDPoS, miner: use pre-transaction state for v1 rewards when producing a block (review R1-F03) |
| review R1-S02 | `9cc49eb825758317ccfa213473296ae1984e126e` | eth: require anchor on every ancestor prefetch batch, reject short responses (review R1-S02) |
| review R1-M01 | `6304ec35e48c49959ac2d658cdcc7a71df480f90` | core/txpool/legacypool: do not lower pending nonce on expired signing-tx replacement (review R1-M01) |
| review R1-M02 | `615127b3db85d20959fae41bff6bda80c9e23447` | core/txpool/legacypool: restore the test file header (review R1-M02) |
| review R1-F04 | `04b465a97a4f260d8e5c810dab4a173b573cce1a` | consensus/XDPoS: remove unused default penalty hook functions (review R1-F04) |
| review R1-R01 | `d5340e2e08a2e7469dad2ad5847f320a7c392d32` | consensus/XDPoS: move propose/discard to authenticated api and bound proposals map (review R1-R01) |
| review R1-R02 | `5d4c25f4cc0a9dec9b11f44cc392376c7b38f0c4` | consensus/XDPoS: reject epoch ranges spanning more than 1000 epochs (review R1-R02) |
| review R1-R03 | `27398e6e64956981459d751e73f537039754ea41` | consensus/XDPoS: report the configured chain id in networkinformation (review R1-R03) |
| review R1-R04 | `e6959b860558fded53c55929779a73031095b098` | internal/ethapi: return an error instead of panicking in proofpairlist delete (review R1-R04) |
| review R1-S01 | `a5d34bc9d408c7febef6037150a7c80caa656978` | eth: stop the rollback window from wrapping to zero (review R1-S01) |
| review R1-S04 | `2e928aa3c2ed48347282450b16eb036d0b987db8` | eth: remove the unused snapkeepalive goroutine fan-out (review R1-S04) |
| review R1-C01 | `433b43bf47644a3d908954a140433e4914ab12d6` | core/vm/privacy: reject nil from deserializecompressed in ring signature (review R1-C01) |
| review R2-P01 | `4aca0fe791b80fee54575183468b091391114da4` | core/vm/privacy: fix uint32 overflow in deserializePointArray (review R2-P01) |
| review R2-P02 | `1e184824c15bae763dcd4a35f366827b45f5455a` | core/vm/privacy: guard EC global with mutex in MRPProve and MRPVerify (review R2-P02) |
| review R2-P03 | `3fdd7fa2d246c28de302171a275ec5f32007a732` | core/vm/privacy: lower HashPoint log calls from Info to Trace (review R2-P03) |
| review R2-W01 | `8b2d44e7ec8f78470930ec8240abdd61637ded65` | consensus/XDPoS: read c.signer under c.lock in prepare and calcdifficulty (review R2-W01) |
| review R2-W02 | `f9dc20e26e7d6a83b9b612e496c6145691ea0d4a` | consensus/XDPoS: guard forensics committed-qc state with a mutex (review R2-W02) |
| review R2-W03 | `380b313620328ed785101260be2fdbdfeeeaf013` | consensus/XDPoS: drop the unlocked signer read in the v2 calcdifficulty (review R2-W03) |
| review R1-R02, review R2-N01 | `e4d7dfcf46c54a6108124b8702267aa2f307d847` | consensus/XDPoS: bound the epoch range in getrewardbyaccount before folder scan (review R2-N01) |
| review R2-N02 | `6cef6a2b06f903357d8d05555abe964123d0da7a` | node: delete unused xdc config file that holds a raw private-key field (review R2-N02) |
| review R2-N03 | `69532baf08c4900a61f9a86f1bd9f17176044603` | core/rawdb: remove unused xdc accessors and fix vote-prefix verkle overlap (review R2-N03) |
| review R2-W04 | `8ebc2c487a466d3710e6fef14c094299eb8403b9` | consensus/XDPoS: stop processforensics after the ancestor-qc lookup fails (review R2-W04) |
| review R2-D03 | `d3ca548f51ef408525b05523ea57b39d3fb00434` | eth: send minted block to peers without blocking the minter's own vote (review R2-D03) |
| review R2-D01 | `c52cf1c5038d90d6b73426fb903c1aa16baa226e` | eth: match header and body responses to the xdcsyncer request (review R2-D01) |
| review R2-D02 | `87a6715b57409c98a442ced96508f0bbff588cad` | eth: cancel delayed future-block handling on handler shutdown (review R2-D02) |
| review R2-D01 | `16075b94d9f1597f0bb79eb2a1f0b43cf2e0197d` | eth: restore peer judgement for mismatched sync responses and accept partial bodies (review R2-D01) |
| CertiK XDC-21 | `928f94ff372c34a33100a4adb363321623d16464` | core: sweep stale canonical hashes and tx-lookup entries on startup (CertiK XDC-21, XDC-27, XDC-28) |
| CertiK XDC-04 | `06682e25774187cfc441cb5642476be972d610c4` | params, core: add SigningTxBoundBlock fork and reject oversized/spam sign-tx blocks (CertiK XDC-04, XDC-09) |
| CertiK XDC-04 | `a07044690417221924a44861692db8693c78206d` | params, core: schedule certik rule blocks as far-future placeholders (CertiK XDC-04/09/11/12) |
| CertiK XDC-10 | `c064ee73c2d9593cef295fc445c8cf82bae67b22` | consensus/XDPoS: recheck switches accepted by the bulk-sync mismatch tolerance (CertiK XDC-10) |
| review R3-C01 | `856ed6d30388d9d8aa2d9feead811a6ea4d52b3d` | params: guard certik rule blocks in checkcompatible (review R3-C01) |
| review R3-R03 | `b5418e61ddca6dbaee1e3c5c456f7ff52430caf5` | cmd/geth: fail start-up when the miner key cannot be unlocked (review R3-R03) |
| review R3-R04 | `d3261741dae900a1844f7113bbab7ec3f70237e5` | internal/ethapi: make GetStakerROIMasternode use its masternode argument (review R3-R04) |
| review R3-R02 | `99a2237e9471b2435a5e140750f7deb9360e5dd2` | node, consensus/XDPoS: remove the unwired health handler and event api (review R3-R02, R3-R05) |
| review R3-P01 | `0b08f0a0c75bbf875b51d181865ee577d62cfc61` | core/vm/privacy: propagate the inner-product decode error in the bulletproof verifier (review R3-P01) |
| review R3-S01 | `eab5fd9c989d2565129a25f553a1ce09cb888af0` | eth: check the returned hash in fetchheaderbyhash (review R3-S01) |
| review R4-V1 | `6069f19c41c5117400bbfce778ab6b3855cc53b6` | consensus/XDPoS: delete the unused engines/engine_v1 package (review R4-V1-01) |
| review R4-P02 | `c47ebb0dc94bab9133f11f5574ddd25c730f73c7` | core/vm/privacy: guard ecpoint.neg against the point at infinity (review R4-P02) |
| review R4-W02 | `20869c5fb567359f738ddadf9b055dd5331f14fa` | consensus/XDPoS: write highestselfminedround under the engine lock in seal (review R4-W02) |
| review R5-F01 | `8eae3c321f52c43e64f9190c58ae92f9fd4c214c` | consensus/XDPoS: bound the forensics ancestor-qc walk and guard a nil qc (review R5-F01) |
| review R6-01 | `acbbd68f71603bfcc1997643eed96b1de6fe92c7` | eth/downloader: return a kept peer to idles after a reverted skeleton request (review R6-01) |
| review SYNC-10 | `0ae2b0ee2bc02c623d068c6b0dcd91d90cf4be51` | eth, eth/protocols/eth: xdpos2 version 101 with request ids for header, body and receipt fetches (review SYNC-10) |
| review SYNC-12 | `d4ab57afb5cbf05ae146b734064c2b744521c201` | eth: hedge slow body requests, rank peers by body latency, pipeline lookahead bodies (review SYNC-12) |
| review SYNC-13 | `71a581f0079cd7cae87397a9a9859a00920a83f7` | eth, eth/downloader: keep sync moving past peers that cannot serve a range (review SYNC-13) |

Areas we advise including, all changed since the previous revalidation commit `4bf67046816d5d2731c4f2bf78d9f327d7886618`:

1. **Upstream go-ethereum v1.17.9 and v1.17.10 merges.**
2. **CertiK findings XDC-07 to XDC-36 and P3**: QC/TC signer recovery, vote-pool caps, BFT broadcast, discovery reseed, txpool and sidechain bounds, reorg log ordering.
3. **Consensus hardening from internal review**: signer locks, vote/timeout round locks, reward and penalty header errors, a pre-transaction state reward, epoch range limits, empty-gap-snapshot rejection, and the V1 engine path removed.
4. **Sync** (snap/fast sync, history backfill, peer handling):
   - **One trust change to review specifically:** under full verification, an epoch switch whose gap block lies below a snap or fast-sync pivot is accepted through the existing header fallback. No state exists locally for that gap. The leader seal, QC and signer-list checks all still run, and the gap snapshot is stored only after every check passes. All other full-verification failures remain fatal.
   - **XDPOS2 protocol version 101**: version 100 plus request ids on the header, body and receipt messages. Legacy peers stay on 100, and consensus messages are unchanged.

### Source changes behind the XDC commit (XDCIndia/go-ethereum main since the previously reviewed source, oldest first)

| Source commit | Change |
|---|---|
| `19efdb071904` | Certik Audit |
| `30d68b5fcdbe` | consensus/XDPoS: stop qc/tc recovery at the first non-masternode signer (CertiK P3) |
| `ffe81e13da89` | consensus/XDPoS/utils: cap vote-pool entries to one per signer per round (CertiK XDC-07) |
| `661a3c95d884` | consensus/XDPoS: include epoch-switch block in GetEpochSwitchInfoBetween when begin equals end (CertiK XDC-30) |
| `f31eb0effbbe` | consensus/XDPoS: reject empty persisted v2 gap snapshots (CertiK XDC-35) |
| `fee62fb21775` | eth: send bft broadcasts to peers without blocking on slow peers (CertiK XDC-13) |
| `a2543b27ce00` | eth/downloader: cap nodedata responses to the requested item count (CertiK XDC-16) |
| `9c2dbb7907bd` | p2p/discover: re-seed from bootnodes when the discovery table empties (CertiK XDC-33) |
| `3f0f46353782` | p2p/discover: run the eip-868 test with xdc ping off |
| `a6720ca64f0a` | core/txpool/legacypool: wait for tx events in the eviction test (CertiK XDC-31) |
| `3c155e573e48` | core/txpool/legacypool: skip reset post-processing when pool reset fails (CertiK XDC-36) |
| `940d3aa73e0b` | core/txpool/legacypool: decouple pool reset from txFeed subscriber delivery (CertiK XDC-15) |
| `f48b2e4860ad` | core: nil-check GetBlock in insertSideChain pruned-ancestor loop (CertiK XDC-22) |
| `f74525d3f3a4` | core: bound sidechain children per pruned parent (CertiK XDC-19) |
| `5f6802d8b812` | core: emit reorg log events after the canonical indexes are written (CertiK XDC-26) |
| `dcf0c22fc533` | consensus/XDPoS: initialise the v2 engine under the write lock in yourturn (review R1-A01) |
| `27eab35189a3` | consensus/XDPoS: read currentround under the lock in vote and timeout verification (review R1-A02) |
| `6c316911876d` | consensus/XDPoS: read the highest qc and tc under the lock in syncinfo verification (review R1-A03) |
| `5b99e93d7fb8` | eth/hooks: return the reward-signers error instead of exiting (review R1-F01) |
| `3b9612b1b329` | eth/hooks: return an error when a parent header is missing in the v2 penalty walk (review R1-F02) |
| `9729cd6e892f` | consensus/XDPoS, miner: use pre-transaction state for v1 rewards when producing a block (review R1-F03) |
| `8e85a272a189` | eth: require anchor on every ancestor prefetch batch, reject short responses (review R1-S02) |
| `59f79508f397` | core/txpool/legacypool: do not lower pending nonce on expired signing-tx replacement (review R1-M01) |
| `4d9602d0f338` | core/txpool/legacypool: restore the test file header (review R1-M02) |
| `f0bf5bc1ff59` | consensus/XDPoS: remove unused default penalty hook functions (review R1-F04) |
| `e96ecec7c20c` | consensus/XDPoS: move propose/discard to authenticated api and bound proposals map (review R1-R01) |
| `0dc3f0819227` | consensus/XDPoS: reject epoch ranges spanning more than 1000 epochs (review R1-R02) |
| `3ee3ba598c81` | consensus/XDPoS: report the configured chain id in networkinformation (review R1-R03) |
| `e0909a30afaf` | internal/ethapi: return an error instead of panicking in proofpairlist delete (review R1-R04) |
| `3c9a0036c66f` | eth: stop the rollback window from wrapping to zero (review R1-S01) |
| `df62fd42d279` | eth: remove the unused snapkeepalive goroutine fan-out (review R1-S04) |
| `d2db20d47a58` | core/vm/privacy: reject nil from deserializecompressed in ring signature (review R1-C01) |
| `a63724e62ec1` | core/vm/privacy: fix uint32 overflow in deserializePointArray (review R2-P01) |
| `8fd7a46ee5d3` | core/vm/privacy: guard EC global with mutex in MRPProve and MRPVerify (review R2-P02) |
| `3af437366928` | core/vm/privacy: lower HashPoint log calls from Info to Trace (review R2-P03) |
| `3b94fe4d7dc2` | consensus/XDPoS: read c.signer under c.lock in prepare and calcdifficulty (review R2-W01) |
| `27aa394e4368` | consensus/XDPoS: guard forensics committed-qc state with a mutex (review R2-W02) |
| `40c2bfb2dae6` | consensus/XDPoS: drop the unlocked signer read in the v2 calcdifficulty (review R2-W03) |
| `6d1af01decd6` | consensus/XDPoS: bound the epoch range in getrewardbyaccount before folder scan (review R2-N01) |
| `12f0646708a7` | node: delete unused xdc config file that holds a raw private-key field (review R2-N02) |
| `7cf82f8c8c8d` | core/rawdb: remove unused xdc accessors and fix vote-prefix verkle overlap (review R2-N03) |
| `be02caba3d02` | consensus/XDPoS: stop processforensics after the ancestor-qc lookup fails (review R2-W04) |
| `2fe759f0dcf7` | eth: send minted block to peers without blocking the minter's own vote (review R2-D03) |
| `ff429ee03f9e` | eth: match header and body responses to the xdcsyncer request (review R2-D01) |
| `42cc47157eda` | eth: cancel delayed future-block handling on handler shutdown (review R2-D02) |
| `53f1b74dff17` | eth: restore peer judgement for mismatched sync responses and accept partial bodies (review R2-D01) |
| `50169c460835` | Merge origin/main into chore/merge-upstream-v1.17.9 |
| `3ffc2cb5b81c` | core: sweep stale canonical hashes and tx-lookup entries on startup (CertiK XDC-21, XDC-27, XDC-28) |
| `1ac91c62cb31` | params, core: add SigningTxBoundBlock fork and reject oversized/spam sign-tx blocks (CertiK XDC-04, XDC-09) |
| `a097ef484e96` | Merge origin/main into fix/1722-validator-order |
| `7624b1f4137d` | Merge origin/main into fix/1723-parole-dedup |
| `65958074a752` | params, core: schedule certik rule blocks as far-future placeholders (CertiK XDC-04/09/11/12) |
| `79f5cc0ad23a` | consensus/XDPoS: recheck switches accepted by the bulk-sync mismatch tolerance (CertiK XDC-10) |
| `180b97317695` | params: guard certik rule blocks in checkcompatible (review R3-C01) |
| `40d66a913837` | cmd/geth: fail start-up when the miner key cannot be unlocked (review R3-R03) |
| `3ef042709ba5` | internal/ethapi: make GetStakerROIMasternode use its masternode argument (review R3-R04) |
| `584db4585f8f` | node, consensus/XDPoS: remove the unwired health handler and event api (review R3-R02, R3-R05) |
| `e6e1742ef395` | core/vm/privacy: propagate the inner-product decode error in the bulletproof verifier (review R3-P01) |
| `a079609a377f` | eth: check the returned hash in fetchheaderbyhash (review R3-S01) |
| `8957f64f75ae` | release: v1.17.9-xdc.19 changelog |
| `5552a68c8647` | consensus/XDPoS: delete the unused engines/engine_v1 package (review R4-V1-01) |
| `3cf0aaf348c4` | core/vm/privacy: guard ecpoint.neg against the point at infinity (review R4-P02) |
| `485b4094056c` | consensus/XDPoS: write highestselfminedround under the engine lock in seal (review R4-W02) |
| `8cefa3030bcc` | eth/protocols/eth: serve xdpos2 receipts in the consensus encoding (sync test SYNC-01) |
| `70d9c4552a03` | eth: bound and race-proof the bulk-sync progress counters (sync test SYNC-03) |
| `094384ea2e16` | eth: make required-block challenges work on xdpos2 and pin the apothem reset (sync test SYNC-04) |
| `e6bf6c2b0ad6` | consensus/XDPoS: find the round-based epoch switch and clean up seeded snap info (sync test SYNC-02) |
| `5fa29106b55e` | consensus/XDPoS, eth: verify backfilled qcs against their own epoch in fast sync (sync test SYNC-05) |
| `62e6546a7509` | ethstats, eth: read the sync info through a backend method, not reflection |
| `8a189c53336f` | consensus/XDPoS: bound the forensics ancestor-qc walk and guard a nil qc (review R5-F01) |
| `545d2094bb20` | eth: allow one xdpos2 header and one body request in flight per peer (sync test SYNC-08) |
| `7286ef6bb1b1` | eth: backfill chain history after xdc snap sync (sync test SYNC-06) |
| `15cd80f4699b` | consensus/XDPoS, eth: skip qc verification for backfilled headers below the fast-sync anchor (sync test SYNC-09) |
| `3e705b6df984` | core, eth: fast sync on path-scheme databases via snap state and header gap snapshots (sync test SYNC-07) |
| `805831b498ed` | eth: read the required-block challenge timeout once per peer |
| `c8e1ce914526` | consensus/XDPoS, eth: give snap and fast sync the gap snapshot for the first epoch switch after the pivot (sync test SYNC-11) |
| `cec5aad61858` | eth/downloader: return a kept peer to idles after a reverted skeleton request (review R6-01) |
| `24d7192e8c00` | eth, eth/protocols/eth: xdpos2 version 101 with request ids for header, body and receipt fetches (review SYNC-10) |


## Final commit

**`4b6ba2d056a7a62740e6c0c6e67521e90fb34f10`**: the head code commit of the remediation branch `hacken/remediation-2026-10`. It is the first hand-over, the follow-up fixes, and the second-report fixes below. Use this commit for per-finding review.

- First hand-over: `main` = `c5e7c37de28ed1b4b1da01918b7fdee03a58f151`.
- On top of `45e2b6ee`: the v1.17.8 audit view (upstream go-ethereum v1.17.8 base), merge `42f2db823550308bf34992bcfdd1bc77cc456340`, then one commit per finding.
- Previous head code commit (before the second-report fixes): `37d91b8d95d7efe72b7dcd7abc8df9d9e409950e`.

## Previous request (second hand-over): revalidate the codebase at `4bf67046816d5d2731c4f2bf78d9f327d7886618`

We ask Hacken to revalidate the codebase at **`4bf67046816d5d2731c4f2bf78d9f327d7886618`**, the head of branch `audit/main-uplift-1178-xdc15`. That commit is the current XDC client source (go-ethereum fork release v1.17.8-xdc.18 plus the fixes below) in the same code-only audit view: `git diff --name-only 4bf67046816d5d2731c4f2bf78d9f327d7886618 <source>` lists only the excluded upstream-history, ops and docs paths.

We advise including two areas in this audit. Both changed after the audited commit `45e2b6ee`:

1. **Upstream go-ethereum v1.17.6, v1.17.7 and v1.17.8.** We merged all three upstream releases. The audited commit was based on v1.17.5. The merges touch core, eth, p2p, trie and rpc, and every XDC-specific path that runs on them.
2. **Reward pipeline.** Several fixes change how epoch rewards are calculated, stored and reported:
   - the TIPUpgradePenalty walk follows the epoch-switch parent hash when the cached link is missing. Before, penalised signers were released early and canonical Apothem blocks were rejected;
   - every V2 gap-snapshot candidate is authorised for signing transactions, and signing counts drive the reward split;
   - reward files are written to the `--store-reward` folder and the reward RPCs read them back (F-2026-19805);
   - signing transactions behind a paid nonce are now included in produced blocks (F-2026-19727), so masternodes keep their signing credit.

## Second report (22 October 2026): residual fixes

The second report rated 20 findings Mitigated. Each residual it described is now either fixed or closed with a reason. Each fix is one commit on its own branch, based on `37d91b8d95d7efe72b7dcd7abc8df9d9e409950e`, with the finding ID in the subject and a `Hacken:` trailer. The same commits are merged into `hacken/remediation-2026-10`. Only the `-r2-` branches below belong to this hand-over.

| Finding | Branch | Commit | What changed |
|---|---|---|---|
| F-2026-19727 | `hacken/f-2026-19727-r2-signing-tx-behind-paid-nonce` | `81990d9e7bcf64a442d1f9b2b1b6a7ccfe824a38` | A zero-price signing transaction behind a paid nonce from the same account is now included in nonce order. Only the leading run of special transactions is committed before the price heap, and the price ordering gives a special transaction below the base fee a zero tip instead of dropping the account. Producer side only. |
| F-2026-19457 | `hacken/f-2026-19457-r2-special-tx-selector` | `6897043e4a090bfeb30f38155adbb32fbb08f5f6` | The masternode fast path in the pool also requires known calldata: a 68-byte `sign` call to 0x89, or `setSecret` / `setOpening` to 0x90. Other calldata takes the normal pool path. |
| F-2026-19460 | `hacken/f-2026-19460-r2-bft-forward-bound` | `896dfac729988e0f7ab3b093b5465efcec0d9780` | BFT forwarding uses 4 workers and a 1024-entry queue. Relayed messages are dropped when the queue is full; the node's own votes and timeouts are never dropped. |
| F-2026-19463 | `hacken/f-2026-19463-r2-tip-leader-turn` | `4abe0d18b1379298baafd9157d40566b44f37de0` | The leader-turn tolerance applies only while bulk sync is on. While `CheckpointSyncNoStateActive` is set, a new tip header from a non-leader is rejected with `ErrNotItsTurn`. |
| F-2026-19638 | `hacken/f-2026-19638-r2-v2-committed-finality` | `3df88cbcb9af357f98f329de8351483927867f33` | A V2 block at or below the finalized header, on the canonical chain, reports finality 100. |
| F-2026-19639 | `hacken/f-2026-19639-r2-v2-epoch-rpcs` | `0d927ef4a95b6b0fcd653a781007839ace927f8b` | Epoch and checkpoint RPCs use the V2 round-based epochs; a V1 range returns an error; `GetV2BlockByHeader` fills `EncodedQC`. |
| F-2026-19462 | `hacken/f-2026-19462-r2-verify-mns-observable` | `8800153a163c13b9318bf5e63e141cf570217da3` | The Apothem and missing-state skips stay (canonical disables full verification on chain 51). Each skip is counted in a metric, and the Apothem skip logs one warning. |
| F-2026-19467 | `hacken/f-2026-19467-r2-signing-tx-cap` | `d6ca8e9a0bc3ee84f3a5d02352bbd95c7d562890` | The miner commits at most 2 × the V2 MaxMasternodes signing transactions per block (216 on mainnet). See the note below on the import-side recommendations. |
| F-2026-19469 | `hacken/f-2026-19469-r2-forensics-decode-error` | `260b099752fc1006b8de8cbecc060679ce22b3f5` | `findAncestorQCs` returns an error on a QC decode error or a nil QC instead of panicking. |
| F-2026-19596, F-2026-19612 | `hacken/f-2026-19596-r2-tc-active-signers` | `b1821fb765654e7334dc5c28d3b8dc15e28a25d5` | A timeout certificate is built only from signers in the active masternode set of the epoch that contains the timeout round, the set `verifyTC` checks. The threshold is checked again after the filter. |
| F-2026-19614 | `hacken/f-2026-19614-r2-v1-light-path-m2` | `9ced28cedde3b6f5984628e1847491cbdf86692a` | The M2 co-signature check runs on the bulk-sync light path too, and an empty Validator is refused on both paths. On the light path only, a creator with no M2 assignment in the checkpoint set is skipped; under full verification it still fails. Backed by a scan of 530,815 mainnet V1 headers (blocks 900–80,370,000): 0 mismatches. The same commit records why F-2026-19602 and F-2026-19621 stay as they are. |
| F-2026-19724 | `hacken/f-2026-19724-r2-at-tip-head-fresh` | `f8bfa2f392ee216f2eccc5c450015c39cef0e0bf` | The at-tip signal is the local head's age (one V2 timeout-sync budget plus a mine period, 30 s floor), not the peer-controlled batch length. Two commits: the fix, then the adapted bulk-mode test. |
| F-2026-19471 | `hacken/f-2026-19471-r2-pivot-hash-before-state` | `087d04e0f9f80f30e2ed0049b025315563351466` | An operator-pinned pivot is checked against the served header hash before state download, and the served header's root is used. |
| F-2026-19459 | `hacken/f-2026-19459-r2-empty-route-reinclusion` | `74cf3965540dfc532dc4195bf9c72c8b03cd0b92` | The miner skips an empty-route transaction (0x91–0x94) that is already in the canonical chain, and the pool drops it once the chain includes it, so the same payload is not mined again on chains where the receiver window stays open. The import rule and the nonce handling are unchanged (see below). |
| F-2026-19805 | `hacken/f-2026-19805-r2-reward-folder` | `c3ba2d4f067a4bf01aa5a8b0f87db53a6ad5bffc` | Reward RPCs and the V1 reward writer read the folder that `--store-reward` sets. |

### Residuals closed without a code change

- **F-2026-19467, import side.** Charging signing transactions to the block gas pool would change header `GasUsed` against canonical XDPoSChain for every historical block. Canonical enforces `MaxBlockSize` only from Osaka, which is unscheduled on mainnet, so an import-side size check would reject blocks canonical accepts. The producer-side cap above bounds blocks this client builds.
- **F-2026-19602.** The `verifySeal` fallback to the checkpoint's own signer list stays for ignore-listed checkpoints, the light path and snapshot recovery. Tightening it would wedge a node that lacks the snapshot. Full verification compares the signer list exactly.
- **F-2026-19621.** Adding Penalties to `encodeSigHeader` would change the seal hash of every historical V1 block. The penalty compare needs parent state, so it is skipped when that state is missing and on the light path.
- **F-2026-19459, import side.** Sender recovery already runs before the empty-transaction route on import and in the miner. The nonce is not advanced in `ApplyEmptyTransaction`, as in canonical XDPoSChain; changing that would alter state roots across mainnet blocks 38,383,838–80,370,899 and Apothem blocks 23,779,191–66,824,999. The producer-side guard above stops this client from including the same payload again.
- **F-2026-19471, QC verification.** The compiled checkpoint does not carry a quorum certificate. The pivot is now checked by hash before state download; the trust root stays the binary, and auto-pin stays opt-in.
- **F-2026-19612, admission.** Single timeouts are still admitted against the gap snapshot, as canonical does. A GapNumber-to-round-epoch check on incoming timeouts could reject honest canonical messages.
- **F-2026-19525.** Fully addressed: QC and TC verification keep one signature per recovered address, and the vote and timeout pool gates count distinct in-set signers (follow-up `512744a9973d00eed2619d53a041830633acf9c1`, listed above). No residual remains in the current code.
- **F-2026-19723.** A state-root mismatch on a complete parent trie is rejected. A mismatch caused by a missing trie node is still imported with the empty root while checkpoint sync without state is active: that mode exists for nodes restored from a snapshot or fast sync before their state is built, and rejecting those blocks would stop such a node permanently. Full verification on a node with complete state rejects every mismatch.
- **F-2026-19815.** Warn-only by design. Canonical XDPoSChain compares the epoch-switch validator list without regard to order, so rejecting a reordered list would split this client from the canonical chain. The warning records any order difference.
- **F-2026-19608, F-2026-19609, F-2026-19615 (Accepted).** No change: the client behaves as canonical XDPoSChain, and a change needs an upstream protocol change first.

## How to verify

- **One commit per finding.** The finding ID is in the commit subject and in a `Hacken:` trailer. Find the fix for one finding with:
  ```sh
  git log --grep 'Hacken: F-2026-19525' 4b6ba2d056a7a62740e6c0c6e67521e90fb34f10
  ```
- **Shared root cause.** Two commits each fix two findings, and list both IDs:
  - F-2026-19596 + F-2026-19612;
  - F-2026-19466 + F-2026-19468.
- **Three commits for F-2026-19525.** One fixes `verifyQC` / `verifyTC`; one dedupes the vote / timeout pools in the threshold callbacks; the follow-up below makes the pool threshold gates count distinct signers.
- **Tests.** Each fix commit adds a test that fails without the fix and passes with it. There are two exceptions:
  - **F-2026-19615:** documentation only. Canonical XDPoSChain's V1 engine has no gas-limit bound either, and adding one would reject historical blocks, so the commit records that in a code comment.
  - **F-2026-19815:** diagnostic only. Canonical compares the epoch-switch validator list without regard to order, so the commit logs a warning when the order differs and does not reject the block.
- **Tests that fail with and without the fixes:** 5 `internal/ethapi` tests (`TestEIP7910Config`, `TestRPC*`) fail on the code before the fixes too.

## Follow-up fixes after the first hand-over

Each follow-up fix is one commit on its own branch, with the finding ID in the subject and in a `Hacken:` trailer. The same commit is merged into `hacken/remediation-2026-10`.

| Finding | Branch | Commit | What changed |
|---|---|---|---|
| F-2026-19614 | `hacken/f-2026-19614-v1-double-validation-mainnet` | `0f1b4363dd0d709d163ab1673c9f71e56547f179` | The V1 double-validation check (empty-Validator and M2-assignment rejection) now runs on mainnet (chain 50) only, where another check already enforces it. The first fix applied it to every chain except Apothem. On private and mixed legacy + geth networks that rejected V1 blocks that canonical XDPoSChain accepts: blocks without an M2 co-signature, when the assigned M2 does not co-sign. Mainnet behaviour does not change. |
| F-2026-19525 | `hacken/f-2026-19525-distinct-signer-gates` | `512744a9973d00eed2619d53a041830633acf9c1` | The vote and timeout pool threshold gates now count distinct, in-set signers instead of raw pool entries. The pool keys entries by message hash, which includes the signature bytes, so before this one masternode key could open the gate with several validly signed entries. On the timeout path that built a single-signer timeout certificate, advanced the local round, and broadcast a certificate that peers reject. The unused `UniqueSignatures` byte-dedupe helper is removed. The test `threshold_distinct_signers_test.go` fails on the first hand-over code and passes with this fix. |
| F-2026-19614 | `hacken/f-2026-19614-v1-double-validation-all-chains` | `3ce495578ef58da43ae56b2ce3fb7f9b211bb576` | Second follow-up. The reference client is now XDPoSChain v2.9.3-testnet, which enforces the V1 double-validation check (empty Validator past epoch 1, or a co-signer other than the assigned M2) on every chain except Apothem. This fork now does the same: mainnet keeps the same behaviour, and Apothem still accepts such blocks. A gossiped V1 block that still waits for its M2 co-signature is no longer imported unsigned, as canonical's fetcher does, so it is not stored as a bad block. `TestV1FullVerifyEnforcesDoubleValidation` fails on the previous hand-over code and passes with this fix. |

## Summary

| Severity | Findings | Fixed with a commit | Closed without a code change |
|---|---|---|---|
| High | 4 | 4 | 0 |
| Medium | 16 | 14 | 2 |
| Low | 16 | 16 | 0 |
| Info | 4 | 4 | 0 |
| **Total** | **40** | **38** | **2** |

## Finding → commit

| Finding | Severity | Title | Commit on `main` (full SHA, subject) or reason |
|---|---|---|---|
| F-2026-19525 | High | One Masternode Key Can Satisfy A Quorum Or Timeout Certificate | `3127524992766aff9bf78ea28edf7c20b25dbc8d`<br>consensus/XDPoS: dedup qc/tc signatures by address and reject high-s (F-2026-19525)<br>`9a1c11225688819019e3f722ee61c7036cab88d9`<br>consensus/XDPoS/engines/engine_v2: dedupe pool entries by signer in threshold callbacks (F-2026-19525)<br>Follow-up: `512744a9973d00eed2619d53a041830633acf9c1`<br>consensus/XDPoS/engines/engine_v2: count distinct signers at the vote and timeout threshold gates (F-2026-19525) |
| F-2026-19601 | High | V2 Proposer Seal Omits Header Fields That Change The Block Hash | `322af2f93960d988c812c685c8c67e9a91904486`<br>consensus/XDPoS, miner: reject post-london header fields xdpos does not seal (F-2026-19601) |
| F-2026-19636 | High | Full Sync Canonizes A Header Batch That Does Not Extend The Local Head | `b7d201b2656b63dc4e3f6fcaf61dde9385844a57`<br>eth: only import full-sync header batches that extend the local head (F-2026-19636) |
| F-2026-19727 | High | A Zero Price Signing Transaction Keeps That Account Out Of The Block | `bdd0fdc0aaa2f8e6175b0e5f3325ecd4bb319729`<br>miner: commit special transactions before the price heap (F-2026-19727) |
| F-2026-19457 | Medium | Destination Only Special Transactions Skip The Tip Check And The Slot Cap | `cd3cd000cdcf9edd74c6e8d35028afd461708a21`<br>core/txpool, eth: apply the slot cap to special txs and require a signer (F-2026-19457) |
| F-2026-19458 | Medium | A Compiled Ethstats Secret Authenticates Default Node Reporting | `59ca7901d9dab453f7adaa846159748edef07c91`<br>cmd/geth, cmd/utils: make ethstats opt-in and drop the compiled secret (F-2026-19458) |
| F-2026-19460 | Medium | Verified BFT Forwarding Can Exhaust Node Through Unbounded Goroutines | `61847faeb6b7719f9af012c71b91ef5179150218`<br>eth/bft: add known-message cache and quit-aware enqueue to Bfter (F-2026-19460) |
| F-2026-19463 | Medium | Snapshot Heal Imports New V2 Headers Without Leader Turn Enforcement | `27fe11ef7de0e149fba7853d6b9962501952cdb4`<br>consensus/XDPoS: do not cache V2 headers whose leader check was skipped (F-2026-19463) |
| F-2026-19466 | Medium | Epoch Switch Full Verification Trusts Header Validators When They Are A Superset Of The Local Set | `5943fad6f59910c544ad7156302e2c7ff3c10534`<br>consensus/XDPoS: require exact epoch-switch validators under full verification (F-2026-19466, F-2026-19468) |
| F-2026-19468 | Medium | Epoch Switch Full Verification Trusts Header Fields When The V2 Snapshot Is Missing | `5943fad6f59910c544ad7156302e2c7ff3c10534`<br>consensus/XDPoS: require exact epoch-switch validators under full verification (F-2026-19466, F-2026-19468) |
| F-2026-19608 | Medium | Header Verification Accepts A Future Round With No Timeout Certificate | No code change. Same behaviour as canonical XDPoSChain (`consensus/XDPoS/engines/engine_v2/verifyHeader.go:89` in v2.9.3-testnet). V2 headers carry no timeout certificate, so a fix needs a protocol change made upstream first. |
| F-2026-19609 | Medium | The Vote Lock Is The Parent Certificate, So A Certified Block Is Not Locked | No code change. Same behaviour as canonical XDPoSChain: `processQC` sets `lockQuorumCert` to the QC carried in the certified block (chained-HotStuff two-chain lock), as v2.9.3-testnet `consensus/XDPoS/engines/engine_v2/engine.go:1035-1036` does. Changing it changes the voting rule for every node. |
| F-2026-19622 | Medium | A Vote That Completes A Certificate Latches The Next Round | `736c67919694047272a131eb83e81752d6b4723a`<br>consensus/XDPoS/engines/engine_v2: fix vote latch to use voted round not currentRound (F-2026-19622) |
| F-2026-19637 | Medium | Apothem Penalty Upgrade Releases A Penalized Signer After One Epoch Instead Of Five | `81050a7e29fafd79e31f70c3b1b6a71bf2d59294`<br>eth/hooks: restore post-upgrade penalty comeback walk (F-2026-19637) |
| F-2026-19638 | Medium | Block Signer And Finality RPCs Read The Seal As Addresses | `1e73ee008d59ccba65e3a5ceec3ad94f24531bc7`<br>internal/ethapi: recover real signer from 65-byte V2 validator seal (F-2026-19638) |
| F-2026-19639 | Medium | Epoch RPCs Use The Block Number Instead Of The V2 Round | `4a38cf842de38b68fe56ca4f9026c6456ec3579a`<br>consensus/XDPoS, internal/ethapi: fix epoch RPCs to use v2 round-based epoch logic (F-2026-19639) |
| F-2026-19643 | Medium | Block Import Does Not Apply The Address Blacklist | `2936ef8ee135bf4d381467fecc48690d20ec1d3e`<br>core: reject blacklisted addresses on block import (F-2026-19643) |
| F-2026-19723 | Medium | Bulk Sync Canonizes A Block Whose State Root Does Not Match Execution | `9e43c84aece17572428a7afbc052971d35e35722`<br>core: reject state root mismatch on complete state in bulk sync (F-2026-19723) |
| F-2026-19726 | Medium | Round Timeout Uses The Round Zero Period | `7194b901f1eb9ef51e08bfdae8cf0d840e701af4`<br>consensus/XDPoS/engines/engine_v2: update per-round timeout config in setNewRound (F-2026-19726) |
| F-2026-19773 | Medium | Chain Reorganisation Can Revert A Committed Block | `424c6285a0741f46b42e85b514dbdb2f34e60a00`<br>core, consensus/XDPoS: refuse reorgs below the XDPoS v2 committed block (F-2026-19773) |
| F-2026-19459 | Low | Empty Transaction Dispatch Imports Unsigned TradingState Payloads Without Nonce | `0a831773d11ddee13973f571189c2db134be0f97`<br>core, miner: route 0x91 and 0x94 txs to ApplyEmptyTransaction (F-2026-19459) |
| F-2026-19462 | Low | V1 Checkpoint Validator Verification Never Rejects Populated Validators Field | `7088fdb476986f9668436d8cda6449c7c8effc4e`<br>eth/hooks, contracts: recompute v1 checkpoint validators from the 0x90 state (F-2026-19462) |
| F-2026-19467 | Low | Block Gas Accounting Does Not Bound BlockSigners Transactions | `fbdd70fd0db78c9a2502b3ef4c83988867462e22`<br>miner: skip malformed and future-block signing transactions (F-2026-19467) |
| F-2026-19469 | Low | Quorum Certificate Forensics Is Never Invoked After processQC | `696d6315a7caaf2ab8e4297f4761a3d7d734ef57`<br>consensus/XDPoS/engines/engine_v2: wire forensics processor for QC and vote monitoring (F-2026-19469) |
| F-2026-19596 | Low | Timeout Certificates Count Penalized Signers Toward The Active Set Quorum | `eb4caa2b12abda7a724c4786abb767ad30092521`<br>consensus/XDPoS: verify tc signers against the tc round epoch masternodes (F-2026-19596, F-2026-19612) |
| F-2026-19602 | Low | V1 Checkpoint Extra Replaces The Signer Set And Authorizes That Sealer | `5fbbf8d385c400ad35f0bce3f86a57995011c45c`<br>consensus/XDPoS, eth/hooks: reject v1 checkpoints with a mismatched signer list (F-2026-19602) |
| F-2026-19612 | Low | Timeout Gap Number Selects The Signer Set Independently Of The Round Epoch | `eb4caa2b12abda7a724c4786abb767ad30092521`<br>consensus/XDPoS: verify tc signers against the tc round epoch masternodes (F-2026-19596, F-2026-19612) |
| F-2026-19614 | Low | V1 Full Seal Logs The M2 Co-Sign And Does Not Reject It | `ab1b7b90c178328df219242885d4e913e445f8ec`<br>consensus/XDPoS: enforce v1 double validation under full verification (F-2026-19614)<br>Follow-up: `0f1b4363dd0d709d163ab1673c9f71e56547f179`<br>consensus/XDPoS: restrict v1 double-validation to mainnet (F-2026-19614)<br>Second follow-up: `3ce495578ef58da43ae56b2ce3fb7f9b211bb576`<br>consensus/XDPoS: enforce v1 double validation on every chain but apothem (F-2026-19614) |
| F-2026-19621 | Low | V1 Checkpoint Penalties Are Outside The Seal And Never Checked | `08d4ec587dbae5b497e87e964b655f3435a9d19a`<br>consensus/XDPoS, eth/hooks: check v1 checkpoint penalties against the computed list (F-2026-19621) |
| F-2026-19640 | Low | A Sealed Sign-Slot Block Submits A Second Signing Transaction | `fbed2764957600cecf9d03bedbcddc0512bd8561`<br>miner: skip sign tx for self-sealed blocks (F-2026-19640) |
| F-2026-19641 | Low | NewBlock Imports A V1 Extension Without Recovering The Seal | `c2541e1ccda34028d4614c51b9a71a171171cae6`<br>consensus/XDPoS: verify the v1 seal on the bulk-sync path (F-2026-19641) |
| F-2026-19642 | Low | Bulk Sync Accepts A V2 Block Dated In The Future | `d7bc331f085ab9a658fd70d0cb007e9f3f68c73b`<br>consensus/XDPoS: reject future-dated v2 headers during bulk sync (F-2026-19642) |
| F-2026-19644 | Low | Apothem Protector Rewards Use Stake From The Payout Block | Fixed by the v1.17.8 uplift: merge `42f2db823550308bf34992bcfdd1bc77cc456340`, file content from commit `60f2a187288a3b491707102256d38015ebc47cb3`. `GetSigningTxCountTiered` reads the tiers from `GetStandbynodes(chain, h)` (the epoch gap snapshot), not from the payout-block stake. Check: `eth/hooks/engine_v2_hooks.go` (`standbyPool := c.GetStandbynodes(chain, h)`). The audited commit `45e2b6ee` does not have this line. |
| F-2026-19724 | Low | A Non-Mainnet Insert Error Clears Bulk Sync Away From The Tip | `5658b3984e65038dccffd097af46a06eb60d223b`<br>eth: keep bulk sync on after an invalid next block while far behind (F-2026-19724) |
| F-2026-19774 | Low | Voted Round, Mined Round And Lock Are Not Kept Across A Restart | `69edf12387724efe3d337c15cd7ec32bb2180312`<br>consensus/XDPoS/engines/engine_v2, core/rawdb: persist BFT local safety state across restart (F-2026-19774) |
| F-2026-19815 | Low | Epoch-Switch Verification Accepts a Reordered Validator List | `7b4c8aa898bd99d50d0625b3c7635e21f7ba751e`<br>consensus/XDPoS/utils, consensus/XDPoS/engines/engine_v2: add validator-order diagnostic for epoch-switch headers (F-2026-19815) |
| F-2026-19471 | Info | FastSync Auto Pin Trusts Compiled Checkpoints Without Quorum Verification | `e68ce155ca23fa63e93b2744fe05050f0b24e40e`<br>eth/ethconfig, eth, cmd: make auto-checkpoint-pivot opt-in (F-2026-19471) |
| F-2026-19615 | Info | V1 Header Verification Does Not Bound The Gas Limit | `71a502fa5127d9fcc1087e8c7ae1c4937fdd6d3a`<br>consensus/XDPoS: document intentional absence of V1 gas-limit bound (F-2026-19615) |
| F-2026-19725 | Info | Protector Ranking Uses A Stable Sort On Equal Stake | Fixed by the v1.17.8 uplift: merge `42f2db823550308bf34992bcfdd1bc77cc456340`, file content from commit `60f2a187288a3b491707102256d38015ebc47cb3`. The tiers now come from `GetStandbynodes`, so `slices.SortStableFunc` is gone from `eth/hooks`. The audited commit `45e2b6ee` has it (`git grep slices.SortStableFunc 45e2b6ee -- eth/hooks`). |
| F-2026-19805 | Info | Reward RPCs Do Not Return The Credited Epoch Reward | `9384370a175bc8d086f0f9156bd03b25a7053e14`<br>consensus/XDPoS, cmd: port reward file storage so reward RPCs return data (F-2026-19805) |
