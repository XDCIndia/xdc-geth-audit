// Copyright 2024 XDC Network
// Extended XDPoS API functions ported from v2.6.8
// Missing RPC functions for explorer/tooling compatibility

package XDPoS

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
)

// V2BlockInfo contains V2-specific block information for RPC responses
type V2BlockInfo struct {
	Hash        common.Hash      `json:"hash"`
	Number      *big.Int         `json:"number"`
	Round       types.Round      `json:"round"`
	Masternodes []common.Address `json:"masternodes"`
	EncodedQC   string           `json:"encodedQC"`
	Error       string           `json:"error,omitempty"`
}

// MasternodesStatus contains masternode status for RPC
type MasternodesStatus struct {
	Number      *big.Int         `json:"number"`
	Masternodes []common.Address `json:"masternodes"`
}

// MessageStatus contains pool status information
type MessageStatus struct {
	VotePool    int `json:"votePool"`
	TimeoutPool int `json:"timeoutPool"`
}

// NetworkInfo contains network summary information
type NetworkInfo struct {
	NetworkId     *big.Int `json:"networkId"`
	XDCValidators int      `json:"xdcValidators"`
	Epoch         uint64   `json:"epoch"`
	V2SwitchBlock *big.Int `json:"v2SwitchBlock,omitempty"`
}

// AccountRewardResponse contains reward query results
type AccountRewardResponse struct {
	Rewards []AccountEpochReward `json:"rewards"`
}

// AccountEpochReward contains per-epoch reward data
type AccountEpochReward struct {
	Epoch       uint64 `json:"epoch"`
	BlockNumber uint64 `json:"blockNumber"`
	Reward      string `json:"reward"`
	Status      string `json:"status"`
}

// rewardFileName helper for reward file lookups
type rewardFileName struct {
	Number uint64
	Hash   string
}

// getHeaderFromApiBlockNum resolves an RPC block number to a header
func (api *API) getHeaderFromApiBlockNum(number *rpc.BlockNumber) *types.Header {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	return header
}

// GetV2BlockByNumber returns V2-specific block information
func (api *API) GetV2BlockByNumber(number *rpc.BlockNumber) *V2BlockInfo {
	header := api.getHeaderFromApiBlockNum(number)
	if header == nil {
		return &V2BlockInfo{Error: "block not found"}
	}
	return api.GetV2BlockByHeader(header, false)
}

// GetV2BlockByHash returns V2-specific block information by hash
func (api *API) GetV2BlockByHash(blockHash common.Hash) *V2BlockInfo {
	header := api.chain.GetHeaderByHash(blockHash)
	if header == nil {
		return &V2BlockInfo{Error: "block not found"}
	}
	return api.GetV2BlockByHeader(header, false)
}

// GetV2BlockByHeader extracts V2-specific info from a header.
// For V2 blocks, recovers the round from the header seal via the V2 engine (issue #1482).
func (api *API) GetV2BlockByHeader(header *types.Header, uncle bool) *V2BlockInfo {
	info := &V2BlockInfo{
		Hash:   header.Hash(),
		Number: header.Number,
	}

	if api.xdpos.config.V2 != nil && api.xdpos.config.V2.SwitchBlock != nil &&
		header.Number.Uint64() > api.xdpos.config.V2.SwitchBlock.Uint64() &&
		api.xdpos.EngineV2 != nil {
		round, err := api.xdpos.EngineV2.GetRoundNumber(header)
		if err == nil {
			info.Round = round
		}
		info.Masternodes = api.xdpos.GetMasternodes(api.chain, header)
		// F-2026-19639: return the quorum certificate carried in the header.
		var extra types.ExtraFields_v2
		if err := utils.DecodeBytesExtraFields(header.Extra, &extra); err == nil && extra.QuorumCert != nil {
			if enc, err := rlp.EncodeToBytes(extra.QuorumCert); err == nil {
				info.EncodedQC = base64.StdEncoding.EncodeToString(enc)
			}
		}
	}

	return info
}

// GetMasternodesByNumber returns masternodes with status info
func (api *API) GetMasternodesByNumber(number *rpc.BlockNumber) MasternodesStatus {
	header := api.getHeaderFromApiBlockNum(number)
	if header == nil {
		return MasternodesStatus{}
	}
	masternodes := api.xdpos.GetMasternodes(api.chain, header)
	return MasternodesStatus{
		Number:      header.Number,
		Masternodes: masternodes,
	}
}

// GetBlockInfoByEpochNum returns block info for a specific epoch number.
// For V2 epochs, uses the V2 engine's round-based lookup (issue #1482).
// For V1 epochs (before SwitchEpoch), falls back to block-number arithmetic.
func (api *API) GetBlockInfoByEpochNum(epochNumber uint64) (*utils.EpochNumInfo, error) {
	if api.xdpos.config.V2 != nil && epochNumber >= api.xdpos.config.V2.SwitchEpoch &&
		api.xdpos.EngineV2 != nil {
		chainReader, ok := api.chain.(consensus.ChainReader)
		if !ok {
			return nil, fmt.Errorf("chain does not implement ChainReader")
		}
		blockInfo, err := api.xdpos.EngineV2.GetBlockByEpochNumber(chainReader, epochNumber)
		if err != nil {
			return nil, err
		}
		info := &utils.EpochNumInfo{
			EpochBlockHash:        blockInfo.Hash,
			EpochRound:            types.Round(blockInfo.Round),
			EpochFirstBlockNumber: blockInfo.Number,
		}
		// Best-effort: look up the last block of this epoch (first block of next epoch - 1).
		nextInfo, err := api.xdpos.EngineV2.GetBlockByEpochNumber(chainReader, epochNumber+1)
		if err == nil {
			info.EpochLastBlockNumber = new(big.Int).Sub(nextInfo.Number, big.NewInt(1))
		}
		return info, nil
	}
	// V1: epoch switch block is always at epochNumber * Epoch.
	epoch := api.xdpos.config.Epoch
	epochBlockNum := epochNumber * epoch
	return &utils.EpochNumInfo{
		EpochRound:            types.Round(epochBlockNum),
		EpochFirstBlockNumber: new(big.Int).SetUint64(epochBlockNum),
		EpochLastBlockNumber:  new(big.Int).SetUint64(epochBlockNum + epoch - 1),
	}, nil
}

// GetEpochNumbersBetween returns the epoch-switch block numbers between two block numbers.
// For V2 ranges, uses the V2 engine's round-based epoch switch lookup (issue #1482).
func (api *API) GetEpochNumbersBetween(begin, end *rpc.BlockNumber) ([]uint64, error) {
	beginHeader := api.getHeaderFromApiBlockNum(begin)
	endHeader := api.getHeaderFromApiBlockNum(end)
	if beginHeader == nil || endHeader == nil {
		return nil, nil
	}

	// Use V2 engine for ranges fully in V2 (both headers past the switch block).
	if api.xdpos.IsV2Block(beginHeader) && api.xdpos.EngineV2 != nil {
		chainReader, ok := api.chain.(consensus.ChainReader)
		if !ok {
			return nil, fmt.Errorf("chain does not implement ChainReader")
		}
		epochSwitchInfos, err := api.xdpos.EngineV2.GetEpochSwitchInfoBetween(chainReader, beginHeader, endHeader)
		if err != nil {
			return nil, err
		}
		result := make([]uint64, len(epochSwitchInfos))
		for i, info := range epochSwitchInfos {
			result[i] = info.EpochSwitchBlockInfo.Number.Uint64()
		}
		return result, nil
	}

	// F-2026-19639: a range that starts at or before the V2 switch has no
	// round-based epoch switches to report, and block-number division gave
	// wrong values. Return the canonical XDPoSChain error instead.
	return nil, errors.New("not supported in the v1 consensus")
}

// GetMissedRoundsInEpochByBlockNum returns missed rounds info for the epoch containing the given block.
// For V2 blocks, delegates to the V2 engine's round-based calculation (issue #1482).
func (api *API) GetMissedRoundsInEpochByBlockNum(number *rpc.BlockNumber) (*utils.PublicApiMissedRoundsMetadata, error) {
	header := api.getHeaderFromApiBlockNum(number)
	if header == nil {
		return nil, nil
	}

	if api.xdpos.IsV2Block(header) && api.xdpos.EngineV2 != nil {
		chainReader, ok := api.chain.(consensus.ChainReader)
		if !ok {
			return nil, fmt.Errorf("chain does not implement ChainReader")
		}
		return api.xdpos.EngineV2.CalculateMissingRounds(chainReader, header)
	}

	// V1: block-number arithmetic.
	num := header.Number.Uint64()
	epochStart := num - num%api.xdpos.config.Epoch
	return &utils.PublicApiMissedRoundsMetadata{
		EpochRound:       types.Round(epochStart),
		EpochBlockNumber: new(big.Int).SetUint64(epochStart),
	}, nil
}

// GetLatestPoolStatus returns current vote/timeout pool sizes
func (api *API) GetLatestPoolStatus() MessageStatus {
	return MessageStatus{
		VotePool:    0, // TODO: integrate with v2 engine vote pool
		TimeoutPool: 0, // TODO: integrate with v2 engine timeout pool
	}
}

// NetworkInformation returns network summary
func (api *API) NetworkInformation() NetworkInfo {
	// Issue #694 §G: prefer the V2 config's MaxMasternodes when available.
	// V1 has 18 masternodes (common.MaxMasternodes); V2 expands to 108 (or
	// whatever the chain config specifies). The V2 engine queries
	// x.config.V2.Config(round).MaxMasternodes for the same reason — this
	// API now does the same so V2-active chains return 108, not the V1
	// constant. Falls back to common.MaxMasternodes when V2 isn't set.
	maxValidators := common.MaxMasternodes
	if api.xdpos.config.V2 != nil &&
		api.xdpos.config.V2.CurrentConfig != nil &&
		api.xdpos.config.V2.CurrentConfig.MaxMasternodes > 0 {
		maxValidators = api.xdpos.config.V2.CurrentConfig.MaxMasternodes
	}
	info := NetworkInfo{
		NetworkId:     big.NewInt(50), // XDC Mainnet chain ID
		XDCValidators: maxValidators,
		Epoch:         api.xdpos.config.Epoch,
	}
	if api.xdpos.config.V2 != nil && api.xdpos.config.V2.SwitchBlock != nil {
		info.V2SwitchBlock = api.xdpos.config.V2.SwitchBlock
	}
	return info
}

// GetRewardByAccount returns reward history for an account by scanning reward files in StoreRewardFolder.
// Fix #65: Reads per-block reward JSON files written by Finalize and filters by account address.
// Reward file format: map[string]map[string]map[string]*big.Int (validator -> voter -> field -> amount)
// TODO: Support reward DB lookup for non-file-based storage.
func (api *API) GetRewardByAccount(account common.Address, begin rpc.BlockNumber, end rpc.BlockNumber) (AccountRewardResponse, error) {
	log.Debug("[GetRewardByAccount]", "account", account.Hex(), "begin", begin, "end", end)

	if len(StoreRewardFolder) == 0 {
		return AccountRewardResponse{Rewards: make([]AccountEpochReward, 0)},
			fmt.Errorf("StoreRewardFolder not configured; start node with --store-reward-folder to enable reward history")
	}

	// Resolve block range
	var beginNum, endNum uint64
	currentHeader := api.chain.CurrentHeader()
	if currentHeader == nil {
		return AccountRewardResponse{Rewards: make([]AccountEpochReward, 0)}, fmt.Errorf("cannot get current header")
	}
	currentNum := currentHeader.Number.Uint64()

	if begin == rpc.LatestBlockNumber || begin < 0 {
		beginNum = currentNum
	} else {
		beginNum = uint64(begin.Int64())
	}
	if end == rpc.LatestBlockNumber || end < 0 {
		endNum = currentNum
	} else {
		endNum = uint64(end.Int64())
	}

	epoch := api.xdpos.config.Epoch
	accountHex := strings.ToLower(account.Hex())

	// Read all reward files in the folder and match by block range and account
	entries, err := os.ReadDir(StoreRewardFolder)
	if err != nil {
		return AccountRewardResponse{Rewards: make([]AccountEpochReward, 0)},
			fmt.Errorf("cannot read StoreRewardFolder %s: %w", StoreRewardFolder, err)
	}

	rewards := make([]AccountEpochReward, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Filename format: "<blockNumber>.<blockHash>"
		name := entry.Name()
		var blockNum uint64
		if _, err := fmt.Sscanf(strings.SplitN(name, ".", 2)[0], "%d", &blockNum); err != nil {
			continue
		}
		if blockNum < beginNum || blockNum > endNum {
			continue
		}

		data, err := os.ReadFile(filepath.Join(StoreRewardFolder, name))
		if err != nil {
			log.Warn("[GetRewardByAccount] cannot read reward file", "file", name, "err", err)
			continue
		}

		// Reward file: map[validator]map[voter]map[field]*big.Int
		// We look for account as either a validator key or a voter key.
		var raw map[string]map[string]map[string]*big.Int
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}

		// Accumulate total reward for this account in this block
		total := new(big.Int)
		for validator, voters := range raw {
			if strings.ToLower(validator) == accountHex {
				// account is a masternode - sum all fields for all voters
				for _, fields := range voters {
					for _, amt := range fields {
						if amt != nil {
							total.Add(total, amt)
						}
					}
				}
			} else {
				// account may be a voter under this validator
				for voter, fields := range voters {
					if strings.ToLower(voter) == accountHex {
						for _, amt := range fields {
							if amt != nil {
								total.Add(total, amt)
							}
						}
					}
				}
			}
		}

		if total.Sign() > 0 {
			epochNum := blockNum / epoch
			rewards = append(rewards, AccountEpochReward{
				Epoch:       epochNum,
				BlockNumber: blockNum,
				Reward:      total.String(),
				Status:      "paid",
			})
		}
	}

	return AccountRewardResponse{Rewards: rewards}, nil
}

// GetCheckpointByEpochNumber returns the epoch-switch block of a V2 epoch
// (epochNum >= V2.SwitchEpoch) from the round-based V2 engine. ok is false
// for V1 epochs, which keep the block-number formula. F-2026-19639.
func (x *XDPoS) GetCheckpointByEpochNumber(chain consensus.ChainReader, epochNum uint64) (number uint64, ok bool, err error) {
	if x.config.V2 == nil || x.config.V2.SwitchBlock == nil || epochNum < x.config.V2.SwitchEpoch || x.EngineV2 == nil {
		return 0, false, nil
	}
	info, err := x.EngineV2.GetBlockByEpochNumber(chain, epochNum)
	if err != nil {
		return 0, true, err
	}
	return info.Number.Uint64(), true, nil
}
