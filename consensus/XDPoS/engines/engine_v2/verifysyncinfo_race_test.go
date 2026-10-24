// Copyright (c) 2024 XDC Network
// Race-detection test for #1750: VerifySyncInfoMessage must snapshot
// highestQuorumCert and highestTimeoutCert under RLock.
//
// Run: go test -race -count=1 -run TestVerifySyncInfo_HighestCertRace ./consensus/XDPoS/engines/engine_v2/

package engine_v2

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestVerifySyncInfo_HighestCertRace calls VerifySyncInfoMessage concurrently
// with a goroutine that replaces highestQuorumCert and highestTimeoutCert
// under Lock.
//
// Bug (origin/main): VerifySyncInfoMessage reads x.highestQuorumCert and
// x.highestTimeoutCert at the staleness check with no lock held.  processQC
// and processTC (called under x.lock.Lock() from SyncInfoHandler and
// ProposedBlockHandler) replace both pointers.  Concurrent read and write on
// the same pointer — a data race the -race flag detects.
//
// Fix: snapshot x.highestQuorumCert.ProposedBlockInfo.Round and
// x.highestTimeoutCert.Round under x.lock.RLock() before the comparison.
func TestVerifySyncInfo_HighestCertRace(t *testing.T) {
	eng, _ := newA894Engine(t)
	eng.isInitialized = true
	eng.currentRound = 5
	eng.highestQuorumCert = &types.QuorumCert{
		ProposedBlockInfo: &types.BlockInfo{
			Round:  4,
			Number: big.NewInt(4),
			Hash:   common.HexToHash("0x4"),
		},
		Signatures: []types.Signature{},
	}
	eng.highestTimeoutCert = &types.TimeoutCert{
		Round:      3,
		Signatures: []types.Signature{},
	}

	// SyncInfo whose QC and TC rounds are behind the local state so
	// VerifySyncInfoMessage returns false at the staleness check — no further
	// network calls or snapshot lookups are made.
	syncInfo := &types.SyncInfo{
		HighestQuorumCert: &types.QuorumCert{
			ProposedBlockInfo: &types.BlockInfo{
				Round:  2,
				Number: big.NewInt(2),
				Hash:   common.HexToHash("0x2"),
			},
			Signatures: []types.Signature{},
		},
		HighestTimeoutCert: &types.TimeoutCert{
			Round:      1,
			Signatures: []types.Signature{},
		},
	}

	chain := &mockChainForA894{}
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer goroutine: continuously replaces highestQuorumCert and
	// highestTimeoutCert under Lock, exactly as processQC / processTC does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := types.Round(10)
		for {
			select {
			case <-stop:
				return
			default:
				eng.lock.Lock()
				eng.highestQuorumCert = &types.QuorumCert{
					ProposedBlockInfo: &types.BlockInfo{
						Round:  r,
						Number: big.NewInt(int64(r)),
						Hash:   common.HexToHash("0xaa"),
					},
					Signatures: []types.Signature{},
				}
				eng.highestTimeoutCert = &types.TimeoutCert{
					Round:      r - 1,
					Signatures: []types.Signature{},
				}
				r++
				eng.lock.Unlock()
			}
		}
	}()

	// Reader: VerifySyncInfoMessage reads the highest cert fields.
	// Under the bug the reads are unlocked → race with the writes above.
	for i := 0; i < 500; i++ {
		eng.VerifySyncInfoMessage(chain, syncInfo) //nolint:errcheck
	}

	close(stop)
	wg.Wait()
}
