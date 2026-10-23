// Tests for issue #1757: NetworkInformation must report the configured chain ID,
// not the hardcoded mainnet ID 50 (review R1-R03).
// Every test here FAILS on origin/main and PASSES with the fix.
package XDPoS

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

// stubChainWithID wraps stubChain and overrides Config to return a custom chain ID.
type stubChainWithID struct {
	stubChain
	chainID *big.Int
}

func (s *stubChainWithID) Config() *params.ChainConfig {
	return &params.ChainConfig{ChainID: s.chainID}
}

// TestNetworkInformationUsesConfiguredChainID verifies that NetworkInformation
// returns the chain's actual ChainID rather than the hardcoded 50.
// FAILS on origin/main (always returns 50), PASSES with the fix.
func TestNetworkInformationUsesConfiguredChainID(t *testing.T) {
	engine := makeTestXDPoSWithV2()

	apothemID := big.NewInt(51)
	chain := &stubChainWithID{chainID: apothemID}
	api := &API{chain: chain, xdpos: engine}

	info := api.NetworkInformation()
	if info.NetworkId.Cmp(apothemID) != 0 {
		t.Fatalf("expected NetworkId=%s for Apothem, got %s", apothemID, info.NetworkId)
	}
}

// TestNetworkInformationMainnet verifies that chainID 50 is still returned when
// the chain config carries it (not just a hardcoded fallback).
func TestNetworkInformationMainnet(t *testing.T) {
	engine := makeTestXDPoSWithV2()

	mainnetID := big.NewInt(50)
	chain := &stubChainWithID{chainID: mainnetID}
	api := &API{chain: chain, xdpos: engine}

	info := api.NetworkInformation()
	if info.NetworkId.Cmp(mainnetID) != 0 {
		t.Fatalf("expected NetworkId=%s for mainnet, got %s", mainnetID, info.NetworkId)
	}
}
