package utils

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// checkpointSpy records, for every checkpoint header handed to VerifyHeaders,
// whether the chain head is the block before it (so its gap block and the
// signing state of its epoch are already in the chain).
type checkpointSpy struct {
	consensus.Engine
	epoch, gap uint64
	seen       int
	early      []uint64
}

func (s *checkpointSpy) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	for _, h := range headers {
		if n := h.Number.Uint64(); n > s.gap && n%s.epoch == 0 {
			s.seen++
			if head := chain.CurrentHeader(); head == nil || head.Number.Uint64() != n-1 {
				s.early = append(s.early, n)
			}
		}
	}
	return s.Engine.VerifyHeaders(chain, headers)
}

// TestImportChainStartsABatchAtEachCheckpoint: a V1 checkpoint is checked
// against the snapshot written when its gap block executes and against the
// signing state of its epoch at the chain head. geth import must not hand
// InsertChain a segment that holds a checkpoint together with blocks it depends
// on, or canonical history is rejected (mainnet blocks 62,100 and 783,900 on
// import with F-2026-19602 and F-2026-19462).
func TestImportChainStartsABatchAtEachCheckpoint(t *testing.T) {
	// Pre-London rules: the XDC base-fee check does not apply to ethash-faked blocks.
	cfg := params.ChainConfig{
		ChainID: big.NewInt(1), HomesteadBlock: big.NewInt(0), EIP150Block: big.NewInt(0),
		EIP155Block: big.NewInt(0), EIP158Block: big.NewInt(0), ByzantiumBlock: big.NewInt(0),
		XDPoS: &params.XDPoSConfig{Epoch: 10, Gap: 3, Period: 2},
	}
	gspec := &core.Genesis{Config: &cfg, Difficulty: common.Big1, GasLimit: 8_000_000, Alloc: types.GenesisAlloc{}}

	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 35, nil)
	src, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Stop()
	if _, err := src.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "chain.rlp")
	if err := ExportChain(src, file); err != nil {
		t.Fatal(err)
	}

	spy := &checkpointSpy{Engine: ethash.NewFaker(), epoch: 10, gap: 3}
	dst, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, spy, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Stop()
	if err := ImportChain(dst, file); err != nil {
		t.Fatal(err)
	}
	if got := dst.CurrentBlock().Number; got.Cmp(big.NewInt(35)) != 0 {
		t.Fatalf("imported head %v, want 35", got)
	}
	if spy.seen == 0 {
		t.Fatal("no checkpoint header was verified")
	}
	if len(spy.early) != 0 {
		t.Fatalf("checkpoints %v were verified while the chain head was not the block before them", spy.early)
	}
}

func TestImportCheckpointEpoch(t *testing.T) {
	if got := importCheckpointEpoch(params.AllEthashProtocolChanges); got != 0 {
		t.Fatalf("non-XDPoS epoch = %d, want 0", got)
	}
	cfg := *params.AllEthashProtocolChanges
	cfg.XDPoS = &params.XDPoSConfig{Epoch: 900, Gap: 450}
	if got := importCheckpointEpoch(&cfg); got != 900 {
		t.Fatalf("XDPoS epoch = %d, want 900", got)
	}
}

// TestAttachXDPoSV1Hooks: the chain that geth import uses has the V1 hooks, in
// particular the contract fallback for a checkpoint whose snapshot disagrees.
func TestAttachXDPoSV1Hooks(t *testing.T) {
	cfg := params.ChainConfig{
		ChainID: big.NewInt(50), HomesteadBlock: big.NewInt(0), EIP150Block: big.NewInt(0),
		EIP155Block: big.NewInt(0), EIP158Block: big.NewInt(0), ByzantiumBlock: big.NewInt(0),
		XDPoS: &params.XDPoSConfig{Epoch: 900, Gap: 450, Period: 2},
	}
	gspec := &core.Genesis{Config: &cfg, Difficulty: common.Big1, GasLimit: 8_000_000, Alloc: types.GenesisAlloc{}}
	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	engine := XDPoS.New(cfg.XDPoS, rawdb.NewMemoryDatabase())
	attachXDPoSV1Hooks(engine, bc, &cfg)
	if engine.HookGetSignersFromContract == nil || engine.HookVerifyMNs == nil {
		t.Fatal("V1 hooks were not attached to the XDPoS engine")
	}
	attachXDPoSV1Hooks(ethash.NewFaker(), bc, &cfg) // a non-XDPoS engine is left alone
}
