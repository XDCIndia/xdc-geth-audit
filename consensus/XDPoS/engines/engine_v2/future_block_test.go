// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package engine_v2

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestVerifyHeaderFutureBlockBulkSync: a V2 header dated in the future must be
// rejected even when fullVerify is false (bulk sync / checkpoint-no-state).
func TestVerifyHeaderFutureBlockBulkSync(t *testing.T) {
	x := &XDPoS_v2{
		isInitialized:   true,
		verifiedHeaders: lru.NewCache[common.Hash, struct{}](8),
	}
	// The in-batch parent lets the pre-fix code fall through to later checks
	// with a clean error instead of a nil chain lookup.
	parent := &types.Header{Number: big.NewInt(99)}
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     big.NewInt(100),
		Time:       uint64(time.Now().Add(time.Hour).Unix()),
		Validator:  make([]byte, 65),
	}
	err := x.verifyHeader(nil, header, []*types.Header{parent}, false)
	if !errors.Is(err, consensus.ErrFutureBlock) {
		t.Fatalf("bulk-sync verifyHeader of future header: got %v, want %v", err, consensus.ErrFutureBlock)
	}
}
