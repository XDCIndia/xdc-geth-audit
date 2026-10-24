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

//go:build cgo

package privacy

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	golog "github.com/ethereum/go-ethereum/log"
)

// infoCounter is a minimal slog.Handler that counts records at Info or above.
type infoCounter struct{ n atomic.Int32 }

func (c *infoCounter) Enabled(_ context.Context, lvl slog.Level) bool { return lvl >= slog.LevelInfo }
func (c *infoCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelInfo {
		c.n.Add(1)
	}
	return nil
}
func (c *infoCounter) WithAttrs(_ []slog.Attr) slog.Handler { return c }
func (c *infoCounter) WithGroup(_ string) slog.Handler      { return c }

// TestHashPointNoInfoLog verifies that HashPoint emits no Info-level log
// records.  On origin/main HashPoint calls log.Info twice per invocation; one
// ring-signature Verify with a 10×10 ring therefore writes 200 Info lines.
// The fix lowers both calls to log.Trace.
//
// Fails on origin/main: the counter increments.
// Passes with the fix: the counter stays at zero.
func TestHashPointNoInfoLog(t *testing.T) {
	counter := &infoCounter{}
	old := golog.Root()
	golog.SetDefault(golog.NewLogger(counter))
	defer golog.SetDefault(old)

	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("crypto.GenerateKey: %v", err)
	}
	HashPoint(&privKey.PublicKey)

	if n := counter.n.Load(); n > 0 {
		t.Fatalf("HashPoint emitted %d Info-level log record(s); expected 0 after lowering to Trace", n)
	}
}
