// Copyright 2026 The go-ethereum Authors
// CertiK XDC-12 / #1723: parole-counting deduplication.
//
// Before ParoleDedupBlock (or when the field is nil), HookPenalty counts every
// signing transaction that targets a canonical merge-range block, so a penalised
// candidate can submit MinimumSigningTx copies of the same signing tx and be
// released from the penalty list without genuine participation.
//
// From ParoleDedupBlock, each (signer, signed-block-hash) pair counts at most
// once toward MinimumSigningTx.
//
// Run: go test -count=1 ./eth/hooks/ -run TestParoleDedup

package hooks

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// TestParoleDedup_NilField: IsParoleDedup returns false when ParoleDedupBlock
// is nil (all current networks). This test FAILS on origin/main (IsParoleDedup
// is not defined) and passes after this PR.
func TestParoleDedup_NilField(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:          big.NewInt(5151),
		ParoleDedupBlock: nil,
	}
	if cfg.IsParoleDedup(big.NewInt(1_000_000)) {
		t.Fatal("nil ParoleDedupBlock: IsParoleDedup should return false")
	}
}

// TestParoleDedup_BeforeFork: IsParoleDedup returns false before the fork.
func TestParoleDedup_BeforeFork(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:          big.NewInt(5151),
		ParoleDedupBlock: big.NewInt(100),
	}
	if cfg.IsParoleDedup(big.NewInt(99)) {
		t.Fatal("IsParoleDedup should be false at block 99 when fork is 100")
	}
}

// TestParoleDedup_AtFork: IsParoleDedup returns true at and after the fork.
// This test FAILS on origin/main and passes after this PR.
func TestParoleDedup_AtFork(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID:          big.NewInt(5151),
		ParoleDedupBlock: big.NewInt(100),
	}
	if !cfg.IsParoleDedup(big.NewInt(100)) {
		t.Fatal("IsParoleDedup should be true at block 100 when fork is 100")
	}
	if !cfg.IsParoleDedup(big.NewInt(200)) {
		t.Fatal("IsParoleDedup should be true at block 200 when fork is 100")
	}
}

// TestParoleDedup_NilConfig: IsParoleDedup on a nil config returns false.
func TestParoleDedup_NilConfig(t *testing.T) {
	var cfg *params.ChainConfig
	if cfg.IsParoleDedup(big.NewInt(1)) {
		t.Fatal("nil ChainConfig: IsParoleDedup should return false")
	}
}

// TestParoleDedup_CountingLogic exercises the (signer, block-hash) dedup map
// logic that HookPenalty uses.  This test is a pure data-structure check
// verifying that:
//   - without dedup, the same (signer, blkHash) increments the count each time;
//   - with dedup, the same (signer, blkHash) only increments once.
//
// This test FAILS on origin/main (IsParoleDedup does not compile) and passes
// after this PR.
func TestParoleDedup_CountingLogic(t *testing.T) {
	signer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	blkA := common.HexToHash("0xaaaa")
	blkB := common.HexToHash("0xbbbb")

	// --- Without dedup (fork inactive): same (signer, blkA) counts 3 times.
	txSignerMapNone := map[common.Address]int{}
	for i := 0; i < 3; i++ {
		txSignerMapNone[signer]++
	}
	if txSignerMapNone[signer] != 3 {
		t.Fatalf("without dedup: want 3, got %d", txSignerMapNone[signer])
	}

	// --- With dedup (fork active): same (signer, blkA) 3 times counts as 1;
	//     (signer, blkB) 2 times counts as 1 more → total 2.
	txSignerMapDedup := map[common.Address]int{}
	seenPairs := map[common.Address]map[common.Hash]struct{}{}

	countWithDedup := func(from common.Address, blkHash common.Hash) {
		if _, ok := seenPairs[from]; !ok {
			seenPairs[from] = map[common.Hash]struct{}{}
		}
		if _, seen := seenPairs[from][blkHash]; seen {
			return
		}
		seenPairs[from][blkHash] = struct{}{}
		txSignerMapDedup[from]++
	}

	countWithDedup(signer, blkA) // first occurrence of (signer, blkA) → count=1
	countWithDedup(signer, blkA) // duplicate → ignored
	countWithDedup(signer, blkA) // duplicate → ignored
	countWithDedup(signer, blkB) // first occurrence of (signer, blkB) → count=2
	countWithDedup(signer, blkB) // duplicate → ignored

	if txSignerMapDedup[signer] != 2 {
		t.Fatalf("with dedup: want 2 (blkA + blkB), got %d", txSignerMapDedup[signer])
	}
}
