#!/bin/bash

# quorum-check.sh: Check if safe to stop a masternode without losing quorum
#
# Usage:
#   ./quorum-check.sh <RPC_URL> [--json]
#
# Arguments:
#   RPC_URL  - JSON-RPC endpoint (e.g., http://localhost:8545)
#   --json   - Output JSON instead of human text
#
# Exit codes:
#   0 - SAFE to stop one more node (quorum would still be maintained)
#   1 - UNSAFE (stopping would lose quorum)
#

set -e

# Parse arguments
RPC_URL="${1:-}"
JSON_OUTPUT=0

if [ -z "$RPC_URL" ]; then
    echo "Usage: $0 <RPC_URL> [--json]" >&2
    exit 1
fi

if [ "$2" = "--json" ]; then
    JSON_OUTPUT=1
fi

# Helper to make JSON-RPC calls
rpc_call() {
    local method="$1"
    local params="$2"
    curl -s "$RPC_URL" \
        -X POST \
        -H "Content-Type: application/json" \
        -d "{\"jsonrpc\":\"2.0\",\"method\":\"$method\",\"params\":$params,\"id\":1}" \
        2>/dev/null || echo '{"error":"RPC call failed"}'
}

# Get the latest block number
get_latest_block() {
    local result
    result=$(rpc_call "eth_blockNumber" "[]")

    # Extract hex block number and convert to decimal
    local hex_block
    hex_block=$(echo "$result" | python3 -c "import sys, json; data=json.load(sys.stdin); print(data.get('result', '0'))" 2>/dev/null || echo "0x0")

    # Convert hex to decimal
    printf "%d" "$hex_block" 2>/dev/null || echo "0"
}

# Get masternodes for a given block
get_masternodes() {
    local block_num="$1"
    local block_hex
    block_hex=$(printf "0x%x" "$block_num")

    local result
    result=$(rpc_call "XDPoS_getMasternodesByNumber" "[\"$block_hex\"]")
    echo "$result"
}

# Get block miner for a given block number
get_block_miner() {
    local block_num="$1"
    local block_hex
    block_hex=$(printf "0x%x" "$block_num")

    local result
    result=$(rpc_call "eth_getBlockByNumber" "[\"$block_hex\",false]")

    # Extract miner address
    echo "$result" | python3 -c "import sys, json; data=json.load(sys.stdin); print(data.get('result', {}).get('miner', '').lower())" 2>/dev/null || echo ""
}

# Main logic
main() {
    local latest_block
    latest_block=$(get_latest_block)

    if [ "$latest_block" -eq 0 ]; then
        if [ $JSON_OUTPUT -eq 1 ]; then
            echo '{"error":"Could not get latest block"}'
        else
            echo "ERROR: Could not get latest block" >&2
        fi
        return 1
    fi

    # Get current seated masternodes
    local mn_json
    mn_json=$(get_masternodes "$latest_block")

    # Parse masternodes list
    local seated_list
    seated_list=$(echo "$mn_json" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    result = data.get('result', {})

    # Handle two possible formats:
    # 1. Direct list: [addresses...]
    # 2. Object with masternodes key: {masternodes: [...]}
    if isinstance(result, list):
        for addr in result:
            if isinstance(addr, str):
                print(addr.lower())
            elif isinstance(addr, dict):
                print(addr.get('address', '').lower())
    elif isinstance(result, dict):
        masternodes = result.get('masternodes', [])
        for addr in masternodes:
            if isinstance(addr, str):
                print(addr.lower())
            elif isinstance(addr, dict):
                print(addr.get('address', '').lower())
except Exception as e:
    pass
" 2>/dev/null)

    # Count seated masternodes
    local seated_count
    seated_count=$(echo "$seated_list" | wc -l)
    seated_count=$((seated_count))  # Remove leading whitespace

    # If no masternodes found, return error
    if [ "$seated_count" -eq 0 ]; then
        if [ $JSON_OUTPUT -eq 1 ]; then
            echo '{"error":"No masternodes found"}'
        else
            echo "ERROR: No masternodes found" >&2
        fi
        return 1
    fi

    # Build a set of seated addresses (lowercase)
    local -A seated_addr_map
    while IFS= read -r addr; do
        if [ -n "$addr" ]; then
            seated_addr_map["${addr}"]="1"
        fi
    done <<<"$seated_list"

    # Check how many seated nodes have mined recently
    # We check the last 2N blocks to see recent activity
    local blocks_to_check=$((seated_count * 2))
    local start_block=$((latest_block - blocks_to_check + 1))

    if [ "$start_block" -lt 0 ]; then
        start_block=0
    fi

    local -A active_miners

    # Collect all miners from recent blocks
    for ((i = start_block; i <= latest_block; i++)); do
        local miner
        miner=$(get_block_miner "$i")

        if [ -n "$miner" ]; then
            # Check if this miner is in the seated set
            if [ -n "${seated_addr_map[$miner]}" ]; then
                active_miners["$miner"]="1"
            fi
        fi
    done

    # Count active miners
    local active_count=${#active_miners[@]}

    # Calculate threshold: ceil(2N/3)
    # Using Python for accurate ceiling division
    local threshold
    threshold=$(python3 -c "import math; print(math.ceil(2 * $seated_count / 3))" 2>/dev/null || echo "0")

    # Determine if safe
    local safe=0
    local safe_str="UNSAFE"

    if [ "$active_count" -ge "$threshold" ]; then
        safe=0
        safe_str="SAFE"
    else
        safe=1
        safe_str="UNSAFE"
    fi

    # Output results
    if [ $JSON_OUTPUT -eq 1 ]; then
        printf '{"seated":%d,"active":%d,"threshold":%d,"status":"%s","safe":%d}' \
            "$seated_count" "$active_count" "$threshold" "$safe_str" "$safe"
        echo
    else
        printf "N=%d  U=%d  threshold=%d  %s\n" \
            "$seated_count" "$active_count" "$threshold" "$safe_str"
    fi

    # Return appropriate exit code
    return $safe
}

main "$@"
