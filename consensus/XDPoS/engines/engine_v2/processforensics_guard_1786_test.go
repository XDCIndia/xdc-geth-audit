// Copyright 2026 XDC Network

package engine_v2

// TestProcessForensicsStopsOnAncestorError verifies that ProcessForensics does
// not call SendForensicProof (and therefore emits no forensics event) when
// findAncestorQcThroughRound returns an error due to a missing header.
//
// Before the fix (origin/main), ProcessForensics logs the error but continues
// to call SendForensicProof with the zero-value ancestorQC, which can fire a
// spurious forensics event.  After the fix it returns the error immediately.
//
// The test subscribes to the forensics event feed and asserts that no event
// arrives within a short window after ProcessForensics returns.
//
// Chain setup:
//   - Committed QCs (rounds 1,2,3) and incoming QCs (rounds 5,6,7) have the
//     same block numbers, so checkQCsOnTheSameChain does 0 walk-back iterations
//     and concludes the two sets diverge (different hashes at equal height).
//   - findQCsInSameRound finds no common round.
//   - findAncestorQcThroughRound starts from the lowest incoming QC (qc25, h25)
//     and calls GetHeaderByHash(h25). h25 is NOT in the chain map, so it
//     returns an error — the bug site (line 124-128 in forensics.go pre-fix).
//
// Related: issue #1786, review R2-W04.

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
)

// forensicsTestChain answers GetHeaderByHash lookups from a fixed map and
// returns nil/nil/zero for everything else.
type forensicsTestChain struct {
	headers map[common.Hash]*types.Header
}

func (c *forensicsTestChain) Config() *params.ChainConfig                     { return nil }
func (c *forensicsTestChain) CurrentHeader() *types.Header                    { return nil }
func (c *forensicsTestChain) GetHeader(_ common.Hash, _ uint64) *types.Header { return nil }
func (c *forensicsTestChain) GetHeaderByNumber(_ uint64) *types.Header        { return nil }
func (c *forensicsTestChain) GetBlock(_ common.Hash, _ uint64) *types.Block   { return nil }
func (c *forensicsTestChain) GetTd(_ common.Hash, _ uint64) *big.Int          { return nil }
func (c *forensicsTestChain) GetHeaderByHash(h common.Hash) *types.Header {
	return c.headers[h]
}

// encodeV2ExtraForTest encodes an ExtraFields_v2 into the format expected by
// utils.DecodeBytesExtraFields: a version byte (0x02) followed by the RLP body.
func encodeV2ExtraForTest(round types.Round, qc *types.QuorumCert) ([]byte, error) {
	extra := &types.ExtraFields_v2{Round: round, QuorumCert: qc}
	body, err := rlp.EncodeToBytes(extra)
	if err != nil {
		return nil, err
	}
	return append([]byte{2}, body...), nil
}

func TestProcessForensicsStopsOnAncestorError(t *testing.T) {
	f := NewForensics()

	// Subscribe to forensics events before calling ProcessForensics so we can
	// detect any spurious event sent by the pre-fix code path.
	ch := make(chan types.ForensicsEvent, 4)
	_ = f.SubscribeForensicsEvent(ch)

	bi := func(hash common.Hash, round types.Round, num int64) *types.BlockInfo {
		return &types.BlockInfo{Hash: hash, Round: round, Number: big.NewInt(num)}
	}
	qcOf := func(hash common.Hash, round types.Round, num int64) types.QuorumCert {
		return types.QuorumCert{ProposedBlockInfo: bi(hash, round, num), GapNumber: 450}
	}

	// Incoming QC chain: rounds 5, 6, 7 at block numbers 905, 906, 907.
	//
	// findAncestorQCs walks two steps back from incomingQC (h27, round 7):
	//   GetHeaderByHash(h27) → header27 whose Extra holds qc26
	//   GetHeaderByHash(h26) → header26 whose Extra holds qc25
	// Result: incomingQuorunCerts = [qc25(h25,5,905), qc26(h26,6,906), qc(h27,7,907)]
	//
	// h25 is intentionally absent from the chain so that
	// findAncestorQcThroughRound fails the first time it calls GetHeaderByHash.

	h25 := common.Hash{0x25}
	h26 := common.Hash{0x26}
	h27 := common.Hash{0x27}

	qc26 := qcOf(h26, 6, 906)
	extra27, err := encodeV2ExtraForTest(7, &qc26)
	if err != nil {
		t.Fatal(err)
	}
	header27 := &types.Header{Number: big.NewInt(907), ParentHash: h26, Extra: extra27}

	qc25 := qcOf(h25, 5, 905)
	extra26, err := encodeV2ExtraForTest(6, &qc25)
	if err != nil {
		t.Fatal(err)
	}
	header26 := &types.Header{Number: big.NewInt(906), ParentHash: h25, Extra: extra26}

	// Smoke-check: DecodeBytesExtraFields must accept the encoding.
	var probe types.ExtraFields_v2
	if decErr := utils.DecodeBytesExtraFields(extra27, &probe); decErr != nil {
		t.Fatalf("extra27 decode check failed: %v", decErr)
	}

	// Committed QCs: rounds 1, 2, 3 at the SAME block numbers as incoming (905,
	// 906, 907) but different hashes.  This makes checkQCsOnTheSameChain walk
	// zero steps (block-number difference = 0) and immediately report that the
	// two sets are on diverging forks (hash mismatch), without any chain
	// lookups.  isOnTheChain = false, no early return.
	f.HighestCommittedQCs = []types.QuorumCert{
		qcOf(common.Hash{0x11}, 1, 905),
		qcOf(common.Hash{0x12}, 2, 906),
		qcOf(common.Hash{0x13}, 3, 907),
	}

	// Chain provides h27 and h26 so findAncestorQCs succeeds.
	// h25 is absent so findAncestorQcThroughRound fails on its first lookup.
	chain := &forensicsTestChain{headers: map[common.Hash]*types.Header{
		h27: header27,
		h26: header26,
		// h25 intentionally omitted
	}}

	incomingQC := qcOf(h27, 7, 907)

	// Before fix (origin/main): ProcessForensics returns nil, then fires a
	// goroutine that sends a ForensicsEvent with a zero ancestorQC.
	// After fix: returns a non-nil error, no event is sent.
	gotErr := f.ProcessForensics(chain, nil, incomingQC)
	if gotErr == nil {
		t.Error("ProcessForensics should return an error when findAncestorQcThroughRound fails, but returned nil (pre-fix behaviour)")
	}

	// Give any goroutine spawned by SendForensicProof time to deliver.
	select {
	case ev := <-ch:
		evJSON, _ := json.Marshal(ev)
		t.Errorf("ProcessForensics sent a forensics event despite a failed ancestor lookup — pre-fix behaviour: %s", evJSON)
	case <-time.After(100 * time.Millisecond):
		// No event received — correct post-fix behaviour.
	}
}
