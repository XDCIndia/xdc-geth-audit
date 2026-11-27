package engine_v2

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// A signature that fails Ecrecover must be skipped, not sliced as a nil key (#2108).
func TestGetQcSignerAddressesSkipsBadSignature(t *testing.T) {
	qc := types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Number: big.NewInt(1)},
		Signatures:        []types.Signature{make([]byte, 65)}, // all-zero signature: not recoverable
	}
	if got := NewForensics().getQcSignerAddresses(qc); len(got) != 0 {
		t.Fatalf("want no signers for an unrecoverable signature, got %v", got)
	}
}

// A panic inside a forensics goroutine must not reach the runtime (#2108).
func TestForensicsEntryPointsRecoverFromPanic(t *testing.T) {
	f := NewForensics()
	f.DetectEquivocationInVotePool(&types.Vote{}, nil)           // nil pool panics
	_ = f.ProcessVoteEquivocation(nil, nil, nil)                 // nil vote panics
	_ = f.ForensicsMonitoring(nil, nil, nil, types.QuorumCert{}) // nil QC info panics
}
