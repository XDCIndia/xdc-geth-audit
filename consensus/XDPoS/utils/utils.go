// Copyright (c) 2018 XDPoSChain
// XDPoS utility functions

package utils

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"golang.org/x/crypto/sha3"
)

func Position(list []common.Address, x common.Address) int {
	for i, item := range list {
		if item == x {
			return i
		}
	}
	return -1
}

func Hop(len, pre, cur int) int {
	switch {
	case pre < cur:
		return cur - (pre + 1)
	case pre > cur:
		return (len - pre) + (cur - 1)
	default:
		return len - 1
	}
}

// Extract validators from byte array.
func ExtractValidatorsFromBytes(byteValidators []byte) []int64 {
	lenValidator := len(byteValidators) / M2ByteLength
	var validators []int64
	for i := 0; i < lenValidator; i++ {
		trimByte := bytes.Trim(byteValidators[i*M2ByteLength:(i+1)*M2ByteLength], "\x00")
		intNumber, err := strconv.Atoi(string(trimByte))
		if err != nil {
			log.Error("Can not convert string to integer", "error", err)
			return []int64{}
		}
		validators = append(validators, int64(intNumber))
	}

	return validators
}

// compare 2 signers lists
// return true if they are same elements, otherwise return false
func CompareSignersLists(list1 []common.Address, list2 []common.Address) bool {
	l1 := make([]common.Address, len(list1))
	l2 := make([]common.Address, len(list2))

	copy(l1, list1)
	copy(l2, list2)

	if len(l1) == 0 && len(l2) == 0 {
		return true
	}

	if len(l1) != len(l2) {
		return false
	}

	sort.Slice(l1, func(i, j int) bool {
		return bytes.Compare(l1[i][:], l1[j][:]) == -1
	})
	sort.Slice(l2, func(i, j int) bool {
		return bytes.Compare(l2[i][:], l2[j][:]) == -1
	})
	return reflect.DeepEqual(l1, l2)
}

// ValidatorsOrderMatches returns true when expected and actual contain the same
// addresses in the same order. Unlike CompareSignersLists (which sorts both
// sides), this is a strict positional check.
//
// Use it under fullVerify at V2 epoch-switch blocks to detect a proposer that
// stored header.Validators in a non-canonical order. A reordered list shifts
// leaderIndex = round % epoch % len for all subsequent blocks in the epoch
// (F-2026-19815). The sorted set-equality check (CompareSignersLists) must
// pass first; call this only when the sets are known equal.
//
// Canonical XDPoSChain v2.9.3-testnet uses the same sorted CompareSignersLists
// at epoch switch (no ordered check). This function is diagnostic: callers log
// a WARN but do not reject blocks. Gate before promoting to rejection: replay-
// parity over mainnet V2 epoch switches (80,370,000 → tip) must fire 0 WARNs.
func ValidatorsOrderMatches(expected, actual []common.Address) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}
	return true
}

// Decode extra fields for consensus version >= 2 (XDPoS 2.0 and future versions)
func DecodeBytesExtraFields(b []byte, val interface{}) error {
	if len(b) == 0 {
		return errors.New("extra field is 0 length")
	}
	switch b[0] {
	case 2:
		return rlp.DecodeBytes(b[1:], val)
	default:
		return fmt.Errorf("consensus version %d is not defined, or this block is v1 block", b[0])
	}
}

func RlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	err := rlp.Encode(hw, x)
	if err != nil {
		log.Error("[rlpHash] Fail to hash item", "Error", err)
	}
	hw.Sum(h[:0])
	return h
}
