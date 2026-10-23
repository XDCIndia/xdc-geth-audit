// Copyright 2026 XDC Network

package engine_v2

// TestCalcDifficultySignerRaceV2 exposes the data race between Authorize
// (writes x.signer under x.signLock.Lock()) and CalcDifficulty (reads
// x.signer without holding x.signLock, then passes it to calcDifficulty
// which ignores it and returns big.NewInt(1) anyway).
//
// Under -race the test fails on origin/main and passes after the fix that
// returns big.NewInt(1) directly from CalcDifficulty without reading x.signer.
//
// Related: issue #1785, review R2-W03.

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestCalcDifficultySignerRaceV2(t *testing.T) {
	engine, _ := newA894Engine(t)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := crypto.PubkeyToAddress(key.PublicKey)
	signFn := func(_ accounts.Account, hash []byte) ([]byte, error) {
		return crypto.Sign(hash, key)
	}

	parent := &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(1)}

	const iters = 400
	var wg sync.WaitGroup
	for i := 0; i < iters; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Writer: stores x.signer under x.signLock.Lock().
			engine.Authorize(signer, signFn)
		}()
		go func() {
			defer wg.Done()
			// Reader (pre-fix): reads x.signer without lock before calling
			// calcDifficulty, which ignores the signer value entirely.
			engine.CalcDifficulty(nil, 0, parent)
		}()
	}
	wg.Wait()
}
