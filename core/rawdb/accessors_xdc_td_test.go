// Copyright 2026 The XDC Network Authors
// Tests for XDC #1645: cumulative TD storage in rawdb.

package rawdb

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestXDCWriteReadTd verifies that WriteTd/ReadTd round-trip correctly and
// that the key format matches the legacy XDPoSChain schema ("h"+num+"hash"+"t").
// This test compiles only on the fix branch (WriteTd/ReadTd are new). #1645
func TestXDCWriteReadTd(t *testing.T) {
	db := NewMemoryDatabase()
	defer db.Close()

	hash := common.HexToHash("0xdeadbeef")
	number := uint64(12345)
	want := big.NewInt(999999)

	// Nothing stored yet.
	if got := ReadTd(db, hash, number); got != nil {
		t.Fatalf("expected nil before write, got %v", got)
	}

	WriteTd(db, hash, number, want)

	got := ReadTd(db, hash, number)
	if got == nil {
		t.Fatal("ReadTd returned nil after WriteTd")
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("ReadTd: got %v, want %v", got, want)
	}

	// Different hash should still return nil.
	if got2 := ReadTd(db, common.HexToHash("0xcafe"), number); got2 != nil {
		t.Fatalf("ReadTd with different hash should be nil, got %v", got2)
	}
}

// TestXDCTdKeyIsLegacyCompatible verifies the on-disk key used by WriteTd
// matches the canonical XDPoSChain schema: headerPrefix("h") + 8-byte BE
// block number + hash + headerTDSuffix("t"). A legacy node and a modern node
// sharing a LevelDB directory must agree on this key. #1645
func TestXDCTdKeyIsLegacyCompatible(t *testing.T) {
	number := uint64(0x0000000000006d60) // 28,000
	hash := common.HexToHash("0xabcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234")

	key := headerTDKey(number, hash)

	// Must start with "h" (headerPrefix).
	if len(key) == 0 || key[0] != 'h' {
		t.Fatalf("key does not start with headerPrefix 'h': %x", key)
	}
	// Bytes 1-8: big-endian block number.
	var gotNum uint64
	for i := 0; i < 8; i++ {
		gotNum = (gotNum << 8) | uint64(key[1+i])
	}
	if gotNum != number {
		t.Fatalf("key encodes block number %d, want %d", gotNum, number)
	}
	// Bytes 9-40: hash.
	gotHash := common.BytesToHash(key[9:41])
	if gotHash != hash {
		t.Fatalf("key encodes hash %v, want %v", gotHash, hash)
	}
	// Last byte: "t" (headerTDSuffix).
	if key[len(key)-1] != 't' {
		t.Fatalf("key does not end with headerTDSuffix 't': %x", key)
	}
}
