package XDPoS

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// Helpers and tests in this file were copied from pre_tx_state_reward_test.go because that file is not in the audit tree.

// preTxChain satisfies consensus.ChainHeaderReader for this test.
type preTxChain struct{ cfg *params.ChainConfig }

func (c preTxChain) Config() *params.ChainConfig { return c.cfg }

func (c preTxChain) CurrentHeader() *types.Header { return nil }

func (c preTxChain) GetHeader(common.Hash, uint64) *types.Header { return nil }

func (c preTxChain) GetHeaderByNumber(uint64) *types.Header { return nil }

func (c preTxChain) GetHeaderByHash(common.Hash) *types.Header { return nil }
