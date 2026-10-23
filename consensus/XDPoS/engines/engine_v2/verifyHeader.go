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
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// verifyHeader performs comprehensive header verification aligned with XDPoSChain v2.6.8.
// This function is the primary validation entrypoint for V2 blocks and must remain
// strictly aligned with the official reference implementation to avoid consensus forks.
func (x *XDPoS_v2) verifyHeader(chain consensus.ChainReader, header *types.Header, parents []*types.Header, fullVerify bool) error {
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

	// A.97.5: QC verification always runs. The masternode set is 100% header-derived
	// via getEpochSwitchInfoWithParents → GetMasternodesFromEpochSwitchHeader →
	// header.Validators; no trusted anchor is required on the clean fast-sync path.
	// The A.89 inCheckpointCatchup QC-skip is removed: when no anchor is installed,
	// inCheckpointCatchup returns false and this branch was already dead.
	// Removing it makes the intent explicit and eliminates the dead dependency on the
	// anchor mechanism on the clean path (refs #894).
	//
	// Degraded-recovery note: when an operator anchor IS installed (operatorPivotNumber!=0),
	// inCheckpointCatchup remains live in GetMasternodesWithParents (engine.go:1271)
	// and the commit-rule skip in utils.go:336. Neither is part of the header verify path,
	// so their behavior is unchanged.
	if err := x.verifyQC(chain, quorumCert, parent, parents); err != nil {
		log.Warn("[verifyHeader] fail to verify QC", "QCNumber", quorumCert.ProposedBlockInfo.Number, "QCsigLength", len(quorumCert.Signatures))
		return err
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
		if err != nil {
			// Canonical XDPoSChain (verifyHeader.go:137-143): under fullVerify any
			// calcMasternodes failure is fatal, including a missing gap snapshot
			// (#1415). The fallback below derives the list from the header being
			// verified, so it is only safe when the caller trusts the header (!fullVerify).
			if fullVerify {
				log.Error("[verifyHeader] calcMasternodes failed under fullVerify", "Number", header.Number, "Hash", header.Hash(), "err", err)
				return err
			}
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

	verified, validatorAddress, err := x.verifyMsgSignature(sigHash(header), header.Validator, masterNodes)
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
		if core.XdcBulkSyncMode.Load() && !localList {
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
