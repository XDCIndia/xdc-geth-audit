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

// pivot_verify_xdc.go: Hacken F-2026-19471 residual — fast-sync pivot hash and
// state-root verification before state download begins.
//
// Prior to this fix, processFastSyncContent started trie state download using
// the compiled operatorPivotRoot without first confirming that the pivot block
// served by the peer actually matches the compiled hash.  An operator-pinned
// pivot was therefore "trusted on faith" from a compiled binary value rather
// than verified against the live chain.
//
// This file extracts that decision into pivotStateRoot so it can be unit-tested
// and applies two changes to the operator-pinned path:
//
//  1. Early hash check: when operatorPivotHash is set, reject a served pivot
//     block whose hash does not match BEFORE state download begins.  Previously
//     the mismatch was only caught after the full trie download completed.
//
//  2. Use served root: when the hash matches, use the served header's own
//     state root (the on-chain truth) rather than the compiled operatorPivotRoot.
//     A compiled root that disagrees with the served header's root would cause
//     the node to download state that does not belong to the block it committed.
//
// The Hacken recommendation also asked for full QC-signature verification of the
// pivot before it is installed.  TrustedSyncCheckpoint does not carry a quorum
// certificate, so cryptographic QC verification is deferred to a future work item
// when QC data is added to the checkpoint struct.  The network-level confirmation
// provided here (hash must match a compiled anchor confirmed by a peer) is the
// strongest check available within the current struct shape.

package downloader

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// pivotStateRoot returns the state-trie root to use when downloading trie state
// for the fast-sync pivot block.  Called once the pivot header P has been
// received from a peer — at the point processFastSyncContent first sees the
// pivot result (oldPivot != P branch).
//
// Decision table (Hacken F-2026-19471 residual):
//
//	opHash set, P.Hash matches  →  P.Root  (on-chain truth; hash peer-verified)
//	opHash set, P.Hash mismatch →  error   (abort; do not start state download)
//	opHash not set, opRoot set  →  opRoot  (legacy: operator root without hash)
//	opHash not set, opRoot not set → P.Root (dynamic-pivot path)
//
// The first case is the residual fix: previously the code always chose opRoot
// when opRoot was non-zero, regardless of whether opHash agreed with the peer's
// header.
func pivotStateRoot(pivotHeader *types.Header, opHash, opRoot common.Hash) (common.Hash, error) {
	if opHash != (common.Hash{}) {
		// Operator supplied a compiled anchor hash — verify it against the served header.
		if pivotHeader.Hash() != opHash {
			return common.Hash{}, fmt.Errorf(
				"fast sync: pivot hash mismatch — operator pinned %s, peer served %s",
				opHash.Hex(), pivotHeader.Hash().Hex())
		}
		// Hash confirmed: use the on-chain root rather than the compiled root.
		// If they differ, the compiled root was wrong; the served root is authoritative.
		return pivotHeader.Root, nil
	}
	if opRoot != (common.Hash{}) {
		// No hash to verify against — fall back to the compiled root (legacy behaviour).
		return opRoot, nil
	}
	// Dynamic-pivot path: use whatever root the served header carries.
	return pivotHeader.Root, nil
}
