package engine_v2

import (
	"math/big"
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Helpers and tests in this file were copied from standbynodes_test.go because that file is not in the audit tree.

func standbyAddrs(n int) []common.Address {
	out := make([]common.Address, n)
	for i := range out {
		out[i] = common.BigToAddress(big.NewInt(int64(0x1000 + i)))
	}
	return out
}

func packAddrs(addrs []common.Address) []byte {
	var b []byte
	for _, a := range addrs {
		b = append(b, a.Bytes()...)
	}
	return b
}

// TestGetStandbynodes pins the XinFinOrg/XDPoSChain #2612 standby pool: the
// candidates of the epoch's gap-block snapshot, in snapshot (stake) order, minus
// the epoch's masternodes and penalties.
func TestGetStandbynodes(t *testing.T) {
	const epoch, gap = 900, 450
	a := standbyAddrs(8) // snapshot candidate order is the stake order
	eng := newA975Engine(0, epoch, gap)

	chain := &mockChain1330{byNumber: map[uint64]*types.Header{}}
	seedGap := func(number uint64, candidates []common.Address) {
		h := &types.Header{Number: new(big.Int).SetUint64(number), Extra: []byte{byte(number)}}
		chain.byNumber[number] = h
		eng.snapshots.Add(h.Hash(), newSnapshot(number, h.Hash(), candidates))
	}
	switchHeader := func(number uint64, masternodes, penalties []common.Address) *types.Header {
		return &types.Header{
			Number:     new(big.Int).SetUint64(number),
			Validators: packAddrs(masternodes),
			Penalties:  packAddrs(penalties),
		}
	}

	// Gap block 2250 backs the epochs switching at 2700 and at 3150.
	seedGap(2250, a)
	// A different snapshot at 3150: an epoch switch on a gap-block number must
	// not be read as its own gap block.
	seedGap(3150, []common.Address{a[7], a[6]})

	for _, tc := range []struct {
		name   string
		header *types.Header
		want   []common.Address
	}{
		{
			name:   "epoch switch on an epoch boundary",
			header: switchHeader(2700, []common.Address{a[0], a[2]}, []common.Address{a[4]}),
			want:   []common.Address{a[1], a[3], a[5], a[6], a[7]},
		},
		{
			name:   "epoch switch on a gap-block number uses the previous gap block",
			header: switchHeader(3150, []common.Address{a[0], a[2]}, []common.Address{a[4]}),
			want:   []common.Address{a[1], a[3], a[5], a[6], a[7]},
		},
		{
			name:   "every candidate is a masternode",
			header: switchHeader(2700, a, nil),
			want:   []common.Address{},
		},
		{
			name:   "missing snapshot",
			header: switchHeader(900, []common.Address{a[0]}, nil),
			want:   []common.Address{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eng.GetStandbynodes(chain, tc.header)
			if tc.name == "missing snapshot" {
				if err == nil {
					t.Fatal("missing snapshot returned no error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("standby pool\n have %v\n want %v", got, tc.want)
			}
		})
	}
}
