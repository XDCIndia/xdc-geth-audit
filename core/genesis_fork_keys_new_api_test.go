// genesis_fork_keys_new_api_test.go requires DecodeGenesisJSON which is new in
// this branch. Does NOT compile on origin/main. Refs #1528.
package core

import (
	"strings"
	"testing"
)

// TestGenesisUnknownConfigKeyRejected: DecodeGenesisJSON must reject an unknown
// key in the "config" object. This is the genesis-file entry point used by
// geth init, --genesis flag, and cmd/evm. On branch, DecodeGenesisJSON calls
// params.CheckGenesisConfigUnknownKeys before the full decode → error → PASS.
// Does NOT compile on origin/main (DecodeGenesisJSON is new). Refs #1528.
func TestGenesisUnknownConfigKeyRejected(t *testing.T) {
	genesisJSON := `{
		"config": {"chainId":50,"unknownFutureKey":99},
		"difficulty": "0x1",
		"gasLimit": "0x5208",
		"alloc": {}
	}`
	_, err := DecodeGenesisJSON(strings.NewReader(genesisJSON))
	if err == nil {
		t.Fatal("expected error for unknown config key in genesis file, got nil")
	}
}
