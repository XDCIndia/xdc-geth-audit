// Copyright (c) 2018 XDCchain
// Copyright 2024 The go-ethereum Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package XDPoS

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// PenaltyConfig holds configuration for penalty calculation
type PenaltyConfig struct {
	// MinimumBlocksPerEpoch is the minimum number of blocks a masternode must sign
	// to avoid being penalized
	MinimumBlocksPerEpoch int64

	// LimitPenaltyEpoch is the number of epochs a penalized masternode remains banned
	LimitPenaltyEpoch int
}

// DefaultPenaltyConfig returns the default penalty configuration
func DefaultPenaltyConfig() *PenaltyConfig {
	return &PenaltyConfig{
		MinimumBlocksPerEpoch: MinimunMinerBlockPerEpoch, // 1
		LimitPenaltyEpoch:     LimitPenaltyEpoch,         // 4
	}
}

// CalculatePenalties determines which masternodes should be penalized
// Masternodes are penalized if they:
// - Failed to sign the minimum required blocks in an epoch
// - Behaved maliciously (double signing, etc.)
func CalculatePenalties(
	chain consensus.ChainHeaderReader,
	epoch uint64,
	masternodes []common.Address,
	signCount map[common.Address]int64,
	config *PenaltyConfig,
) []common.Address {
	penalties := make([]common.Address, 0)

	if config == nil {
		config = DefaultPenaltyConfig()
	}

	for _, masternode := range masternodes {
		count, exists := signCount[masternode]

		// Penalize masternodes who didn't sign enough blocks
		if !exists || count < config.MinimumBlocksPerEpoch {
			penalties = append(penalties, masternode)
			log.Info("Masternode penalized for insufficient signing",
				"address", masternode.Hex(),
				"signCount", count,
				"minimum", config.MinimumBlocksPerEpoch,
				"epoch", epoch)
		}
	}

	return penalties
}

// ExtractPenaltiesFromHeader extracts penalty addresses from a block header.
// In XDC, penalized masternodes are encoded in header.Penalties as a packed
// byte array of 20-byte addresses, identical to how masternodes are packed in
// header.Extra.  This matches v2.6.8 behaviour.
func ExtractPenaltiesFromHeader(header *types.Header) []common.Address {
	if len(header.Penalties) == 0 {
		return []common.Address{}
	}
	return extractAddressFromBytes(header.Penalties)
}

// IsPenalized checks if an address is in the penalty list
func IsPenalized(address common.Address, penalties []common.Address) bool {
	for _, penalty := range penalties {
		if penalty == address {
			return true
		}
	}
	return false
}
