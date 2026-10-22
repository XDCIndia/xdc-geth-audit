// Copyright (c) 2026 XDC Network
// F-2026-19639 residuals: V2 checkpoint by epoch number, V1 ranges in
// GetEpochNumbersBetween, and the QC in the V2 block RPC.
//
// Run: go test ./consensus/XDPoS/ -run 'F19639' -count=1

package XDPoS

import (
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestF19639CheckpointByEpochNumberUsesV2Engine(t *testing.T) {
	engine := makeTestXDPoSWithV2() // V2 switch block 900, SwitchEpoch 1
	stub := &stubV2Engine{blockByEpochResult: &types.BlockInfo{Number: big.NewInt(1823), Round: 923}}
	engine.EngineV2 = stub
	chain := &stubChain{currentHeader: &types.Header{Number: big.NewInt(2000)}}

	n, ok, err := engine.GetCheckpointByEpochNumber(chain, 2)
	if err != nil || !ok || n != 1823 {
		t.Fatalf("V2 epoch 2: got (%d, %v, %v), want (1823, true, nil) from the V2 engine", n, ok, err)
	}
	if !stub.blockByEpochCalled {
		t.Fatal("V2 engine GetBlockByEpochNumber not called")
	}
	if _, ok, _ := engine.GetCheckpointByEpochNumber(chain, 0); ok {
		t.Fatal("epoch below SwitchEpoch must stay on the V1 formula")
	}
}

func TestF19639EpochNumbersBetweenRejectsV1Range(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	stub := &stubV2Engine{}
	engine.EngineV2 = stub
	v1Begin, v2End := &types.Header{Number: big.NewInt(500)}, &types.Header{Number: big.NewInt(2000)}
	chain := &stubChain{currentHeader: v2End, headers: map[uint64]*types.Header{500: v1Begin, 2000: v2End}}
	api := makeTestAPI(engine, chain)

	b, e := rpc.BlockNumber(500), rpc.BlockNumber(2000)
	res, err := api.GetEpochNumbersBetween(&b, &e)
	if err == nil {
		t.Fatalf("range starting at V1 block returned %v, want the canonical 'not supported in the v1 consensus' error", res)
	}
}

func TestF19639V2BlockByHeaderReturnsQC(t *testing.T) {
	engine := makeTestXDPoSWithV2()
	qc := &types.QuorumCert{ProposedBlockInfo: &types.BlockInfo{Hash: common.HexToHash("0x01"), Number: big.NewInt(1799), Round: 1822}, GapNumber: 450}
	extra, err := (&types.ExtraFields_v2{Round: 1823, QuorumCert: qc}).EncodeToBytes()
	if err != nil {
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(1800), Extra: extra}
	engine.EngineV2 = &stubV2Engine{roundByHeader: map[common.Hash]types.Round{header.Hash(): 1823}}
	api := makeTestAPI(engine, &stubChain{currentHeader: header, headers: map[uint64]*types.Header{1800: header}})

	info := api.GetV2BlockByHeader(header, false)
	if info.EncodedQC == "" {
		t.Fatal("EncodedQC empty: the V2 block RPC must return the header's quorum certificate")
	}
	raw, err := base64.StdEncoding.DecodeString(info.EncodedQC)
	if err != nil {
		t.Fatal(err)
	}
	var got types.QuorumCert
	if err := rlp.DecodeBytes(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ProposedBlockInfo.Round != 1822 || got.GapNumber != 450 {
		t.Fatalf("decoded QC = %+v, want round 1822 gap 450", got.ProposedBlockInfo)
	}
}
