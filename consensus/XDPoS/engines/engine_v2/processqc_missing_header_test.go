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
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestProcessQCMissingProposedHeader: a QC whose proposed block is unknown must
// return an error (canonical XDPoSChain engine.go processQC), not nil.
func TestProcessQCMissingProposedHeader(t *testing.T) {
	x, _ := newA894Engine(t)
	x.highestQuorumCert = &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Round: 1, Number: big.NewInt(1)}}
	x.currentRound = 2

	qc := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{
		Hash:   common.Hash{0xab},
		Round:  10,
		Number: big.NewInt(10),
	}}
	if err := x.processQC(&mockChainForA894{}, qc); err == nil {
		t.Fatal("processQC with an unknown proposed block returned nil, want an error")
	}
	if x.highestQuorumCert != qc {
		t.Error("highestQC was not updated")
	}
	if x.currentRound != 2 {
		t.Errorf("round advanced to %d, want 2 (canonical does not advance)", x.currentRound)
	}
}
