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
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomic checks XDC-HUNT-41: the reward file is written through a
// temporary file and a rename. A cut-short earlier file is replaced as a whole,
// no temporary file is left, and a failed write leaves the old file alone.
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1800.0xabc")
	if err := os.WriteFile(path, []byte(`{"cut sho`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := `{"complete":true}`
	if err := WriteFileAtomic(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file = %q, %v, want %q", got, err, want)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, %v, want 0644", fi.Mode(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the reward file", len(entries))
	}
	// A directory that does not exist fails and creates nothing.
	if err := WriteFileAtomic(filepath.Join(dir, "missing", "x"), []byte("x"), 0o644); err == nil {
		t.Fatal("write into a missing directory returned no error")
	}
}
