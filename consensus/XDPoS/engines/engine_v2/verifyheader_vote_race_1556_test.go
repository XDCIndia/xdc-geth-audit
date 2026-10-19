// Copyright (c) 2026 XDC Network
// #1556 (same class as #969): votes for a block arrive before the block. The
// vote path looks the block up by hash only (vote.go verifyVoteMessage →
// getEpochSwitchInfo(chain, nil, hash)) and fails with "header not found".
// That failure must not leak into a concurrent verifyHeader of the same block,
// which holds the header and can resolve its masternodes. Canonical XDPoSChain
// has no shared lookup: each getEpochSwitchInfo call resolves its own header
// (v2.9.3-testnet epochSwitch.go:16-36).
//
// Run: go test ./consensus/XDPoS/engines/engine_v2/ -run TestVoteRace1556 -count=1

package engine_v2

import (
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// voteRaceChain serves ltChain, except that looking up the not-yet-stored block
// by hash is slow and finds nothing, like a vote handler racing the block.
type voteRaceChain struct {
	*ltChain
	pending common.Hash
	gate    func()
}

func (c *voteRaceChain) GetHeaderByHash(h common.Hash) *types.Header {
	if h == c.pending {
		c.gate()
		return nil
	}
	return c.ltChain.GetHeaderByHash(h)
}

// A by-hash lookup of an unstored block that is in flight must not answer a
// concurrent lookup that carries the header.
func TestVoteRace1556_HeaderLookupNotPoisoned(t *testing.T) {
	eng, chain, keys, mns, _, h := ltFixture(t, 1799)
	ltSeal(t, h, keys[3])

	entered, release := make(chan struct{}, 1), make(chan struct{})
	rc := &voteRaceChain{ltChain: chain, pending: h.Hash(), gate: func() { entered <- struct{}{}; <-release }}
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()

	voteErr := make(chan error, 1)
	go func() { _, err := eng.getEpochSwitchInfo(rc, nil, h.Hash()); voteErr <- err }()
	<-entered

	type result struct {
		info *types.EpochSwitchInfo
		err  error
	}
	res := make(chan result, 1)
	go func() { info, err := eng.getEpochSwitchInfo(rc, h, h.Hash()); res <- result{info, err} }()

	var r result
	select {
	case r = <-res:
	case <-time.After(500 * time.Millisecond):
		unblock() // the header lookup waited on the vote lookup
		r = <-res
	}
	unblock()
	if r.err != nil {
		t.Fatalf("lookup with header got the vote lookup's error: %v", r.err)
	}
	if len(r.info.Masternodes) != len(mns) {
		t.Fatalf("masternodes: got %d, want %d", len(r.info.Masternodes), len(mns))
	}
	if err := <-voteErr; err == nil {
		t.Fatal("by-hash lookup of an unstored block must still fail")
	}
}

// Gossip path (parents == nil) under a stream of early votes for the block:
// verifyHeader must accept the canonical block, not report "empty masternode list".
func TestVoteRace1556_VerifyHeaderUnderEarlyVotes(t *testing.T) {
	for i := 0; i < 20; i++ {
		eng, chain, keys, _, _, h := ltFixture(t, 1799)
		ltSeal(t, h, keys[3])
		rc := &voteRaceChain{ltChain: chain, pending: h.Hash(), gate: func() { time.Sleep(20 * time.Millisecond) }}

		stop := make(chan struct{})
		var wg sync.WaitGroup
		started := make(chan struct{}, 4)
		for v := 0; v < 4; v++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				started <- struct{}{}
				for {
					select {
					case <-stop:
						return
					default:
						eng.getEpochSwitchInfo(rc, nil, h.Hash())
					}
				}
			}()
		}
		for v := 0; v < 4; v++ {
			<-started
		}
		time.Sleep(time.Millisecond)
		err := eng.verifyHeader(rc, h, nil, true)
		close(stop)
		wg.Wait()
		if err != nil {
			t.Fatalf("run %d: canonical block rejected under early votes: %v", i, err)
		}
	}
}

// Missing parent: retry-later, never a masternode error (verifyHeader.go:62-63,
// canonical verifyHeader.go:59-60).
func TestVoteRace1556_MissingParentIsUnknownAncestor(t *testing.T) {
	eng, chain, keys, _, p, h := ltFixture(t, 1799)
	ltSeal(t, h, keys[3])
	delete(chain.byHash, p.Hash())
	delete(chain.byNumber, p.Number.Uint64())
	if err := eng.verifyHeader(chain, h, nil, true); !errors.Is(err, consensus.ErrUnknownAncestor) {
		t.Fatalf("want ErrUnknownAncestor, got %v", err)
	}
}

// Known ancestry whose epoch has no resolvable masternodes (epoch switch without
// Validators, no gap snapshot) is still rejected, and not as unknown ancestor.
func TestVoteRace1556_KnownAncestryEmptySetRejected(t *testing.T) {
	cfg := &params.ChainConfig{
		ChainID: big.NewInt(551),
		XDPoS: &params.XDPoSConfig{Epoch: 900, Gap: 450, V2: &params.V2{
			SwitchBlock:   big.NewInt(0),
			CurrentConfig: &params.V2Config{MaxMasternodes: 18, MinePeriod: 2, TimeoutPeriod: 10, CertThreshold: 2.0 / 3.0},
			AllConfigs:    map[uint64]*params.V2Config{},
		}},
	}
	eng := createEngine(cfg, rawdb.NewMemoryDatabase(), nil, nil)
	eng.isInitialized = true
	keys, _ := genNKeys(t, 4)
	chain := &ltChain{cfg: cfg, byHash: map[common.Hash]*types.Header{}, byNumber: map[uint64]*types.Header{}}
	now := uint64(time.Now().Unix())

	pre := &types.Header{Number: big.NewInt(900), Difficulty: big.NewInt(1), Extra: []byte{0x02}}
	e1 := ltV2Header(t, 901, pre, 900, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: pre.Hash(), Round: 899, Number: big.NewInt(900)},
	}, now-200)
	chain.add(e1)
	p := ltV2Header(t, 902, e1, 1798, &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{Hash: e1.Hash(), Round: 900, Number: big.NewInt(901)},
		GapNumber:         450,
	}, now-100)
	chain.add(p)
	pInfo := &types.BlockInfo{Hash: p.Hash(), Round: 1798, Number: big.NewInt(902)}
	h := ltV2Header(t, 903, p, 1799, &types.QuorumCert{
		ProposedBlockInfo: pInfo,
		Signatures:        makeQCSignatures(t, pInfo, 450, keys),
		GapNumber:         450,
	}, now-50)
	ltSeal(t, h, keys[3])

	if mns := eng.GetMasternodesWithParents(chain, h, nil); len(mns) != 0 {
		t.Fatalf("fixture must have an empty set, got %d masternodes", len(mns))
	}
	err := eng.verifyHeader(chain, h, nil, true)
	if err == nil || errors.Is(err, consensus.ErrUnknownAncestor) {
		t.Fatalf("empty set with known ancestry: want a rejection, got %v", err)
	}
}
