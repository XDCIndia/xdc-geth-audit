package XDPoS

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// Helpers and tests in this file were copied from checkpoint_signers_test.go because that file is not in the audit tree.

// cpSignersChain serves only the chain config; parents come from the batch.
type cpSignersChain struct{ cfg *params.ChainConfig }

func (c cpSignersChain) Config() *params.ChainConfig { return c.cfg }

func (c cpSignersChain) CurrentHeader() *types.Header { return nil }

func (c cpSignersChain) GetHeader(common.Hash, uint64) *types.Header { return nil }

func (c cpSignersChain) GetHeaderByNumber(uint64) *types.Header { return nil }

func (c cpSignersChain) GetHeaderByHash(common.Hash) *types.Header { return nil }
