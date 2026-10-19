// Package hooks provides consensus hooks for XDPoS engine integration.
// V2 hooks are in engine_v2_hooks.go.

package hooks

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	xdc_sort "github.com/ethereum/go-ethereum/common/sort"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// AttachConsensusV1Hooks attaches V1 consensus hooks to XDPoS engine. (#72)
// Mirrors v2.6.8 eth/hooks/engine_v1_hooks.go AttachConsensusV1Hooks.
func AttachConsensusV1Hooks(adaptor *XDPoS.XDPoS, bc *core.BlockChain, chainConfig *params.ChainConfig) {
	xdposConf := chainConfig.XDPoS

	// HookValidator: computes the Validators bytes (M2 list) for a checkpoint
	// block this node mints. Port of canonical XDPoSChain eth/hooks/
	// engine_v1_hooks.go HookValidator / getValidatorsAtNumber: at every
	// checkpoint the list is derived from the 0x90 randomize values in the
	// parent's state, the same list HookVerifyMNs checks on import.
	adaptor.HookValidator = func(header *types.Header, signers []common.Address) ([]byte, error) {
		parent := bc.GetHeaderByHash(header.ParentHash)
		if parent == nil {
			return nil, errors.New("v1 HookValidator: nil parent block to derive the validators from")
		}
		statedb, err := bc.StateAt(parent.Root, parent.Number, parent.Time)
		if err != nil {
			return nil, err
		}
		validators, err := validatorsFromState(statedb, signers)
		if err != nil {
			return nil, err
		}
		header.Validators = validators
		return validators, nil
	}

	// HookVerifyMNs: verifies the Validators field at epoch checkpoints against
	// the M2 list recomputed from the 0x90 randomize values in the parent's
	// state. Port of canonical XDPoSChain eth/hooks/engine_v1_hooks.go
	// HookVerifyMNs (#1412). It never writes to the header under verification.
	adaptor.HookVerifyMNs = func(header *types.Header, signers []common.Address) error {
		number := header.Number.Uint64()
		if number == 0 || number%common.EpocBlockRandomize != 0 {
			return nil
		}
		// Canonical runs this check only under fullVerify, which is off on
		// chain 51, and not at all on the ignore-listed checkpoints.
		var chainID uint64
		if chainConfig.ChainID != nil {
			chainID = chainConfig.ChainID.Uint64()
		}
		if chainID == params.XDCApothemChainConfig.ChainID.Uint64() || XDPoS.IsIgnoreSignerCheckBlock(chainID, number) {
			return nil
		}
		// Enforce only when the parent's state is here. Without it the
		// randomize values cannot be read (e.g. a checkpoint-synced node).
		parent := bc.GetHeaderByHash(header.ParentHash)
		if parent == nil {
			log.Debug("[V1 HookVerifyMNs] parent unknown, skipping", "number", number)
			return nil
		}
		statedb, err := bc.StateAt(parent.Root, parent.Number, parent.Time)
		if err != nil {
			log.Debug("[V1 HookVerifyMNs] parent state unavailable, skipping", "number", number, "err", err)
			return nil
		}
		validators, err := validatorsFromState(statedb, signers)
		if err != nil {
			return err
		}
		if !bytes.Equal(header.Validators, validators) {
			return XDPoS.ErrInvalidCheckpointValidators
		}
		return nil
	}

	// HookPenalty (before TIPSigning): masternodes of the previous epoch that
	// signed none of the blocks it checks in the 0x89 block-signer state are
	// penalized. Port of canonical XDPoSChain eth/hooks/engine_v1_hooks.go
	// HookPenalty (#1422), including its in-place list removal.
	adaptor.HookPenalty = func(chain consensus.ChainHeaderReader, blockNumberEpoch uint64, parents []*types.Header) ([]common.Address, error) {
		if xdposConf == nil {
			return []common.Address{}, nil
		}
		epoch := xdposConf.Epoch
		if blockNumberEpoch < epoch {
			return []common.Address{}, nil
		}
		// Canonical reads the 0x89 block-signer mapping at the canonical head
		// (bc.State()) and log.Crit's without it. A non-archive node can lack it
		// after a rewind: report it as unavailable so the compare is skipped.
		canonicalState, err := bc.State()
		if canonicalState == nil || err != nil {
			return nil, fmt.Errorf("%w: v1 HookPenalty: no state at the canonical head: %v", XDPoS.ErrPenaltyDataUnavailable, err)
		}
		byNumber := batchHeaderByNumber(chain, parents)
		prevEpoch := blockNumberEpoch - epoch
		prevHeader := byNumber(prevEpoch)
		if prevHeader == nil {
			return nil, fmt.Errorf("v1 HookPenalty: no header at %d", prevEpoch)
		}
		// Copy: the removal below works in place.
		penSigners := append([]common.Address(nil), adaptor.GetMasternodes(chain, prevHeader)...)
		if len(penSigners) > 0 {
			for i := prevEpoch; i < blockNumberEpoch; i++ {
				if i%common.MergeSignRange == 0 || !chainConfig.IsTIP2019(new(big.Int).SetUint64(i)) {
					bheader := byNumber(i)
					if bheader == nil {
						return nil, fmt.Errorf("v1 HookPenalty: no header at %d", i)
					}
					// The signer mapping is keyed by block hash only, so the
					// header is enough (the batch has no bodies).
					block := types.NewBlockWithHeader(bheader)
					if len(penSigners) > 0 {
						signedMasternodes, err := contracts.GetSignersFromContract(canonicalState, block)
						if err != nil {
							return nil, err
						}
						for _, signed := range signedMasternodes {
							for j, addr := range penSigners {
								if signed == addr {
									penSigners = append(penSigners[:j], penSigners[j+1:]...)
								}
							}
						}
					} else {
						break
					}
				}
			}
		}
		return penSigners, nil
	}

	// HookPenaltyTIPSigning (from TIPSigning): masternodes of the previous epoch
	// that mined no block in it, plus (from TIPRandomize) penalized nodes coming
	// back that signed none of the recent MergeSignRange blocks. Port of
	// canonical XDPoSChain eth/hooks/engine_v1_hooks.go HookPenaltyTIPSigning (#1422).
	adaptor.HookPenaltyTIPSigning = func(chain consensus.ChainHeaderReader, header *types.Header, candidates []common.Address, parents []*types.Header) ([]common.Address, error) {
		number := header.Number.Uint64()
		if xdposConf == nil {
			return []common.Address{}, nil
		}
		epoch := xdposConf.Epoch
		if number < epoch {
			return []common.Address{}, nil
		}
		byNumber := batchHeaderByNumber(chain, parents)
		inBatch := make(map[common.Hash]*types.Header, len(parents))
		for _, p := range parents {
			if p != nil {
				inBatch[p.Hash()] = p
			}
		}
		prevEpoch := number - epoch
		combackEpoch := uint64(0)
		comebackLength := (common.LimitPenaltyEpoch + 1) * epoch
		if number > comebackLength {
			combackEpoch = number - comebackLength
		}

		// Hashes of the epoch's blocks, newest first, and blocks mined per miner.
		listBlockHash := make([]common.Hash, epoch)
		statMiners := make(map[common.Address]int)
		listBlockHash[0] = header.ParentHash
		parentNumber := number - 1
		parentHash := header.ParentHash
		for i := uint64(1); i < epoch; i++ {
			parentHeader := inBatch[parentHash]
			if parentHeader == nil || parentHeader.Number.Uint64() != parentNumber {
				parentHeader = chain.GetHeader(parentHash, parentNumber)
			}
			if parentHeader == nil {
				return nil, fmt.Errorf("v1 HookPenaltyTIPSigning: no header at %d", parentNumber)
			}
			miner, _ := adaptor.RecoverSigner(parentHeader)
			statMiners[miner]++
			parentHash = parentHeader.ParentHash
			parentNumber--
			listBlockHash[i] = parentHash
		}

		prevHeader := byNumber(prevEpoch)
		if prevHeader == nil {
			return nil, fmt.Errorf("v1 HookPenaltyTIPSigning: no header at %d", prevEpoch)
		}
		penalties := []common.Address{}
		for miner, total := range statMiners {
			if total < common.MinimunMinerBlockPerEpoch {
				penalties = append(penalties, miner)
			}
		}
		for _, addr := range adaptor.GetMasternodes(chain, prevHeader) {
			if _, exist := statMiners[addr]; !exist {
				penalties = append(penalties, addr)
			}
		}

		// Penalized LimitPenaltyEpoch+1 epochs ago and candidates again.
		penComebacks := []common.Address{}
		if combackEpoch > 0 {
			combackHeader := byNumber(combackEpoch)
			if combackHeader == nil {
				return nil, fmt.Errorf("v1 HookPenaltyTIPSigning: no header at %d", combackEpoch)
			}
			for _, penalty := range contracts.ExtractAddressFromBytes(combackHeader.Penalties) {
				for _, addr := range candidates {
					if penalty == addr {
						penComebacks = append(penComebacks, penalty)
					}
				}
			}
		}

		// Comeback nodes that signed one of the recent MergeSignRange blocks are cleared.
		mapBlockHash := map[common.Hash]bool{}
		for i := common.RangeReturnSigner - 1; i >= 0; i-- {
			if len(penComebacks) == 0 {
				break
			}
			blockNumber := number - uint64(i) - 1
			bhash := listBlockHash[i]
			if blockNumber%common.MergeSignRange == 0 {
				mapBlockHash[bhash] = true
			}
			signingTxs, ok := adaptor.GetCachedSigningTxs(bhash)
			if !ok {
				block := bc.GetBlock(bhash, blockNumber)
				if block == nil {
					// Header verification gets no bodies; a block of the
					// batch is not written yet.
					return nil, fmt.Errorf("%w: v1 HookPenaltyTIPSigning: no block at %d", XDPoS.ErrPenaltyDataUnavailable, blockNumber)
				}
				signingTxs = adaptor.CacheSigner(bhash, block.Transactions())
			}
			signer := types.MakeSigner(chainConfig, new(big.Int).SetUint64(blockNumber), 0)
			for _, tx := range signingTxs {
				blkHash := common.BytesToHash(tx.Data()[len(tx.Data())-32:])
				from, err := types.Sender(signer, tx)
				if err != nil {
					continue
				}
				if mapBlockHash[blkHash] {
					for j, addr := range penComebacks {
						if from == addr {
							penComebacks = append(penComebacks[:j], penComebacks[j+1:]...)
							break
						}
					}
				}
			}
		}

		penalties = append(penalties, penComebacks...)
		if chainConfig.IsTIPRandomize(header.Number) {
			return penalties, nil
		}
		return penComebacks, nil
	}

	// HookGetSignersFromContract: the candidates of the gap block's state, by stake
	// descending, at most 150. Port of canonical XDPoSChain
	// eth/hooks/engine_v1_hooks.go HookGetSignersFromContract (#1418).
	adaptor.HookGetSignersFromContract = func(gapHeader *types.Header) ([]common.Address, error) {
		if gapHeader == nil {
			return nil, errors.New("nil gap block header in HookGetSignersFromContract")
		}
		statedb, err := bc.StateAt(gapHeader.Root, gapHeader.Number, gapHeader.Time)
		if err != nil {
			return nil, err
		}
		return signersFromState(statedb)
	}

	log.Info("[XDPoS] V1 consensus hooks attached (#72, #97)")
}

// signersFromState returns the candidates of the 0x88 validator contract, sorted
// by stake descending with canonical's frozen sort, at most 150.
func signersFromState(statedb *state.StateDB) ([]common.Address, error) {
	type candidate struct {
		addr  common.Address
		stake *big.Int
	}
	var candidates []candidate
	for _, addr := range state.GetCandidates(statedb) {
		candidates = append(candidates, candidate{addr, state.GetCandidateCap(statedb, addr)})
	}
	if err := statedb.Error(); err != nil {
		return nil, fmt.Errorf("reading the signers from state: %w", err)
	}
	xdc_sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].stake.Cmp(candidates[j].stake) >= 0
	})
	if len(candidates) > 150 {
		candidates = candidates[:150]
	}
	signers := make([]common.Address, 0, len(candidates))
	for _, c := range candidates {
		signers = append(signers, c.addr)
	}
	return signers, nil
}

// validatorsFromState derives the next epoch's M2 list (header.Validators) from
// the 0x90 randomize values of masternodes in statedb. Port of canonical
// XDPoSChain eth/hooks/engine_v1_hooks.go getValidatorsAtNumber.
func validatorsFromState(statedb *state.StateDB, masternodes []common.Address) ([]byte, error) {
	if len(masternodes) == 0 {
		return nil, errors.New("list M1 not found")
	}
	var candidates []int64
	for _, addr := range masternodes {
		random, err := contracts.DecryptRandomizeFromSecretsAndOpening(state.GetSecret(statedb, addr), state.GetOpening(statedb, addr))
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, random)
	}
	if err := statedb.Error(); err != nil {
		return nil, fmt.Errorf("reading the randomize values from state: %w", err)
	}
	m2, err := contracts.GenM2FromRandomize(candidates, int64(len(masternodes)))
	if err != nil {
		return nil, err
	}
	return contracts.BuildValidatorFromM2(m2), nil
}

// batchHeaderByNumber looks a header up among the verify batch's parents first,
// then in the chain, as canonical's batch chain reader does
// (consensus/XDPoS/verify_chain_reader.go, core/blockchain.go:2583-2585).
func batchHeaderByNumber(chain consensus.ChainHeaderReader, parents []*types.Header) func(uint64) *types.Header {
	byNumber := make(map[uint64]*types.Header, len(parents))
	for _, p := range parents {
		if p != nil && p.Number != nil {
			byNumber[p.Number.Uint64()] = p
		}
	}
	return func(n uint64) *types.Header {
		if h, ok := byNumber[n]; ok {
			return h
		}
		return chain.GetHeaderByNumber(n)
	}
}
