// Copyright 2026 XDC Network
// Behavioral test for #1612: the devnet 5551 tiered reward pays protectors from
// the parent-state candidates (pre XinFinOrg/XDPoSChain#2612), every other chain
// from GetStandbynodes. It drives the real HookReward closure wired by
// AttachConsensusV2Hooks and compiles against origin/main.

package hooks

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/XDPoS"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// rewardFakeV2 marks blocks 2, 31 and 61 as epoch switches, takes the
// rewarded epoch's masternodes from mn and its standby pool from standby.
type rewardFakeV2 struct {
	comebackFakeV2
	mn, standby []common.Address
}

func (f *rewardFakeV2) IsEpochSwitch(h *types.Header) (bool, uint64, error) {
	n := h.Number.Uint64()
	return n == 2 || n == 31 || n == 61, 0, nil
}
func (f *rewardFakeV2) GetMasternodesFromEpochSwitchHeader(consensus.ChainReader, *types.Header) []common.Address {
	return f.mn
}
func (f *rewardFakeV2) GetStandbynodes(consensus.ChainReader, *types.Header) []common.Address {
	return f.standby
}

// setCandidate writes candidates[i] = c, validatorsState[c].owner and .cap in
// the 0x88 storage layout read by state.GetCandidates/GetCandidateOwner/Cap.
func setCandidate(st *state.StateDB, i, n int, c, owner common.Address, stake int64) {
	smc := common.MasternodeVotingSMCBinary
	slot8 := common.BigToHash(big.NewInt(8))
	st.SetState(smc, slot8, common.BigToHash(big.NewInt(int64(n))))
	st.SetState(smc, state.GetLocDynamicArrAtElement(slot8, uint64(i), 1), common.BytesToHash(c.Bytes()))
	loc := state.GetLocMappingAtKey(common.BytesToHash(c.Bytes()), 1)
	st.SetState(smc, common.BigToHash(loc), common.BytesToHash(owner.Bytes()))
	st.SetState(smc, common.BigToHash(new(big.Int).Add(loc, big.NewInt(1))), common.BigToHash(big.NewInt(stake)))
}

// runTieredReward runs HookReward for epoch-switch block 61 on chain chainID.
// Rewarded epoch: blocks 3..30 (sign blocks 15 and 30). Masternode M; standby
// pool from the epoch snapshot = [Q]; parent-state candidates = [M, P] (Q
// left after the rewarded epoch, P joined during it).
// P and Q both sign block 15. It returns the balances credited to the owners
// of P and Q.
func runTieredReward(t *testing.T, chainID int64) (ownerP, ownerQ *big.Int) {
	t.Helper()
	keys := make([]*ecdsa.PrivateKey, 3)
	addrs := make([]common.Address, 3)
	for i := range keys {
		keys[i], _ = crypto.GenerateKey()
		addrs[i] = crypto.PubkeyToAddress(keys[i].PublicKey)
	}
	m, p, q := addrs[0], addrs[1], addrs[2]
	ownM, ownP, ownQ := common.HexToAddress("0x0a"), common.HexToAddress("0x0b"), common.HexToAddress("0x0c")

	cfg := &params.ChainConfig{
		ChainID: big.NewInt(chainID), HomesteadBlock: big.NewInt(0), EIP150Block: big.NewInt(0),
		EIP155Block: big.NewInt(0), EIP158Block: big.NewInt(0), ByzantiumBlock: big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0), PetersburgBlock: big.NewInt(0), IstanbulBlock: big.NewInt(0),
		BerlinBlock: big.NewInt(0), LondonBlock: big.NewInt(0), TIPFixedRewardBlock: big.NewInt(0),
		XDPoS: &params.XDPoSConfig{
			Epoch: 30, RewardCheckpoint: 30,
			FoudationWalletAddr: common.HexToAddress("0xf0"),
			V2: &params.V2{SwitchBlock: big.NewInt(0), CurrentConfig: &params.V2Config{
				MaxMasternodes: 1, MaxProtectorNodes: 5, MaxObserverNodes: 5,
				MasternodeReward: 63.42, ProtectorReward: 50.27, ObserverReward: 25.13,
			}},
		},
	}

	gspec := &core.Genesis{Config: params.AllEthashProtocolChanges, Difficulty: common.Big1, GasLimit: 8_000_000}
	_, blocks, _ := core.GenerateChainWithGenesis(gspec, ethash.NewFaker(), 61, nil)
	bc, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), gspec, ethash.NewFaker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Stop()
	if _, err := bc.InsertChain(blocks); err != nil {
		t.Fatal(err)
	}

	adaptor := XDPoS.New(&params.XDPoSConfig{Epoch: 30, V2: &params.V2{SwitchBlock: big.NewInt(0)}}, rawdb.NewMemoryDatabase())
	adaptor.SetEngineV2(&rewardFakeV2{mn: []common.Address{m}, standby: []common.Address{q}})
	AttachConsensusV2Hooks(adaptor, bc, cfg)

	// Signing txs: P and Q sign block 15, carried in block 16.
	signed := bc.GetHeaderByNumber(15)
	var txs []*types.Transaction
	for _, k := range []*ecdsa.PrivateKey{keys[1], keys[2]} {
		data := common.Hex2Bytes(common.HexSignMethod)
		data = append(data, common.LeftPadBytes(big.NewInt(15).Bytes(), 32)...)
		data = append(data, signed.Hash().Bytes()...)
		tx, err := types.SignTx(types.NewTransaction(0, common.BlockSignersBinary, new(big.Int), 200_000, new(big.Int), data),
			types.MakeSigner(cfg, big.NewInt(16), bc.GetHeaderByNumber(16).Time), k)
		if err != nil {
			t.Fatal(err)
		}
		txs = append(txs, tx)
	}
	for n := uint64(1); n <= 60; n++ {
		h := bc.GetHeaderByNumber(n)
		if n == 16 {
			adaptor.BlockSigners.Add(h.Hash(), txs)
		} else {
			adaptor.BlockSigners.Add(h.Hash(), []*types.Transaction{})
		}
	}

	parentState, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	setCandidate(parentState, 0, 2, m, ownM, 100)
	setCandidate(parentState, 1, 2, p, ownP, 50)
	// Q resigned before the parent state: it keeps its owner record but is no
	// longer in candidates[].
	locQ := state.GetLocMappingAtKey(common.BytesToHash(q.Bytes()), 1)
	parentState.SetState(common.MasternodeVotingSMCBinary, common.BigToHash(locQ), common.BytesToHash(ownQ.Bytes()))
	stateBlock, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())

	if _, err := adaptor.HookReward(bc, stateBlock, parentState, bc.GetHeaderByNumber(61)); err != nil {
		t.Fatalf("HookReward: %v", err)
	}
	return stateBlock.GetBalance(ownP).ToBig(), stateBlock.GetBalance(ownQ).ToBig()
}

// protectorOwnerShare is the owner's 90% of the fixed 50.27 XDC protector
// reward, computed on the same big.Float path as HookReward.
func protectorOwnerShare() *big.Int {
	r, _ := new(big.Float).Mul(new(big.Float).SetFloat64(50.27), new(big.Float).SetUint64(params.Ether)).Int(nil)
	return r.Div(r.Mul(r, big.NewInt(XDPoS.RewardMasterPercent)), big.NewInt(100))
}

func TestTieredRewardDevnet5551PaysParentStateProtectors(t *testing.T) {
	want := protectorOwnerShare()
	gotP, gotQ := runTieredReward(t, 5551)
	if gotP.Cmp(want) != 0 || gotQ.Sign() != 0 {
		t.Fatalf("chain 5551: owner(P)=%v owner(Q)=%v, want owner(P)=%v (parent-state candidate) owner(Q)=0", gotP, gotQ, want)
	}
}

func TestTieredRewardOtherChainsKeepStandbynodes(t *testing.T) {
	want := protectorOwnerShare()
	gotP, gotQ := runTieredReward(t, 551)
	if gotQ.Cmp(want) != 0 || gotP.Sign() != 0 {
		t.Fatalf("chain 551: owner(P)=%v owner(Q)=%v, want owner(P)=0 owner(Q)=%v (GetStandbynodes)", gotP, gotQ, want)
	}
}
