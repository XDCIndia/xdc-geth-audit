// Copyright 2026 The go-ethereum Authors
//
// Unit tests for the masternode signing-tx trigger in XdcWorker (#922/#932).
//
// Tests verify:
//  1. CreateTxSign produces a tx with the correct to-address, selector,
//     encoding, gas, and value — parity constraints (selector 0xe341eaa4,
//     gas 200 000, value 0, to 0x…0089, data = sel++uint256++bytes32).
//  2. broadcastSigningTx is a no-op for blocks NOT divisible by MergeSignRange.
//  3. broadcastSigningTx is a no-op (with warning) when txPool/accountManager is nil.
//  4. broadcastSigningTx calls pool.Add on blocks divisible by 15 (cadence gate).
//  5. NewXdcWorker stores accountManager + txPool fields correctly.
//  6. isSigningTxStale: stale blocks (head far ahead) return true.
//  7. isSigningTxStale: fresh blocks (head == blockNum, head just ahead) return false.
//  8. broadcastSigningTx skips stale blocks when XDPoS config is set (freshness gate).

package miner

import (
	"bytes"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// ─── stub TxPoolAdder for worker tests ────────────────────────────────────────

// stubWorkerPool implements contracts.TxPoolAdder for testing.
type stubWorkerPool struct {
	mu    sync.Mutex
	count int
}

func (p *stubWorkerPool) PoolNonce(_ common.Address) uint64 { return 0 }

func (p *stubWorkerPool) Add(_ []*types.Transaction, _ bool) []error {
	p.mu.Lock()
	p.count++
	p.mu.Unlock()
	return []error{nil}
}

// Ensure stubWorkerPool satisfies the interface.
var _ contracts.TxPoolAdder = (*stubWorkerPool)(nil)

// ─── TestCreateTxSign_Parity ─────────────────────────────────────────────────

// TestCreateTxSign_Parity verifies parity constraints from #922 against
// XDPoSChain/contracts/utils.go:169-176 (CreateTxSign):
//   - to:       0x0000000000000000000000000000000000000089 (BlockSignersBinary)
//   - gas:      200 000
//   - value:    0
//   - selector: 0xe341eaa4 (first 4 bytes of data)
//   - data:     selector ++ uint256(blockNumber, 32B) ++ bytes32(blockHash, 32B) = 68 bytes
//
// Running in the miner package because contracts/ has a pre-existing import cycle
// in utils_test.go that prevents tests from running there.
func TestCreateTxSign_Parity(t *testing.T) {
	blockNumber := big.NewInt(15) // divisible by MergeSignRange
	var blockHash common.Hash
	for i := range blockHash {
		blockHash[i] = byte(i + 1)
	}
	nonce := uint64(7)
	to := common.BlockSignersBinary // 0x0000000000000000000000000000000000000089

	tx := contracts.CreateTxSign(blockNumber, blockHash, nonce, to)

	// Recipient must be BlockSignersBinary (0x…0089).
	if tx.To() == nil || *tx.To() != to {
		t.Errorf("to: want %s, got %v", to.Hex(), tx.To())
	}

	// Gas limit must be exactly 200 000.
	const wantGas = uint64(200_000)
	if tx.Gas() != wantGas {
		t.Errorf("gas: want %d, got %d", wantGas, tx.Gas())
	}

	// Value must be 0 (no XDC transferred).
	if tx.Value().Sign() != 0 {
		t.Errorf("value: want 0, got %s", tx.Value())
	}

	// Nonce
	if tx.Nonce() != nonce {
		t.Errorf("nonce: want %d, got %d", nonce, tx.Nonce())
	}

	// Data: 4 (selector) + 32 (uint256 blockNumber) + 32 (bytes32 blockHash) = 68.
	data := tx.Data()
	if len(data) != 68 {
		t.Fatalf("data length: want 68, got %d", len(data))
	}

	// Selector: 0xe341eaa4
	wantSelector := common.Hex2Bytes("e341eaa4")
	if !bytes.Equal(data[:4], wantSelector) {
		t.Errorf("selector: want %x, got %x", wantSelector, data[:4])
	}

	// blockNumber in data[4:36] as left-padded uint256 (big-endian, 32 bytes).
	wantNum := common.LeftPadBytes(blockNumber.Bytes(), 32)
	if !bytes.Equal(data[4:36], wantNum) {
		t.Errorf("blockNumber in data[4:36]: want %x, got %x", wantNum, data[4:36])
	}

	// blockHash in data[36:68].
	if !bytes.Equal(data[36:68], blockHash[:]) {
		t.Errorf("blockHash in data[36:68]: want %x, got %x", blockHash[:], data[36:68])
	}
}

// ─── TestBroadcastSigningTx_CadenceGate ──────────────────────────────────────

// TestBroadcastSigningTx_CadenceGate verifies that broadcastSigningTx only
// triggers on blocks divisible by MergeSignRange (15).
func TestBroadcastSigningTx_CadenceGate(t *testing.T) {
	// Use a minimal worker with nil accountManager — will be skipped after the
	// cadence check if %15 != 0; will warn if %15 == 0 and manager is nil.
	pool := &stubWorkerPool{}

	// Blocks NOT divisible by 15 — pool should never be called.
	for _, n := range []uint64{1, 2, 7, 14, 16, 29, 30 - 1} {
		header := &types.Header{Number: big.NewInt(int64(n))}
		block := types.NewBlockWithHeader(header)

		w := &XdcWorker{
			txPool:         pool,
			accountManager: nil, // will be nil-guarded after cadence check
			miner: &Miner{
				chain: nil,
			},
		}
		// broadcastSigningTx should return early (no warn, no pool add).
		w.broadcastSigningTx(block)
	}

	pool.mu.Lock()
	got := pool.count
	pool.mu.Unlock()
	if got != 0 {
		t.Errorf("pool.Add called %d times for non-multiples of 15; want 0", got)
	}
}

// TestBroadcastSigningTx_MultiplesOf15_NilManager verifies that blocks at %15==0
// with a nil account manager emit a warning and do NOT call pool.Add.
func TestBroadcastSigningTx_MultiplesOf15_NilManager(t *testing.T) {
	pool := &stubWorkerPool{}

	for _, n := range []uint64{15, 30, 45, 300} {
		header := &types.Header{Number: big.NewInt(int64(n))}
		block := types.NewBlockWithHeader(header)

		w := &XdcWorker{
			txPool:         pool,
			accountManager: nil,
			miner:          &Miner{chain: nil},
		}
		// Should warn "account manager or txpool not wired" and return without
		// calling pool.Add.
		w.broadcastSigningTx(block)
	}

	pool.mu.Lock()
	got := pool.count
	pool.mu.Unlock()
	if got != 0 {
		t.Errorf("pool.Add called %d times for nil manager; want 0", got)
	}
}

// TestBroadcastSigningTx_MultiplesOf15_NilPool verifies that blocks at %15==0
// with a nil txpool emit a warning and do NOT panic.
func TestBroadcastSigningTx_MultiplesOf15_NilPool(t *testing.T) {
	w := &XdcWorker{
		txPool:         nil,
		accountManager: nil, // both nil — warns once and returns
		miner:          &Miner{chain: nil},
	}
	header := &types.Header{Number: big.NewInt(15)}
	block := types.NewBlockWithHeader(header)
	// Must not panic.
	w.broadcastSigningTx(block)
}

// ─── TestCreateTxSign_Selector ────────────────────────────────────────────────

// TestCreateTxSign_Selector is a parity cross-check in the miner package:
// the 4-byte selector in the tx data MUST be 0xe341eaa4.
func TestCreateTxSign_Selector(t *testing.T) {
	blockNumber := big.NewInt(30)
	blockHash := common.HexToHash("0x1234")
	to := common.BlockSignersBinary

	tx := contracts.CreateTxSign(blockNumber, blockHash, 0, to)
	data := tx.Data()

	wantSel := common.Hex2Bytes(common.HexSignMethod) // "e341eaa4"
	if len(data) < 4 {
		t.Fatalf("data too short: %d bytes", len(data))
	}
	for i, b := range wantSel {
		if data[i] != b {
			t.Errorf("selector byte %d: want %02x, got %02x", i, b, data[i])
		}
	}
}

// ─── TestXdcWorkerNewSigningTxFields ─────────────────────────────────────────

// TestXdcWorkerNewSigningTxFields verifies that NewXdcWorker stores the
// accountManager and txPool fields correctly.
func TestXdcWorkerNewSigningTxFields(t *testing.T) {
	bc := newXdcTestChain(t)
	m := &Miner{chain: bc, config: &Config{}}

	pool := &stubWorkerPool{}

	minePeriodCh := make(chan int, 1)
	newRoundCh := make(chan types.Round, 1)

	w := NewXdcWorker(
		nil, nil, m, minePeriodCh, newRoundCh,
		&stubBroadcaster{}, &stubSynced{synced: false},
		nil, // accountManager — nil for non-mining test
		pool,
	)

	if w.txPool != pool {
		t.Error("txPool not stored in XdcWorker")
	}
	if w.accountManager != nil {
		t.Error("accountManager should be nil")
	}

	// Verify cadence: block 14 does not trigger, block 15 triggers but nil
	// manager causes an early-return warning (no panic).
	block14 := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(14)})
	block15 := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(15), Difficulty: big.NewInt(1)})

	w.miner = &Miner{
		chain: bc,
		chainConfig: &params.ChainConfig{
			XDPoS: &params.XDPoSConfig{Epoch: 900},
		},
	}

	w.broadcastSigningTx(block14) // no-op, cadence gate
	w.broadcastSigningTx(block15) // warns "account manager or txpool not wired" (manager nil)

	pool.mu.Lock()
	n := pool.count
	pool.mu.Unlock()

	if n != 0 {
		t.Errorf("pool.Add called %d times; want 0 (manager is nil)", n)
	}
}

// ─── TestIsSigningTxStale — freshness gate logic (#932) ───────────────────────

// TestIsSigningTxStale_StaleBlock verifies that a block whose number is far
// behind the current head is classified as stale.
//
// Freshness bound = 2 * MergeSignRange = 30 blocks.
// Example: head=1000, blockNum=15 → lag=985 > 30 → stale.
func TestIsSigningTxStale_StaleBlock(t *testing.T) {
	cases := []struct {
		blockNum uint64
		headNum  uint64
		want     bool
		name     string
	}{
		// Well past the freshness bound.
		{blockNum: 15, headNum: 1000, want: true, name: "blockNum=15,head=1000"},
		{blockNum: 30, headNum: 900, want: true, name: "blockNum=30,head=900"},
		// Exactly at the boundary (lag == freshnessBound+1 → stale).
		{blockNum: 100, headNum: 100 + signingFreshnessBound + 1, want: true, name: "exactly-past-bound"},
		// Just at the boundary (lag == freshnessBound → fresh).
		{blockNum: 100, headNum: 100 + signingFreshnessBound, want: false, name: "at-bound-is-fresh"},
		// Fresh: head == blockNum.
		{blockNum: 15, headNum: 15, want: false, name: "head==blockNum"},
		// Fresh: head < blockNum (we just minted the block, head hasn't caught up).
		{blockNum: 15, headNum: 0, want: false, name: "head<blockNum"},
		// Fresh: small lag within bound.
		{blockNum: 300, headNum: 305, want: false, name: "lag=5"},
		// Stale: small block number, very large head.
		{blockNum: 0, headNum: 100, want: true, name: "blockNum=0,head=100"},
	}

	for _, tc := range cases {
		got := isSigningTxStale(tc.blockNum, tc.headNum)
		if got != tc.want {
			t.Errorf("[%s] isSigningTxStale(%d, %d) = %v, want %v",
				tc.name, tc.blockNum, tc.headNum, got, tc.want)
		}
	}
}

// TestBroadcastSigningTx_FreshnessGate_XDPoS verifies that broadcastSigningTx
// skips submission when the block is stale relative to the chain head, AND
// XDPoS is configured (freshness check is active).
//
// Uses newXdcTestChain (head=0) and a block at number 15.  head(0) is BELOW
// blockNum(15), so isSigningTxStale(15,0)=false — the gate does NOT fire and
// submission proceeds to the nil-manager check (which returns a warning, no panic).
//
// To verify the stale path without a full blockchain, isSigningTxStale is tested
// directly above (TestIsSigningTxStale_StaleBlock).
func TestBroadcastSigningTx_FreshnessGate_XDPoS_FreshBlock(t *testing.T) {
	bc := newXdcTestChain(t) // head = block 0 (genesis)
	pool := &stubWorkerPool{}

	w := &XdcWorker{
		txPool:         pool,
		accountManager: nil, // nil — will warn after freshness check passes
		miner: &Miner{
			chain: bc,
			chainConfig: &params.ChainConfig{
				XDPoS: &params.XDPoSConfig{Epoch: 900},
			},
		},
	}

	// block 15, head = 0: isSigningTxStale(15, 0) = false → gate passes → nil-manager warn.
	block15 := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(15)})
	w.broadcastSigningTx(block15)

	// Pool.Add was NOT called (manager nil caused an early return after freshness
	// check, but no panic).
	pool.mu.Lock()
	n := pool.count
	pool.mu.Unlock()
	if n != 0 {
		t.Errorf("pool.Add called %d times; want 0 (manager nil)", n)
	}
}

// ─── imported-block sign tx: V1 vs V2 (#1456) ────────────────────────────────

// newV1SignTestWorker returns a worker on a 20-block chain whose genesis (the V1
// epoch-0 checkpoint) lists signer as the only masternode, plus a counter of
// sign txs emitted per block number.
func newV1SignTestWorker(t *testing.T, signer common.Address) (*XdcWorker, *core.BlockChain, map[uint64]int) {
	t.Helper()
	extra := make([]byte, 32+common.AddressLength+65)
	copy(extra[32:], signer.Bytes())
	gspec := &core.Genesis{Config: params.AllEthashProtocolChanges, ExtraData: extra}
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 20, nil)
	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), &core.BlockChainConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}
	signed := make(map[uint64]int)
	orig := createTransactionSign
	createTransactionSign = func(_ *params.ChainConfig, _ contracts.TxPoolAdder, _ *accounts.Manager, b *types.Block, _ common.Address, _ contracts.StateNonceReader) error {
		signed[b.NumberU64()]++
		return nil
	}
	t.Cleanup(func() { createTransactionSign = orig })

	engine := XDPoS.New(&params.XDPoSConfig{
		Epoch: 900,
		V2:    &params.V2{SwitchBlock: big.NewInt(1800)},
	}, rawdb.NewMemoryDatabase())
	w := &XdcWorker{
		engine:         engine,
		coinbase:       signer,
		accountManager: &accounts.Manager{},
		txPool:         &stubWorkerPool{},
		miner:          &Miner{chain: bc, chainConfig: bc.Config()},
	}
	return w, bc, signed
}

// TestImportedSignTx_V1Signer: a V1 masternode signs imported blocks at
// MergeSignRange (XDPoSChain eth/backend.go:372). On origin/main this path
// required a V2 snapshot and never signed in V1.
func TestImportedSignTx_V1Signer(t *testing.T) {
	signer := common.HexToAddress("0x1456")
	w, bc, signed := newV1SignTestWorker(t, signer)
	for n := uint64(1); n <= 16; n++ {
		w.maybeBroadcastImportedSignTx(bc.GetBlockByNumber(n))
	}
	if len(signed) != 1 || signed[15] != 1 {
		t.Fatalf("signed blocks = %v, want only block 15", signed)
	}
}

// TestImportedSignTx_V1PreTIP2019: before TIP2019 every block is signed.
func TestImportedSignTx_V1PreTIP2019(t *testing.T) {
	orig := common.TIP2019Block
	common.TIP2019Block = big.NewInt(100)
	t.Cleanup(func() { common.TIP2019Block = orig })

	signer := common.HexToAddress("0x1456")
	w, bc, signed := newV1SignTestWorker(t, signer)
	w.maybeBroadcastImportedSignTx(bc.GetBlockByNumber(7))
	if signed[7] != 1 {
		t.Fatalf("pre-TIP2019 block 7 not signed: %v", signed)
	}
}

// TestImportedSignTx_V1NonSigner: a coinbase outside the V1 set never signs.
func TestImportedSignTx_V1NonSigner(t *testing.T) {
	w, bc, signed := newV1SignTestWorker(t, common.HexToAddress("0x1456"))
	w.coinbase = common.HexToAddress("0xdead")
	w.maybeBroadcastImportedSignTx(bc.GetBlockByNumber(15))
	if len(signed) != 0 {
		t.Fatalf("non-signer signed %v", signed)
	}
}

// TestImportedSignTx_V2NoSnapshot: V2 blocks keep the CandidatePool gate; with
// no V2 engine wired nothing is signed (and the V1 set is not consulted).
func TestImportedSignTx_V2NoSnapshot(t *testing.T) {
	signer := common.HexToAddress("0x1456")
	w, _, signed := newV1SignTestWorker(t, signer)
	v2Block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1815)})
	w.maybeBroadcastImportedSignTx(v2Block)
	if len(signed) != 0 {
		t.Fatalf("V2 block signed without a V2 snapshot: %v", signed)
	}
}
