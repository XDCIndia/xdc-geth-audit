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
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// maxProposals caps the in-memory proposals map to prevent unbounded growth
// from repeated XDPoS_propose calls on the authenticated endpoint.
const maxProposals = 200

// API is a user facing RPC API to allow controlling the signer and voting
// mechanisms of the XDPoS consensus engine.
type API struct {
	chain consensus.ChainHeaderReader
	xdpos *XDPoS
}

// GetSnapshot retrieves the state snapshot at a given block.
func (api *API) GetSnapshot(number *rpc.BlockNumber) (*Snapshot, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.xdpos.GetSnapshot(api.chain, header)
}

// GetSnapshotAtHash retrieves the state snapshot at a given block hash.
func (api *API) GetSnapshotAtHash(hash common.Hash) (*Snapshot, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.xdpos.GetSnapshot(api.chain, header)
}

// GetSigners retrieves the list of authorized signers at the specified block.
func (api *API) GetSigners(number *rpc.BlockNumber) ([]common.Address, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, errUnknownBlock
	}
	snap, err := api.xdpos.GetSnapshot(api.chain, header)
	if err != nil {
		return nil, err
	}
	return snap.GetSigners(), nil
}

// GetSignersAtHash retrieves the list of authorized signers at the specified block hash.
func (api *API) GetSignersAtHash(hash common.Hash) ([]common.Address, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, errUnknownBlock
	}
	snap, err := api.xdpos.GetSnapshot(api.chain, header)
	if err != nil {
		return nil, err
	}
	return snap.GetSigners(), nil
}

// GetMasternodes retrieves the list of masternodes at the specified block.
func (api *API) GetMasternodes(number *rpc.BlockNumber) ([]common.Address, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.xdpos.GetMasternodes(api.chain, header), nil
}

// Proposals returns the current proposals the node tries to uphold and vote on.
func (api *API) Proposals() map[common.Address]bool {
	api.xdpos.lock.RLock()
	defer api.xdpos.lock.RUnlock()

	proposals := make(map[common.Address]bool)
	for address, auth := range api.xdpos.proposals {
		proposals[address] = auth
	}
	return proposals
}

// AdminAPI is a restricted XDPoS API for node operators.
// It is registered with Authenticated: true and is therefore only accessible
// over the local IPC or --authrpc endpoint, not over public HTTP/WS.
type AdminAPI struct {
	chain consensus.ChainHeaderReader
	xdpos *XDPoS
}

// Propose injects a new authorization proposal that the signer will attempt to
// push through. It returns an error when the proposals map is full to prevent
// unbounded memory growth from repeated calls.
func (api *AdminAPI) Propose(address common.Address, auth bool) error {
	api.xdpos.lock.Lock()
	defer api.xdpos.lock.Unlock()
	if _, exists := api.xdpos.proposals[address]; !exists && len(api.xdpos.proposals) >= maxProposals {
		return errors.New("proposals map full: discard an existing proposal first")
	}
	api.xdpos.proposals[address] = auth
	return nil
}

// Discard drops a currently running proposal.
func (api *AdminAPI) Discard(address common.Address) {
	api.xdpos.lock.Lock()
	defer api.xdpos.lock.Unlock()
	delete(api.xdpos.proposals, address)
}

// SignerTypes tracks signer participation status for vote/timeout pools.
// Ported from v2.6.8 api.go for api_test.go coverage. (#301)
type SignerTypes struct {
	CurrentNumber  int
	CurrentSigners []common.Address
	MissingSigners []common.Address
}

// calculateSigners computes current and missing signers from a message pool.
// Ported from v2.6.8 api.go for api_test.go coverage. (#301)
func calculateSigners(message map[string]SignerTypes, pool map[string]map[common.Hash]utils.PoolObj, masternodes []common.Address) {
	for name, objs := range pool {
		var currentSigners []common.Address
		missingSigners := make([]common.Address, len(masternodes))
		copy(missingSigners, masternodes)

		num := len(objs)
		for _, obj := range objs {
			signer := obj.GetSigner()
			currentSigners = append(currentSigners, signer)
			for i, mn := range missingSigners {
				if mn == signer {
					missingSigners = append(missingSigners[:i], missingSigners[i+1:]...)
					break
				}
			}
		}
		message[name] = SignerTypes{
			CurrentNumber:  num,
			CurrentSigners: currentSigners,
			MissingSigners: missingSigners,
		}
	}
}

// GetCandidates returns the current candidates
func (api *API) GetCandidates(number *rpc.BlockNumber) ([]common.Address, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, errUnknownBlock
	}
	snap, err := api.xdpos.GetSnapshot(api.chain, header)
	if err != nil {
		return nil, err
	}
	return snap.GetSigners(), nil
}
