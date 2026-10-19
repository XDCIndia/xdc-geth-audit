// Copyright 2026 XDC Network

package XDPoS

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// v1TurnChain serves the epoch-0 checkpoint header that lists the masternodes.
type v1TurnChain struct {
	cfg *params.ChainConfig
	cp  *types.Header
}

func (c v1TurnChain) Config() *params.ChainConfig                 { return c.cfg }
func (c v1TurnChain) CurrentHeader() *types.Header                { return nil }
func (c v1TurnChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (c v1TurnChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }
func (c v1TurnChain) GetHeaderByNumber(n uint64) *types.Header {
	if n == 0 {
		return c.cp
	}
	return nil
}

// TestV1YourTurnFollowsParentCreator checks the canonical XDPoSChain engine_v1
// turn rule: the masternode right after the parent's creator is in turn, and
// another masternode may seal only after 10 s per hop since the parent.
func TestV1YourTurnFollowsParentCreator(t *testing.T) {
	var mns []common.Address
	privs := map[common.Address][]byte{}
	for i := 0; i < 3; i++ {
		k, _ := crypto.GenerateKey()
		a := crypto.PubkeyToAddress(k.PublicKey)
		mns = append(mns, a)
		privs[a] = crypto.FromECDSA(k)
	}
	extra := make([]byte, extraVanity+len(mns)*common.AddressLength+extraSeal)
	for i, a := range mns {
		copy(extra[extraVanity+i*common.AddressLength:], a[:])
	}
	cp := &types.Header{Number: big.NewInt(0), Extra: extra}
	chain := v1TurnChain{&params.ChainConfig{ChainID: big.NewInt(2012), XDPoS: testConfig}, cp}
	engine := New(testConfig, rawdb.NewMemoryDatabase())

	// Parent block 5 (6 % 3 == 0 would make mns[0] the "position rule" owner),
	// sealed by mns[1], so mns[2] is in turn and mns[0] is one hop away.
	parent := func(age int64) *types.Header {
		h := &types.Header{
			Number:     big.NewInt(5),
			Difficulty: big.NewInt(3),
			Extra:      make([]byte, extraVanity+extraSeal),
			UncleHash:  types.EmptyUncleHash,
			GasLimit:   420000000,
			Time:       uint64(time.Now().Unix() - age),
		}
		key, _ := crypto.ToECDSA(privs[mns[1]])
		sig, err := crypto.Sign(sigHash(h).Bytes(), key)
		if err != nil {
			t.Fatal(err)
		}
		copy(h.Extra[len(h.Extra)-extraSeal:], sig)
		engine.recents.Add(h.Hash(), newSnapshot(testConfig, engine.signatures, 5, h.Hash(), mns))
		return h
	}
	turn := func(p *types.Header, signer common.Address) bool {
		_, _, _, ok, err := engine.YourTurn(chain, p, signer)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	fresh := parent(1)
	if !turn(fresh, mns[2]) {
		t.Error("masternode after the parent's creator must be in turn")
	}
	if turn(fresh, mns[0]) {
		t.Error("masternode one hop away must wait 10s after the parent")
	}
	if turn(fresh, mns[1]) {
		t.Error("the parent's creator must wait two hops")
	}
	old := parent(11)
	if !turn(old, mns[0]) {
		t.Error("masternode one hop away may seal 10s after the parent")
	}
	if turn(old, mns[1]) {
		t.Error("the parent's creator must wait 20s")
	}
}
