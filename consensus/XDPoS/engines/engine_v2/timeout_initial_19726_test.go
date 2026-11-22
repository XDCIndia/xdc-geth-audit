// Copyright (c) 2026 XDC Network

package engine_v2

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// TestF202619726InitialUsesRoundTimeout checks that the first countdown after
// start uses the head round's timeout period when processQC does not run.
func TestF202619726InitialUsesRoundTimeout(t *testing.T) {
	eng, _ := newA894Engine(t)
	t.Cleanup(func() { eng.timeoutWorker.Stop() })
	eng.config.V2.AllConfigs = map[uint64]*params.V2Config{
		0: {
			SwitchRound:      0,
			TimeoutPeriod:    10,
			ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 2, MaxExponent: 6},
		},
		101: {
			SwitchRound:      101,
			TimeoutPeriod:    60,
			ExpTimeoutConfig: params.ExpTimeoutConfig{Base: 2, MaxExponent: 6},
		},
	}
	eng.config.V2.CurrentConfig = eng.config.V2.AllConfigs[0]
	eng.config.V2.BuildConfigIndex()

	head := buildV2Header(t, 5, 101, 0, common.Hash{0x99}, nil)
	chain := &mockChain1330{byNumber: map[uint64]*types.Header{5: head}}
	eng.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{}}
	eng.isInitialized = false

	eng.lock.Lock()
	err := eng.initial(chain, head)
	eng.lock.Unlock()
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	if got := eng.timeoutWorker.BaseDuration(); got != 60*time.Second {
		t.Fatalf("countdown period = %s, want 60s", got)
	}
}
