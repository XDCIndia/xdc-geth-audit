// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// TestIsAuthorisedAddressV2Candidates: in V2 every candidate of the gap
// snapshot may send signing txs, not only the active masternodes, as in
// canonical XDPoSChain engine_v2 IsAuthorisedAddress (#1568).
func TestIsAuthorisedAddressV2Candidates(t *testing.T) {
	cfg := &params.XDPoSConfig{Period: 2, Epoch: 900, Gap: 450, V2: &params.V2{SwitchBlock: big.NewInt(900)}}
	db := rawdb.NewMemoryDatabase()
	engine := New(cfg, db)
	v2 := engine_v2.New(&params.ChainConfig{ChainID: big.NewInt(2012), XDPoS: cfg}, db, nil, nil)
	engine.EngineV2 = v2

	gap := &types.Header{Number: big.NewInt(1350)}
	head := &types.Header{Number: big.NewInt(2000)}
	chain := &stubChain{currentHeader: head, headers: map[uint64]*types.Header{1350: gap, 2000: head}}
	standby := common.HexToAddress("0x5")
	cands := []common.Address{common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4"), standby}
	if err := v2.UpdateMasternodes(chain, gap, cands); err != nil {
		t.Fatal(err)
	}
	if !engine.IsAuthorisedAddress(chain, head, standby) {
		t.Error("a gap-snapshot candidate outside the active set must be authorised")
	}
	if engine.IsAuthorisedAddress(chain, head, common.HexToAddress("0xdead")) {
		t.Error("a non-candidate must not be authorised")
	}
}
