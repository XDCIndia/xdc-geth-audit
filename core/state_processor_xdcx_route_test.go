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

// TestXDCxEmptyTxRouting pins the routing of txs to the XDCx contracts
// 0x91-0x94 (#1407, #1411) against canonical XDPoSChain routeTransaction:
// inside the TIPXDCXReceiver window each goes to ApplyEmptyTransaction (zero
// gas, one log carrying the To address, no nonce or balance change); outside
// it each runs through the EVM; and a tx with a bad signature is rejected
// even on the empty route, because the sender is recovered first.
func TestXDCxEmptyTxRouting(t *testing.T) {
	oldStart, oldEnd := common.TIPXDCX, common.TIPXDCXReceiverDisable
	t.Cleanup(func() { common.TIPXDCX, common.TIPXDCXReceiverDisable = oldStart, oldEnd })

	var (
		config   = params.TestChainConfig
		engine   = ethash.NewFaker()
		key, _   = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		from     = crypto.PubkeyToAddress(key.PublicKey)
		signer   = types.LatestSigner(config)
		funds    = big.NewInt(1e18)
		gasPrice = big.NewInt(10 * params.GWei)
	)
	gspec := &Genesis{Config: config, Alloc: types.GenesisAlloc{from: {Balance: funds}}}
	bc, err := NewBlockChain(rawdb.NewMemoryDatabase(), gspec, engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()

	process := func(tx *types.Transaction) (*ProcessResult, uint64, *big.Int, error) {
		block := GenerateBadBlock(bc.Genesis(), engine, types.Transactions{tx}, config, true)
		statedb, err := bc.State()
		if err != nil {
			t.Fatal(err)
		}
		res, err := NewStateProcessor(bc).Process(context.Background(), block, statedb, nil, nil, vm.Config{DisableParallelExecution: true}, nil)
		return res, statedb.GetNonce(from), statedb.GetBalance(from).ToBig(), err
	}
	sign := func(to common.Address) *types.Transaction {
		tx, err := types.SignTx(types.NewTransaction(0, to, big.NewInt(0), 100000, gasPrice, nil), signer, key)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	targets := []common.Address{
		common.XDCXAddrBinary,                         // 0x91
		common.TradingStateAddrBinary,                 // 0x92
		common.XDCXLendingAddressBinary,               // 0x93
		common.XDCXLendingFinalizedTradeAddressBinary, // 0x94
	}

	// Inside the window: block 1 is in [TIPXDCX, TIPXDCXReceiverDisable).
	common.TIPXDCX, common.TIPXDCXReceiverDisable = big.NewInt(1), big.NewInt(2)
	for _, to := range targets {
		res, nonce, balance, err := process(sign(to))
		if err != nil {
			t.Fatalf("%x in window: %v", to, err)
		}
		r := res.Receipts[0]
		if r.GasUsed != 0 || res.GasUsed != 0 || r.Status != types.ReceiptStatusSuccessful {
			t.Errorf("%x in window: gasUsed=%d blockGas=%d status=%d, want empty-tx receipt", to, r.GasUsed, res.GasUsed, r.Status)
		}
		if len(r.Logs) != 1 || r.Logs[0].Address != to {
			t.Errorf("%x in window: logs=%v, want one log with the To address", to, r.Logs)
		}
		if nonce != 0 || balance.Cmp(funds) != 0 {
			t.Errorf("%x in window: nonce=%d balance=%v, want state untouched", to, nonce, balance)
		}
	}

	// A bad signature is rejected even on the empty route.
	bad, err := sign(common.TradingStateAddrBinary).WithSignature(signer, make([]byte, 65))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := process(bad); err == nil {
		t.Error("0x92 tx with a bad signature in window: accepted, want rejected")
	}

	// Outside the window (closed at block 1): normal EVM path.
	common.TIPXDCX, common.TIPXDCXReceiverDisable = big.NewInt(1), big.NewInt(1)
	for _, to := range targets {
		res, nonce, _, err := process(sign(to))
		if err != nil {
			t.Fatalf("%x outside window: %v", to, err)
		}
		if r := res.Receipts[0]; r.GasUsed != params.TxGas || nonce != 1 {
			t.Errorf("%x outside window: gasUsed=%d nonce=%d, want EVM path (%d, 1)", to, r.GasUsed, nonce, params.TxGas)
		}
	}
}
