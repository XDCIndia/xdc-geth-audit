// Hand-maintained RLP encoder for Header.
// DO NOT run go:generate on this file — the generated output differs from the
// XDC-customised encoding and would break block-hash parity.
//
// Encoding order (matches the struct definition in block.go):
//   ParentHash · UncleHash · Coinbase · Root · TxHash · ReceiptHash · Bloom
//   · Difficulty · Number · GasLimit · GasUsed · Time · Extra · MixDigest · Nonce
//   [XDPoS always-present] · Validators · Validator · Penalties
//   [optional, trailing] · BaseFee · WithdrawalsHash · BlobGasUsed · ExcessBlobGas
//   · ParentBeaconRoot · RequestsHash
//   · BlockAccessListHash (EIP-7928, Amsterdam+)
//   · SlotNumber (EIP-7843, Amsterdam+; nil on XDC blocks — XDC miner omits it)
//
// Hash-compat guarantee: a nil optional field is encoded as a null placeholder
// only when a later optional field is non-nil. Fields that are always nil for
// a given block era are never written. Pre-Amsterdam XDC blocks have
// BlockAccessListHash=nil and SlotNumber=nil → _tmp7=false, _tmp8=false →
// existing conditions unchanged → identical RLP and block hash.

package types

import (
	"io"

	"github.com/ethereum/go-ethereum/rlp"
)

func (obj *Header) EncodeRLP(_w io.Writer) error {
	w := rlp.NewEncoderBuffer(_w)
	_tmp0 := w.List()
	w.WriteBytes(obj.ParentHash[:])
	w.WriteBytes(obj.UncleHash[:])
	w.WriteBytes(obj.Coinbase[:])
	w.WriteBytes(obj.Root[:])
	w.WriteBytes(obj.TxHash[:])
	w.WriteBytes(obj.ReceiptHash[:])
	w.WriteBytes(obj.Bloom[:])
	if obj.Difficulty == nil {
		w.Write(rlp.EmptyString)
	} else {
		if obj.Difficulty.Sign() == -1 {
			return rlp.ErrNegativeBigInt
		}
		w.WriteBigInt(obj.Difficulty)
	}
	if obj.Number == nil {
		w.Write(rlp.EmptyString)
	} else {
		if obj.Number.Sign() == -1 {
			return rlp.ErrNegativeBigInt
		}
		w.WriteBigInt(obj.Number)
	}
	w.WriteUint64(obj.GasLimit)
	w.WriteUint64(obj.GasUsed)
	w.WriteUint64(obj.Time)
	w.WriteBytes(obj.Extra)
	w.WriteBytes(obj.MixDigest[:])
	w.WriteBytes(obj.Nonce[:])
	// XDPoS fields - always encode (not optional)
	w.WriteBytes(obj.Validators)
	w.WriteBytes(obj.Validator)
	w.WriteBytes(obj.Penalties)
	_tmp1 := obj.BaseFee != nil
	_tmp2 := obj.WithdrawalsHash != nil
	_tmp3 := obj.BlobGasUsed != nil
	_tmp4 := obj.ExcessBlobGas != nil
	_tmp5 := obj.ParentBeaconRoot != nil
	_tmp6 := obj.RequestsHash != nil
	// EIP-7928 (Amsterdam): BAL hash in header. nil on pre-Amsterdam blocks.
	// XDC: always non-nil on Amsterdam+ blocks (set by state_processor.go).
	_tmp7 := obj.BlockAccessListHash != nil
	// EIP-7843 (Amsterdam): slot number. nil on XDC blocks (XDC miner omits it).
	_tmp8 := obj.SlotNumber != nil
	if _tmp1 || _tmp2 || _tmp3 || _tmp4 || _tmp5 || _tmp6 || _tmp7 || _tmp8 {
		if obj.BaseFee == nil {
			w.Write(rlp.EmptyString)
		} else {
			if obj.BaseFee.Sign() == -1 {
				return rlp.ErrNegativeBigInt
			}
			w.WriteBigInt(obj.BaseFee)
		}
	}
	if _tmp2 || _tmp3 || _tmp4 || _tmp5 || _tmp6 || _tmp7 || _tmp8 {
		if obj.WithdrawalsHash == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteBytes(obj.WithdrawalsHash[:])
		}
	}
	if _tmp3 || _tmp4 || _tmp5 || _tmp6 || _tmp7 || _tmp8 {
		if obj.BlobGasUsed == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteUint64((*obj.BlobGasUsed))
		}
	}
	if _tmp4 || _tmp5 || _tmp6 || _tmp7 || _tmp8 {
		if obj.ExcessBlobGas == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteUint64((*obj.ExcessBlobGas))
		}
	}
	if _tmp5 || _tmp6 || _tmp7 || _tmp8 {
		if obj.ParentBeaconRoot == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteBytes(obj.ParentBeaconRoot[:])
		}
	}
	if _tmp6 || _tmp7 || _tmp8 {
		if obj.RequestsHash == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteBytes(obj.RequestsHash[:])
		}
	}
	if _tmp7 || _tmp8 {
		if obj.BlockAccessListHash == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteBytes(obj.BlockAccessListHash[:])
		}
	}
	if _tmp8 {
		if obj.SlotNumber == nil {
			w.Write([]byte{0x80})
		} else {
			w.WriteUint64((*obj.SlotNumber))
		}
	}
	w.ListEnd(_tmp0)
	return w.Flush()
}
