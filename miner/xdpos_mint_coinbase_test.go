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

package miner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestXDPoSMintEnvUsesSignerCoinbase guards #1383/#1385: every XDPoS mint path
// must build its EVM env with the signer (w.coinbase), never header.Coinbase.
// V1 Prepare zeroes header.Coinbase while InsertChain re-execution credits
// Engine().Author (the signer), so passing header.Coinbase makes the minter
// reject its own first sign-tx block with "invalid merkle root".
func TestXDPoSMintEnvUsesSignerCoinbase(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "xdpos_worker.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "makeEnv" || len(call.Args) < 3 {
			return true
		}
		calls++
		arg, ok := call.Args[2].(*ast.SelectorExpr)
		if !ok || arg.Sel.Name != "coinbase" {
			t.Errorf("makeEnv coinbase arg is not w.coinbase: %#v", call.Args[2])
		} else if x, ok := arg.X.(*ast.Ident); !ok || x.Name != "w" {
			t.Errorf("makeEnv coinbase arg is not w.coinbase (receiver %#v)", arg.X)
		}
		return true
	})
	if calls != 2 {
		t.Fatalf("expected 2 makeEnv calls (V1 + V2 mint paths), found %d", calls)
	}
}
