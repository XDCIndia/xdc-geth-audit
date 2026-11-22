// Copyright 2026 XDC Network
// XDC-HUNT-28 (#2081): a block body that is not in the database must give the
// retryable ErrPenaltyDataUnavailable in the pre-upgrade penalty branch and in
// both reward walks, never count as a block with zero signing txs.

package hooks

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// Pre-upgrade penalty branch (TIPUpgradePenalty not set): a penalised candidate
// is scanned for signing txs in the recent blocks; a missing body must stop the
// scan with the retryable error.
func TestHookPenaltyV2PreUpgradeMissingBody(t *testing.T) {
	const epoch = 10
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signerA := crypto.PubkeyToAddress(key.PublicKey)

	chainConfig := &params.ChainConfig{
		ChainID:        big.NewInt(551),
		HomesteadBlock: big.NewInt(0),
		EIP150Block:    big.NewInt(0),
		EIP155Block:    big.NewInt(0),
		EIP158Block:    big.NewInt(0),
		ByzantiumBlock: big.NewInt(0),
		XDPoS: &params.XDPoSConfig{
			Epoch: epoch,
			V2: &params.V2{
				SwitchBlock:   big.NewInt(0),
				CurrentConfig: &params.V2Config{},
			},
		},
	}
	adaptor := XDPoS.New(&params.XDPoSConfig{Epoch: epoch, V2: &params.V2{SwitchBlock: big.NewInt(0)}}, rawdb.NewMemoryDatabase())
	fakeV2 := &comebackFakeV2{
		penaltiesAtEpoch: func(int) []common.Address { return []common.Address{signerA} },
	}
	adaptor.SetEngineV2(fakeV2)
	AttachConsensusV2Hooks(adaptor, nil, chainConfig)
	if fakeV2.capturedFn == nil {
		t.Fatal("AttachConsensusV2Hooks did not wire the penalty hook")
	}
	number := big.NewInt(31) // above the comeback height (LimitPenaltyEpochV2+1)*epoch
	chain := &comebackTestChain{
		parent: &types.Header{Number: big.NewInt(30)},
		cfg:    chainConfig,
	}
	_, err = fakeV2.capturedFn(chain, number, common.HexToHash("0xabcdef01"), []common.Address{signerA})
	if !errors.Is(err, XDPoS.ErrPenaltyDataUnavailable) {
		t.Fatalf("missing body in the pre-upgrade comeback scan: err = %v, want ErrPenaltyDataUnavailable", err)
	}
}

// The reward walks run on a real BlockChain, so these two sites are guarded
// by source, like TestSigningTxCountMissPathsUseCacheSigner: the `found`
// result of resolveMissedSigningTxs must be read and answered with an error
// that wraps ErrPenaltyDataUnavailable.
func TestRewardWalksReturnErrorForMissingBody(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "engine_v2_hooks.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GetSigningTxCount", "GetSigningTxCountTiered"} {
		var fn *ast.FuncDecl
		for _, d := range file.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Recv == nil && f.Name.Name == name {
				fn = f
			}
		}
		if fn == nil {
			t.Fatalf("%s not found in engine_v2_hooks.go", name)
		}
		var (
			foundRead  bool // the third result of resolveMissedSigningTxs is bound to a name
			usesErrVar bool // ErrPenaltyDataUnavailable is returned
		)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				if len(x.Lhs) == 3 && len(x.Rhs) == 1 {
					if call, ok := x.Rhs[0].(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "resolveMissedSigningTxs" {
							if l, ok := x.Lhs[2].(*ast.Ident); ok && l.Name == "found" {
								foundRead = true
							}
						}
					}
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "ErrPenaltyDataUnavailable" {
					usesErrVar = true
				}
			}
			return true
		})
		if !foundRead {
			t.Errorf("%s drops the found result of resolveMissedSigningTxs: a missing body counts as zero signing txs", name)
		}
		if !usesErrVar {
			t.Errorf("%s does not return ErrPenaltyDataUnavailable for a missing body", name)
		}
	}
}
