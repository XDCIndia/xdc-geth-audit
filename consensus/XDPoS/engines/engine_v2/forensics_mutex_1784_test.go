// Copyright 2026 XDC Network

package engine_v2

// TestForensicsQCRace exposes the data race on Forensics.HighestCommittedQCs.
//
// ForensicsMonitoring spawns goroutines that call SetCommittedQCs (writer)
// and VoteHandler spawns goroutines that call ProcessVoteEquivocation (reader).
// Both access f.HighestCommittedQCs without any synchronisation pre-fix.
//
// Under -race the test fails on origin/main and passes after adding
// sync.RWMutex to Forensics and guarding all HighestCommittedQCs accesses.
//
// Related: issue #1784, review R2-W02.

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// makeQC builds a minimal QuorumCert pointing at the given hash/round.
func makeQC(hash common.Hash, round types.Round, num int64) types.QuorumCert {
	return types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   hash,
			Round:  round,
			Number: big.NewInt(num),
		},
		GapNumber: 450,
	}
}

// buildForensicsHeaders constructs the two headers and an incomingQC that
// SetCommittedQCs will accept without error.
//
// Layout (SetCommittedQCs validation rules):
//
//	headers[0]: any valid V2 extra with a QC
//	headers[1]: QC must point to headers[0].Hash(); its own hash == incomingQC.ProposedBlockInfo.Hash
func buildForensicsHeaders(t *testing.T) ([]types.Header, types.QuorumCert) {
	t.Helper()

	// headers[0] — arbitrary QC inside; its own hash is used by headers[1]'s QC.
	qc0 := makeQC(common.Hash{0xaa}, 1, 900)
	extra0, err := (&types.ExtraFields_v2{Round: 1, QuorumCert: &qc0}).EncodeToBytes()
	if err != nil {
		t.Fatal("failed to encode extra0:", err)
	}
	h0 := types.Header{
		Number:     big.NewInt(901),
		Difficulty: big.NewInt(1),
		Extra:      extra0,
	}

	// headers[1] — its QC must point to h0.Hash().
	qc1 := makeQC(h0.Hash(), 1, 901)
	extra1, err := (&types.ExtraFields_v2{Round: 2, QuorumCert: &qc1}).EncodeToBytes()
	if err != nil {
		t.Fatal("failed to encode extra1:", err)
	}
	h1 := types.Header{
		Number:     big.NewInt(902),
		ParentHash: h0.Hash(),
		Difficulty: big.NewInt(1),
		Extra:      extra1,
	}

	// incomingQC must point to h1.Hash().
	incoming := makeQC(h1.Hash(), 2, 902)
	return []types.Header{h0, h1}, incoming
}

func TestForensicsQCRace(t *testing.T) {
	f := NewForensics()

	// Seed with a 2-element slice (wrong length) so that concurrent readers
	// calling ProcessVoteEquivocation fail the length check and return early
	// without needing a chain or engine.  The race is on the slice-header
	// read/write between the goroutines, not on what they do afterwards.
	f.HighestCommittedQCs = []types.QuorumCert{
		makeQC(common.Hash{1}, 1, 1),
		makeQC(common.Hash{2}, 2, 2),
	}

	headers, incomingQC := buildForensicsHeaders(t)

	// minimalVote for ProcessVoteEquivocation.  Round 0 is always < the
	// QCs written by SetCommittedQCs (rounds 1, 2, 2), so once the slice
	// has 3 elements the "too old vote" guard fires and the function returns
	// without touching the chain.  When the slice has 2 elements it returns
	// from the length check.  Neither path reaches a chain call.
	vote := &types.Vote{
		ProposedBlockInfo: &types.BlockInfo{
			Hash:   common.Hash{0xff},
			Round:  0,
			Number: big.NewInt(1),
		},
		GapNumber: 450,
	}

	const iters = 500
	var wg sync.WaitGroup
	for i := 0; i < iters; i++ {
		wg.Add(2)

		// Reader goroutine: calls ProcessVoteEquivocation which reads
		// f.HighestCommittedQCs at its first statement (pre-fix: no lock).
		go func() {
			defer wg.Done()
			// Ignore the error — we only care about the memory access race.
			_ = f.ProcessVoteEquivocation(nil, nil, vote)
		}()

		// Writer goroutine: calls SetCommittedQCs which writes
		// f.HighestCommittedQCs at its last statement (pre-fix: no lock).
		go func() {
			defer wg.Done()
			_ = f.SetCommittedQCs(headers, incomingQC)
		}()
	}
	wg.Wait()
}
