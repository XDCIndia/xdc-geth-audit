// Tests for reward file storage (fix #1475): saveRewardToFile and deepCloneJSON.
// These tests FAIL on origin/main (latestReward field and saveRewardToFile do not exist)
// and PASS with the fix.

package engine_v2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDeepCloneJSON(t *testing.T) {
	orig := map[string]interface{}{
		"validator": map[string]interface{}{
			"voter": map[string]interface{}{
				"reward": json.Number("1000"),
			},
		},
	}
	clone, err := deepCloneJSON(orig)
	if err != nil {
		t.Fatalf("deepCloneJSON: %v", err)
	}
	// Mutate clone; original must be unaffected.
	clone["extra"] = "injected"
	if _, ok := orig["extra"]; ok {
		t.Fatal("deepCloneJSON did not deep-clone: mutation leaked to original")
	}
}

func TestDeepCloneJSONNil(t *testing.T) {
	clone, err := deepCloneJSON(nil)
	if err != nil {
		t.Fatalf("deepCloneJSON(nil): %v", err)
	}
	if clone != nil {
		t.Fatal("deepCloneJSON(nil) should return nil")
	}
}

func TestSaveRewardToFile(t *testing.T) {
	dir := t.TempDir()

	eng := &XDPoS_v2{
		latestReward: map[string]interface{}{
			"0xAddr": map[string]interface{}{
				"0xVoter": map[string]interface{}{
					"reward": json.Number("500"),
				},
			},
		},
		latestRewardBlocknum: 900,
	}

	// With StoreRewardFolder unset, saveRewardToFile must be a no-op.
	old := common.StoreRewardFolder
	common.StoreRewardFolder = ""
	eng.saveRewardToFile(common.Hash{1}, 900)
	common.StoreRewardFolder = old

	// Now set folder and verify the file is written.
	common.StoreRewardFolder = dir
	defer func() { common.StoreRewardFolder = old }()

	hash := common.HexToHash("0xdeadbeef")
	eng.saveRewardToFile(hash, 900)

	filename := "900." + hash.Hex()
	data, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatalf("reward file not written: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("reward file not valid JSON: %v", err)
	}
	if _, ok := got["0xAddr"]; !ok {
		t.Fatalf("reward file missing expected key; got: %s", string(data))
	}
}

func TestSaveRewardToFileBlockMismatch(t *testing.T) {
	dir := t.TempDir()
	old := common.StoreRewardFolder
	common.StoreRewardFolder = dir
	defer func() { common.StoreRewardFolder = old }()

	eng := &XDPoS_v2{
		latestReward:         map[string]interface{}{"x": "y"},
		latestRewardBlocknum: 900,
	}
	// Pass wrong block number; file must NOT be written.
	eng.saveRewardToFile(common.Hash{2}, 901)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("reward file written despite block number mismatch")
	}
}
