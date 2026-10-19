package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// m1Recorder records the V1 gap-block refreshes the chain asks for.
type m1Recorder struct {
	consensus.Engine
	calls      []uint64
	candidates []common.Address
}

func (r *m1Recorder) UpdateMasternodesV1(_ consensus.ChainHeaderReader, header *types.Header, statedb *state.StateDB) error {
	r.calls = append(r.calls, header.Number.Uint64())
	r.candidates = state.GetCandidates(statedb)
	return nil
}

// TestWriteBlockWithStateUpdatesM1AtV1Gap: writing a V1 gap block hands the
// engine that block's committed state, like canonical BlockChain.UpdateM1At;
// non-gap blocks and blocks past the V2 switch are left alone.
func TestWriteBlockWithStateUpdatesM1AtV1Gap(t *testing.T) {
	cand := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	for _, tc := range []struct {
		name   string
		xdpos  *params.XDPoSConfig
		called bool
	}{
		// Block 1 is the gap block when Epoch-Gap == 1.
		{"v1 gap block", &params.XDPoSConfig{Epoch: 10, Gap: 9}, true},
		{"v1 non-gap block", &params.XDPoSConfig{Epoch: 10, Gap: 8}, false},
		{"v2 gap block", &params.XDPoSConfig{Epoch: 10, Gap: 9, V2: &params.V2{SwitchBlock: big.NewInt(0)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, chain, err := newCanonical(ethash.NewFaker(), 0, true, rawdb.HashScheme)
			if err != nil {
				t.Fatal(err)
			}
			defer chain.Stop()
			cfg := *chain.chainConfig
			cfg.XDPoS = tc.xdpos
			chain.chainConfig = &cfg
			rec := &m1Recorder{Engine: chain.engine}
			chain.engine = rec

			genesis := chain.CurrentBlock()
			statedb, err := chain.StateAt(genesis.Root, genesis.Number, genesis.Time)
			if err != nil {
				t.Fatal(err)
			}
			statedb.SetNonce(common.MasternodeVotingSMCBinary, 1, tracing.NonceChangeUnspecified) // non-empty, so the commit keeps it
			slot8 := common.BigToHash(big.NewInt(8))
			statedb.SetState(common.MasternodeVotingSMCBinary, slot8, common.BigToHash(big.NewInt(1)))
			statedb.SetState(common.MasternodeVotingSMCBinary, state.GetLocDynamicArrAtElement(slot8, 0, 1), common.BytesToHash(cand.Bytes()))

			block := types.NewBlockWithHeader(&types.Header{
				ParentHash: genesis.Hash(),
				Number:     big.NewInt(1),
				Difficulty: big.NewInt(1),
				Time:       genesis.Time + 1,
			})
			if err := chain.writeBlockWithState(block, nil, statedb); err != nil {
				t.Fatal(err)
			}
			if got := len(rec.calls) == 1; got != tc.called {
				t.Fatalf("refresh calls %v, want called=%v", rec.calls, tc.called)
			}
			if tc.called && (len(rec.candidates) != 1 || rec.candidates[0] != cand) {
				t.Fatalf("refresh saw candidates %v, want [%x] from the committed state", rec.candidates, cand)
			}
		})
	}
}
