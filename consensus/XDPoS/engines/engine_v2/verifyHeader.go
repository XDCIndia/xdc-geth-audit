package engine_v2

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// sync11ShouldRelax reports whether a calcMasternodes ErrV2SnapshotMissing error
// under fullVerify may be relaxed for a snap-synced node: the gap block for the
// given epoch-switch block (switchNum) is strictly below the last snap pivot, so
// its state was never downloaded and the gap snapshot was never built.
// When it returns true, the caller should trust header.Validators (fall through to
// the bulk-sync path) and call sync11SeedGap once all remaining checks have passed.
// Returns (true, gapNum) on a relaxable miss; (false, 0) otherwise.
func (x *XDPoS_v2) sync11ShouldRelax(calcErr error, switchNum uint64) (bool, uint64) {
	if !errors.Is(calcErr, utils.ErrV2SnapshotMissing) {
		return false, 0
	}
	epoch := x.config.Epoch
	gap := x.config.Gap
	base := switchNum - switchNum%epoch
	var gapNum uint64
	if base >= gap {
		gapNum = base - gap
	}
	pivot := rawdb.ReadLastPivotNumber(x.db)
	if pivot == nil || gapNum >= *pivot {
		return false, 0
	}
	return true, gapNum
}

// sync11SeedGap seeds the masternode snapshot for gapNum from the epoch-switch
// header. It must only be called after every verifyHeader check has passed —
// specifically after the proposer-signature check — so that a forged Validators
// field cannot poison the snapshot without also forging the block seal.
func (x *XDPoS_v2) sync11SeedGap(chain consensus.ChainReader, gapNum uint64, switchHdr *types.Header) {
	gapHdr := chain.GetHeaderByNumber(gapNum)
	if gapHdr == nil {
		log.Warn("[verifyHeader] SYNC-11: gap header not in local DB, snapshot not seeded",
			"gap", gapNum, "switch", switchHdr.Number)
		return
	}
	mns := contracts.ExtractAddressFromBytes(switchHdr.Validators)
	pens := contracts.ExtractAddressFromBytes(switchHdr.Penalties)
	if err := x.UpdateMasternodesWithPenalties(chain, gapHdr, mns, pens); err != nil {
		log.Warn("[verifyHeader] SYNC-11: UpdateMasternodesWithPenalties failed",
			"gap", gapNum, "switch", switchHdr.Number, "err", err)
	} else {
		log.Info("[verifyHeader] SYNC-11: gap snapshot seeded from verified switch header",
			"gap", gapNum, "switch", switchHdr.Number,
			"masternodes", len(mns), "penalties", len(pens))
	}
}

// requireDerivedSwitchSet is the F-2026-20648 gate for an epoch-switch header
// on a node without state. With no locally computed list, the header's own
// Validators must not become the set that checks its seal. The set that signed
// the parent comes from verified epoch info; header.Validators must keep more
// than two thirds of it, so a rotation passes and a list of keys chosen by the
// header does not. When that set cannot be derived the header is refused and
// the node waits for a gap-seed or checkpoint.
func (x *XDPoS_v2) requireDerivedSwitchSet(chain consensus.ChainReader, header, parent *types.Header, parents []*types.Header) error {
	if n := len(parents); n > 0 && parents[n-1].Hash() == parent.Hash() {
		parents = parents[:n-1] // the parent's own epoch, not the header's
	}
	ref := x.GetMasternodesWithParents(chain, parent, parents)
	if len(ref) == 0 {
		log.Warn("[verifyHeader] F-2026-20648: epoch-switch set cannot be derived, refusing header", "Number", header.Number, "Hash", header.Hash())
		return fmt.Errorf("%w at block %s", utils.ErrEpochSwitchSetNotDerivable, header.Number)
	}
	if !utils.SignersOverlap(ref, contracts.ExtractAddressFromBytes(header.Validators)) {
		log.Warn("[verifyHeader] F-2026-20648: epoch-switch validators do not follow the verified set", "Number", header.Number, "Hash", header.Hash())
		return utils.ErrValidatorsNotLegit
	}
	return nil
}

// verifyHeader performs comprehensive header verification aligned with XDPoSChain v2.6.8.
// This function is the primary validation entrypoint for V2 blocks and must remain
// strictly aligned with the official reference implementation to avoid consensus forks.
func (x *XDPoS_v2) verifyHeader(chain consensus.ChainReader, header *types.Header, parents []*types.Header, fullVerify bool) (retErr error) {
	if !x.isInitialized {
		if err := x.initial(chain, header); err != nil {
			return err
		}
	}

	_, ok := x.verifiedHeaders.Get(header.Hash())
	if ok {
		return nil
	}

	if header.Number == nil {
		return utils.ErrUnknownBlock
	}

	if len(header.Validator) == 0 {
		return utils.ErrNoValidatorSignatureV2
	} else if len(header.Validator) != 65 {
		return fmt.Errorf("invalid validator signature length %d, want 65", len(header.Validator))
	}

	// Checked regardless of fullVerify: canonical XDPoSChain gates this on
	// fullVerify too, but its InsertChain always verifies seals, so every
	// imported block gets the check. Our fullVerify is a global bulk-sync
	// switch, so gating here would let a future-dated block through.
	if header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}

	var parent *types.Header
	number := header.Number.Uint64()

	if len(parents) != 0 {
		parent = parents[len(parents)-1]
	} else {
		parent = chain.GetHeader(header.ParentHash, number-1)
	}
	if parent == nil || parent.Number.Uint64() != number-1 || parent.Hash() != header.ParentHash {
		return consensus.ErrUnknownAncestor
	}

	// Gas limit validation: strict 1/1024 bound (aligned with v2.6.8 consensus/misc/gaslimit.go)
	diff := int64(parent.GasLimit) - int64(header.GasLimit)
	if diff < 0 {
		diff *= -1
	}
	limit := parent.GasLimit / params.GasLimitBoundDivisor // 1/1024 bound
	if uint64(diff) >= limit {
		return fmt.Errorf("invalid gas limit: have %d, want %d +/- %d", header.GasLimit, parent.GasLimit, limit-1)
	}
	if header.GasLimit < params.MinGasLimit {
		return fmt.Errorf("invalid gas limit below %d", params.MinGasLimit)
	}
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("gas used exceeded gaslimit, gas used: %d, gas limit: %d", header.GasUsed, header.GasLimit)
	}

	// Verify this is truly a v2 block
	quorumCert, round, _, err := x.getExtraFields(chain, header)
	if err != nil {
		log.Warn("[verifyHeader] decode extra field error", "err", err)
		return utils.ErrInvalidV2Extra
	}
	if quorumCert == nil {
		log.Warn("[verifyHeader] quorumCert is nil")
		return utils.ErrInvalidQuorumCert
	}

	minePeriod := uint64(x.config.V2.Config(uint64(round)).MinePeriod)
	// #951: strict `>` matches legacy (XDPoSChain engine_v2/verifyHeader.go:72). The first
	// V2 block (SwitchBlock+1) has parent.Number == SwitchBlock; legacy SKIPS the 2s mine-period
	// spacing check there, modern's `>=` enforced it — newly rejecting an otherwise-valid
	// legacy-produced first-V2 block timed <2s after the switch block. Relax to match legacy.
	if parent.Number.Uint64() > x.config.V2.SwitchBlock.Uint64() && parent.Time+minePeriod > header.Time {
		log.Warn("[verifyHeader] Fail to verify header due to invalid timestamp", "ParentTime", parent.Time, "MinePeriod", minePeriod, "HeaderTime", header.Time, "Hash", header.Hash().Hex())
		return utils.ErrInvalidTimestamp
	}

	if round <= quorumCert.ProposedBlockInfo.Round {
		return utils.ErrRoundInvalid
	}
	// F-2026-19608: a round more than one past the parent certificate is a
	// skipped round. Until roundSkipTCBlock, that header is still valid, which
	// is what every current block relies on. From the fork block it needs a
	// timeout certificate for the round immediately before it.
	if err := x.verifyRoundSkip(chain, header, parent, parents, round, quorumCert); err != nil {
		return err
	}

	// A.97.5: QC verification always runs. The masternode set is 100% header-derived
	// via getEpochSwitchInfoWithParents → GetMasternodesFromEpochSwitchHeader →
	// header.Validators; no trusted anchor is required on the clean fast-sync path.
	//
	// #1859: skip QC/TC signature and gap-number verification for headers below
	// the BeaconSync pivot while the epoch-switch headers they reference have not
	// yet been downloaded. The downloader authenticates the entire backfill range
	// via the parent-hash chain from the trusted pivot; individual QC gap and
	// signer checks can only run once the epoch-switch blocks are locally available.
	// inCheckpointCatchup returns false when no anchor is active (full sync, post-
	// sync, clean fast-sync with no dynamic pivot yet), preserving strict
	// verification everywhere outside the backfill window.
	//
	// F-2026-20645: only the range the anchor authenticates is skipped. A header
	// above the anchor is checked, so one masternode signature cannot extend the
	// chain on a fast-synced node.
	if !x.inAnchorBackfill(chain, header) {
		if err := x.verifyQC(chain, quorumCert, parent, parents); err != nil {
			log.Warn("[verifyHeader] fail to verify QC", "QCNumber", quorumCert.ProposedBlockInfo.Number, "QCsigLength", len(quorumCert.Signatures))
			return err
		}
	}

	// Nonces must be 0x00..0 or 0xff..f, zeroes enforced on checkpoints
	if !bytes.Equal(header.Nonce[:], utils.NonceAuthVote) && !bytes.Equal(header.Nonce[:], utils.NonceDropVote) {
		return utils.ErrInvalidVote
	}
	// Ensure that the mix digest is zero as we don't have fork protection currently
	if header.MixDigest != (common.Hash{}) {
		return utils.ErrInvalidMixDigest
	}
	// Ensure that the block doesn't contain any uncles which are meaningless in XDPoS_v2
	if header.UncleHash != utils.UncleHash {
		return utils.ErrInvalidUncleHash
	}
	// Verify the header's EIP-1559 attributes (only when active)
	if chain.Config().IsEIP1559(header.Number) {
		if err := eip1559.VerifyEIP1559Header(chain.Config(), parent, header); err != nil {
			return err
		}
	}
	if header.Difficulty.Cmp(big.NewInt(1)) != 0 {
		return utils.ErrInvalidDifficulty
	}

	var masterNodes []common.Address
	// localList: masterNodes was computed locally by calcMasternodes and matched
	// header.Validators, i.e. it does not come from a header (#1413).
	localList := false
	// SYNC-11 (#1863): set when calcMasternodes fails with ErrV2SnapshotMissing
	// under fullVerify AND the gap is below the snap pivot. In that case we fall
	// through to the bulk-sync path (trusting header.Validators) and then seed
	// the gap snapshot once ALL remaining checks pass.
	var seedGapFromSwitch bool
	var seedGapNum uint64
	// Seed runs in a defer so it fires exactly once, only when retErr == nil
	// (i.e. every check in this function has passed). A forged Validators field
	// on a switch header that later fails the proposer-signature or leader check
	// therefore cannot poison the gap snapshot — the defer sees retErr != nil
	// and skips the seed entirely. This ordering is the SYNC-11 fix for item 1
	// of the ordering bug identified in PR #1864 review.
	defer func() {
		if retErr == nil && seedGapFromSwitch {
			x.sync11SeedGap(chain, seedGapNum, header)
		}
	}()
	isEpochSwitch, _, err := x.IsEpochSwitch(header)
	if err != nil {
		log.Error("[verifyHeader] error when checking if header is epoch switch header", "Hash", header.Hash(), "Number", header.Number, "Error", err)
		return err
	}
	if isEpochSwitch {
		if !bytes.Equal(header.Nonce[:], utils.NonceDropVote) {
			return utils.ErrInvalidCheckpointVote
		}
		if len(header.Validators) == 0 {
			return utils.ErrEmptyEpochSwitchValidators
		}
		if len(header.Validators)%common.AddressLength != 0 {
			return utils.ErrInvalidCheckpointSigners
		}

		localMasterNodes, localPenalties, _, err := x.calcMasternodes(chain, header.Number, header.ParentHash, round, parents)
		masterNodes = localMasterNodes
		localList = err == nil
		// F-2026-20648: a node without state, above the range its anchor
		// authenticates, does not let the header pick the next epoch's set.
		noStateSwitch := core.CheckpointSyncNoStateActive.Load() && !x.inAnchorBackfill(chain, header)
		if err != nil {
			// Canonical XDPoSChain (verifyHeader.go:137-143): under fullVerify any
			// calcMasternodes failure is fatal, including a missing gap snapshot
			// (#1415). The fallback below derives the list from the header being
			// verified, so it is only safe when the caller trusts the header (!fullVerify).
			if fullVerify {
				// A missing snapshot or a missing penalty ancestor is not a reason
				// to treat header.Validators and header.Penalties as the local
				// lists. That includes the snap-pivot carve-out: the header would
				// be checked against itself and then stored as the gap snapshot.
				log.Error("[verifyHeader] calcMasternodes failed under fullVerify", "Number", header.Number, "Hash", header.Hash(), "err", err)
				return err
			}
			// F-2026-19463: at the tip (not bulk sync) a node without state does not
			// take the leader list from the header either, so a reordered
			// Validators cannot pick the slot leader. The missing data is retried:
			// ErrV2SnapshotMissing and ErrPenaltyDataUnavailable write no bad-block
			// record and taint no peer. Bulk sync above the anchor keeps the
			// two-thirds overlap gate below.
			if noStateSwitch && !core.XdcBulkSyncMode.Load() {
				log.Debug("[verifyHeader] F-2026-19463: no-state leader list not taken from the header", "Number", header.Number, "Hash", header.Hash(), "err", err)
				return err
			}
			if noStateSwitch {
				if derr := x.requireDerivedSwitchSet(chain, header, parent, parents); derr != nil {
					return derr
				}
			}
			if seedGapFromSwitch {
				// SYNC-11: gap is below snap pivot — trust header.Validators directly.
				// The proposer-signature check below enforces that the signer's key
				// belongs to this claimed set, so a forged Validators field cannot
				// pass undetected.
				masterNodes = contracts.ExtractAddressFromBytes(header.Validators)
				localPenalties = contracts.ExtractAddressFromBytes(header.Penalties)
				localList = false
			} else {
				// Bulk-sync fallback: snapshots may not yet be in the DB on a fresh node, so fall
				// back to GetMasternodesWithParents. The CompareSignersLists checks below still run
				// against the fallback list and header.Validators / header.Penalties, preventing
				// a malicious peer from slipping bogus validator sets through the fallback path.
				log.Debug("[verifyHeader] calcMasternodes failed during bulk sync, using GetMasternodesWithParents fallback", "Number", header.Number, "Hash", header.Hash(), "err", err)
				masterNodes = x.GetMasternodesWithParents(chain, header, parents)
				if len(masterNodes) == 0 {
					// FIX #339-v2: When checkpoint sync crosses V2 epoch switch without state,
					// GetMasternodesWithParents may also fail because epoch switch info is not
					// yet in DB. Extract validators directly from header.Validators as last resort.
					log.Warn("[verifyHeader] GetMasternodesWithParents returned empty, extracting from header.Validators", "Number", header.Number)
					validatorsAddress := contracts.ExtractAddressFromBytes(header.Validators)
					if len(validatorsAddress) > 0 {
						log.Info("[verifyHeader] using header.Validators as final fallback", "Number", header.Number, "validators", len(validatorsAddress))
						masterNodes = validatorsAddress
					} else {
						log.Warn("[verifyHeader] header.Validators also empty, signature verification will fail", "Number", header.Number)
						masterNodes = nil
					}
				}
			}
			// FIX (loophole D): calcMasternodes errored, so localPenalties is nil.
			// HookPenalty walks ancestors via DB only; during in-batch bulk sync
			// the ancestors are still in the parents slice and not yet committed,
			// so HookPenalty fails and we end up here even when the seeded snap
			// would have been correct. Without populating localPenalties, the
			// CompareSignersLists check below would mismatch on any block that
			// has non-empty header.Penalties. Mirror the masterNodes fallback:
			// trust header.Penalties for the duration of the bulk-sync fallback.
			// This is the same security trade-off the masterNodes fallback above
			// already takes — both rely on the trusted-checkpoint boundary rather
			// than per-block recomputation when state isn't yet built.
			localPenalties = contracts.ExtractAddressFromBytes(header.Penalties)
			// CertiK XDC-10: calcMasternodes failed, so we cannot verify header.Validators
			// locally. Record this header for re-verification once the canonical DB is
			// committed (RecheckDeferredSwitches, called from eth/sync_xdc.go after each
			// successful InsertChain batch).
			x.pendingSwitchRechecks.Add(header.Hash(), header.Number.Uint64())
			log.Debug("[verifyHeader] XDC-10: recorded epoch-switch header for deferred recheck",
				"block", header.Number.Uint64(), "hash", header.Hash())
		}
		// CompareSignersLists runs unconditionally for both calcMasternodes success AND fallback.
		// This prevents malicious peers from feeding bogus Validators/Penalties during bulk sync.
		validatorsAddress := contracts.ExtractAddressFromBytes(header.Validators)
		if !utils.CompareSignersLists(masterNodes, validatorsAddress) {
			// Bulk-sync masternode-rotation tolerance: between gap N and the
			// next epoch switch (typically gap + 450..650 blocks), one or
			// more candidates can rotate via the "comeback" mechanic — a
			// penalised candidate signs a recent block and rejoins the
			// active set, evicting a lower-stake active. Our local
			// computation uses the seeded snapshot's CandidatePool ∪
			// Penalties from gap N, but during bulk sync the recent-signing
			// data needed for an exact HookPenalty replay isn't reliably
			// available (parents may not yet be in DB, fallback chain
			// returns the previous epoch's set). Result: mn ≈ val with
			// exactly one swap, length identical.
			//
			// During bulk sync (!fullVerify): the chain-extension via
			// parent_hash continuity from the trusted anchor is the actual
			// security guarantee. A malicious peer can't fabricate a
			// header.Validators that hashes back to our trusted anchor
			// without a chain-quality / equivocation attack — independent
			// of this comparison. So accept header.Validators as
			// authoritative, log the divergence for postmortem, and
			// continue.
			//
			// In steady-state (fullVerify=true) we still hard-fail because
			// HookPenalty has full state and should produce the exact
			// match. The relaxation is bulk-sync-only.
			//
			// #1404/#1415: under fullVerify the compare is exact, as in canonical
			// XDPoSChain (verifyHeader.go:145-155). The former fullVerify tolerances
			// (nil HookPenalty, missing snapshot, header list a superset of ours) let
			// the header under verification choose the list its own signer and leader
			// are then checked against.
			if !fullVerify {
				if noStateSwitch && !utils.SignersOverlap(masterNodes, validatorsAddress) {
					log.Warn("[verifyHeader] F-2026-20648: epoch-switch validators do not follow the derived set",
						"block", header.Number.Uint64(), "mnCount", len(masterNodes), "valCount", len(validatorsAddress))
					return utils.ErrValidatorsNotLegit
				}
				// While state is still healing and this header is not an in-batch
				// bulk-sync ancestor, do not take the leader from the header's own
				// validator list. Any key that list places at the leader index would
				// otherwise pass. The proposer is checked against the local list.
				// XdcBulkSyncMode keeps the reduced check below.
				if core.CheckpointSyncNoStateActive.Load() && !core.XdcBulkSyncMode.Load() {
					log.Warn("[verifyHeader] epoch-switch validator list differs from the local list; leader stays on the local list",
						"block", header.Number.Uint64(),
						"mnCount", len(masterNodes),
						"valCount", len(validatorsAddress))
				} else {
					log.Warn("[verifyHeader] masternode set mismatch tolerated (trusting header.Validators)",
						"block", header.Number.Uint64(),
						"mnCount", len(masterNodes),
						"valCount", len(validatorsAddress),
						"hookPenaltyNil", x.HookPenalty == nil,
						"snapshotMissing", err != nil && errors.Is(err, utils.ErrV2SnapshotMissing))
					masterNodes = validatorsAddress
					localList = false
					localPenalties = contracts.ExtractAddressFromBytes(header.Penalties)
					// CertiK XDC-10: the header's lists are trusted here too; recheck
					// them once the batch is committed.
					x.pendingSwitchRechecks.Add(header.Hash(), header.Number.Uint64())
				}
			} else {
				log.Warn("[verifyHeader DIAG]",
					"block", header.Number.Uint64(),
					"mnCount", len(masterNodes),
					"valCount", len(validatorsAddress),
					"mnFirst", func() string {
						if len(masterNodes) > 0 {
							return masterNodes[0].Hex()
						}
						return "<empty>"
					}(),
					"valFirst", func() string {
						if len(validatorsAddress) > 0 {
							return validatorsAddress[0].Hex()
						}
						return "<empty>"
					}(),
					"mnLast", func() string {
						if len(masterNodes) > 0 {
							return masterNodes[len(masterNodes)-1].Hex()
						}
						return "<empty>"
					}(),
					"valLast", func() string {
						if len(validatorsAddress) > 0 {
							return validatorsAddress[len(validatorsAddress)-1].Hex()
						}
						return "<empty>"
					}())
				for i, addr := range masterNodes {
					log.Warn("[verifyHeader] masterNodes", "i", i, "addr", addr.Hex())
				}
				for i, addr := range validatorsAddress {
					log.Warn("[verifyHeader] validatorsAddress", "i", i, "addr", addr.Hex())
				}
				return utils.ErrValidatorsNotLegit
			}
		}

		// F-2026-19815 / CertiK XDC-11: Under fullVerify, check that the stored
		// order of header.Validators matches the locally-computed calcMasternodes
		// order. The set equality above already passed; an order mismatch lets
		// the epoch-switch proposer decide the leader schedule for the next epoch
		// by publishing any permutation of the correct validator set.
		// Before ValidatorOrderBlock: diagnostic only (warn, no rejection).
		// From ValidatorOrderBlock: hard rejection — the header is invalid.
		//
		// The block stays at the far-future placeholder on mainnet and Apothem,
		// and nil everywhere else. Both leave a reordered list valid, which is
		// what current-main and legacy XDPoSChain accept. Moving it earlier on
		// this client alone would reject headers those clients still seal
		// (#1722). Nil and the placeholder are the same rule at every block a
		// node imports today.
		if fullVerify && !utils.ValidatorsOrderMatches(masterNodes, validatorsAddress) {
			if chain.Config().IsValidatorOrder(header.Number) {
				return fmt.Errorf("epoch-switch header.Validators order does not match calcMasternodes at block %s", header.Number)
			}
			log.Warn("[verifyHeader F-2026-19815] epoch-switch header.Validators order differs from calcMasternodes; "+
				"leaderIndex rotation for remaining epoch blocks may shift",
				"block", header.Number,
				"hash", header.Hash().Hex()[:16])
		}

		penaltiesAddress := contracts.ExtractAddressFromBytes(header.Penalties)
		if !utils.CompareSignersLists(localPenalties, penaltiesAddress) {
			// Same bulk-sync tolerance reasoning as the validators check
			// above: penalties also rotate as candidates come back from
			// penalty into the active set. Accept header.Penalties in bulk
			// sync; hard-fail in fullVerify mode (canonical verifyHeader.go:157-165).
			if !fullVerify {
				log.Warn("[verifyHeader] bulk-sync penalty rotation tolerated (trusting header.Penalties)",
					"block", header.Number.Uint64(),
					"localCount", len(localPenalties),
					"headerCount", len(penaltiesAddress),
					"snapshotMissing", err != nil && errors.Is(err, utils.ErrV2SnapshotMissing))
				localPenalties = penaltiesAddress
				// CertiK XDC-10: recheck once the batch is committed.
				x.pendingSwitchRechecks.Add(header.Hash(), header.Number.Uint64())
			} else {
				for i, addr := range localPenalties {
					log.Warn("[verifyHeader] localPenalties", "i", i, "addr", addr.Hex())
				}
				for i, addr := range penaltiesAddress {
					log.Warn("[verifyHeader] penaltiesAddress", "i", i, "addr", addr.Hex())
				}
				return utils.ErrPenaltiesNotLegit
			}
		}
	} else {
		if len(header.Validators) != 0 {
			log.Warn("[verifyHeader] Validators shall not have values in non-epochSwitch block", "Hash", header.Hash(), "Number", header.Number, "header.Validators", header.Validators)
			return utils.ErrInvalidFieldInNonEpochSwitch
		}
		if len(header.Penalties) != 0 {
			log.Warn("[verifyHeader] Penalties shall not have values in non-epochSwitch block", "Hash", header.Hash(), "Number", header.Number, "header.Penalties", header.Penalties)
			return utils.ErrInvalidFieldInNonEpochSwitch
		}
		// SYNC FIX: Use GetMasternodesWithParents for non-epoch blocks during bulk sync
		// when parents may not yet be in the DB. v2.6.8 uses GetMasternodes which does
		// a DB lookup that can fail during sync.
		masterNodes = x.GetMasternodesWithParents(chain, header, parents)
	}

	verified, validatorAddress, err := x.verifyMsgSignature(x.sealHash(header), header.Validator, masterNodes)
	if err != nil {
		for index, mn := range masterNodes {
			log.Error("[verifyHeader] masternode list during validator verification", "Masternode Address", mn.Hex(), "index", index)
		}
		log.Error("[verifyHeader] Error while verifying header validator signature", "BlockNumber", header.Number, "Hash", header.Hash().Hex(), "validator in hex", hexutil.Encode(header.Validator))
		return err
	}
	if !verified {
		log.Warn("[verifyHeader] Fail to verify the block validator as the validator address not within the masternode list", "BlockNumber", header.Number, "Hash", header.Hash().Hex(), "validatorAddress", validatorAddress.Hex())
		return utils.ErrValidatorNotWithinMasternodes
	}
	if validatorAddress != header.Coinbase {
		log.Warn("[verifyHeader] Header validator and coinbase address not match", "BlockNumber", header.Number, "Hash", header.Hash().Hex(), "validatorAddress", validatorAddress.Hex(), "coinbase", header.Coinbase.Hex())
		return utils.ErrCoinbaseAndValidatorMismatch
	}
	// Check the proposer is the leader
	curIndex := utils.Position(masterNodes, validatorAddress)
	leaderIndex := uint64(round) % x.config.Epoch % uint64(len(masterNodes))
	if masterNodes[leaderIndex] != validatorAddress {
		// A.87: bulk-sync tolerance — during sync the masternode ordering returned by
		// GetMasternodesWithParents / calcMasternodes may differ from canonical by one
		// swap when a penalised candidate rejoins via the "comeback" mechanic between
		// gap-N and the next epoch switch. The pivot hash chain already establishes
		// canonicality; the proposer's signature was verified above.
		//
		// The tolerance is gated on XdcBulkSyncMode (actively fetching a batch of
		// historical blocks), NOT on CheckpointSyncNoStateActive. After a fast-sync or
		// snapshot restore, CheckpointSyncNoStateActive stays set until state healing
		// completes, but the node can already be at the tip receiving new single headers.
		// Skipping the leader check at the tip would let any masternode propose without
		// turn enforcement — the Hacken F-2026-19463 residual. Canonical XDPoSChain
		// (verifyHeader.go:199-204) enforces the check unconditionally; we mirror that
		// at the tip by only tolerating the mismatch while XdcBulkSyncMode is set.
		//
		// #1413: the tolerance only covers lists taken from headers; a list computed
		// locally (localList) is checked as in canonical (verifyHeader.go:199-204).
		//
		// F-2026-20645: with a fast-sync anchor installed, the tolerance ends at
		// the anchor. Above it the header's own list does not decide who may seal.
		anchor, _, _ := checkpointAnchor(chain)
		if core.XdcBulkSyncMode.Load() && !localList && (anchor == 0 || header.Number.Uint64() <= anchor) {
			log.Warn("[verifyHeader] A.87 leader mismatch tolerated during bulk sync",
				"block", header.Number, "curIndex", curIndex, "leaderIndex", leaderIndex,
				"expected", masterNodes[leaderIndex], "actual", validatorAddress)
		} else {
			log.Warn("[verifyHeader] Invalid blocker proposer, not its turn", "curIndex", curIndex, "leaderIndex", leaderIndex, "Hash", header.Hash().Hex(), "masterNodes[leaderIndex]", masterNodes[leaderIndex], "validatorAddress", validatorAddress)
			return utils.ErrNotItsTurn
		}
	}

	// #1413: only a full verification is worth remembering. The cache short-circuits
	// every later call, so caching a header whose leader check was skipped would let a
	// later fullVerify pass accept it unchecked (canonical verifyHeader.go:206-212).
	if fullVerify {
		x.verifiedHeaders.Add(header.Hash(), struct{}{})
	}
	return nil
}

// verifyRoundSkip applies the F-2026-19608 round bound.
//
// With the gate unset, a header is valid under today's rule (round higher
// than the parent certificate) and a third extra field is rejected, matching
// a decoder that has no timeout-certificate field. With the gate set, a
// consecutive round stays a two-field extra, and a skip must carry a timeout
// certificate whose round is the one immediately before the header and newer
// than the parent certificate. Signature checks follow the same catch-up
// exception as the quorum certificate: the parent-hash link to the trusted
// pivot authenticates the bytes until the epoch headers exist locally.
func (x *XDPoS_v2) verifyRoundSkip(chain consensus.ChainReader, header, parent *types.Header, parents []*types.Header, round types.Round, quorumCert *types.QuorumCert) error {
	var extra types.ExtraFields_v2
	if err := DecodeExtraFields(header.Extra, &extra); err != nil {
		return utils.ErrInvalidV2Extra
	}
	active := x.config != nil && x.config.V2.IsRoundSkipTC(header.Number)
	if !active {
		if extra.TimeoutCert != nil {
			return utils.ErrInvalidV2Extra
		}
		return nil
	}
	qcRound := quorumCert.ProposedBlockInfo.Round
	if round == qcRound+1 {
		if extra.TimeoutCert != nil {
			return utils.ErrInvalidV2Extra
		}
		return nil
	}
	tc := extra.TimeoutCert
	if tc == nil || tc.Round+1 != round || tc.Round <= qcRound {
		return utils.ErrRoundSkipWithoutTimeout
	}
	// F-2026-20645: only a header on the anchor's hash chain skips the check.
	if x.inAnchorBackfill(chain, header) {
		return nil
	}
	// The certificate is checked against the ancestors of this header, not the
	// local head, so the verdict does not depend on where the head is.
	if err := x.verifyTCAt(chain, tc, parent, parents); err != nil {
		return err
	}
	return nil
}
