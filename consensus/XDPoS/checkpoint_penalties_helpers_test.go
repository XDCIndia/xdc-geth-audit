package XDPoS

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// Helpers and tests in this file were copied from checkpoint_penalties_test.go because that file is not in the audit tree.

// cpPenaltiesChain serves only the chain config; parents come from the batch.
type cpPenaltiesChain struct{ cfg *params.ChainConfig }

func (c cpPenaltiesChain) Config() *params.ChainConfig { return c.cfg }

func (c cpPenaltiesChain) CurrentHeader() *types.Header { return nil }

func (c cpPenaltiesChain) GetHeader(common.Hash, uint64) *types.Header { return nil }

func (c cpPenaltiesChain) GetHeaderByNumber(uint64) *types.Header { return nil }

func (c cpPenaltiesChain) GetHeaderByHash(common.Hash) *types.Header { return nil }
