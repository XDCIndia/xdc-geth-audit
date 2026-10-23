package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestProposalsMapBound verifies that AdminAPI.Propose rejects entries once
// the proposals map reaches maxProposals (review R1-R01).
// This test fails on origin/main because AdminAPI did not exist and Propose
// had no error return.
func TestProposalsMapBound(t *testing.T) {
	engine := &XDPoS{
		proposals: make(map[common.Address]bool),
	}
	api := &AdminAPI{xdpos: engine}

	// Fill up to the limit — all should succeed.
	for i := 0; i < maxProposals; i++ {
		addr := common.BigToAddress(big.NewInt(int64(i + 1)))
		if err := api.Propose(addr, true); err != nil {
			t.Fatalf("proposal %d/%d failed unexpectedly: %v", i+1, maxProposals, err)
		}
	}
	if got := len(engine.proposals); got != maxProposals {
		t.Fatalf("expected %d proposals, got %d", maxProposals, got)
	}

	// The next unique address must be rejected.
	overflow := common.BigToAddress(big.NewInt(int64(maxProposals + 1)))
	if err := api.Propose(overflow, true); err == nil {
		t.Fatal("expected error when proposals map is full, got nil")
	}

	// Updating an existing address must still succeed (no new slot consumed).
	existing := common.BigToAddress(big.NewInt(1))
	if err := api.Propose(existing, false); err != nil {
		t.Fatalf("updating existing proposal failed: %v", err)
	}

	// After Discard the slot is freed and a new address can be added.
	api.Discard(existing)
	if err := api.Propose(overflow, true); err != nil {
		t.Fatalf("proposal after discard failed: %v", err)
	}
}

// TestProposalsPublicReadOnly verifies that the read-only Proposals method on
// the public API reflects the current map state.
func TestProposalsPublicReadOnly(t *testing.T) {
	engine := &XDPoS{
		proposals: make(map[common.Address]bool),
	}
	adminAPI := &AdminAPI{xdpos: engine}
	pubAPI := &API{xdpos: engine}

	addr := common.BigToAddress(big.NewInt(42))
	if err := adminAPI.Propose(addr, true); err != nil {
		t.Fatalf("propose failed: %v", err)
	}
	props := pubAPI.Proposals()
	if auth, ok := props[addr]; !ok || !auth {
		t.Fatalf("expected public Proposals to contain %v→true, got %v", addr, props)
	}
}
