// Copyright 2026 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"testing"

	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/node"
)

// TestRegisterEthStatsOptIn checks that ethstats is registered only when
// --ethstats is set, and then with exactly the URL given (#1410).
func TestRegisterEthStatsOptIn(t *testing.T) {
	var calls []string
	orig := registerEthStatsService
	registerEthStatsService = func(_ *node.Node, _ *eth.EthAPIBackend, url string) {
		calls = append(calls, url)
	}
	defer func() { registerEthStatsService = orig }()

	registerEthStats(nil, nil, "")
	if len(calls) != 0 {
		t.Fatalf("ethstats registered without --ethstats: %v", calls)
	}
	const url = "mynode:sekret@stats.example.org:443"
	registerEthStats(nil, nil, url)
	if len(calls) != 1 || calls[0] != url {
		t.Fatalf("explicit --ethstats: have %v, want [%s]", calls, url)
	}
}
