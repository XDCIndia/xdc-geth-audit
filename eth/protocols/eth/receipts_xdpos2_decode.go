// Copyright 2026 The go-ethereum Authors
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

package eth

import (
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// decodeReceiptsXDPOS2 decodes a bare receipts list received on the xdpos2
// wire. Each block's list is accepted in either of two shapes (#1889):
//
//	consensus (legacy XDPoSChain, modern >= xdc.20): [status, gas, bloom, logs]
//	eth/69 network form (xdc.19 on version 100):     [type, status, gas, logs]
//
// The shapes cannot be confused: the third field is a 256-byte string in the
// first and a list in the second (typed consensus receipts are plain strings),
// so the consensus decode always fails on an eth/69 receipt. The bloom is
// recomputed from the logs for the eth/69 form, so DeriveSha is unaffected.
func decodeReceiptsXDPOS2(raw rlp.RawValue) ([]types.Receipts, error) {
	var blocks []rlp.RawValue
	if err := rlp.DecodeBytes(raw, &blocks); err != nil {
		return nil, err
	}
	out := make([]types.Receipts, len(blocks))
	for i, b := range blocks {
		if err := rlp.DecodeBytes(b, &out[i]); err == nil {
			continue
		}
		var rl ReceiptList
		if err := rlp.DecodeBytes(b, &rl); err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		rs, err := rl.toReceipts()
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		out[i] = rs
	}
	return out, nil
}

// toReceipts converts an eth/69 network-form list into consensus receipts.
func (rl *ReceiptList) toReceipts() (types.Receipts, error) {
	rs := make(types.Receipts, 0, rl.items.Len())
	it := rl.items.ContentIterator()
	for it.Next() {
		var nr Receipt
		if err := nr.decode(it.Value()); err != nil {
			return nil, err
		}
		r := &types.Receipt{Type: nr.TxType, CumulativeGasUsed: nr.GasUsed}
		switch len(nr.PostStateOrStatus) {
		case 32:
			r.PostState = nr.PostStateOrStatus
		case 0:
			r.Status = types.ReceiptStatusFailed
		default:
			r.Status = types.ReceiptStatusSuccessful
		}
		if err := rlp.DecodeBytes(nr.Logs, &r.Logs); err != nil {
			return nil, err
		}
		r.Bloom = types.CreateBloom(r)
		rs = append(rs, r)
	}
	return rs, nil
}
