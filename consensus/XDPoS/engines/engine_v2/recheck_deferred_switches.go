// Copyright (c) 2026 XDC Network
// CertiK XDC-10: re-verify epoch-switch headers accepted during bulk sync without
// local masternode verification. See also verifyHeader.go and eth/sync_xdc.go.

package engine_v2

import (
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// RecheckDeferredSwitches re-verifies epoch-switch headers that were accepted
// during bulk sync without local masternode computation (calcMasternodes failed
// because the gap snapshot was not yet in the DB). It should be called after
// each successful InsertChain batch so the newly committed state is available.
//
// For each recorded header that is now in the canonical chain:
//   - If calcMasternodes still fails (state genuinely unavailable, e.g. during
//     a fast-sync pivot walk where state below the pivot is absent), the entry
//     is kept for a later attempt. No rejection is issued on missing data.
//   - If calcMasternodes succeeds and the lists match, the entry is removed.
//   - If calcMasternodes succeeds and the lists mismatch, the header is returned
//     as bad (the caller should roll back and drop the delivering peer).
//
// Entries that are no longer canonical (reorged away) are silently dropped.
//
// Residual: a node fast-syncing with no state below its pivot trusts the pivot
// hash as the anchor (operator-pinned --fastsyncpivot or peer-chosen pivot) —
// same eclipse limit as any fast sync. That invariant is unchanged by this check.
func (x *XDPoS_v2) RecheckDeferredSwitches(chain consensus.ChainReader) (bad *types.Header, err error) {
	if x.pendingSwitchRechecks.Len() == 0 {
		return nil, nil
	}
	// If the node is in checkpoint-sync-without-state mode, HookPenalty cannot
	// compute penalties (requires state). Defer all rechecks until state is built.
	if core.CheckpointSyncNoStateActive.Load() {
		return nil, nil
	}

	hashes := x.pendingSwitchRechecks.Keys()
	for _, hash := range hashes {
		num, ok := x.pendingSwitchRechecks.Get(hash)
		if !ok {
			continue
		}
		// Drop if no longer canonical (reorged away or never committed).
		canonical := chain.GetHeaderByNumber(num)
		if canonical == nil || canonical.Hash() != hash {
			x.pendingSwitchRechecks.Remove(hash)
			continue
		}
		// Decode extra to get the round needed by calcMasternodes.
		_, round, _, extraErr := x.getExtraFields(chain, canonical)
		if extraErr != nil {
			// Header with undecodable extra is corrupt — remove and skip.
			log.Warn("[RecheckDeferredSwitches] cannot decode extra, removing entry",
				"block", num, "hash", hash, "err", extraErr)
			x.pendingSwitchRechecks.Remove(hash)
			continue
		}
		localMns, localPenalties, _, calcErr := x.calcMasternodes(chain, canonical.Number, canonical.ParentHash, round, nil)
		if calcErr != nil {
			// Data still unavailable — keep the entry for the next batch.
			log.Debug("[RecheckDeferredSwitches] calcMasternodes still failing, keeping entry",
				"block", num, "err", calcErr)
			continue
		}
		// calcMasternodes succeeded: compare with what the peer stored in the header.
		storedValidators := contracts.ExtractAddressFromBytes(canonical.Validators)
		if !utils.CompareSignersLists(localMns, storedValidators) {
			log.Error("[RecheckDeferredSwitches] epoch-switch Validators mismatch — forged header detected",
				"block", num, "hash", hash,
				"localCount", len(localMns), "storedCount", len(storedValidators))
			return canonical, utils.ErrValidatorsNotLegit
		}
		storedPenalties := contracts.ExtractAddressFromBytes(canonical.Penalties)
		if !utils.CompareSignersLists(localPenalties, storedPenalties) {
			log.Error("[RecheckDeferredSwitches] epoch-switch Penalties mismatch — forged header detected",
				"block", num, "hash", hash,
				"localCount", len(localPenalties), "storedCount", len(storedPenalties))
			return canonical, utils.ErrPenaltiesNotLegit
		}
		x.pendingSwitchRechecks.Remove(hash)
		log.Debug("[RecheckDeferredSwitches] epoch-switch header verified clean", "block", num)
	}
	return nil, nil
}
