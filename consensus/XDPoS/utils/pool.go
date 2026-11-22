// Copyright (c) 2024 XDC Network
// Pool implementation for XDPoS 2.0 vote and timeout messages

package utils

import (
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// PoolObj is the interface for objects that can be added to the pool
type PoolObj interface {
	Hash() common.Hash
	PoolKey() string
	GetSigner() common.Address
	SetSigner(common.Address)
}

// Pool is a thread-safe message pool for votes and timeouts
type Pool struct {
	lock sync.RWMutex
	// Key: PoolKey (round:gapNumber:number:hash for votes, round:gapNumber for timeouts)
	// Value: map of message hash -> PoolObj
	pool map[string]map[common.Hash]PoolObj
	// signerRound keeps one message per recovered signer per round, across
	// every block hash, gap and block number. The map value is the pool key
	// that holds that signer's entry.
	signerRound map[string]string
}

// NewPool creates a new Pool
func NewPool() *Pool {
	return &Pool{
		pool:        make(map[string]map[common.Hash]PoolObj),
		signerRound: make(map[string]string),
	}
}

// signerSlotID names the one slot that a recovered signer may hold. For a vote
// (key round:gap:number:hash) the slot is per round, across every block, gap
// and number. For a timeout (key round:gap) it is per round and gap: a signer
// that sends its timeout again with the gap number of a new epoch inside one
// round is an honest signer, and canonical keeps both entries (#2089).
func signerSlotID(signer common.Address, key string) (string, bool) {
	round, rest, ok := strings.Cut(key, ":")
	if !ok || round == "" {
		return "", false
	}
	if !strings.Contains(rest, ":") {
		return signer.Hex() + ":" + key, true
	}
	return signer.Hex() + ":" + round, true
}

// Add adds an object to the pool
// Returns the count of objects with the same pool key and all objects with that key.
//
// At most one entry is kept per (pool key, recovered signer).  Once a signer
// address has been recovered via SetSigner/GetSigner, any later message from
// the same signer for the same pool key is dropped regardless of whether its
// Hash() differs (e.g. due to a re-signing or ECDSA malleation).  Objects
// whose signer has not been set yet (zero address) bypass this check and are
// deduplicated by Hash() only, as before (CertiK XDC-07).
func (p *Pool) Add(obj PoolObj) (int, map[common.Hash]PoolObj) {
	p.lock.Lock()
	defer p.lock.Unlock()

	key := obj.PoolKey()
	hash := obj.Hash()

	if p.pool[key] == nil {
		p.pool[key] = make(map[common.Hash]PoolObj)
	}

	// One recovered signer may occupy one slot per round, whichever block hash,
	// gap or number the message carries. A later message from that signer in
	// the same round is dropped (XDC-07, F-2026-20590).
	incomingSigner := obj.GetSigner()
	if incomingSigner != (common.Address{}) {
		if id, ok := signerSlotID(incomingSigner, key); ok {
			if _, held := p.signerRound[id]; held {
				if len(p.pool[key]) == 0 {
					delete(p.pool, key)
				}
				result := make(map[common.Hash]PoolObj)
				for k, v := range p.pool[key] {
					result[k] = v
				}
				return len(result), result
			}
		}
		for _, existing := range p.pool[key] {
			if existing.GetSigner() == incomingSigner {
				result := make(map[common.Hash]PoolObj)
				for k, v := range p.pool[key] {
					result[k] = v
				}
				return len(p.pool[key]), result
			}
		}
	}

	// Only add if not already present by hash
	if _, exists := p.pool[key][hash]; !exists {
		p.pool[key][hash] = obj
		if incomingSigner != (common.Address{}) {
			if id, ok := signerSlotID(incomingSigner, key); ok {
				p.signerRound[id] = key
			}
		}
	}

	// Return a copy of the pool content for this key
	result := make(map[common.Hash]PoolObj)
	for k, v := range p.pool[key] {
		result[k] = v
	}

	return len(p.pool[key]), result
}

// Get returns the entire pool
func (p *Pool) Get() map[string]map[common.Hash]PoolObj {
	p.lock.RLock()
	defer p.lock.RUnlock()

	result := make(map[string]map[common.Hash]PoolObj)
	for key, objects := range p.pool {
		result[key] = make(map[common.Hash]PoolObj)
		for hash, obj := range objects {
			result[key][hash] = obj
		}
	}
	return result
}

// GetByPoolKey returns all objects for a specific pool key
func (p *Pool) GetByPoolKey(key string) map[common.Hash]PoolObj {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.pool[key] == nil {
		return nil
	}

	result := make(map[common.Hash]PoolObj)
	for hash, obj := range p.pool[key] {
		result[hash] = obj
	}
	return result
}

// PoolObjKeysList returns all pool keys
func (p *Pool) PoolObjKeysList() []string {
	p.lock.RLock()
	defer p.lock.RUnlock()

	keys := make([]string, 0, len(p.pool))
	for key := range p.pool {
		keys = append(keys, key)
	}
	return keys
}

// Clear clears the entire pool
func (p *Pool) Clear() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.pool = make(map[string]map[common.Hash]PoolObj)
	p.signerRound = make(map[string]string)
}

// ClearByPoolKey clears objects for a specific pool key
func (p *Pool) ClearByPoolKey(key string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	// Drop every slot that points at this key. Do not look the slots up by the
	// stored objects: verifyVotes blanks a vote's signer after it was pooled,
	// which would leave its slot behind (#2089).
	for id, k := range p.signerRound {
		if k == key {
			delete(p.signerRound, id)
		}
	}
	delete(p.pool, key)
}

// Size returns the total number of objects in the pool
func (p *Pool) Size() int {
	p.lock.RLock()
	defer p.lock.RUnlock()

	count := 0
	for _, objects := range p.pool {
		count += len(objects)
	}
	return count
}

// GetObjsByKey returns all objects matching the given pool key.
func (p *Pool) GetObjsByKey(key string) map[common.Hash]PoolObj {
	p.lock.RLock()
	defer p.lock.RUnlock()

	result := make(map[common.Hash]PoolObj)
	if items, ok := p.pool[key]; ok {
		for hash, obj := range items {
			result[hash] = obj
		}
	}
	return result
}
