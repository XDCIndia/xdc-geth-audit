// Copyright (c) 2024 XDC Network
// Epoch switch utility functions for XDPoS 2.0
// Ported from v2.6.8 engines/engine_v2/epochSwitch.go
// Fixes: https://github.com/AnilChinchawale/go-ethereum/issues/36

package engine_v2

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// getPreviousEpochSwitchInfoByHash returns the epoch switch info of the epoch
// number `limit` below the epoch containing `hash`, or (nil, nil) when that
// epoch has no block.
//
// The epoch is found by number, as canonical does (engine_v2/engine.go:1240-1261
// in XDPoSChain v3.0.0-testnet-2: epochNum - limit, empty result when no block
// has that epoch number or when epochNum < limit). It must not count
// epoch-switch blocks: after an outage that skips a whole epoch number, the
// count lands on an older epoch and the penalty lists differ from canonical.
//
// The walk goes through the epoch-switch parent links, one existing epoch per
// step, and stops at the first epoch number not above the target.
//
// If the walk stops because an epoch-switch header is absent from the local DB
// (A.68.2 priming incomplete or its depth less than the penalty lookback
// window), ErrPenaltyAncestorMissing is returned so the caller can treat the
// missing-data case as retryable rather than silently computing wrong penalties.
func (x *XDPoS_v2) getPreviousEpochSwitchInfoByHash(chain consensus.ChainReader, hash common.Hash, limit int) (*types.EpochSwitchInfo, error) {
	epochSwitchInfo, err := x.getEpochSwitchInfo(chain, nil, hash)
	if err != nil {
		log.Error("[getPreviousEpochSwitchInfoByHash] getEpochSwitchInfo error", "err", err)
		return nil, err
	}
	epochOf := func(info *types.EpochSwitchInfo) uint64 {
		return x.config.V2.SwitchEpoch + uint64(info.EpochSwitchBlockInfo.Round)/x.config.Epoch
	}
	if epochOf(epochSwitchInfo) < uint64(limit) {
		return nil, nil
	}
	target := epochOf(epochSwitchInfo) - uint64(limit)
	for epochOf(epochSwitchInfo) > target {
		parentHash, ok, lookupErr := x.epochSwitchParentHashOrErr(chain, epochSwitchInfo)
		if lookupErr != nil {
			// Epoch-switch header not in DB — retryable, not a bad block.
			log.Warn("[getPreviousEpochSwitchInfoByHash] epoch-switch header missing from DB",
				"limit", limit, "err", lookupErr)
			return nil, lookupErr
		}
		if !ok {
			// Chain too young: no previous V2 epoch, so the target has no block.
			return nil, nil
		}
		epochSwitchInfo, err = x.getEpochSwitchInfo(chain, nil, parentHash)
		if err != nil {
			log.Error("[getPreviousEpochSwitchInfoByHash] recursive getEpochSwitchInfo error", "err", err)
			return nil, err
		}
	}
	if epochOf(epochSwitchInfo) != target {
		// The target epoch number was skipped: no block, empty list.
		return nil, nil
	}
	return epochSwitchInfo, nil
}

// epochSwitchParentHashOrErr is like epochSwitchParentHash but distinguishes
// two kinds of "not found":
//   - (hash{}, false, nil): no previous V2 epoch (chain too young)
//   - (hash{}, false, ErrPenaltyAncestorMissing): epoch-switch header not in DB
//
// Used by getPreviousEpochSwitchInfoByHash so the penalty lookback can signal
// a retryable missing-data condition instead of silently computing incorrect
// penalties. GetEpochSwitchInfoBetween continues to use epochSwitchParentHash
// (the old 2-value form) to preserve existing break-on-missing behaviour for
// the reward-window walk.
func (x *XDPoS_v2) epochSwitchParentHashOrErr(chain consensus.ChainReader, info *types.EpochSwitchInfo) (common.Hash, bool, error) {
	if info.EpochSwitchParentBlockInfo != nil {
		return info.EpochSwitchParentBlockInfo.Hash, true, nil
	}
	es := info.EpochSwitchBlockInfo
	if es == nil || es.Number == nil || es.Number.Cmp(x.config.V2.SwitchBlock) <= 0 {
		// Chain too young: no previous V2 epoch, not an error.
		return common.Hash{}, false, nil
	}
	header := chain.GetHeaderByHash(es.Hash)
	if header == nil {
		// Header exists in the epoch-switch chain but is absent from the local
		// DB. This is a transient, retryable condition (A.68.2 still priming).
		log.Warn("[epochSwitchParentHashOrErr] epoch-switch header not in DB; penalty ancestor missing",
			"number", es.Number, "hash", es.Hash)
		return common.Hash{}, false, utils.ErrPenaltyAncestorMissing
	}
	return header.ParentHash, true, nil
}

// epochSwitchParentHash returns the hash of the block before the epoch switch
// block in info, which lies in the previous epoch. ok is false when there is no
// previous V2 epoch (the V1->V2 switch block or genesis).
//
// Some cache entries carry no EpochSwitchParentBlockInfo: the in-batch path
// getEpochSwitchInfoWithParents (#1636) builds them without it. Treating a
// missing link as "no previous epoch" made getPreviousEpochSwitchInfoByHash
// return the current epoch for every limit, so HookPenalty counted the last
// penalty list LimitPenaltyEpoch times and released penalised signers one
// epoch after they were penalised ("validators not legit" on canonical Apothem
// 83,665,617 and 83,738,378). The epoch switch header's ParentHash is the same
// block (a V2 header's QC certifies its parent), so fall back to it.
func (x *XDPoS_v2) epochSwitchParentHash(chain consensus.ChainReader, info *types.EpochSwitchInfo) (common.Hash, bool) {
	if info.EpochSwitchParentBlockInfo != nil {
		return info.EpochSwitchParentBlockInfo.Hash, true
	}
	es := info.EpochSwitchBlockInfo
	if es == nil || es.Number == nil || es.Number.Cmp(x.config.V2.SwitchBlock) <= 0 {
		return common.Hash{}, false
	}
	header := chain.GetHeaderByHash(es.Hash)
	if header == nil {
		log.Warn("[epochSwitchParentHash] epoch switch header not found; cannot walk to previous epoch", "number", es.Number, "hash", es.Hash)
		return common.Hash{}, false
	}
	return header.ParentHash, true
}

// GetCurrentEpochSwitchBlock returns the epoch switch block number and epoch number
// for the epoch containing the given block number.
// Matches v2.6.8 engines/engine_v2/epochSwitch.go.
func (x *XDPoS_v2) GetCurrentEpochSwitchBlock(chain consensus.ChainReader, blockNum *big.Int) (uint64, uint64, error) {
	header := chain.GetHeaderByNumber(blockNum.Uint64())
	if header == nil {
		return 0, 0, ErrNotFoundBlockByNum
	}
	epochSwitchInfo, err := x.getEpochSwitchInfo(chain, header, header.Hash())
	if err != nil {
		log.Error("[GetCurrentEpochSwitchBlock] Fail to get epoch switch info", "Num", header.Number, "Hash", header.Hash())
		return 0, 0, err
	}

	currentCheckpointNumber := epochSwitchInfo.EpochSwitchBlockInfo.Number.Uint64()
	epochNum := x.config.V2.SwitchEpoch + uint64(epochSwitchInfo.EpochSwitchBlockInfo.Round)/x.config.Epoch
	return currentCheckpointNumber, epochNum, nil
}

// GetEpochSwitchInfoBetween returns all epoch switch infos between begin and end headers.
// Searches backward from end to begin.
// Matches v2.6.8 engines/engine_v2/epochSwitch.go.
func (x *XDPoS_v2) GetEpochSwitchInfoBetween(chain consensus.ChainReader, begin, end *types.Header) ([]*types.EpochSwitchInfo, error) {
	infos := make([]*types.EpochSwitchInfo, 0)
	iteratorHeader := end
	iteratorHash := end.Hash()
	iteratorNum := end.Number

	// Special case: begin == end.  The main loop below uses strict > so it never
	// processes a header whose number equals begin.Number.  When both point to
	// the same epoch-switch block the caller expects [epochSwitchInfo] not [].
	// Return immediately to avoid the trivial no-op loop and any risk of a
	// duplicate entry (CertiK XDC-30).
	if begin.Number.Cmp(end.Number) == 0 {
		epochSwitchInfo, err := x.getEpochSwitchInfo(chain, end, iteratorHash)
		if err != nil {
			log.Error("[GetEpochSwitchInfoBetween] getEpochSwitchInfo error (begin==end)", "err", err)
			return nil, err
		}
		if epochSwitchInfo.EpochSwitchBlockInfo.Number.Cmp(end.Number) == 0 {
			return []*types.EpochSwitchInfo{epochSwitchInfo}, nil
		}
		return infos, nil
	}

	for iteratorNum.Cmp(begin.Number) > 0 {
		epochSwitchInfo, err := x.getEpochSwitchInfo(chain, iteratorHeader, iteratorHash)
		if err != nil {
			log.Error("[GetEpochSwitchInfoBetween] getEpochSwitchInfo error", "err", err)
			return nil, err
		}
		iteratorHeader = nil
		parentHash, ok := x.epochSwitchParentHash(chain, epochSwitchInfo)
		if !ok {
			break
		}
		iteratorHash = parentHash
		iteratorNum = epochSwitchInfo.EpochSwitchBlockInfo.Number
		if iteratorNum.Cmp(begin.Number) >= 0 {
			infos = append(infos, epochSwitchInfo)
		}
	}

	// Reverse to chronological order
	for i := 0; i < len(infos)/2; i++ {
		infos[i], infos[len(infos)-1-i] = infos[len(infos)-1-i], infos[i]
	}
	return infos, nil
}
