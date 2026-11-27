// Copyright (c) 2026 XDC Network
// F-2026-19639: the V1 branch of GetBlockInfoByEpochNum follows the epoch
// checkpoint convention. This test is the V1 test of api_epoch_rpc_19639_test.go
// in the source tree; that file is not in the audit tree, so it is a new file here.
//
// Run: go test ./consensus/XDPoS/ -run 'F19639' -count=1

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// TestF19639V1BlockInfoMatchesCheckpoint checks that a V1 epoch maps to the
// block internal/ethapi GetCheckpointFromEpoch reads (canonical convention:
// 0 below epoch 2, else Epoch*(epoch-1)).
func TestF19639V1BlockInfoMatchesCheckpoint(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	engine.EngineV2 = nil // force the V1 branch
	api := makeTestAPI(engine, &stubChain{currentHeader: &types.Header{Number: big.NewInt(100)}})
	epoch := engine.config.Epoch

	for _, tc := range []struct{ epochNum, first uint64 }{{0, 0}, {1, 0}, {2, epoch}, {5, 4 * epoch}} {
		info, err := api.GetBlockInfoByEpochNum(tc.epochNum)
		if err != nil {
			t.Fatal(err)
		}
		if info.EpochFirstBlockNumber.Uint64() != tc.first {
			t.Fatalf("epoch %d: first block %d, want %d", tc.epochNum, info.EpochFirstBlockNumber, tc.first)
		}
	}
}
