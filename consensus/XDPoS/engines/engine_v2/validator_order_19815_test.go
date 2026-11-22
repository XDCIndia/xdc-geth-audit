// Copyright (c) 2026 XDC Network
// F-2026-19815: an epoch-switch header may list the right validators in a
// different order. The order decides the leader schedule. verifyHeader rejects
// that header only once ValidatorOrderBlock is active.
//
// Mainnet and Apothem keep the block at the far-future placeholder. Devnet,
// private nets and a genesis that omits the field (net 2040 is configured that
// way) leave it nil. Both accept the reordered header, which is what a
// current-main node accepts. Scheduling a sooner block on this client alone
// would reject headers current-main and legacy XDPoSChain still treat as
// valid, so the placeholder is not moved here.

package engine_v2

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func swapValidators(mns []common.Address) []common.Address {
	out := append([]common.Address(nil), mns...)
	out[0], out[1] = out[1], out[0]
	return out
}

// verifyReordered seals an epoch switch whose validator set matches the local
// set but not its order, then verifies it. fork nil leaves the rule off.
func verifyReordered(t *testing.T, fork *big.Int, full bool) error {
	t.Helper()
	eng, chain, keys, mns, parent, h := esFixture(t, false)
	if fork != nil {
		chain.cfg.ValidatorOrderBlock = new(big.Int).Set(fork)
	}
	swapped := swapValidators(mns)
	same := true
	for i := range mns {
		if mns[i] != swapped[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("swap did not change validator order")
	}
	h.Validators = packValidators(swapped)
	// Local leader stays mns[0]. Round 1800 % epoch 900 % 4 == 0.
	esSeal(t, h, keys[0])
	return eng.verifyHeader(chain, h, []*types.Header{parent}, full)
}

// TestF202619815ShippedGatesLeaveReorderedListsValid checks that every shipped
// network, and a net-2040 genesis that does not know the field, still accepts
// a reordered validator list under full verification.
func TestF202619815ShippedGatesLeaveReorderedListsValid(t *testing.T) {
	head := big.NewInt(200_000_000)
	for name, cfg := range map[string]*params.ChainConfig{
		"mainnet":   params.XDCMainnetChainConfig,
		"apothem":   params.XDCApothemChainConfig,
		"devnet":    params.XDCDevnetChainConfig,
		"devnet551": params.XDCDevnet551ChainConfig,
		"private":   params.XDCDefaultPrivateChainConfig,
	} {
		if cfg.IsValidatorOrder(head) {
			t.Errorf("%s rejects a reordered list at block %s", name, head)
		}
	}
	var cfg params.ChainConfig
	raw := []byte(`{"chainId":2040,"xdpos":{"period":2,"epoch":900,"reward":1,"gap":450,"v2":{"switchBlock":1}}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ValidatorOrderBlock != nil || cfg.IsValidatorOrder(head) {
		t.Fatal("a genesis that omits validatorOrderBlock enables the order rule")
	}

	// nil, the mainnet placeholder, and a fork that has not been reached yet
	// are the rules a current-main node applies to this header.
	forks := []*big.Int{
		nil,
		params.XDCMainnetChainConfig.ValidatorOrderBlock,
		big.NewInt(904), // header is block 903
	}
	for _, fork := range forks {
		if err := verifyReordered(t, fork, true); err != nil {
			t.Fatalf("fork %v rejected a reordered list current-main accepts: %v", fork, err)
		}
	}
}

// TestF202619815ActiveForkRejectsReorderedList is the rule an operator gets
// by setting validatorOrderBlock. It must not be set on a network until the
// same block is set on current-main and on legacy.
func TestF202619815ActiveForkRejectsReorderedList(t *testing.T) {
	err := verifyReordered(t, big.NewInt(903), true)
	if err == nil || !strings.Contains(err.Error(), "order does not match") {
		t.Fatalf("reordered list at the fork block: got %v", err)
	}
	// Bulk sync does not apply the order rule, matching current-main.
	if err := verifyReordered(t, big.NewInt(0), false); err != nil {
		t.Fatalf("bulk sync rejected a reordered list: %v", err)
	}
}

// TestF202619815CanonicalOrderStaysValid checks that turning the rule on does
// not reject the order calcMasternodes already produced. A header this node
// seals is still a header current-main accepts.
func TestF202619815CanonicalOrderStaysValid(t *testing.T) {
	eng, chain, keys, mns, parent, h := esFixture(t, false)
	chain.cfg.ValidatorOrderBlock = big.NewInt(0)
	h.Validators = packValidators(mns)
	esSeal(t, h, keys[0])
	if err := eng.verifyHeader(chain, h, []*types.Header{parent}, true); err != nil {
		t.Fatalf("canonical order with the rule active: %v", err)
	}
}
