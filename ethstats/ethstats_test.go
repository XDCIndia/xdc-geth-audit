// Copyright 2021 The go-ethereum Authors
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

package ethstats

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestParseEthstatsURL(t *testing.T) {
	cases := []struct {
		url              string
		node, pass, host string
	}{
		{
			url:  `"debug meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `"debug @meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug @meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `"debug: @meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug: @meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `name:@ws://mordor.dash.fault.dev:3000`,
			node: "name", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `name@ws://mordor.dash.fault.dev:3000`,
			node: "name", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `:mypass@ws://mordor.dash.fault.dev:3000`,
			node: "", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `:@ws://mordor.dash.fault.dev:3000`,
			node: "", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
	}

	for i, c := range cases {
		parts, err := parseEthstatsURL(c.url)
		if err != nil {
			t.Fatal(err)
		}
		node, pass, host := parts[0], parts[1], parts[2]

		// unquote because the value provided will be used as a CLI flag value, so unescaped quotes will be removed
		nodeUnquote, err := strconv.Unquote(node)
		if err == nil {
			node = nodeUnquote
		}

		if node != c.node {
			t.Errorf("case=%d mismatch node value, got: %v ,want: %v", i, node, c.node)
		}
		if pass != c.pass {
			t.Errorf("case=%d mismatch pass value, got: %v ,want: %v", i, pass, c.pass)
		}
		if host != c.host {
			t.Errorf("case=%d mismatch host value, got: %v ,want: %v", i, host, c.host)
		}
	}
}

type fakeSigner struct {
	addr   common.Address
	active bool
}

func (f fakeSigner) Signer() (common.Address, bool) { return f.addr, f.active }

func TestCoinbaseAndRole(t *testing.T) {
	realAddr := common.HexToAddress("0xAbCdEf1234567890aBcDeF1234567890ABcdEf12")
	zeroAddr := strings.ToLower((common.Address{}).Hex())
	cases := []struct {
		name         string
		engine       any
		wantCoinbase string
		wantRole     string
	}{
		{"no-provider", struct{}{}, zeroAddr, "follower"},
		{"not-active", fakeSigner{addr: realAddr, active: false}, zeroAddr, "follower"},
		{"active-zero-address", fakeSigner{addr: common.Address{}, active: true}, zeroAddr, "follower"},
		{"active-real-address", fakeSigner{addr: realAddr, active: true}, strings.ToLower(realAddr.Hex()), "validator"},
	}
	for _, c := range cases {
		gotCoinbase, gotRole := coinbaseAndRole(c.engine)
		if gotCoinbase != c.wantCoinbase || gotRole != c.wantRole {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, gotCoinbase, gotRole, c.wantCoinbase, c.wantRole)
		}
	}
}

func TestNodeInfoJSON(t *testing.T) {
	// Test that nodeInfo correctly serializes the sync mode, gc mode, and state scheme fields
	cases := []struct {
		name       string
		info       nodeInfo
		wantKeys   []string
		wantValues map[string]string
	}{
		{
			name: "snap-sync-full-gc-path-scheme",
			info: nodeInfo{
				Name:        "test-node",
				Node:        "geth/v1.0",
				Port:        30303,
				Network:     "1",
				Protocol:    "eth/68",
				API:         "No",
				Os:          "linux",
				OsVer:       "amd64",
				Client:      "0.1.1",
				History:     true,
				Coinbase:    "0x1234567890123456789012345678901234567890",
				Role:        "follower",
				SyncMode:    "snap",
				GcMode:      "full",
				StateScheme: "path",
			},
			wantKeys: []string{"name", "node", "port", "net", "protocol", "api", "os", "os_v", "client", "canUpdateHistory", "coinbase", "role", "syncMode", "gcMode", "stateScheme"},
			wantValues: map[string]string{
				"syncMode":    "snap",
				"gcMode":      "full",
				"stateScheme": "path",
			},
		},
		{
			name: "full-sync-archive-gc-hash-scheme",
			info: nodeInfo{
				Name:        "test-node",
				Node:        "geth/v1.0",
				Port:        30303,
				Network:     "1",
				Protocol:    "eth/68",
				API:         "No",
				Os:          "linux",
				OsVer:       "amd64",
				Client:      "0.1.1",
				History:     true,
				Coinbase:    "0x0000000000000000000000000000000000000000",
				Role:        "validator",
				SyncMode:    "full",
				GcMode:      "archive",
				StateScheme: "hash",
			},
			wantKeys: []string{"name", "node", "port", "net", "protocol", "api", "os", "os_v", "client", "canUpdateHistory", "coinbase", "role", "syncMode", "gcMode", "stateScheme"},
			wantValues: map[string]string{
				"syncMode":    "full",
				"gcMode":      "archive",
				"stateScheme": "hash",
			},
		},
		{
			name: "fast-sync-empty-values",
			info: nodeInfo{
				Name:        "test-node",
				Node:        "geth/v1.0",
				Port:        30303,
				Network:     "1",
				Protocol:    "eth/68",
				API:         "No",
				Os:          "linux",
				OsVer:       "amd64",
				Client:      "0.1.1",
				History:     true,
				Coinbase:    "0x0000000000000000000000000000000000000000",
				Role:        "follower",
				SyncMode:    "fast",
				GcMode:      "",
				StateScheme: "",
			},
			wantKeys: []string{"name", "node", "port", "net", "protocol", "api", "os", "os_v", "client", "canUpdateHistory", "coinbase", "role", "syncMode", "gcMode", "stateScheme"},
			wantValues: map[string]string{
				"syncMode": "fast",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Marshal nodeInfo to JSON
			data, err := json.Marshal(tc.info)
			if err != nil {
				t.Fatalf("Failed to marshal nodeInfo: %v", err)
			}

			// Unmarshal back to check fields
			var result map[string]interface{}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			// Check that all expected keys are present
			for _, key := range tc.wantKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("Missing expected key: %s", key)
				}
			}

			// Check specific values
			for key, wantVal := range tc.wantValues {
				gotVal, ok := result[key]
				if !ok {
					t.Errorf("Key %s not found in JSON", key)
					continue
				}
				if gotStr := gotVal.(string); gotStr != wantVal {
					t.Errorf("Key %s: got %q, want %q", key, gotStr, wantVal)
				}
			}
		})
	}
}
