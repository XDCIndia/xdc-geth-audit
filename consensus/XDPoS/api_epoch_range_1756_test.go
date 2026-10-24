// Tests for issue #1756: GetEpochNumbersBetween must reject ranges spanning
// more than 1000 epochs (review R1-R02).
// Every test here FAILS on origin/main and PASSES with the fix.
package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// TestGetEpochNumbersBetweenRangeLimit verifies that a range spanning more than
// 1000 epochs is rejected before the epoch-switch walk is started.
// FAILS on origin/main (no limit check), PASSES with the fix.
func TestGetEpochNumbersBetweenRangeLimit(t *testing.T) {
	const epoch = uint64(900) // matches makeTestXDPoSWithV2

	engine := makeTestXDPoSWithV2() // SwitchBlock=900, Epoch=900
	stub := &stubV2Engine{}
	engine.EngineV2 = stub

	// beginNum is inside V2 (> SwitchBlock=900).
	// endNum is 1001 epochs beyond beginNum — over the limit.
	beginNum := uint64(1800)
	endNum := beginNum + 1001*epoch

	beginHeader := &types.Header{Number: big.NewInt(int64(beginNum))}
	endHeader := &types.Header{Number: big.NewInt(int64(endNum))}
	chain := &stubChain{
		currentHeader: endHeader,
		headers: map[uint64]*types.Header{
			beginNum: beginHeader,
			endNum:   endHeader,
		},
	}
	api := makeTestAPI(engine, chain)

	begin := rpc.BlockNumber(beginNum)
	end := rpc.BlockNumber(endNum)
	_, err := api.GetEpochNumbersBetween(&begin, &end)
	if err == nil {
		t.Fatal("expected error for range > 1000 epochs, got nil")
	}
	if stub.epochSwitchInfoCalled {
		t.Fatal("GetEpochSwitchInfoBetween must not be called when range is rejected")
	}
}

// TestGetEpochNumbersBetweenRangeAllowed verifies that a range of exactly 1000
// epochs is accepted and delegates to the V2 engine.
func TestGetEpochNumbersBetweenRangeAllowed(t *testing.T) {
	const epoch = uint64(900)

	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{epochSwitchResult: nil}
	engine.EngineV2 = stub

	beginNum := uint64(1800)
	endNum := beginNum + 1000*epoch // exactly at the limit

	beginHeader := &types.Header{Number: big.NewInt(int64(beginNum))}
	endHeader := &types.Header{Number: big.NewInt(int64(endNum))}
	chain := &stubChain{
		currentHeader: endHeader,
		headers: map[uint64]*types.Header{
			beginNum: beginHeader,
			endNum:   endHeader,
		},
	}
	api := makeTestAPI(engine, chain)

	begin := rpc.BlockNumber(beginNum)
	end := rpc.BlockNumber(endNum)
	_, err := api.GetEpochNumbersBetween(&begin, &end)
	if err != nil {
		t.Fatalf("unexpected error for range exactly at limit: %v", err)
	}
	if !stub.epochSwitchInfoCalled {
		t.Fatal("expected GetEpochSwitchInfoBetween to be called for allowed range")
	}
}
