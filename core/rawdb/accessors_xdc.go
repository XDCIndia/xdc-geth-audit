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

// XDPoS-specific database key prefixes
var (
	// BFT local safety state — singleton keys (F-2026-19774).
	// These persist highestVotedRound, highestSelfMinedRound, and lockQuorumCert
	// across restarts so the equivocation guards survive a crash/restart inside a round.
	// The values are local safety state only; they do not affect block acceptance.
	bftHighestVotedRoundKey     = []byte("bftsafety-hvr")
	bftHighestSelfMinedRoundKey = []byte("bftsafety-hsmr")
	bftLockQCKey                = []byte("bftsafety-lqc")

	// Masternode data
	masternodePrefix     = []byte("masternode-")
	masternodeListPrefix = []byte("masternodelist-")

	// Epoch data
	epochPrefix         = []byte("epoch-")
	epochSnapshotPrefix = []byte("epochsnapshot-")

	// Penalty data
	penaltyPrefix       = []byte("penalty-")
	penalizedListPrefix = []byte("penalizedlist-")

	// Reward data
	rewardPrefix      = []byte("reward-")
	rewardEpochPrefix = []byte("rewardepoch-")

	// Block signer data
	blockSignerPrefix = []byte("blocksigner-")

	// Vote data
	votePrefix = []byte("vote-")

	// Trading state root prefix
	tradingStatePrefix = []byte("tradingstate-")

	// Lending state root prefix
	lendingStatePrefix = []byte("lendingstate-")

	// QC (Quorum Certificate) data
	qcPrefix = []byte("qc-")

	// Timeout pool data
	timeoutPoolPrefix = []byte("timeoutpool-")
)

// encodeBlockNumber encodes a block number as big endian uint64
func encodeXDCBlockNumber(number uint64) []byte {
	enc := make([]byte, 8)
	binary.BigEndian.PutUint64(enc, number)
	return enc
}

// MasternodeKey returns the database key for a masternode entry
func MasternodeKey(address common.Address) []byte {
	return append(masternodePrefix, address.Bytes()...)
}

// MasternodeListKey returns the database key for masternode list at epoch
func MasternodeListKey(epoch uint64) []byte {
	return append(masternodeListPrefix, encodeBlockNumber(epoch)...)
}

// EpochKey returns the database key for epoch data
func EpochKey(epoch uint64) []byte {
	return append(epochPrefix, encodeBlockNumber(epoch)...)
}

// EpochSnapshotKey returns the database key for epoch snapshot
func EpochSnapshotKey(epoch uint64) []byte {
	return append(epochSnapshotPrefix, encodeBlockNumber(epoch)...)
}

// PenaltyKey returns the database key for penalty data at epoch
func PenaltyKey(epoch uint64) []byte {
	return append(penaltyPrefix, encodeBlockNumber(epoch)...)
}

// PenalizedListKey returns the database key for penalized validators list
func PenalizedListKey(epoch uint64) []byte {
	return append(penalizedListPrefix, encodeBlockNumber(epoch)...)
}

// RewardKey returns the database key for reward at block number
func RewardKey(number uint64) []byte {
	return append(rewardPrefix, encodeXDCBlockNumber(number)...)
}

// RewardEpochKey returns the database key for epoch reward
func RewardEpochKey(epoch uint64) []byte {
	return append(rewardEpochPrefix, encodeBlockNumber(epoch)...)
}

// BlockSignerKey returns the database key for block signer at block number
func BlockSignerKey(number uint64) []byte {
	return append(blockSignerPrefix, encodeXDCBlockNumber(number)...)
}

// VoteKey returns the database key for vote data
func VoteKey(hash common.Hash) []byte {
	return append(votePrefix, hash.Bytes()...)
}

// TradingStateKey returns the database key for trading state root
func TradingStateKey(hash common.Hash) []byte {
	return append(tradingStatePrefix, hash.Bytes()...)
}

// LendingStateKey returns the database key for lending state root
func LendingStateKey(hash common.Hash) []byte {
	return append(lendingStatePrefix, hash.Bytes()...)
}

// QCKey returns the database key for quorum certificate
func QCKey(hash common.Hash) []byte {
	return append(qcPrefix, hash.Bytes()...)
}

// TimeoutPoolKey returns the database key for timeout pool at round
func TimeoutPoolKey(round uint64) []byte {
	return append(timeoutPoolPrefix, encodeBlockNumber(round)...)
}

// Issue #682 Tier 2: the eight bypass-era state-root cache accessors
// (XdcLocalStateRootKey, XdcRemoteToLocalKey, Write/Read XdcLocalStateRoot,
// Write/Read XdcRemoteToLocal, ReadAllXdcLocalStateRoots,
// ReadAllXdcRemoteToLocal) were deleted here. PR #703 removed the only
// writer; this PR removes the readers; #682 Tier 2 deletes the accessors
// outright. Bypass-era cache is fully gone.
//
// Stale on-disk entries under xdcLocalStateRootPrefix / xdcRemoteToLocalPrefix
// are now orphaned. They occupy a few hundred KB at most and are harmless;
// nothing reads them.

// ReadMasternodeList reads the masternode list for an epoch
func ReadMasternodeList(db ethdb.Reader, epoch uint64) []common.Address {
	data, err := db.Get(MasternodeListKey(epoch))
	if err != nil || len(data) == 0 {
		return nil
	}

	var masternodes []common.Address
	if err := rlp.DecodeBytes(data, &masternodes); err != nil {
		log.Error("Invalid masternode list RLP", "epoch", epoch, "err", err)
		return nil
	}
	return masternodes
}

// WriteMasternodeList writes the masternode list for an epoch
func WriteMasternodeList(db ethdb.KeyValueWriter, epoch uint64, masternodes []common.Address) {
	data, err := rlp.EncodeToBytes(masternodes)
	if err != nil {
		log.Crit("Failed to RLP encode masternode list", "err", err)
	}
	if err := db.Put(MasternodeListKey(epoch), data); err != nil {
		log.Crit("Failed to store masternode list", "err", err)
	}
}

// ReadPenalizedList reads the penalized validators list for an epoch
func ReadPenalizedList(db ethdb.Reader, epoch uint64) []common.Address {
	data, err := db.Get(PenalizedListKey(epoch))
	if err != nil || len(data) == 0 {
		return nil
	}

	var penalized []common.Address
	if err := rlp.DecodeBytes(data, &penalized); err != nil {
		log.Error("Invalid penalized list RLP", "epoch", epoch, "err", err)
		return nil
	}
	return penalized
}

// WritePenalizedList writes the penalized validators list for an epoch
func WritePenalizedList(db ethdb.KeyValueWriter, epoch uint64, penalized []common.Address) {
	data, err := rlp.EncodeToBytes(penalized)
	if err != nil {
		log.Crit("Failed to RLP encode penalized list", "err", err)
	}
	if err := db.Put(PenalizedListKey(epoch), data); err != nil {
		log.Crit("Failed to store penalized list", "err", err)
	}
}

// ReadBlockSigner reads the block signer for a block number
func ReadBlockSigner(db ethdb.Reader, number uint64) common.Address {
	data, err := db.Get(BlockSignerKey(number))
	if err != nil || len(data) == 0 {
		return common.Address{}
	}
	return common.BytesToAddress(data)
}

// WriteBlockSigner writes the block signer for a block number
func WriteBlockSigner(db ethdb.KeyValueWriter, number uint64, signer common.Address) {
	if err := db.Put(BlockSignerKey(number), signer.Bytes()); err != nil {
		log.Crit("Failed to store block signer", "err", err)
	}
}

// ReadTradingStateRoot reads the trading state root for a block
func ReadTradingStateRoot(db ethdb.Reader, blockHash common.Hash) common.Hash {
	data, err := db.Get(TradingStateKey(blockHash))
	if err != nil || len(data) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteTradingStateRoot writes the trading state root for a block
func WriteTradingStateRoot(db ethdb.KeyValueWriter, blockHash common.Hash, root common.Hash) {
	if err := db.Put(TradingStateKey(blockHash), root.Bytes()); err != nil {
		log.Crit("Failed to store trading state root", "err", err)
	}
}

// ReadLendingStateRoot reads the lending state root for a block
func ReadLendingStateRoot(db ethdb.Reader, blockHash common.Hash) common.Hash {
	data, err := db.Get(LendingStateKey(blockHash))
	if err != nil || len(data) == 0 {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteLendingStateRoot writes the lending state root for a block
func WriteLendingStateRoot(db ethdb.KeyValueWriter, blockHash common.Hash, root common.Hash) {
	if err := db.Put(LendingStateKey(blockHash), root.Bytes()); err != nil {
		log.Crit("Failed to store lending state root", "err", err)
	}
}

// EpochData stores epoch-specific information
type EpochData struct {
	Epoch       uint64           `json:"epoch"`
	StartBlock  uint64           `json:"startBlock"`
	EndBlock    uint64           `json:"endBlock"`
	Masternodes []common.Address `json:"masternodes"`
	Penalties   []common.Address `json:"penalties"`
}

// ReadEpochData reads epoch data from database
func ReadEpochData(db ethdb.Reader, epoch uint64) *EpochData {
	data, err := db.Get(EpochKey(epoch))
	if err != nil || len(data) == 0 {
		return nil
	}

	var epochData EpochData
	if err := rlp.DecodeBytes(data, &epochData); err != nil {
		log.Error("Invalid epoch data RLP", "epoch", epoch, "err", err)
		return nil
	}
	return &epochData
}

// WriteEpochData writes epoch data to database
func WriteEpochData(db ethdb.KeyValueWriter, epoch uint64, epochData *EpochData) {
	data, err := rlp.EncodeToBytes(epochData)
	if err != nil {
		log.Crit("Failed to RLP encode epoch data", "err", err)
	}
	if err := db.Put(EpochKey(epoch), data); err != nil {
		log.Crit("Failed to store epoch data", "err", err)
	}
}

// HasEpochData checks if epoch data exists
func HasEpochData(db ethdb.Reader, epoch uint64) bool {
	has, _ := db.Has(EpochKey(epoch))
	return has
}

// DeleteEpochData deletes epoch data
func DeleteEpochData(db ethdb.KeyValueWriter, epoch uint64) {
	if err := db.Delete(EpochKey(epoch)); err != nil {
		log.Crit("Failed to delete epoch data", "err", err)
	}
}

// --- BFT local safety state (F-2026-19774) ---------------------------------
//
// highestVotedRound, highestSelfMinedRound, and lockQuorumCert are in-memory
// equivocation guards.  Persisting them prevents a restart inside a round from
// lowering the guard and allowing a double vote or double seal.
//
// These keys hold local-validator state only; they do not affect which blocks
// other nodes accept.  Canonical XDPoSChain keeps these in memory only; this
// is an intentional local-safety improvement beyond canonical.

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
