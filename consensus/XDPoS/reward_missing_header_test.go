// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// rewardGapChain is a BlockReader that serves a chain of linked headers up to a
// certain block number, returning nil for any block at or below gapAt.
type rewardGapChain struct {
	cfg     *params.ChainConfig
	headers map[uint64]*types.Header // number -> header
}

func (r *rewardGapChain) Config() *params.ChainConfig               { return r.cfg }
func (r *rewardGapChain) CurrentHeader() *types.Header              { return nil }
func (r *rewardGapChain) GetHeaderByHash(common.Hash) *types.Header { return nil }
func (r *rewardGapChain) GetHeaderByNumber(n uint64) *types.Header  { return r.headers[n] }
func (r *rewardGapChain) GetHeader(hash common.Hash, n uint64) *types.Header {
	h, ok := r.headers[n]
	if !ok {
		return nil
	}
	if h.Hash() == hash {
		return h
	}
	return nil
}

// TestGetRewardForCheckpointMissingHeaderReturnsError verifies that when a
// header is unavailable in the reward window, GetRewardForCheckpoint returns
// an error rather than silently returning partial signer counts.
//
// Before the fix the code hit a silent break and returned (partialMap, nil);
// after the fix it returns (nil, error).
func TestGetRewardForCheckpointMissingHeaderReturnsError(t *testing.T) {
	cfg := *testConfig
	epoch := uint64(10)
	cfg.Epoch = epoch
	cfg.RewardCheckpoint = epoch

	engine := New(&cfg, rawdb.NewMemoryDatabase())

	// Build a chain of headers linked by parent hash.
	// rCheckpoint = epoch = 10, so we need blocks from (number - 2*epoch + 1)
	// to (number - 1).  With number = 20 that is blocks 1..19.
	//
	// We deliberately omit block 5 so the parent-hash walk hits a gap.
	number := uint64(20)
	headers := make(map[uint64]*types.Header)
	var prevHash common.Hash
	for n := uint64(1); n < number; n++ {
		if n == 5 {
			// Skip block 5 – simulates a pruned / not-yet-synced header.
			// Advance prevHash by fabricating a hash so that block 6 has a
			// parent hash that the map doesn't serve.
			prevHash = common.BytesToHash([]byte{byte(n)})
			continue
		}
		h := &types.Header{
			Number:     new(big.Int).SetUint64(n),
			ParentHash: prevHash,
			Difficulty: big.NewInt(1),
		}
		headers[n] = h
		prevHash = h.Hash()
	}
	// Checkpoint header (number = 20)
	checkpoint := &types.Header{
		Number:     new(big.Int).SetUint64(number),
		ParentHash: prevHash,
		Difficulty: big.NewInt(1),
	}

	chain := &rewardGapChain{
		cfg:     &params.ChainConfig{ChainID: big.NewInt(50), XDPoS: &cfg},
		headers: headers,
	}

	var totalSigner uint64
	_, err := engine.GetRewardForCheckpoint(chain, checkpoint, epoch, &totalSigner)
	if err == nil {
		t.Fatal("expected an error when a header is missing in the reward window, got nil")
	}
}
