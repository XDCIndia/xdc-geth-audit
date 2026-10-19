// Copyright 2026 The XDC Network Authors
// Tests for XDC #1645: cumulative TD in handshake and NewBlock broadcasts.
//
// These tests compile on both the fix branch and on main.
// On main (estimateXDTDP returns block number), they fail with a
// descriptive value-mismatch message.  On the fix branch (xdcHandshakeTD
// returns stored cumulative TD), they pass.

package eth

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
)

// xdcTDTestChain is a minimal forkid.Blockchain + xdcTDGetter mock for
// handshake TD tests.  It implements all three interfaces so that — on the
// fix branch — the type assertion chain.(xdcTDGetter) succeeds and the
// stored cumulative TD is returned.  On main (where xdcHandshakeTD /
// xdcTDGetter do not exist) the test still compiles because the interface
// methods are never missing; only the behaviour differs.
type xdcTDTestChain struct {
	chainConfig *params.ChainConfig
	genesis     *types.Block
	head        *types.Header
	storedTD    map[common.Hash]*big.Int // preloaded per-block cumulative TD
}

func (c *xdcTDTestChain) Config() *params.ChainConfig  { return c.chainConfig }
func (c *xdcTDTestChain) Genesis() *types.Block        { return c.genesis }
func (c *xdcTDTestChain) CurrentHeader() *types.Header { return c.head }
func (c *xdcTDTestChain) GetTd(hash common.Hash, _ uint64) *big.Int {
	if td, ok := c.storedTD[hash]; ok {
		return new(big.Int).Set(td)
	}
	return nil
}

// buildXDCTDTestChain creates a minimal XDC chain mock for the handshake tests.
//
//	blocks       number of non-genesis blocks
//	genDiff      genesis block difficulty
//	blockDiff    difficulty of each subsequent block
//
// Returns the chain mock and the expected cumulative TD at the head.
func buildXDCTDTestChain(blocks int, genDiff, blockDiff *big.Int) (*xdcTDTestChain, *big.Int) {
	// Minimal XDC chain config: only XDPoS != nil matters for IsXDC().
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(99999),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		XDPoS: &params.XDPoSConfig{
			Period: 2,
			Epoch:  900,
			V2: &params.V2{
				SwitchBlock: big.NewInt(1_000_000), // far in the future; all test blocks are V1
				SwitchEpoch: 1112,
				AllConfigs:  params.DevnetV2Configs,
			},
		},
	}

	// Build a fake genesis block (no real state needed).
	genHeader := &types.Header{
		Number:     big.NewInt(0),
		Difficulty: new(big.Int).Set(genDiff),
		GasLimit:   8_000_000,
		Time:       1000,
	}
	genBlock := types.NewBlockWithHeader(genHeader)

	// Compute cumulative TD walking forward.
	storedTD := make(map[common.Hash]*big.Int, blocks+1)
	cumTD := new(big.Int).Set(genDiff)
	storedTD[genBlock.Hash()] = new(big.Int).Set(cumTD)

	var head *types.Header
	parentHash := genBlock.Hash()
	for i := 1; i <= blocks; i++ {
		h := &types.Header{
			Number:     big.NewInt(int64(i)),
			ParentHash: parentHash,
			Difficulty: new(big.Int).Set(blockDiff),
			GasLimit:   8_000_000,
			Time:       uint64(1000 + i*2),
		}
		hash := h.Hash()
		cumTD = new(big.Int).Add(cumTD, blockDiff)
		storedTD[hash] = new(big.Int).Set(cumTD)
		head = h
		parentHash = hash
	}

	chain := &xdcTDTestChain{
		chainConfig: cfg,
		genesis:     genBlock,
		head:        head,
		storedTD:    storedTD,
	}
	return chain, cumTD
}

// runXDCHandshakeCaptureTD performs an XDPOS2 handshake and returns the TD
// advertised in the local peer's StatusPacket62.
//
// It connects two ends of a MsgPipe.  The "remote" side reads the first
// StatusMsg (the local peer's advertisement), captures its TD, then replies
// with a mirrored status so the handshake completes.
func runXDCHandshakeCaptureTD(t *testing.T, chain *xdcTDTestChain) *big.Int {
	t.Helper()

	app, net := p2p.MsgPipe()
	t.Cleanup(func() {
		app.Close()
		net.Close()
	})

	var id enode.ID
	rand.Read(id[:])
	peer := NewPeer(XDPOS2, p2p.NewPeer(id, "xdc-td-test", nil), net, nil, nil, nil)

	type result struct {
		td  *big.Int
		err error
	}
	remoteCh := make(chan result, 1)

	go func() {
		// Read the local peer's status advertisement.
		msg, err := app.ReadMsg()
		if err != nil {
			remoteCh <- result{err: err}
			return
		}
		var status StatusPacket62
		if err := msg.Decode(&status); err != nil {
			remoteCh <- result{err: err}
			return
		}
		td := new(big.Int).Set(status.TD)

		// Reply with a valid mirrored status so the local peer's readStatus62 succeeds.
		reply := &StatusPacket62{
			ProtocolVersion: uint32(XDPOS2),
			NetworkID:       chain.chainConfig.ChainID.Uint64(),
			TD:              td,
			Head:            chain.head.Hash(),
			Genesis:         chain.genesis.Hash(),
		}
		if err := p2p.Send(app, StatusMsg, reply); err != nil {
			remoteCh <- result{td: td, err: err}
			return
		}
		remoteCh <- result{td: td}
	}()

	var rangeMsg BlockRangeUpdatePacket
	if err := peer.Handshake(chain.chainConfig.ChainID.Uint64(), chain, rangeMsg); err != nil {
		t.Fatalf("handshake error: %v", err)
	}

	r := <-remoteCh
	if r.err != nil {
		t.Fatalf("remote side error: %v", r.err)
	}
	return r.td
}

// TestXDCHandshakeStatusTDIsCumulative is the primary regression test for
// XDC issue #1645.  It verifies that the StatusPacket62 advertised during an
// XDPOS2 handshake carries the cumulative total difficulty (Σ block.Difficulty
// from genesis to head), NOT the block number.
//
// On main (pre-fix), the handler calls estimateXDTDP which returns block number.
// On the fix branch, xdcHandshakeTD looks up the stored cumulative TD.
// If the two values match, the bug is present; if they differ as expected, the
// fix is in place.
func TestXDCHandshakeStatusTDIsCumulative(t *testing.T) {
	t.Parallel()

	const blocks = 10
	// genDiff=10, blockDiff=2 → at head: TD = 10 + 10*2 = 30 ≠ 10 (block number)
	chain, wantTD := buildXDCTDTestChain(blocks, big.NewInt(10), big.NewInt(2))

	got := runXDCHandshakeCaptureTD(t, chain)

	blockNum := chain.head.Number.Uint64()
	if got.Cmp(wantTD) != 0 {
		t.Fatalf(
			"StatusPacket62.TD = %v, want cumulative TD %v"+
				"\n(pre-fix code returns block number %d — if got == %d the bug is present)",
			got, wantTD, blockNum, blockNum,
		)
	}
}

// TestXDCHandshakeTDNotBlockNumber is a canary that explicitly fails when the
// TD equals the block number (the pre-fix behaviour of estimateXDTDP).
func TestXDCHandshakeTDNotBlockNumber(t *testing.T) {
	t.Parallel()

	const blocks = 10
	// Same config as above: cumulative TD = 30, block number = 10.
	chain, _ := buildXDCTDTestChain(blocks, big.NewInt(10), big.NewInt(2))

	got := runXDCHandshakeCaptureTD(t, chain)

	blockNum := chain.head.Number.Uint64()
	if got.Uint64() == blockNum {
		t.Fatalf(
			"StatusPacket62.TD = %d equals block number %d — "+
				"this is the pre-fix bug: estimateXDTDP returned block number instead of cumulative TD",
			got, blockNum,
		)
	}
}
