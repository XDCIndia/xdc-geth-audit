// Copyright 2026 XDC Network

package XDPoS

import (
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// cpPenaltiesChain serves only the chain config; parents come from the batch.
type cpPenaltiesChain struct{ cfg *params.ChainConfig }

func (c cpPenaltiesChain) Config() *params.ChainConfig                 { return c.cfg }
func (c cpPenaltiesChain) CurrentHeader() *types.Header                { return nil }
func (c cpPenaltiesChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c cpPenaltiesChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (c cpPenaltiesChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// TestCheckpointPenaltiesChecked: under full verification a V1 checkpoint's
// Penalties must equal the list computed by the hook canonical selects by
// IsTIPSigning (#1422).
func TestCheckpointPenaltiesChecked(t *testing.T) {
	savedBulk := core.XdcBulkSyncMode.Load()
	core.XdcBulkSyncMode.Store(false)
	defer core.XdcBulkSyncMode.Store(savedBulk)
	savedTIP := common.TIPSigning
	common.TIPSigning = big.NewInt(3000000)
	defer func() { common.TIPSigning = savedTIP }()

	key, _ := crypto.GenerateKey()
	a := crypto.PubkeyToAddress(key.PublicKey)
	p, q := mn(0xb1), mn(0xb2) // penalized before / from TIPSigning

	verify := func(number uint64, tipHook bool, penalized common.Address, penalties []byte, unavailable bool) error {
		chain := cpPenaltiesChain{&params.ChainConfig{ChainID: big.NewInt(50), XDPoS: testConfig}}
		engine := New(testConfig, rawdb.NewMemoryDatabase())
		var hookErr error
		if unavailable {
			hookErr = fmt.Errorf("%w: no state", ErrPenaltyDataUnavailable)
		}
		var batch []*types.Header
		engine.HookPenalty = func(_ consensus.ChainHeaderReader, _ uint64, parents []*types.Header) ([]common.Address, error) {
			batch = parents
			return []common.Address{p}, hookErr
		}
		if tipHook {
			engine.HookPenaltyTIPSigning = func(_ consensus.ChainHeaderReader, _ *types.Header, _ []common.Address, parents []*types.Header) ([]common.Address, error) {
				batch = parents
				return []common.Address{q}, hookErr
			}
		}
		parent := &types.Header{
			Number:     new(big.Int).SetUint64(number - 1),
			Difficulty: big.NewInt(1),
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   420000000,
			Time:       1000,
		}
		engine.recents.Add(parent.Hash(), newSnapshot(testConfig, engine.signatures, number-1, parent.Hash(), []common.Address{a, penalized}))

		// The checkpoint lists the snapshot minus this epoch's penalty: [a].
		extra := make([]byte, extraVanity+common.AddressLength+extraSeal)
		copy(extra[extraVanity:], a[:])
		cp := &types.Header{
			ParentHash: parent.Hash(),
			Number:     new(big.Int).SetUint64(number),
			Time:       parent.Time + testConfig.Period,
			Extra:      extra,
			Validators: []byte("\x00\x00\x000"), // a's M2 is a
			Penalties:  penalties,
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   parent.GasLimit,
			Difficulty: engine.calcDifficulty(chain, parent, a),
		}
		sig, _ := crypto.Sign(sigHash(cp).Bytes(), key)
		copy(cp.Extra[len(cp.Extra)-extraSeal:], sig)
		cp.Validator, _ = crypto.Sign(sigHash(cp).Bytes(), key)

		_, results := engine.VerifyHeaders(chain, []*types.Header{parent, cp})
		<-results // parent: not under test
		err := <-results
		if len(batch) != 1 || batch[0] != parent {
			t.Errorf("hook got parents %v, want the batch's [parent]", batch)
		}
		return err
	}

	const epoch = 900
	const tipEpoch = 3000600 // first checkpoint past TIPSigning
	cases := []struct {
		name      string
		number    uint64
		tipHook   bool
		penalized common.Address
		penalties []byte
		// unavailable: the hook reports ErrPenaltyDataUnavailable (state
		// pruned after a rewind); the compare is skipped, not failed.
		unavailable bool
		want        error
	}{
		{"pre-TIPSigning, computed", epoch, false, p, p[:], false, nil},
		{"pre-TIPSigning, wrong", epoch, false, p, q[:], false, errInvalidCheckpointPenalties},
		{"pre-TIPSigning, empty", epoch, false, p, nil, false, errInvalidCheckpointPenalties},
		{"pre-TIPSigning uses HookPenalty", epoch, true, p, p[:], false, nil},
		{"TIPSigning uses HookPenaltyTIPSigning", tipEpoch, true, q, q[:], false, nil},
		{"TIPSigning, wrong", tipEpoch, true, q, p[:], false, errInvalidCheckpointPenalties},
		{"pre-TIPSigning, data unavailable", epoch, false, p, p[:], true, nil},
		{"TIPSigning, data unavailable", tipEpoch, true, q, q[:], true, nil},
	}
	for _, tc := range cases {
		if err := verify(tc.number, tc.tipHook, tc.penalized, tc.penalties, tc.unavailable); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}
