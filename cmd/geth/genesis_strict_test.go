// Copyright 2024 The go-ethereum Authors
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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenesisInitUnknownConfigKey verifies that `geth init` fails when the
// genesis "config" object contains an unknown key.
//
// On origin/main (no strict check), init succeeds → ExitStatus()==0 → test FAILS.
// On branch, DecodeGenesisJSON rejects the unknown key → exit 1 → test PASSES.
// Compiles on origin/main (uses only existing test harness symbols).
func TestGenesisInitUnknownConfigKey(t *testing.T) {
	t.Parallel()
	const genesis = `{
		"config": {
			"chainId": 999,
			"homesteadBlock": 0,
			"eip150Block": 0,
			"eip155Block": 0,
			"eip158Block": 0,
			"byzantiumBlock": 0,
			"constantinopleBlock": 0,
			"petersburgBlock": 0,
			"istanbulBlock": 0,
			"unknownFutureKey": 42
		},
		"difficulty": "0x1",
		"gasLimit": "0x5208",
		"alloc": {}
	}`
	datadir := t.TempDir()
	file := filepath.Join(datadir, "genesis.json")
	if err := os.WriteFile(file, []byte(genesis), 0600); err != nil {
		t.Fatal(err)
	}
	geth := runGeth(t, "--datadir", datadir, "init", file)
	geth.WaitExit()
	if geth.ExitStatus() == 0 {
		t.Fatal("geth init expected to fail on unknown config key, but succeeded")
	}
	if !strings.Contains(geth.StderrText(), "unknown chain config key") {
		t.Fatalf("expected 'unknown chain config key' in stderr, got:\n%s", geth.StderrText())
	}
}

// TestGenesisInitValidConfig verifies that `geth init` succeeds for a
// genesis whose "config" contains only known keys (regression guard).
func TestGenesisInitValidConfig(t *testing.T) {
	t.Parallel()
	const genesis = `{
		"config": {
			"chainId": 999,
			"homesteadBlock": 0,
			"eip150Block": 0,
			"eip155Block": 0,
			"eip158Block": 0,
			"byzantiumBlock": 0,
			"constantinopleBlock": 0,
			"petersburgBlock": 0,
			"istanbulBlock": 0
		},
		"difficulty": "0x1",
		"gasLimit": "0x5208",
		"alloc": {}
	}`
	datadir := t.TempDir()
	file := filepath.Join(datadir, "genesis.json")
	if err := os.WriteFile(file, []byte(genesis), 0600); err != nil {
		t.Fatal(err)
	}
	geth := runGeth(t, "--datadir", datadir, "init", file)
	geth.WaitExit()
	if geth.ExitStatus() != 0 {
		t.Fatalf("geth init expected success, got exit %d\nstderr: %s", geth.ExitStatus(), geth.StderrText())
	}
}
