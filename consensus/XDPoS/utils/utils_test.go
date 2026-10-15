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

package utils

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

var (
	addr1 = common.HexToAddress("0x1000000000000000000000000000000000000001")
	addr2 = common.HexToAddress("0x1000000000000000000000000000000000000002")
	addr3 = common.HexToAddress("0x1000000000000000000000000000000000000003")
)

// TestValidatorsOrderMatchesSameOrder verifies that identical ordered lists match.
func TestValidatorsOrderMatchesSameOrder(t *testing.T) {
	if !ValidatorsOrderMatches(
		[]common.Address{addr1, addr2, addr3},
		[]common.Address{addr1, addr2, addr3},
	) {
		t.Fatal("same order should match")
	}
}

// TestValidatorsOrderMatchesReordered confirms that a reordered list — accepted
// by CompareSignersLists (sorted set equality) — is detected as an order mismatch
// by ValidatorsOrderMatches (F-2026-19815: epoch-switch leader-rotation risk).
func TestValidatorsOrderMatchesReordered(t *testing.T) {
	// sorted set equality passes for these two lists
	if !CompareSignersLists(
		[]common.Address{addr1, addr2, addr3},
		[]common.Address{addr2, addr1, addr3},
	) {
		t.Fatal("CompareSignersLists should accept a reordered list (sets are equal)")
	}
	// strict order check must detect the reordering
	if ValidatorsOrderMatches(
		[]common.Address{addr1, addr2, addr3},
		[]common.Address{addr2, addr1, addr3},
	) {
		t.Fatal("ValidatorsOrderMatches must reject a reordered list (F-2026-19815)")
	}
}

// TestValidatorsOrderMatchesLengthMismatch verifies length-mismatch returns false.
func TestValidatorsOrderMatchesLengthMismatch(t *testing.T) {
	if ValidatorsOrderMatches(
		[]common.Address{addr1, addr2},
		[]common.Address{addr1},
	) {
		t.Fatal("different lengths should not match")
	}
}

// TestValidatorsOrderMatchesEmpty verifies two empty slices match.
func TestValidatorsOrderMatchesEmpty(t *testing.T) {
	if !ValidatorsOrderMatches(nil, nil) {
		t.Fatal("two nil slices should match")
	}
	if !ValidatorsOrderMatches([]common.Address{}, []common.Address{}) {
		t.Fatal("two empty slices should match")
	}
}
