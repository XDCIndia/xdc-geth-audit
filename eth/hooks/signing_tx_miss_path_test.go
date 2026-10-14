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

package hooks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestSigningTxCountMissPathsUseCacheSigner guards #1364/#1366: on a signing-tx
// cache miss both reward counters must resolve the block's signing txs through
// resolveMissedSigningTxs (CacheSigner), never through rawdb.ReadRawReceipts +
// CacheData. Raw receipts carry no TxHash, so that path drops every signing tx,
// pays no rewards and wedges the node on "invalid merkle root".
func TestSigningTxCountMissPathsUseCacheSigner(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "engine_v2_hooks.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GetSigningTxCount", "GetSigningTxCountTiered"} {
		var fn *ast.FuncDecl
		for _, d := range file.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Recv == nil && f.Name.Name == name {
				fn = f
			}
		}
		if fn == nil {
			t.Fatalf("%s not found in engine_v2_hooks.go", name)
		}
		calls := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch f := call.Fun.(type) {
				case *ast.Ident:
					calls[f.Name] = true
				case *ast.SelectorExpr:
					calls[f.Sel.Name] = true
				}
			}
			return true
		})
		if !calls["resolveMissedSigningTxs"] {
			t.Errorf("%s does not resolve signing-tx cache misses through resolveMissedSigningTxs", name)
		}
		for _, banned := range []string{"ReadRawReceipts", "CacheData"} {
			if calls[banned] {
				t.Errorf("%s calls %s: raw receipts carry no TxHash, every signing tx is dropped (#1364/#1366)", name, banned)
			}
		}
	}
}
