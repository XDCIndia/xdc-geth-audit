// Copyright (c) 2026 XDC Network Authors
//
// This file tests the XDC special-transaction (gasPrice=0 signing txs to
// BlockSignersBinary/RandomizeSMCBinary) exemption in the legacy tx pool.
//
// Canonical reference: XDPoSChain/core/txpool/txpool.go:675,710-730,779-780

package legacypool

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/contracts"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// spyReserver is a txpool.Reserver that records which addresses are currently held.
// Used by R1-M02 tests to verify that the special-tx fast path holds the reservation.
type spyReserver struct {
	mu   sync.Mutex
	held map[common.Address]bool
}

func newSpyReserver() *spyReserver {
	return &spyReserver{held: make(map[common.Address]bool)}
}

func (s *spyReserver) Hold(addr common.Address) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[addr] {
		return errors.New("spyReserver: already held")
	}
	s.held[addr] = true
	return nil
}

func (s *spyReserver) Release(addr common.Address) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, addr)
	return nil
}

func (s *spyReserver) Has(_ common.Address) bool { return false }

func (s *spyReserver) isHeld(addr common.Address) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[addr]
}

// xdcSpecialPool returns a pool with a 1 Gwei MinTip to exercise the
// special-tx exemption (ordinary gasPrice=0 txs would fail at this MinTip).
func xdcSpecialPool(t *testing.T) (*LegacyPool, func()) {
	t.Helper()

	cpy := *params.TestChainConfig
	cpy.LondonBlock = common.Big0
	cpy.BerlinBlock = common.Big0

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	bc := newTestBlockChain(&cpy, 10_000_000, statedb, new(event.Feed))

	cfg := DefaultConfig
	cfg.Journal = ""
	cfg.NoLocals = true // disable local-tx pricing exception — test special-tx path explicitly

	pool := New(cfg, bc)
	if err := pool.Init(cfg.PriceLimit, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatalf("pool init: %v", err)
	}
	<-pool.initDoneCh
	// Raise the min tip so ordinary gasPrice=0 txs are rejected.
	pool.SetGasTip(big.NewInt(1_000_000_000)) // 1 Gwei

	return pool, func() { pool.Close() }
}

// makeSpecialTx builds a signed gasPrice=0 tx to dest (BlockSigners or Randomize).
// Mirrors CreateTxSign in contracts/utils.go:56 (gasPrice = big.NewInt(0)).
func makeSpecialTx(nonce uint64, dest common.Address, privKey *ecdsa.PrivateKey) *types.Transaction {
	data := common.Hex2Bytes(common.HexSignMethod)
	blockNum := new(big.Int).SetUint64(nonce * 15)
	inputData := append(data, common.LeftPadBytes(blockNum.Bytes(), 32)...)
	inputData = append(inputData, common.LeftPadBytes(blockNum.Bytes(), 32)...)
	// gasPrice = big.NewInt(0) — canonical gasless special tx.
	// XDPoSChain/contracts/utils.go:173 and this fork's contracts/utils.go:56.
	raw := types.NewTransaction(nonce, dest, big.NewInt(0), 200000, big.NewInt(0), inputData)
	tx, err := types.SignTx(raw, types.HomesteadSigner{}, privKey)
	if err != nil {
		panic(err)
	}
	return tx
}

// makeSigningTxForBlock builds a signed BlockSigners tx that signs a specific
// targetBlock.  Used for freshness-expiry tests where the block number embedded
// in the tx data must be controlled precisely.
func makeSigningTxForBlock(nonce, targetBlock uint64, privKey *ecdsa.PrivateKey) *types.Transaction {
	data := common.Hex2Bytes(common.HexSignMethod)
	blockNumBig := new(big.Int).SetUint64(targetBlock)
	var blockHash common.Hash
	blockHash[0] = byte(targetBlock & 0xff)
	inputData := append(data, common.LeftPadBytes(blockNumBig.Bytes(), 32)...)
	inputData = append(inputData, blockHash[:]...)
	raw := types.NewTransaction(nonce, common.BlockSignersBinary, big.NewInt(0), 200000, big.NewInt(0), inputData)
	tx, err := types.SignTx(raw, types.HomesteadSigner{}, privKey)
	if err != nil {
		panic(err)
	}
	return tx
}

// xdcSpecialPoolWithEpoch returns a pool configured for XDPoS with a given
// epoch, allowing expiry tests.
func xdcSpecialPoolWithEpoch(t *testing.T, epoch uint64) (*LegacyPool, func()) {
	t.Helper()

	cpy := *params.TestChainConfig
	cpy.LondonBlock = common.Big0
	cpy.BerlinBlock = common.Big0
	cpy.XDPoS = &params.XDPoSConfig{Epoch: epoch}

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	bc := newTestBlockChain(&cpy, 10_000_000, statedb, new(event.Feed))

	cfg := DefaultConfig
	cfg.Journal = ""
	cfg.NoLocals = true

	pool := New(cfg, bc)
	if err := pool.Init(cfg.PriceLimit, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatalf("pool init: %v", err)
	}
	<-pool.initDoneCh
	pool.SetGasTip(big.NewInt(1_000_000_000))

	return pool, func() { pool.Close() }
}

// TestXDCSpecialTxLocalAdd verifies that a gasPrice=0 tx to BlockSignersBinary
// is accepted by AddLocal even when MinTip is set to 1 Gwei.
//
// XDPoSChain parity: txpool.go:710-730 — the entire ErrZeroGasPrice /
// ErrUnderMinGasPrice block is guarded by !tx.IsSpecialTransaction().
func TestXDCSpecialTxLocalAdd(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	// Tiny balance: cost = value + gasPrice*gas = 0 + 0*200000 = 0,
	// so balance=1 Wei is sufficient.
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	tx := makeSpecialTx(0, common.BlockSignersBinary, privKey)

	if !tx.IsSpecialTransaction() {
		t.Fatal("tx to BlockSignersBinary must be IsSpecialTransaction()==true")
	}

	errs := pool.Add([]*types.Transaction{tx}, true /* sync */)
	if errs[0] != nil {
		t.Fatalf("AddLocal of gasPrice=0 special tx rejected: %v", errs[0])
	}

	pending, _ := pool.Pending(txpool.PendingFilter{})
	if _, ok := pending[addr]; !ok {
		t.Fatal("special tx not found in pending after AddLocal")
	}
}

// TestXDCSpecialTxRemoteAdd verifies that a gasPrice=0 tx to BlockSignersBinary
// is accepted via the remote (gossip) path.
//
// XDPoSChain parity: txpool.go:675 — special txs from registered signers are
// exempt from ErrUnderpriced even for remote adds.
func TestXDCSpecialTxRemoteAdd(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	tx := makeSpecialTx(0, common.BlockSignersBinary, privKey)

	// addRemoteSync simulates the p2p gossip (non-local) path.
	if err := pool.addRemoteSync(tx); err != nil {
		t.Fatalf("addRemote of gasPrice=0 special tx rejected: %v", err)
	}

	pending, _ := pool.Pending(txpool.PendingFilter{})
	if _, ok := pending[addr]; !ok {
		t.Fatal("special tx not found in pending after remote add")
	}
}

// TestXDCNonSpecialTxZeroGasPriceRejected verifies that a gasPrice=0 tx to a
// non-special address IS still rejected. Only 0x89/0x90 destinations are exempt.
//
// XDPoSChain parity: txpool.go:710 — guard is !tx.IsSpecialTransaction().
func TestXDCNonSpecialTxZeroGasPriceRejected(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1_000_000_000_000_000_000), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// Normal tx to a non-special address with gasPrice=0 — must be rejected.
	dest := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	raw := types.NewTransaction(0, dest, big.NewInt(0), 21000, big.NewInt(0), nil)
	tx, _ := types.SignTx(raw, types.HomesteadSigner{}, privKey)

	if tx.IsSpecialTransaction() {
		t.Fatal("tx to non-special address must NOT be IsSpecialTransaction()")
	}

	errs := pool.Add([]*types.Transaction{tx}, true)
	if errs[0] == nil {
		t.Fatal("gasPrice=0 tx to non-special address should have been rejected by MinTip check")
	}
}

// TestXDCRandomizeTxLocalAdd verifies that a gasPrice=0 tx to RandomizeSMCBinary
// (0x90) is also accepted — the special-address set covers both 0x89 and 0x90.
//
// XDPoSChain/core/types/transaction.go:485:
//
//	IsSpecialTx = to == BlockSignersBinary || to == RandomizeSMCBinary
func TestXDCRandomizeTxLocalAdd(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	tx := makeSpecialTx(0, common.RandomizeSMCBinary, privKey)

	if !tx.IsSpecialTransaction() {
		t.Fatal("tx to RandomizeSMCBinary must be IsSpecialTransaction()==true")
	}

	errs := pool.Add([]*types.Transaction{tx}, true)
	if errs[0] != nil {
		t.Fatalf("AddLocal of gasPrice=0 special tx to RandomizeSMC rejected: %v", errs[0])
	}

	pending, _ := pool.Pending(txpool.PendingFilter{})
	if _, ok := pending[addr]; !ok {
		t.Fatal("RandomizeSMC special tx not found in pending")
	}
}

// TestXDCCreateTxSignGasPriceZero verifies that contracts.CreateTxSign builds
// a tx with gasPrice=0 exactly matching canonical XDPoSChain.
//
// Regression guard against commit d6b863156 which wrongly raised gasPrice to
// 25 Gwei — canonical on-chain txs use gasPrice=0 (sample blocks 82958701,
// 82958716, 82958731 all show gasPrice:0x0).
//
// XDPoSChain/contracts/utils.go:173: gasPrice = big.NewInt(0).
func TestXDCCreateTxSignGasPriceZero(t *testing.T) {
	t.Parallel()

	tx := contracts.CreateTxSign(
		big.NewInt(82_958_701),
		common.HexToHash("0xdeadbeef"),
		42,
		common.BlockSignersBinary,
	)
	if tx == nil {
		t.Fatal("CreateTxSign returned nil")
	}
	if tx.GasPrice().Sign() != 0 {
		t.Fatalf("CreateTxSign gasPrice must be 0, got %v — canonical XDPoSChain/contracts/utils.go:173 uses big.NewInt(0)", tx.GasPrice())
	}
	if tx.Value().Sign() != 0 {
		t.Fatalf("CreateTxSign value must be 0, got %v", tx.Value())
	}
	if !tx.IsSpecialTransaction() {
		t.Fatal("CreateTxSign output must satisfy IsSpecialTransaction() (dest == BlockSignersBinary)")
	}
}

// TestXDCSpecialTxIsSigner verifies that the promoteSpecialTx fast-path is
// used when IsSigner is wired and the sender is a registered masternode.
//
// XDPoSChain/core/txpool/txpool.go:779-780:
//
//	if tx.IsSpecialTransaction() && pool.IsSigner != nil && pool.IsSigner(from)
//	   && pool.pendingNonces.get(from) == tx.Nonce() { promoteSpecialTx }
func TestXDCSpecialTxIsSigner(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	// Register the sender as a known masternode signer.
	pool.IsSigner = func(address common.Address) bool {
		return address == addr
	}

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	tx := makeSpecialTx(0, common.BlockSignersBinary, privKey)

	errs := pool.Add([]*types.Transaction{tx}, true)
	if errs[0] != nil {
		t.Fatalf("IsSigner fast-path special tx rejected: %v", errs[0])
	}

	pending, _ := pool.Pending(txpool.PendingFilter{})
	senderTxs, ok := pending[addr]
	if !ok || len(senderTxs) == 0 {
		t.Fatal("special tx not in pending after IsSigner fast-path")
	}
}

// TestXDCSpecialTxDuplicateRejected verifies that a second special tx at the
// same nonce via the IsSigner fast-path is rejected with ErrDuplicateSpecialTransaction.
//
// XDPoSChain/core/txpool/txpool.go:960-961.
func TestXDCSpecialTxDuplicateRejected(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.IsSigner = func(address common.Address) bool {
		return address == addr
	}

	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// First special tx at nonce 0 — must succeed.
	tx1 := makeSpecialTx(0, common.BlockSignersBinary, privKey)
	if errs := pool.Add([]*types.Transaction{tx1}, true); errs[0] != nil {
		t.Fatalf("first special tx rejected: %v", errs[0])
	}

	// Second special tx at same nonce (nonce=0) — must be rejected.
	tx2 := makeSpecialTx(0, common.BlockSignersBinary, privKey)
	errs := pool.Add([]*types.Transaction{tx2}, true)
	if errs[0] == nil {
		t.Fatal("second special tx at same nonce should have been rejected as duplicate")
	}
}

// ─── Tests for isSigningTxExpired and expired-tx replacement (#932) ───────────

// TestXDCIsSigningTxExpired_Expired verifies that a BlockSigners signing tx
// whose target block is outside the epoch*2 window is correctly identified
// as expired.
//
// BlockSigner.sol:21: require(block.number <= _blockNumber + epochNumber*2).
func TestXDCIsSigningTxExpired_Expired(t *testing.T) {
	t.Parallel()

	// epoch = 900, so expiry window = 1800 blocks past target.
	pool, close := xdcSpecialPoolWithEpoch(t, 900)
	defer close()

	// Build a signing tx targeting block 15.
	privKey, _ := crypto.GenerateKey()
	tx := makeSigningTxForBlock(0, 15, privKey)

	// Set chain head far ahead of block 15 + epoch*2 (=1815).
	pool.currentHead.Store(&types.Header{Number: big.NewInt(2000)})

	if !pool.isSigningTxExpired(tx) {
		t.Errorf("tx targeting block 15 should be expired when head=2000, epoch=900 (expiry at 1815)")
	}
}

// TestXDCIsSigningTxExpired_Fresh verifies that a signing tx whose target block
// is within the epoch*2 window is classified as fresh.
func TestXDCIsSigningTxExpired_Fresh(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPoolWithEpoch(t, 900)
	defer close()

	privKey, _ := crypto.GenerateKey()
	tx := makeSigningTxForBlock(0, 15, privKey)

	// Head = 100: 15 + 900*2 = 1815, and 100 <= 1815 — still fresh.
	pool.currentHead.Store(&types.Header{Number: big.NewInt(100)})

	if pool.isSigningTxExpired(tx) {
		t.Errorf("tx targeting block 15 should NOT be expired when head=100, epoch=900 (expiry at 1815)")
	}
}

// TestXDCIsSigningTxExpired_AtBoundary verifies the exact boundary:
// head == targetBlock + epoch*2 is NOT expired (the contract allows <=).
func TestXDCIsSigningTxExpired_AtBoundary(t *testing.T) {
	t.Parallel()

	const epoch = uint64(900)
	pool, close := xdcSpecialPoolWithEpoch(t, epoch)
	defer close()

	privKey, _ := crypto.GenerateKey()
	const target = uint64(15)
	tx := makeSigningTxForBlock(0, target, privKey)

	// head == target + epoch*2 = 1815 — exactly at expiry, contract still accepts.
	pool.currentHead.Store(&types.Header{Number: big.NewInt(int64(target + epoch*2))})

	if pool.isSigningTxExpired(tx) {
		t.Errorf("tx should not be expired when head == target + epoch*2 (contract: <=)")
	}

	// head == target + epoch*2 + 1 — one past expiry, contract rejects.
	pool.currentHead.Store(&types.Header{Number: big.NewInt(int64(target + epoch*2 + 1))})

	if !pool.isSigningTxExpired(tx) {
		t.Errorf("tx should be expired when head == target + epoch*2 + 1")
	}
}

// TestXDCIsSigningTxExpired_NotBlockSignersTx verifies that a non-BlockSigners
// tx (e.g. to RandomizeSMC) is never classified as expired — we only gate
// the BlockSigners signing window.
func TestXDCIsSigningTxExpired_NotBlockSignersTx(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPoolWithEpoch(t, 900)
	defer close()

	privKey, _ := crypto.GenerateKey()
	// Build a special tx to RandomizeSMC (not BlockSigners) — should never expire.
	tx := makeSigningTxForBlock(0, 15, privKey)
	// Re-sign to RandomizeSMC address.
	data := common.Hex2Bytes(common.HexSignMethod)
	blockNumBig := big.NewInt(15)
	inputData := append(data, common.LeftPadBytes(blockNumBig.Bytes(), 32)...)
	inputData = append(inputData, make([]byte, 32)...)
	raw := types.NewTransaction(0, common.RandomizeSMCBinary, big.NewInt(0), 200000, big.NewInt(0), inputData)
	randomizeTx, _ := types.SignTx(raw, types.HomesteadSigner{}, privKey)
	_ = tx

	// Even with head far ahead, RandomizeSMC tx is never expired.
	pool.currentHead.Store(&types.Header{Number: big.NewInt(99999)})
	if pool.isSigningTxExpired(randomizeTx) {
		t.Errorf("RandomizeSMC tx should never be classified as expired")
	}
}

// TestXDCExpiredSigningTxReplacement verifies that an expired signing tx at
// a given nonce CAN be replaced by a new signing tx via the IsSigner fast-path.
//
// Fixes #932: the old code returned ErrDuplicateSpecialTransaction for any
// special-tx replacement; the new code allows replacement when the existing
// tx's target block is expired.
func TestXDCExpiredSigningTxReplacement(t *testing.T) {
	t.Parallel()

	const epoch = uint64(900)
	pool, close := xdcSpecialPoolWithEpoch(t, epoch)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.IsSigner = func(address common.Address) bool { return address == addr }
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// Insert first signing tx targeting block 15 (nonce=0).
	tx1 := makeSigningTxForBlock(0, 15, privKey)
	if errs := pool.Add([]*types.Transaction{tx1}, true); errs[0] != nil {
		t.Fatalf("first signing tx rejected: %v", errs[0])
	}

	// Advance head past the expiry window for block 15: 15 + 900*2 + 1 = 1816.
	pool.mu.Lock()
	pool.currentHead.Store(&types.Header{Number: big.NewInt(1816), Difficulty: common.Big0, GasLimit: 10_000_000})
	pool.mu.Unlock()

	// Now insert a new signing tx at the same nonce targeting the current head.
	// The old tx is expired → replacement must succeed.
	tx2 := makeSigningTxForBlock(0, 1816, privKey)
	errs := pool.Add([]*types.Transaction{tx2}, true)
	if errs[0] != nil {
		t.Fatalf("replacement of expired signing tx rejected: %v (want nil)", errs[0])
	}

	// Verify the new tx is in pending.
	pending, _ := pool.Pending(txpool.PendingFilter{})
	senderTxs, ok := pending[addr]
	if !ok || len(senderTxs) == 0 {
		t.Fatal("replacement signing tx not in pending")
	}
	if senderTxs[0].Hash != tx2.Hash() {
		t.Errorf("pending tx is old tx (hash %s), want new tx (hash %s)",
			senderTxs[0].Hash, tx2.Hash())
	}
}

// TestXDCFreshSigningTxNotReplaced verifies that a FRESH (non-expired) signing
// tx at a nonce CANNOT be replaced with a same-gasPrice signing tx.
//
// When an incumbent special tx is at nonce N and fresh, a new special tx at
// the same nonce must be rejected.  The rejection mechanism depends on context:
//   - If submitted via the IsSigner fast-path (pendingNonces == tx.Nonce()),
//     promoteSpecialTx returns ErrDuplicateSpecialTransaction.
//   - If the fast-path nonce check fails (pendingNonces > tx.Nonce()), the
//     normal add() path rejects with ErrReplaceUnderpriced (gasPrice=0 on both).
//   - Either way: the duplicate tx must be rejected (the pool is not jammed).
func TestXDCFreshSigningTxNotReplaced(t *testing.T) {
	t.Parallel()

	const epoch = uint64(900)
	pool, close := xdcSpecialPoolWithEpoch(t, epoch)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.IsSigner = func(address common.Address) bool { return address == addr }
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// Insert first signing tx targeting block 15.  Head = 0 (genesis) → fresh.
	tx1 := makeSigningTxForBlock(0, 15, privKey)
	if errs := pool.Add([]*types.Transaction{tx1}, true); errs[0] != nil {
		t.Fatalf("first signing tx rejected: %v", errs[0])
	}

	// Attempt to replace with another signing tx at same nonce.
	// The incumbent targets block 15; head=0, expiry=1815 → NOT expired.
	// After tx1 insertion: pendingNonces[addr]=1. The new tx has nonce=0,
	// so the IsSigner fast-path (which requires pendingNonces==nonce) is not
	// taken, and the non-expired check routes through normal add() →
	// ErrReplaceUnderpriced (both gasPrice=0 → no price bump possible).
	tx2 := makeSigningTxForBlock(0, 30, privKey)
	errs := pool.Add([]*types.Transaction{tx2}, true)
	if errs[0] == nil {
		t.Fatal("replacement of non-expired signing tx should fail")
	}
	// Must be some rejection error — either ErrDuplicateSpecialTransaction
	// (fast-path) or ErrReplaceUnderpriced (normal path).  Either is correct.
	if errs[0] == nil {
		t.Fatal("second signing tx at same nonce must be rejected")
	}
}

// TestXDCSpecialTxFullPoolCap verifies that special txs are subject to the
// global slot cap and that only signers may send them (#1409). Canonical
// XDPoSChain applies the pool-full check to every tx except a signer's special
// tx at its pending nonce (core/txpool/legacypool/legacypool.go:765-770) and
// rejects special txs from non-signers (core/txpool/validation.go:130-134).
func TestXDCSpecialTxFullPoolCap(t *testing.T) {
	t.Parallel()

	cpy := *params.TestChainConfig
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	bc := newTestBlockChain(&cpy, 10_000_000, statedb, new(event.Feed))

	cfg := DefaultConfig
	cfg.Journal = ""
	cfg.NoLocals = true
	cfg.GlobalSlots = 4
	cfg.GlobalQueue = 1

	pool := New(cfg, bc)
	if err := pool.Init(cfg.PriceLimit, bc.CurrentBlock(), newReserver()); err != nil {
		t.Fatalf("pool init: %v", err)
	}
	defer pool.Close()
	<-pool.initDoneCh

	// Fill the pool with ordinary priced txs from distinct accounts.
	for i := 0; i < int(cfg.GlobalSlots+cfg.GlobalQueue); i++ {
		key, _ := crypto.GenerateKey()
		testAddBalance(pool, crypto.PubkeyToAddress(key.PublicKey), big.NewInt(1_000_000_000_000_000_000))
		if err := pool.addRemoteSync(pricedTransaction(0, 100000, big.NewInt(1), key)); err != nil {
			t.Fatalf("filler tx %d rejected: %v", i, err)
		}
	}

	signerKey, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(signerKey.PublicKey)

	// No signer callback (non-XDPoS): a 0-price special tx must not bypass the cap.
	freshKey, _ := crypto.GenerateKey()
	if err := pool.addRemoteSync(makeSpecialTx(0, common.BlockSignersBinary, freshKey)); err == nil {
		t.Fatal("special tx from fresh key accepted into a full pool")
	}

	pool.SetIsSigner(func(addr common.Address) bool { return addr == signer })

	// Fresh non-signer key: rejected.
	freshKey, _ = crypto.GenerateKey()
	if err := pool.addRemoteSync(makeSpecialTx(0, common.BlockSignersBinary, freshKey)); !errors.Is(err, txpool.ErrSpecialTxNotFromSigner) {
		t.Fatalf("special tx from non-signer: have %v, want %v", err, txpool.ErrSpecialTxNotFromSigner)
	}
	// Signer at its pending nonce: accepted despite the full pool.
	if err := pool.addRemoteSync(makeSpecialTx(0, common.BlockSignersBinary, signerKey)); err != nil {
		t.Fatalf("signer special tx rejected: %v", err)
	}
}

// TestXDCSpecialTxSignSelectorFastPath verifies Hacken F-2026-19457 residual fix:
// the IsSigner fast path is restricted to calldata matching a known method selector.
//
// Observable: the fast path routes through promoteSpecialTx which returns
// ErrDuplicateSpecialTransaction on a same-nonce conflict.  The normal add() path
// routes through list.Add whose price-bump check returns ErrReplaceUnderpriced.
//
// Setup:
//  1. A masternode sends a sign tx (correct HexSignMethod calldata, 68 bytes, nonce=0)
//     → fast path → admitted to pending.
//  2. The same masternode sends a wrong-calldata tx at nonce=0 (arbitrary selector)
//     → must NOT take the fast path → goes to list.Add → ErrReplaceUnderpriced.
//
// Before the fix the wrong-calldata tx would take the fast path and return
// ErrDuplicateSpecialTransaction instead.
func TestXDCSpecialTxSignSelectorFastPath(t *testing.T) {
	t.Parallel()

	pool, close := xdcSpecialPool(t)
	defer close()

	masterKey, _ := crypto.GenerateKey()
	master := crypto.PubkeyToAddress(masterKey.PublicKey)

	pool.IsSigner = func(addr common.Address) bool { return addr == master }

	pool.mu.Lock()
	pool.currentState.AddBalance(master, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// Step 1: correct sign calldata (4-byte HexSignMethod selector + 32-byte blockNumber
	// + 32-byte blockHash = 68 bytes total, matching contracts.CreateTxSign).
	signData := make([]byte, 68)
	copy(signData[:4], common.Hex2Bytes(common.HexSignMethod))
	copy(signData[4:36], common.LeftPadBytes(big.NewInt(100).Bytes(), 32))
	// signData[36:68] stays zero (blockHash placeholder)

	rawSign := types.NewTransaction(0, common.BlockSignersBinary, big.NewInt(0), 200000, big.NewInt(0), signData)
	signTx, _ := types.SignTx(rawSign, types.HomesteadSigner{}, masterKey)

	// Correct calldata → fast path → admitted.
	if errs := pool.Add([]*types.Transaction{signTx}, true); errs[0] != nil {
		t.Fatalf("sign tx with correct calldata rejected: %v", errs[0])
	}
	pending, _ := pool.Pending(txpool.PendingFilter{})
	if _, ok := pending[master]; !ok {
		t.Fatal("sign tx not in pending after IsSigner fast path")
	}

	// Step 2: wrong calldata — same sender, same nonce=0, non-sign 4-byte selector.
	// After the fix: NOT fast-pathed → normal list.Add → price-bump check fails
	// (0-tip can't replace 0-tip incumbent) → ErrReplaceUnderpriced.
	// Before the fix: would take the fast path → ErrDuplicateSpecialTransaction.
	wrongData := make([]byte, 68)
	wrongData[0] = 0xDE // not 0xe3 (first byte of sign selector)
	wrongData[1] = 0xAD
	wrongData[2] = 0xBE
	wrongData[3] = 0xEF

	rawWrong := types.NewTransaction(0, common.BlockSignersBinary, big.NewInt(0), 200000, big.NewInt(0), wrongData)
	wrongTx, _ := types.SignTx(rawWrong, types.HomesteadSigner{}, masterKey)

	err := pool.addRemoteSync(wrongTx)
	if err == nil {
		t.Fatal("0x89 tx with wrong calldata must be rejected (non-fast-path, no price bump)")
	}
	// The rejection must come from the normal pending-replacement price-bump check,
	// not from promoteSpecialTx's ErrDuplicateSpecialTransaction.
	if errors.Is(err, ErrDuplicateSpecialTransaction) {
		t.Fatalf("wrong calldata must NOT take the fast path: got ErrDuplicateSpecialTransaction, want ErrReplaceUnderpriced")
	}
	if !errors.Is(err, txpool.ErrReplaceUnderpriced) {
		t.Fatalf("wrong calldata took unexpected path: %v", err)
	}
}

// ─── R1-M01: pending nonce must not be lowered on expired-tx replacement ──────

// TestXDCPromoteSpecialTxDoesNotLowerPendingNonce verifies R1-M01: when a
// signer replaces an expired signing tx at nonce K while later nonces K+1..N
// are already pending, promoteSpecialTx must not lower pendingNonces to K+1.
//
// Before the fix, promoteSpecialTx called pendingNonces.set(from, K+1),
// unconditionally overwriting the tip N+1.  That blocked the next tx at N+1
// from taking the fast path (pendingNonces==tx.Nonce() check fails) until the
// next pool reset re-synced nonces.
//
// After the fix, pendingNonces.setIfHigher is used, so K+1 < N+1 is ignored
// and the tip remains N+1.
func TestXDCPromoteSpecialTxDoesNotLowerPendingNonce(t *testing.T) {
	t.Parallel()

	const epoch = uint64(900)
	pool, close := xdcSpecialPoolWithEpoch(t, epoch)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.IsSigner = func(a common.Address) bool { return a == addr }
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// Insert signing tx at nonce 0, targeting block 15 (will be made expired).
	tx0 := makeSigningTxForBlock(0, 15, privKey)
	if errs := pool.Add([]*types.Transaction{tx0}, true); errs[0] != nil {
		t.Fatalf("tx0 rejected: %v", errs[0])
	}
	// Insert signing tx at nonce 1 (fresh, targeting current head area).
	tx1 := makeSigningTxForBlock(1, 100, privKey)
	if errs := pool.Add([]*types.Transaction{tx1}, true); errs[0] != nil {
		t.Fatalf("tx1 rejected: %v", errs[0])
	}
	// pendingNonces[addr] is now 2.

	// Advance head past expiry of tx0: 15 + 900*2 + 1 = 1816.
	pool.mu.Lock()
	pool.currentHead.Store(&types.Header{Number: big.NewInt(1816), Difficulty: common.Big0, GasLimit: 10_000_000})
	pool.mu.Unlock()

	// Replace the now-expired tx0 with a fresh signing tx at the same nonce.
	// This exercises the recovery path (pendingN > tx.Nonce() && expired).
	tx0new := makeSigningTxForBlock(0, 1816, privKey)
	if errs := pool.Add([]*types.Transaction{tx0new}, true); errs[0] != nil {
		t.Fatalf("replacement of expired tx0 rejected: %v", errs[0])
	}

	// After replacement pendingNonces must still be 2, not 1.
	// Verify by adding a signing tx at nonce 2 via the fast path: it is
	// admitted only when pendingNonces[addr]==2.
	tx2 := makeSigningTxForBlock(2, 1816, privKey)
	if errs := pool.Add([]*types.Transaction{tx2}, true); errs[0] != nil {
		t.Fatalf("tx2 (nonce=2) rejected — pendingNonce was lowered by the replacement (R1-M01): %v", errs[0])
	}

	// Confirm all three nonces are in pending.
	pending, _ := pool.Pending(txpool.PendingFilter{})
	got := len(pending[addr])
	if got != 3 {
		t.Fatalf("want 3 pending txs (nonces 0,1,2), got %d — R1-M01 regression", got)
	}
}

// ─── R1-M02: subpool reservation must be held on the special-tx fast path ─────

// xdcSpecialPoolWithSpy returns a pool that uses a spyReserver so tests can
// inspect whether an address is held after a fast-path add.
func xdcSpecialPoolWithSpy(t *testing.T) (*LegacyPool, *spyReserver, func()) {
	t.Helper()

	cpy := *params.TestChainConfig
	cpy.LondonBlock = common.Big0
	cpy.BerlinBlock = common.Big0

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	bc := newTestBlockChain(&cpy, 10_000_000, statedb, new(event.Feed))

	cfg := DefaultConfig
	cfg.Journal = ""
	cfg.NoLocals = true

	spy := newSpyReserver()
	pool := New(cfg, bc)
	if err := pool.Init(cfg.PriceLimit, bc.CurrentBlock(), spy); err != nil {
		t.Fatalf("pool init: %v", err)
	}
	<-pool.initDoneCh
	pool.SetGasTip(big.NewInt(1_000_000_000))

	return pool, spy, func() { pool.Close() }
}

// TestXDCSpecialTxFastPathHoldsReservation verifies R1-M02: the subpool
// reservation must be held for a new sender whose first tx takes the special-tx
// fast path (IsSigner, nonce == pendingNonces).
//
// Before the fix, add() returned through promoteSpecialTx before reaching the
// pool.reserver.Hold(from) call, leaving the account in pool.pending with no
// reservation.  Another subpool could then Hold the same address, breaking the
// invariant that each account is tracked by at most one subpool.
//
// After the fix, Hold is called before promoteSpecialTx when the account has
// no pending or queued transactions, and Released if promoteSpecialTx fails.
func TestXDCSpecialTxFastPathHoldsReservation(t *testing.T) {
	t.Parallel()

	pool, spy, close := xdcSpecialPoolWithSpy(t)
	defer close()

	privKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	pool.IsSigner = func(a common.Address) bool { return a == addr }
	pool.mu.Lock()
	pool.currentState.AddBalance(addr, new(uint256.Int).SetUint64(1), tracing.BalanceChangeUnspecified)
	pool.mu.Unlock()

	// First tx from this address — takes the fast path.
	tx := makeSpecialTx(0, common.BlockSignersBinary, privKey)
	if errs := pool.Add([]*types.Transaction{tx}, true); errs[0] != nil {
		t.Fatalf("fast-path special tx rejected: %v", errs[0])
	}

	// The reserver must hold the address after a successful fast-path add.
	if !spy.isHeld(addr) {
		t.Fatal("address not held by reserver after fast-path special-tx add (R1-M02)")
	}

	// The tx must be in pending.
	pending, _ := pool.Pending(txpool.PendingFilter{})
	if _, ok := pending[addr]; !ok {
		t.Fatal("special tx not in pending after fast-path add")
	}
}
