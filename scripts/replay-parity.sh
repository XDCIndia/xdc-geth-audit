#!/usr/bin/env bash
# replay-parity.sh — replay canonical block ranges through a candidate geth with
# XDC full verification forced on, and fail if any canonical block is rejected.
#
# This is the gate for consensus-tightening PRs (tracker #1430). Each range is
# exported as RLP from a synced datadir, the work datadir is rewound to from-1,
# and the range is re-imported with XDC_FORCE_FULL_VERIFY=1, so the bulk-sync
# relaxations (light V1 seal path, V2 fullVerify=false, checkpointSyncNoState,
# A.93/A.94 state masking) are all off: every block is header-verified,
# executed, and its state root / receipt root recomputed.
#
# Usage
# -----
#   scripts/replay-parity.sh --datadir DIR [options] [FROM-TO ...]
#
#   --datadir DIR    Work datadir. IT IS REWOUND (blocks above each range start
#                    are deleted): use a disposable copy/snapshot. Must hold
#                    full state at every from-1, i.e. a hash-scheme archive
#                    (--gcmode archive --state.scheme hash). A range starting at
#                    1 works on an empty datadir (replay from genesis).
#   --source DIR     Datadir to export canonical blocks from (default: --datadir;
#                    exports then happen before any rewind). Skipped for ranges
#                    whose RLP already exists in --out.
#   --network NET    mainnet | apothem (default: mainnet).
#   --geth PATH      Candidate binary (default: build/bin/geth).
#   --out DIR        RLP files, logs and summary (default: ./replay-parity-out).
#   FROM-TO          Ranges to replay (default: the network's set below).
#   GETH_ARGS env    Extra flags for every geth call (e.g. "--cache 4096").
#
# Exit codes: 0 every block accepted; 1 a block was rejected or a rewind
# failed; 2 bad arguments.
#
# Default ranges (sources: common/types.go, params/config.go). V1 epochs are
# 900 blocks, so each V1 range spans 4 checkpoints; V2 ranges span several
# epoch switches past the fork.
MAINNET_RANGES=(
    2998200-3001800   # TIPSigning 3,000,000              common/types.go TIPSigning
    3462300-3465900   # TIPRandomize 3,464,000            common/types.go TIPRandomize
    38382000-38385600 # TIPTRC21Fee/TIPXDCX 38,383,838    common/types.go CopyXDCConstants(50)
    80368200-80375400 # V2 switch 80,370,000 (+~6 V2 epoch switches; XDCX miner/receiver
                      # disable 80,370,000/80,370,900)    params/config.go XDPoS.V2.SwitchBlock
    98799300-98805000 # EIP-1559 98,800,200, Cancun 98,802,000 (+3,000) params/config.go
)
APOTHEM_RANGES=(
    2998200-3001800   # TIPSigning 3,000,000              common/types.go CopyXDCConstants(51)
    3462300-3465900   # TIPRandomize 3,464,000 (default)  common/types.go TIPRandomize
    23777400-23781000 # TIPTRC21Fee/TIPXDCX 23,779,191    common/types.go CopyXDCConstants(51)
    56826900-56834100 # V2 switch 56,828,700 (+~6 V2 epoch switches) params/config.go
    71549100-71555000 # EIP-1559 71,550,000, Cancun 71,551,800 (+3,000) params/config.go
    83575300-83579000 # round 27,360,000 (25-masternode tier) -> block 83,577,112 params/config.go
    83598200-83601800 # TIPUpgradeReward/Penalty, Gas2500x 83,600,000 params/config.go
)

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GETH="$SCRIPT_DIR/../build/bin/geth"
NETWORK=mainnet
DATADIR=""
SOURCE=""
OUT="./replay-parity-out"
RANGES=()

usage() { sed -n '2,/^set -u/p' "$0" | sed -E 's/^# ?//' | sed '$d'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --datadir) DATADIR="$2"; shift 2 ;;
        --source)  SOURCE="$2"; shift 2 ;;
        --network) NETWORK="$2"; shift 2 ;;
        --geth)    GETH="$2"; shift 2 ;;
        --out)     OUT="$2"; shift 2 ;;
        -h|--help) usage ;;
        [0-9]*-[0-9]*) RANGES+=("$1"); shift ;;
        *) echo "unknown argument: $1" >&2; usage 2 ;;
    esac
done

case "$NETWORK" in
    mainnet) NETFLAG=--xdcmainnet; [[ ${#RANGES[@]} -eq 0 ]] && RANGES=("${MAINNET_RANGES[@]}") ;;
    apothem) NETFLAG=--apothem;    [[ ${#RANGES[@]} -eq 0 ]] && RANGES=("${APOTHEM_RANGES[@]}") ;;
    *) echo "--network must be mainnet or apothem" >&2; exit 2 ;;
esac
[[ -n "$DATADIR" ]] || { echo "--datadir is required" >&2; usage 2; }
[[ -x "$GETH" ]] || { echo "geth binary not found: $GETH" >&2; exit 2; }
SOURCE="${SOURCE:-$DATADIR}"
mkdir -p "$OUT"
read -r -a EXTRA <<< "${GETH_ARGS:-}"

# Rewinds only go backwards, so replay the highest range first: one disposable
# datadir then serves every range.
mapfile -t RANGES < <(printf '%s\n' "${RANGES[@]}" | sort -t- -k1,1nr)

# Export everything up front, before the first rewind deletes it.
for r in "${RANGES[@]}"; do
    from=${r%-*} to=${r#*-}
    (( from >= 1 && from <= to )) || { echo "bad range: $r" >&2; exit 2; }
    rlp="$OUT/$NETWORK-$r.rlp"
    [[ -s "$rlp" ]] && continue
    echo "export $r from $SOURCE"
    "$GETH" "$NETFLAG" --datadir "$SOURCE" "${EXTRA[@]}" export "$rlp" "$from" "$to" \
        > "$OUT/$NETWORK-$r.export.log" 2>&1 || { echo "export of $r failed, see $OUT/$NETWORK-$r.export.log" >&2; exit 2; }
done

summary="$OUT/$NETWORK-summary.tsv"
printf 'range\tresult\tblocks\telapsed_s\tbad_number\tbad_hash\tstate_root_mismatch\treceipt_root_mismatch\terror\n' > "$summary"
fail=0
for r in "${RANGES[@]}"; do
    from=${r%-*} to=${r#*-}
    log="$OUT/$NETWORK-$r.import.log"

    # Rewind to from-1 so the range is genuinely re-executed (geth import skips
    # blocks it already has). The node starts offline only for this call.
    got=$("$GETH" "$NETFLAG" --datadir "$DATADIR" "${EXTRA[@]}" --nodiscover --maxpeers 0 --port 0 \
        --ipcpath "$DATADIR/replay-parity.ipc" \
        --exec "debug.setHead('0x$(printf %x $((from - 1)))'); eth.blockNumber" console 2>"$OUT/$NETWORK-$r.rewind.log" | tail -n1)
    if [[ "$got" != "$((from - 1))" ]]; then
        echo "FAIL $r: rewind to $((from - 1)) left head at '${got}' (state missing? see $OUT/$NETWORK-$r.rewind.log)"
        printf '%s\tREWIND_FAILED\t0\t0\t\t\t\t\thead=%s\n' "$r" "$got" >> "$summary"
        fail=1; continue
    fi

    start=$(date +%s)
    XDC_FORCE_FULL_VERIFY=1 "$GETH" "$NETFLAG" --datadir "$DATADIR" "${EXTRA[@]}" \
        import "$OUT/$NETWORK-$r.rlp" > "$log" 2>&1
    rc=$? elapsed=$(( $(date +%s) - start ))

    grep -q "XDC_FORCE_FULL_VERIFY" "$log" && echo "note $r: node tried to enter checkpointSyncNoState and was refused (see log)"
    if [[ $rc -eq 0 ]]; then
        echo "OK   $r: $((to - from + 1)) blocks accepted in ${elapsed}s"
        printf '%s\tOK\t%d\t%d\t\t\t0\t0\t\n' "$r" $((to - from + 1)) "$elapsed" >> "$summary"
        continue
    fi
    fail=1
    # "Import error ... invalid block N: <err>" is authoritative; the BAD BLOCK
    # report (when the chain emitted one) adds the hash.
    err=$(grep -m1 -o 'invalid block [0-9]*: .*' "$log" | sed 's/"$//' || true)
    [[ -n "$err" ]] || err=$(tail -n1 "$log")
    num=$(sed -nE 's/^invalid block ([0-9]+):.*/\1/p' <<< "$err")
    hash=$(grep -m1 -oE 'Hash: *0x[0-9a-f]{64}' "$log" | grep -oE '0x[0-9a-f]{64}')
    sr=0; rr=0
    grep -q 'invalid merkle root' <<< "$err" && sr=1
    grep -q 'invalid receipt root hash' <<< "$err" && rr=1
    imported=$(( ${num:-$from} - from ))
    echo "FAIL $r: block ${num:-?} ${hash:-} rejected after $imported blocks (${elapsed}s): $err"
    printf '%s\tREJECTED\t%d\t%d\t%s\t%s\t%d\t%d\t%s\n' "$r" "$imported" "$elapsed" "${num:-}" "${hash:-}" $sr $rr "$err" >> "$summary"
done

echo "summary: $summary"
exit $fail
