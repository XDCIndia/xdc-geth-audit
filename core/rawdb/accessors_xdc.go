// Copyright 2023 The XDC Network Authors
// This file is part of the XDC Network library.
//
// The XDC Network library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package rawdb

import (
	"encoding/binary"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

// headerTDKey returns the DB key for a block's cumulative total difficulty.
// Format matches the canonical XDPoSChain legacy key:
//
//	headerPrefix + num (uint64 big-endian) + hash + headerTDSuffix
//
// ("h" + 8-byte number + 32-byte hash + "t").
func headerTDKey(number uint64, hash common.Hash) []byte {
	enc := make([]byte, 8)
	binary.BigEndian.PutUint64(enc, number)
	key := make([]byte, 0, len(headerPrefix)+8+common.HashLength+len(headerTDSuffix))
	key = append(key, headerPrefix...)
	key = append(key, enc...)
	key = append(key, hash.Bytes()...)
	key = append(key, headerTDSuffix...)
	return key
}

// ReadTd retrieves a block's total difficulty from the DB, nil if absent.
func ReadTd(db ethdb.Reader, hash common.Hash, number uint64) *big.Int {
	data, err := db.Get(headerTDKey(number, hash))
	if err != nil || len(data) == 0 {
		return nil
	}
	td := new(big.Int)
	if err := rlp.DecodeBytes(data, td); err != nil {
		log.Error("XDC ReadTd: invalid RLP", "hash", hash, "number", number, "err", err)
		return nil
	}
	return td
}

// WriteTd persists a block's cumulative total difficulty.
func WriteTd(db ethdb.KeyValueWriter, hash common.Hash, number uint64, td *big.Int) {
	data, err := rlp.EncodeToBytes(td)
	if err != nil {
		log.Crit("XDC WriteTd: RLP encode failed", "err", err)
	}
	if err := db.Put(headerTDKey(number, hash), data); err != nil {
		log.Crit("XDC WriteTd: DB write failed", "err", err)
	}
}

// xdcTDBackfillProgressKey is a singleton key recording the highest block
// number for which cumulative TD has been persisted by the background backfill
// goroutine. It lets the goroutine resume after a restart without re-scanning
// from genesis.
var xdcTDBackfillProgressKey = []byte("xdc-td-backfill-progress")

// ReadTdBackfillProgress returns the highest canonical block number whose TD
// was persisted by the background backfill, or 0 if the key is absent.
func ReadTdBackfillProgress(db ethdb.Reader) uint64 {
	data, err := db.Get(xdcTDBackfillProgressKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

// WriteTdBackfillProgress persists the highest block number covered by the
// background TD backfill. Called inside the same batch as the TD writes so
// progress is always consistent with what is on disk.
func WriteTdBackfillProgress(db ethdb.KeyValueWriter, number uint64) {
	var enc [8]byte
	binary.BigEndian.PutUint64(enc[:], number)
	if err := db.Put(xdcTDBackfillProgressKey, enc[:]); err != nil {
		log.Crit("XDC WriteTdBackfillProgress: DB write failed", "err", err)
	}
}

// XDPoS-specific database key prefixes — BFT local safety state (F-2026-19774).
//
// highestVotedRound, highestSelfMinedRound, and lockQuorumCert are in-memory
// equivocation guards.  Persisting them prevents a restart inside a round from
// lowering the guard and allowing a double vote or double seal.
//
// These keys hold local-validator state only; they do not affect which blocks
// other nodes accept.  Canonical XDPoSChain keeps these in memory only; this
// is an intentional local-safety improvement beyond canonical.
//
// Removed in review R2-N03: masternodePrefix, masternodeListPrefix, epochPrefix,
// epochSnapshotPrefix, penaltyPrefix, penalizedListPrefix, rewardPrefix,
// rewardEpochPrefix, blockSignerPrefix, votePrefix ("vote-" shared first byte
// 'v' with VerklePrefix — collision risk resolved by deletion), tradingStatePrefix,
// lendingStatePrefix, qcPrefix, timeoutPoolPrefix, and all their associated
// exported key-builder functions and Read/Write accessors.  None had callers
// outside this file (verified by grep across the full tree).
var (
	bftHighestVotedRoundKey     = []byte("bftsafety-hvr")
	bftHighestSelfMinedRoundKey = []byte("bftsafety-hsmr")
	bftLockQCKey                = []byte("bftsafety-lqc")
)

// ReadBFTHighestVotedRound reads the persisted highest voted round.
// Returns 0 (safe default) if the key does not exist.
func ReadBFTHighestVotedRound(db ethdb.Reader) types.Round {
	data, err := db.Get(bftHighestVotedRoundKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return types.Round(binary.BigEndian.Uint64(data))
}

// WriteBFTHighestVotedRound persists the highest voted round.
func WriteBFTHighestVotedRound(db ethdb.KeyValueWriter, round types.Round) {
	var enc [8]byte
	binary.BigEndian.PutUint64(enc[:], uint64(round))
	if err := db.Put(bftHighestVotedRoundKey, enc[:]); err != nil {
		log.Crit("Failed to persist BFT highest voted round", "err", err)
	}
}

// ReadBFTHighestSelfMinedRound reads the persisted highest self-mined round.
// Returns 0 (safe default) if the key does not exist.
func ReadBFTHighestSelfMinedRound(db ethdb.Reader) types.Round {
	data, err := db.Get(bftHighestSelfMinedRoundKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return types.Round(binary.BigEndian.Uint64(data))
}

// WriteBFTHighestSelfMinedRound persists the highest self-mined round.
func WriteBFTHighestSelfMinedRound(db ethdb.KeyValueWriter, round types.Round) {
	var enc [8]byte
	binary.BigEndian.PutUint64(enc[:], uint64(round))
	if err := db.Put(bftHighestSelfMinedRoundKey, enc[:]); err != nil {
		log.Crit("Failed to persist BFT highest self-mined round", "err", err)
	}
}

// ReadBFTLockQC reads the persisted lock quorum certificate.
// Returns nil if the key does not exist or decoding fails.
func ReadBFTLockQC(db ethdb.Reader) *types.QuorumCert {
	data, err := db.Get(bftLockQCKey)
	if err != nil || len(data) == 0 {
		return nil
	}
	var qc types.QuorumCert
	if err := rlp.DecodeBytes(data, &qc); err != nil {
		log.Error("Invalid BFT lock QC RLP in DB", "err", err)
		return nil
	}
	return &qc
}

// WriteBFTLockQC persists the lock quorum certificate.
func WriteBFTLockQC(db ethdb.KeyValueWriter, qc *types.QuorumCert) {
	if qc == nil {
		return
	}
	data, err := rlp.EncodeToBytes(qc)
	if err != nil {
		log.Crit("Failed to RLP encode BFT lock QC", "err", err)
	}
	if err := db.Put(bftLockQCKey, data); err != nil {
		log.Crit("Failed to persist BFT lock QC", "err", err)
	}
}
