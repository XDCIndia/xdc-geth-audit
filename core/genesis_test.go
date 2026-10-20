// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
)

func TestSetupGenesis(t *testing.T) {
	testSetupGenesis(t, rawdb.HashScheme)
	testSetupGenesis(t, rawdb.PathScheme)
}

func testSetupGenesis(t *testing.T, scheme string) {
	var (
		// XDC: extended header RLP (Validators/Penalties/SlotNumber fields) changes the
		// genesis hash. This is the XDC-encoded hash of this custom genesis block.
		customghash = common.HexToHash("0xfc8a143549950b0d3e3e7b70b0067b152e6b903ab5438f1ea87f0448ec93da48")
		customg     = Genesis{
			Config: &params.ChainConfig{HomesteadBlock: big.NewInt(3), Ethash: &params.EthashConfig{}},
			Alloc: types.GenesisAlloc{
				{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
			},
		}
		oldcustomg = customg
	)
	oldcustomg.Config = &params.ChainConfig{HomesteadBlock: big.NewInt(2), Ethash: &params.EthashConfig{}}

	tests := []struct {
		name           string
		fn             func(ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error)
		wantConfig     *params.ChainConfig
		wantHash       common.Hash
		wantErr        error
		wantCompactErr *params.ConfigCompatError
	}{
		{
			name: "genesis without ChainConfig",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				return SetupGenesisBlock(db, triedb.NewDatabase(db, newDbConfig(scheme)), new(Genesis))
			},
			wantErr: errGenesisNoConfig,
		},
		{
			name: "no block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				return SetupGenesisBlock(db, triedb.NewDatabase(db, newDbConfig(scheme)), nil)
			},
			// XDC: extended header RLP changes the hash of the Ethereum mainnet genesis.
			// params.MainnetGenesisHash is the Ethereum canonical value; XDC encodes as below.
			wantHash:   common.HexToHash("0x8d13370621558f4ed0da587934473c0404729f28b0ff1d50e5fdd840457a2f17"),
			wantConfig: params.MainnetChainConfig,
		},
		{
			name: "mainnet block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				DefaultGenesisBlock().MustCommit(db, triedb.NewDatabase(db, newDbConfig(scheme)))
				return SetupGenesisBlock(db, triedb.NewDatabase(db, newDbConfig(scheme)), nil)
			},
			// XDC: extended header RLP changes the hash of the Ethereum mainnet genesis.
			wantHash:   common.HexToHash("0x8d13370621558f4ed0da587934473c0404729f28b0ff1d50e5fdd840457a2f17"),
			wantConfig: params.MainnetChainConfig,
		},
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				tdb := triedb.NewDatabase(db, newDbConfig(scheme))
				customg.Commit(db, tdb, nil)
				return SetupGenesisBlock(db, tdb, nil)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "custom block in DB, genesis == sepolia",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				tdb := triedb.NewDatabase(db, newDbConfig(scheme))
				customg.Commit(db, tdb, nil)
				return SetupGenesisBlock(db, tdb, DefaultSepoliaGenesisBlock())
			},
			// XDC: New hash is the XDC-encoded Sepolia genesis, not the canonical Ethereum value.
			wantErr: &GenesisMismatchError{Stored: customghash, New: common.HexToHash("0x7cd5d13c645a4ae75314cc4696d59598e1f0b3096fd937e565105e95c1367407")},
		},
		{
			name: "custom block in DB, genesis == hoodi",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				tdb := triedb.NewDatabase(db, newDbConfig(scheme))
				customg.Commit(db, tdb, nil)
				return SetupGenesisBlock(db, tdb, DefaultHoodiGenesisBlock())
			},
			// XDC: New hash is the XDC-encoded Hoodi genesis, not the canonical Ethereum value.
			wantErr: &GenesisMismatchError{Stored: customghash, New: common.HexToHash("0x5480f38aa532f2e2f1892384645842678a8709f726befd7d4784223d29288ef0")},
		},
		{
			name: "compatible config in DB",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				tdb := triedb.NewDatabase(db, newDbConfig(scheme))
				oldcustomg.Commit(db, tdb, nil)
				return SetupGenesisBlock(db, tdb, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "incompatible config in DB",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, *params.ConfigCompatError, error) {
				// Commit the 'old' genesis block with Homestead transition at #2.
				// Advance to block #4, past the homestead transition block of customg.
				tdb := triedb.NewDatabase(db, newDbConfig(scheme))
				oldcustomg.Commit(db, tdb, nil)

				bc, _ := NewBlockChain(db, &oldcustomg, ethash.NewFullFaker(), DefaultConfig().WithStateScheme(scheme))
				defer bc.Stop()

				_, blocks, _ := GenerateChainWithGenesis(&oldcustomg, ethash.NewFaker(), 4, nil)
				bc.InsertChain(blocks)

				// This should return a compatibility error.
				return SetupGenesisBlock(db, tdb, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
			wantCompactErr: &params.ConfigCompatError{
				What:          "Homestead fork block",
				StoredBlock:   big.NewInt(2),
				NewBlock:      big.NewInt(3),
				RewindToBlock: 1,
			},
		},
	}

	for _, test := range tests {
		db := rawdb.NewMemoryDatabase()
		config, hash, compatErr, err := test.fn(db)
		// Check the return values.
		if !reflect.DeepEqual(err, test.wantErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(err), spew.NewFormatter(test.wantErr))
		}
		if !reflect.DeepEqual(compatErr, test.wantCompactErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(compatErr), spew.NewFormatter(test.wantCompactErr))
		}
		if !reflect.DeepEqual(config, test.wantConfig) {
			t.Errorf("%s:\nreturned %v\nwant     %v", test.name, config, test.wantConfig)
		}
		if hash != test.wantHash {
			t.Errorf("%s: returned hash %s, want %s", test.name, hash.Hex(), test.wantHash.Hex())
		} else if err == nil {
			// Check database content.
			stored := rawdb.ReadBlock(db, test.wantHash, 0)
			if stored.Hash() != test.wantHash {
				t.Errorf("%s: block in DB has hash %s, want %s", test.name, stored.Hash(), test.wantHash)
			}
		}
	}
}

// TestGenesisHashes checks the congruity of default genesis data to
// corresponding hardcoded genesis hash values.
//
// XDC note: the "want" hashes here are the XDC-RLP-encoded hashes of each
// genesis block. They differ from the canonical Ethereum values stored in
// params.MainnetGenesisHash / params.SepoliaGenesisHash / params.HoodiGenesisHash
// because XDC's extended header RLP adds Validators, Penalties and SlotNumber
// fields, changing the block hash. The canonical params.* constants are kept
// as-is for chain-ID and fork-ID checks; only these test expectations change.
func TestGenesisHashes(t *testing.T) {
	for i, c := range []struct {
		genesis *Genesis
		want    common.Hash
	}{
		{DefaultGenesisBlock(), common.HexToHash("0x8d13370621558f4ed0da587934473c0404729f28b0ff1d50e5fdd840457a2f17")},
		{DefaultSepoliaGenesisBlock(), common.HexToHash("0x7cd5d13c645a4ae75314cc4696d59598e1f0b3096fd937e565105e95c1367407")},
		{DefaultHoodiGenesisBlock(), common.HexToHash("0x5480f38aa532f2e2f1892384645842678a8709f726befd7d4784223d29288ef0")},
	} {
		// Test via MustCommit
		db := rawdb.NewMemoryDatabase()
		if have := c.genesis.MustCommit(db, triedb.NewDatabase(db, triedb.HashDefaults)).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
		// Test via ToBlock
		if have := c.genesis.ToBlock().Hash(); have != c.want {
			t.Errorf("case: %d b), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
	}
}

func TestGenesisCommit(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.TestChainConfig,
		// difficulty is nil
	}

	db := rawdb.NewMemoryDatabase()
	genesisBlock := genesis.MustCommit(db, triedb.NewDatabase(db, triedb.HashDefaults))

	if genesis.Difficulty != nil {
		t.Fatalf("assumption wrong")
	}

	// This value should have been set as default in the ToBlock method.
	if genesisBlock.Difficulty().Cmp(params.GenesisDifficulty) != 0 {
		t.Errorf("assumption wrong: want: %d, got: %v", params.GenesisDifficulty, genesisBlock.Difficulty())
	}
}

func TestReadWriteGenesisAlloc(t *testing.T) {
	var (
		db    = rawdb.NewMemoryDatabase()
		alloc = &types.GenesisAlloc{
			{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
			{2}: {Balance: big.NewInt(2), Storage: map[common.Hash]common.Hash{{2}: {2}}},
		}
		hash, _ = hashAlloc(alloc, false)
	)
	blob, _ := json.Marshal(alloc)
	rawdb.WriteGenesisStateSpec(db, hash, blob)

	var reload types.GenesisAlloc
	err := reload.UnmarshalJSON(rawdb.ReadGenesisStateSpec(db, hash))
	if err != nil {
		t.Fatalf("Failed to load genesis state %v", err)
	}
	if len(reload) != len(*alloc) {
		t.Fatal("Unexpected genesis allocation")
	}
	for addr, account := range reload {
		want, ok := (*alloc)[addr]
		if !ok {
			t.Fatal("Account is not found")
		}
		if !reflect.DeepEqual(want, account) {
			t.Fatal("Unexpected account")
		}
	}
}

func newDbConfig(scheme string) *triedb.Config {
	if scheme == rawdb.HashScheme {
		return triedb.HashDefaults
	}
	config := *pathdb.Defaults
	config.NoAsyncFlush = true
	return &triedb.Config{PathDB: &config}
}

func TestBinaryGenesisCommit(t *testing.T) {
	var ubtTime uint64 = 0
	ubtConfig := &params.ChainConfig{
		ChainID:                 big.NewInt(1),
		HomesteadBlock:          big.NewInt(0),
		DAOForkBlock:            nil,
		DAOForkSupport:          false,
		EIP150Block:             big.NewInt(0),
		EIP155Block:             big.NewInt(0),
		EIP158Block:             big.NewInt(0),
		ByzantiumBlock:          big.NewInt(0),
		ConstantinopleBlock:     big.NewInt(0),
		PetersburgBlock:         big.NewInt(0),
		IstanbulBlock:           big.NewInt(0),
		MuirGlacierBlock:        big.NewInt(0),
		BerlinBlock:             big.NewInt(0),
		LondonBlock:             big.NewInt(0),
		ArrowGlacierBlock:       big.NewInt(0),
		GrayGlacierBlock:        big.NewInt(0),
		MergeNetsplitBlock:      nil,
		ShanghaiTime:            &ubtTime,
		CancunTime:              &ubtTime,
		PragueTime:              &ubtTime,
		OsakaTime:               &ubtTime,
		UBTTime:                 &ubtTime,
		TerminalTotalDifficulty: big.NewInt(0),
		EnableUBTAtGenesis:      true,
		Ethash:                  nil,
		Clique:                  nil,
		BlobScheduleConfig: &params.BlobScheduleConfig{
			Cancun: params.DefaultCancunBlobConfig,
			Prague: params.DefaultPragueBlobConfig,
		},
	}

	genesis := &Genesis{
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Config:     ubtConfig,
		Timestamp:  ubtTime,
		Difficulty: big.NewInt(0),
		Alloc: types.GenesisAlloc{
			{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
		},
	}

	expected := common.FromHex("0870fd587c41dc778019de8c5cb3193fe4ef1f417976461952d3712ba39163f5")
	got := genesis.ToBlock().Root().Bytes()
	if !bytes.Equal(got, expected) {
		t.Fatalf("invalid genesis state root, expected %x, got %x", expected, got)
	}

	db := rawdb.NewMemoryDatabase()

	config := *pathdb.Defaults
	config.NoAsyncFlush = true

	triedb := triedb.NewDatabase(db, &triedb.Config{
		IsUBT:             true,
		PathDB:            &config,
		BinTrieGroupDepth: triedb.DefaultBinTrieGroupDepth,
	})
	block := genesis.MustCommit(db, triedb)
	if !bytes.Equal(block.Root().Bytes(), expected) {
		t.Fatalf("invalid genesis state root, expected %x, got %x", expected, block.Root())
	}

	// Test that the trie is a unified binary trie
	if !triedb.IsUBT() {
		t.Fatalf("expected trie to be a unified binary trie")
	}
	vdb := rawdb.NewTable(db, string(rawdb.VerklePrefix))
	if !rawdb.HasAccountTrieNode(vdb, nil) {
		t.Fatal("could not find node")
	}
}
