package core

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// TestDevnet5151XDCxTxPaysGas pins F-2026-19459: canonical v2.7.1
// common/constants.local.go sets tipXDCXReceiverDisable=0, so on chain 5151 a
// tx to 0x91-0x94 is not an empty tx. It runs through the EVM, charges gas and
// bumps the nonce. Chain 5050 keeps the open empty-tx window.
func TestDevnet5151XDCxTxPaysGas(t *testing.T) {
	saved := []**big.Int{&common.BlockNumberGas50x, &common.TIPSigning, &common.TIPTRC21Fee, &common.TIPNoHalvingMNReward,
		&common.TIPXDCXCancellationFee, &common.TIPXDCX, &common.TIPXDCXLending, &common.TIPXDCXMinerDisable,
		&common.TIPXDCXReceiverDisable, &common.TIP2019Block, &common.TIPRandomize}
	old := make([]*big.Int, len(saved))
	for i, p := range saved {
		old[i] = *p
	}
	oldIssuer, oldBlacklist := common.TRC21IssuerSMC, common.BlackListHFNumber
	t.Cleanup(func() {
		for i, p := range saved {
			*p = old[i]
		}
		common.TRC21IssuerSMC, common.BlackListHFNumber = oldIssuer, oldBlacklist
	})

	var (
		engine = ethash.NewFaker()
		key, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		from   = crypto.PubkeyToAddress(key.PublicKey)
	)
	run := func(chainID int64) (gasUsed, nonce uint64) {
		common.CopyXDCConstants(uint64(chainID))
		config := *params.TestChainConfig
		config.ChainID = big.NewInt(chainID)
		gspec := &Genesis{Config: &config, Alloc: types.GenesisAlloc{from: {Balance: big.NewInt(1e18)}}}
		bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer bc.Stop()
		tx, err := types.SignTx(types.NewTransaction(0, common.TradingStateAddrBinary, big.NewInt(0), 100000, big.NewInt(10*params.GWei), nil), types.LatestSigner(&config), key)
		if err != nil {
			t.Fatal(err)
		}
		block := GenerateBadBlock(bc.Genesis(), engine, types.Transactions{tx}, &config, true)
		statedb, err := bc.State()
		if err != nil {
			t.Fatal(err)
		}
		res, err := NewStateProcessor(bc).Process(context.Background(), block, statedb, nil, nil, vm.Config{DisableParallelExecution: true}, nil)
		if err != nil {
			t.Fatalf("chain %d: %v", chainID, err)
		}
		return res.Receipts[0].GasUsed, statedb.GetNonce(from)
	}

	if gas, nonce := run(5151); gas != params.TxGas || nonce != 1 {
		t.Errorf("chain 5151: gasUsed=%d nonce=%d, want EVM path (%d, 1)", gas, nonce, params.TxGas)
	}
	if gas, _ := run(5050); gas != 0 {
		t.Errorf("chain 5050: gasUsed=%d, want 0 (empty-tx window kept open)", gas)
	}
}
