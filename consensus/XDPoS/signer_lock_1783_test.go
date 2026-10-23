// Copyright 2026 XDC Network

package XDPoS

// TestSignerRaceCalcDifficulty exposes the data race between Authorize
// (which writes c.signer under c.lock.Lock()) and CalcDifficulty (which reads
// c.signer with no lock on the V1 path, original line 1876 of xdpos.go) and
// Prepare (which reads c.signer with no lock, original line 1432 of xdpos.go).
//
// Under -race the tests fail on origin/main and pass after the fix that adds
// c.lock.RLock()/RUnlock() around both c.signer reads.
//
// Related: issue #1783, review R2-W01.

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// w01NilChain is a ChainHeaderReader where every lookup returns nil.
// YourTurn→snapshot→GetHeaderByNumber(0) returns nil so calcDifficulty
// degrades to returning big.NewInt(0), which is fine — we only care that
// the race on c.signer is detectable.
type w01NilChain struct{ cfg *params.ChainConfig }

func (c w01NilChain) Config() *params.ChainConfig                     { return c.cfg }
func (c w01NilChain) CurrentHeader() *types.Header                    { return nil }
func (c w01NilChain) GetHeader(_ common.Hash, _ uint64) *types.Header { return nil }
func (c w01NilChain) GetHeaderByHash(_ common.Hash) *types.Header     { return nil }
func (c w01NilChain) GetHeaderByNumber(_ uint64) *types.Header        { return nil }

// TestSignerRaceCalcDifficulty races Authorize (c.signer writer) against
// CalcDifficulty (c.signer reader, V1 path) to expose the missing lock.
func TestSignerRaceCalcDifficulty(t *testing.T) {
	// V1-only engine: no V2 switch block, so CalcDifficulty always uses the
	// V1 code path that read c.signer without a lock (pre-fix).
	cfg := &params.XDPoSConfig{Period: 2, Epoch: 900, Gap: 450}
	engine := New(cfg, rawdb.NewMemoryDatabase())

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := crypto.PubkeyToAddress(key.PublicKey)
	signFn := func(_ accounts.Account, _ string, hash []byte) ([]byte, error) {
		return crypto.Sign(hash, key)
	}

	chainCfg := &params.ChainConfig{ChainID: big.NewInt(50), XDPoS: cfg}
	chain := w01NilChain{cfg: chainCfg}
	parent := &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(1)}

	const iters = 400
	var wg sync.WaitGroup
	for i := 0; i < iters; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Writer: updates c.signer under c.lock.Lock().
			engine.Authorize(signer, signFn)
		}()
		go func() {
			defer wg.Done()
			// Reader (pre-fix): reads c.signer with NO lock on the V1 path.
			engine.CalcDifficulty(chain, 0, parent)
		}()
	}
	wg.Wait()
}
