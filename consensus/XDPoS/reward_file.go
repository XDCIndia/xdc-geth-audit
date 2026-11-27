// Copyright (c) 2026 XDC Network

package XDPoS

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// hookRewardKeys are the top-level names HookReward writes. signSlots is a
// scalar count. The signer keys are signer to RewardLog. The reward keys are
// signer to holder to credited amount.
var hookRewardKeys = map[string]struct{}{
	"signers":          {},
	"signersProtector": {},
	"signersObserver":  {},
	"rewards":          {},
	"rewardsProtector": {},
	"rewardsObserver":  {},
	"signSlots":        {},
}

func isHookRewardRecord(raw map[string]json.RawMessage) bool {
	for key := range raw {
		if _, ok := hookRewardKeys[key]; ok {
			return true
		}
	}
	return false
}

// DecodeRewardFileObject decodes a stored reward file, including a record
// that carries the scalar signSlots entry.
func DecodeRewardFileObject(data []byte) (map[string]interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out map[string]interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]interface{}{}
	}
	return out, nil
}

// AccountRewardFromFile returns the amount credited to account in one reward
// file: the sum of its entries in the rewards keys, whether it is a signer,
// holder, owner, voter or foundation address. signSlots is not an amount.
func AccountRewardFromFile(data []byte, accountHex string) (*big.Int, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if isHookRewardRecord(raw) {
		return accountRewardFromHook(raw, accountHex)
	}
	var legacy map[string]map[string]map[string]*big.Int
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	return accountRewardFromLegacy(legacy, accountHex), nil
}

func accountRewardFromHook(raw map[string]json.RawMessage, accountHex string) (*big.Int, error) {
	// F-2026-19805: the amount credited to an address is its entry in the
	// rewards tables (signer to holder to credited amount), summed over every
	// signer's table. RewardLog.Reward is the masternode's gross epoch reward,
	// split among its holders, so it is not credited to the signer address.
	// An address that is both a signer and a holder gets its holder shares.
	for _, key := range []string{"signers", "signersProtector", "signersObserver"} {
		// Not summed, but a record whose signer tables do not decode is an error.
		if payload, ok := raw[key]; ok {
			var signers map[string]RewardLog
			if err := json.Unmarshal(payload, &signers); err != nil {
				return nil, fmt.Errorf("reward key %s: %w", key, err)
			}
		}
	}
	total := new(big.Int)
	for _, key := range []string{"rewards", "rewardsProtector", "rewardsObserver"} {
		payload, ok := raw[key]
		if !ok {
			continue
		}
		var table map[string]map[string]*big.Int
		if err := json.Unmarshal(payload, &table); err != nil {
			return nil, fmt.Errorf("reward key %s: %w", key, err)
		}
		for _, holders := range table {
			for holder, amt := range holders {
				if strings.ToLower(holder) == accountHex && amt != nil {
					total.Add(total, amt)
				}
			}
		}
	}
	return total, nil
}

func accountRewardFromLegacy(raw map[string]map[string]map[string]*big.Int, accountHex string) *big.Int {
	total := new(big.Int)
	for signer, holders := range raw {
		if strings.ToLower(signer) == accountHex {
			for _, fields := range holders {
				for _, amt := range fields {
					if amt != nil {
						total.Add(total, amt)
					}
				}
			}
			continue
		}
		for holder, fields := range holders {
			if strings.ToLower(holder) == accountHex {
				for _, amt := range fields {
					if amt != nil {
						total.Add(total, amt)
					}
				}
			}
		}
	}
	return total
}
