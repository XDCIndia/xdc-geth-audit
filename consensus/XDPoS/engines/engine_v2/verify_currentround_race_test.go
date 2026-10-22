// Copyright (c) 2024 XDC Network
// Race-detection tests for #1749: VerifyVoteMessage and VerifyTimeoutMessage
// must snapshot currentRound under RLock.
//
// Run: go test -race -count=1 -run TestVerifyVote_CurrentRoundRace ./consensus/XDPoS/engines/engine_v2/
// Run: go test -race -count=1 -run TestVerifyTimeout_CurrentRoundRace ./consensus/XDPoS/engines/engine_v2/

package engine_v2

import (
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestVerifyVote_CurrentRoundRace calls VerifyVoteMessage concurrently with a
// goroutine that updates currentRound under Lock.
//
// Bug (origin/main): VerifyVoteMessage reads x.currentRound after releasing
// the init lock, with no lock held.  setNewRound (called from
// SyncInfoHandler/ProposedBlockHandler) writes x.currentRound under
// x.lock.Lock().  Concurrent read and write on the same field — a data race
// the -race flag detects.
//
// Fix: snapshot x.currentRound under x.lock.RLock() before the comparison.
func TestVerifyVote_CurrentRoundRace(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.isInitialized = true
	eng.currentRound = 5

	// Vote with round 0: always < currentRound so VerifyVoteMessage returns
	// early at the round check without calling getSnapshot.
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	vote := makeVote(t, key, 0, 0, common.HexToHash("0x1"), 0)
	chain := &mockChainForA894{}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer goroutine: continuously advances currentRound under Lock,
	// exactly as SyncInfoHandler → processQC → setNewRound does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				eng.lock.Lock()
				eng.currentRound++
				eng.lock.Unlock()
			}
		}
	}()

	// Reader: VerifyVoteMessage reads x.currentRound.
	// Under the bug the read is unlocked → race with the write above.
	for i := 0; i < 500; i++ {
		eng.VerifyVoteMessage(chain, vote) //nolint:errcheck
	}

	close(stop)
	wg.Wait()
}

// TestVerifyTimeout_CurrentRoundRace mirrors TestVerifyVote_CurrentRoundRace
// for VerifyTimeoutMessage.
//
// Bug: VerifyTimeoutMessage reads x.currentRound after releasing the init
// lock, with no lock held.
//
// Fix: snapshot x.currentRound under x.lock.RLock() before the comparison.
func TestVerifyTimeout_CurrentRoundRace(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.isInitialized = true
	eng.currentRound = 5

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	timeoutMsg := makeTimeout(t, key, 0, 0)
	chain := &mockChainForA894{}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				eng.lock.Lock()
				eng.currentRound++
				eng.lock.Unlock()
			}
		}
	}()

	for i := 0; i < 500; i++ {
		eng.VerifyTimeoutMessage(chain, timeoutMsg) //nolint:errcheck
	}

	close(stop)
	wg.Wait()
}
