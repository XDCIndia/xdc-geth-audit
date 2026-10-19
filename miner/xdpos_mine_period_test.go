// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package miner

import (
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	engine_v2 "github.com/ethereum/go-ethereum/consensus/XDPoS/engines/engine_v2"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// v2MintTestBuilt guards newV2MintTestWorker: engine_v2.New returns a process
// singleton that keeps the first genesis it was initialised on.
var v2MintTestBuilt atomic.Bool

// newV2MintTestWorker returns a V2-from-genesis worker whose coinbase is the
// only masternode, on a genesis stamped at genesisTime.
func newV2MintTestWorker(t *testing.T, genesisTime uint64) *XdcWorker {
	t.Helper()
	if !v2MintTestBuilt.CompareAndSwap(false, true) {
		t.Skip("engine_v2.New is a process singleton; run with -count=1")
	}
	key, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(key.PublicKey)

	cfg := *params.AllEthashProtocolChanges
	cfg.ChainID = big.NewInt(2002)
	cfg.ShanghaiTime, cfg.CancunTime, cfg.PragueTime, cfg.OsakaTime = nil, nil, nil, nil
	cfg.BlobScheduleConfig = nil
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900, Gap: 450, V2: &params.V2{
		SwitchBlock:   big.NewInt(0),
		CurrentConfig: &params.V2Config{MaxMasternodes: 18, MinePeriod: 2, TimeoutPeriod: 10, CertThreshold: 2.0 / 3.0},
		AllConfigs:    map[uint64]*params.V2Config{},
	}}
	extra := make([]byte, 32+20+65)
	copy(extra[32:], signer.Bytes())
	db := rawdb.NewMemoryDatabase()
	gspec := &core.Genesis{Config: &cfg, GasLimit: 30_000_000, Timestamp: genesisTime, ExtraData: extra}
	bc, err := core.NewBlockChain(db, gspec, ethash.NewFullFaker(), &core.BlockChainConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)

	v2 := engine_v2.New(bc.Config(), db, nil, nil)
	engine := XDPoS.New(cfg.XDPoS, db)
	engine.SetEngineV2(v2)
	engine.Authorize(signer, func(_ accounts.Account, _ string, hash []byte) ([]byte, error) {
		return crypto.Sign(hash, key)
	})
	w := &XdcWorker{
		engine:   engine,
		v2engine: v2,
		miner:    &Miner{chain: bc, chainConfig: bc.Config(), config: &Config{Recommit: time.Second}},
		synced:   &stubSynced{synced: true},
		coinbase: signer,
		quit:     make(chan struct{}),
	}
	w.mining.Store(1)
	return w
}

// TestXdcV2WorkerWaitsOutMinePeriod: the V2 sealer does not build on a parent
// younger than the mine period, as canonical checkPreCommit → YourTurn
// (engine_v2/engine.go:323-328) does not, so txs that reach the pool during the
// period make it into the block (#1594). Once the period has passed it builds.
func TestXdcV2WorkerWaitsOutMinePeriod(t *testing.T) {
	genesisTime := uint64(time.Now().Unix())
	w := newV2MintTestWorker(t, genesisTime)
	w.commitNewWork()
	if w.lastParentBlockCommit != "" {
		t.Fatal("built a block on a parent younger than the mine period")
	}

	time.Sleep(time.Until(time.Unix(int64(genesisTime)+2, 0)))
	w.commitNewWork()
	if w.lastParentBlockCommit == "" {
		t.Fatal("did not build a block once the mine period had passed")
	}
}

// TestXdcV1WorkerWaitsOutParentSecond: like canonical commitNewWork
// (miner/worker.go:808-818), the V1 sealer does not build in its parent's
// second, so a tx that reaches the pool then is in the block (#1598).
func TestXdcV1WorkerWaitsOutParentSecond(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := crypto.PubkeyToAddress(key.PublicKey)

	cfg := *params.AllEthashProtocolChanges
	cfg.ChainID = big.NewInt(2002)
	cfg.ShanghaiTime, cfg.CancunTime, cfg.PragueTime, cfg.OsakaTime = nil, nil, nil, nil
	cfg.BlobScheduleConfig = nil
	cfg.XDPoS = &params.XDPoSConfig{Period: 2, Epoch: 900, Gap: 450, V2: &params.V2{
		SwitchBlock:   big.NewInt(900),
		CurrentConfig: &params.V2Config{MaxMasternodes: 18, MinePeriod: 2, TimeoutPeriod: 10, CertThreshold: 2.0 / 3.0},
		AllConfigs:    map[uint64]*params.V2Config{},
	}}
	extra := make([]byte, 32+20+65)
	copy(extra[32:], signer.Bytes())

	// Start at the top of a second so the parent's second is nearly whole.
	time.Sleep(time.Until(time.Unix(time.Now().Unix()+1, 0)))
	db := rawdb.NewMemoryDatabase()
	engine := XDPoS.New(cfg.XDPoS, db)
	engine.Authorize(signer, func(_ accounts.Account, _ string, hash []byte) ([]byte, error) {
		return crypto.Sign(hash, key)
	})
	gspec := &core.Genesis{
		Config: &cfg, GasLimit: 30_000_000, Timestamp: uint64(time.Now().Unix()), ExtraData: extra,
		Alloc: types.GenesisAlloc{signer: {Balance: big.NewInt(params.Ether)}},
	}
	bc, err := core.NewBlockChain(db, gspec, engine, &core.BlockChainConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bc.Stop)
	pool, _ := txpool.New(testTxPoolConfig.PriceLimit, bc, []txpool.SubPool{legacypool.New(testTxPoolConfig, bc)})
	t.Cleanup(func() { pool.Close() })

	w := &XdcWorker{
		engine:   engine,
		miner:    &Miner{chain: bc, chainConfig: bc.Config(), txpool: pool, config: &Config{Recommit: time.Second, GasPrice: big.NewInt(1)}},
		synced:   &stubSynced{synced: true},
		coinbase: signer,
		quit:     make(chan struct{}),
	}
	w.mining.Store(1)

	done := make(chan struct{})
	go func() { w.commitNewWork(); close(done) }()
	time.Sleep(300 * time.Millisecond)
	tx := types.MustSignNewTx(key, types.LatestSigner(&cfg), &types.LegacyTx{
		To: &common.Address{1}, Gas: params.TxGas, GasPrice: big.NewInt(params.GWei),
	})
	if errs := pool.Add([]*types.Transaction{tx}, true); errs[0] != nil {
		t.Fatal(errs[0])
	}
	<-done

	block := bc.GetBlockByNumber(1)
	if block == nil {
		t.Fatal("no block minted")
	}
	if block.Transactions().Len() != 1 {
		t.Fatal("built the block in the parent's second, before the tx arrived")
	}
}
