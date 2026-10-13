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
//
// NOTE: Full M2 randomization via the 0x0090 randomize contract requires
// a ContractBackend (IPC client). That wiring (bc.GetClient / bc.IPCEndpoint)
// is tracked in issue #72 as a follow-up. For now we wire the hooks with a
// best-effort implementation: direct signer encoding pre-TIPRandomize, and
// pass-through (non-enforcing) post-TIPRandomize to unblock sync.
func AttachConsensusV1Hooks(adaptor *XDPoS.XDPoS, bc *core.BlockChain, chainConfig *params.ChainConfig) {
	xdposConf := chainConfig.XDPoS

	// HookValidator: computes the Validators bytes for a checkpoint block header.
	// Stored in header.Validators field; used by V1 verifySeal / M2 double-signing.
	adaptor.HookValidator = func(header *types.Header, signers []common.Address) ([]byte, error) {
		if xdposConf == nil || len(signers) == 0 {
			return []byte{}, nil
		}
		number := header.Number.Uint64()

		// Before TIPRandomize (3,464,000): encode signers in order
		if number < 3464000 {
			result := make([]byte, 0, len(signers)*common.AddressLength)
			for _, s := range signers {
				result = append(result, s.Bytes()...)
			}
			header.Validators = result
			return result, nil
		}

		// After TIPRandomize: M2 randomization is derived from the 0x0090 randomize contract.
		// Full contract-call support requires bc.GetClient() (IPC) — see issue #72 follow-up.
		// For now: if header.Validators is already set (synced from network), use it as-is.
		// When mining, this hook is called to SET the field — we can't derive M2 without IPC,
		// so we fall back to encoding signers directly (produces wrong M2 but won't crash).
		if len(header.Validators) > 0 {
			log.Trace("[V1 HookValidator] using existing Validators from header", "number", number)
			return header.Validators, nil
		}

		// Fallback: direct encoding (wrong M2 order post-TIPRandomize, but non-fatal in sync mode)
		log.Debug("[V1 HookValidator] M2 contract unavailable, direct encoding", "number", number)
		result := make([]byte, 0, len(signers)*common.AddressLength)
		for _, s := range signers {
			result = append(result, s.Bytes()...)
		}
		header.Validators = result
		return result, nil
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

	// Fix #97: Wire V1 HookPenalty.
	// Walks through the epoch, checks which masternodes signed blocks via block signers.
	// Masternodes that never signed are penalized (removed from next epoch list).
	adaptor.HookPenalty = func(chain consensus.ChainHeaderReader, blockNumberEpoch uint64) ([]common.Address, error) {
		epoch := uint64(900)
		if xdposConf != nil && xdposConf.Epoch > 0 {
			epoch = xdposConf.Epoch
		}
		if blockNumberEpoch < epoch {
			return []common.Address{}, nil
		}
		prevEpoch := blockNumberEpoch - epoch
		prevHeader := chain.GetHeaderByNumber(prevEpoch)
		if prevHeader == nil {
			return []common.Address{}, nil
		}

		// Get masternode list at previous epoch
		masternodes := adaptor.GetMasternodes(chain, prevHeader)
		if len(masternodes) == 0 {
			return []common.Address{}, nil
		}

		// Track which masternodes signed at least one block
		signed := make(map[common.Address]bool)
		for i := prevEpoch + 1; i < blockNumberEpoch; i++ {
			h := chain.GetHeaderByNumber(i)
			if h == nil {
				continue
			}
			// Recover signer from header
			signer, err := adaptor.RecoverSigner(h)
			if err == nil {
				signed[signer] = true
			}
		}

		// Penalize masternodes that never signed
		var penalties []common.Address
		for _, mn := range masternodes {
			if !signed[mn] {
				penalties = append(penalties, mn)
			}
		}
		log.Info("[V1 HookPenalty] Calculated penalties", "block", blockNumberEpoch, "penalized", len(penalties), "total", len(masternodes))
		return penalties, nil
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
