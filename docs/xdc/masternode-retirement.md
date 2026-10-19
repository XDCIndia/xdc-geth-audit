# Masternode Retirement and Maintenance

**Purpose**: This guide helps you stop, restart, or add masternodes on XDC Network without slowing blocks or stopping the chain.

**Audience**: Masternode operators who run XDPoS v2.

---

## Why a Stopped Masternode Slows the Network

### Seat Removal Takes Time

In XDPoS v2, a masternode keeps its seat for about 3 epochs after it stops running.

The seat does not go away immediately.

### Timeout Cost per Round

On each round where the stopped node is the leader, other nodes wait for a timeout.

The timeout is 10 seconds.

Then these nodes make a timeout certificate (TC).

The round advances by 2 instead of the normal 1.

### Evidence from net2030

We tested on net2030 (issue #1633). These are actual measurements:

| Window | Seated / Up | Avg Block Time | Blocks 10–11 s | Status |
|--------|------------|---|---|---|
| 24,000–24,600 | 4 / 4 | 2.00 s | 0 | Normal |
| 17,000–17,600 | 5 / 4 (one stopped) | 4.01 s | 149 | Slow |
| 13,300–13,900 | 4 / 3 (one stopped) | 4.68 s | 197 | Slow |
| 9,926+ | 5 / 2 (two stopped) | No blocks | — | Stalled |

With one stopped node, blocks take 4 seconds instead of 2.

With two stopped nodes, the network stops completely.

---

## The Quorum Rule

### Why Quorum Matters

XDPoS v2 needs agreement to make blocks and move rounds.

A timeout certificate needs votes from at least 2/3 of the seated masternodes.

If not enough nodes vote, the network stalls.

### How to Calculate the Threshold

Count the seated masternodes. Call this N.

The minimum votes needed is: `ceil(2N / 3)`

This is "ceiling of two-thirds of N".

Examples:

- N = 4: threshold = `ceil(8/3)` = `ceil(2.67)` = **3 votes** (3/4 up)
- N = 5: threshold = `ceil(10/3)` = `ceil(3.33)` = **4 votes** (4/5 up)
- N = 6: threshold = `ceil(12/3)` = **4 votes** (4/6 up)

### The Unsafe Zone

Never stop a node if the remaining up nodes would fall below the threshold.

**Safe check**: Count up nodes after the stop. If `up_remaining ≥ ceil(2N / 3)`, you can stop.

**Unsafe check**: If `up_remaining < ceil(2N / 3)`, do NOT stop. The network will stall.

Use the `quorum-check.sh` script to verify before you stop any node.

---

## Safe Procedure to Stop a Masternode

### Step 1: Resign the Masternode

To stop a masternode for longer than a restart, first resign it.

You resign through the XDC validator contract at address `0x88`.

Call the validator contract. Use the `resign()` function.

Your masternode is now marked for removal.

### Step 2: Wait for the Epoch Switch

Resignation does not remove the seat immediately.

The seat is removed at the next epoch boundary.

An epoch is 360 blocks in XDPoS v2.

At 2-second block times, one epoch takes about 12 minutes.

Wait for the epoch to finish. Monitor the blockchain.

You can check `epoch` in the block header.

### Step 3: Stop the Node

After the epoch switches and your node is no longer in the seated set, stop it.

Stop the node process. Do not remove its data.

Example:
```bash
systemctl stop xdc-node
```

### Step 4: Restart When Ready

To restart, start the node process again.

The node will sync from its stored data.

Do not wipe the data directory.

Do not reset the chaindata.

Example:
```bash
systemctl start xdc-node
```

---

## Rolling Restarts of Masternodes

### One Node at a Time

Restart only one masternode at a time.

Do not restart multiple nodes in parallel.

### Wait for the Tip

After you restart a node, wait until it is at the chain tip.

The tip is the latest block height.

Check this with:
```bash
curl http://localhost:8545 -X POST \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

Compare the returned block number with the network tip. They must match.

Only then restart the next node.

### Check Quorum Before Each Restart

Before you restart any node, run `quorum-check.sh`.

It must print `SAFE`.

Do not restart if it prints `UNSAFE`.

---

## Adding Nodes When Margin Is Thin

### Add Before You Stop

If you have only the minimum number of nodes running, add more before you stop any.

Do not attempt to add and remove in the same epoch.

### Process

If you plan to add a new node:

1. Prepare the new node hardware.
2. Deploy the node software.
3. Generate or import its validator key.
4. Call `register()` on the validator contract (`0x88`).
5. Wait for the epoch switch to activate the new masternode.
6. Verify the node appears in `XDPoS_getMasternodesByNumber("latest")`.
7. Only then can you safely stop another node.

---

## The Gap-Block Rule

### What Is a Gap Block

A proposal is valid only if it is made before the gap block of its epoch.

The gap block is the last block of the epoch before the one that uses the proposal.

In practice, a masternode that is seated in the current epoch can propose in the current and next epoch only if the proposal happens before the gap block.

### Implication for Seating

After you resign and wait for an epoch switch, your node might not stop being seated immediately.

The removal can take up to one epoch after the resignation.

Account for this delay in your maintenance schedule.

### Example

- Block 1000: You resign your node. It is still seated for the current epoch.
- Block 1360 (one epoch later): Epoch switches. Your node is now removed.
- Now you can safely stop the node.

---

## KYC Requirement for Registering Masternodes

### Who Can Register New Candidates

To register a new masternode candidate, the owner account must satisfy the `onlyKYCWhitelisted` requirement on the validator contract (0x88).

This means the account must have either uploaded KYC (called `uploadKYC()`) or already own a masternode candidate.

### Calling propose()

When you call `propose()` on the validator contract without meeting KYC requirements, the transaction reverts with status 0x0, consuming about 28,000 gas.

### Seated Nodes and Blocks

A seated masternode that is running produces blocks regardless of KYC status.

KYC only controls whether you can register new candidates.

### Before You Register

Before you register a new masternode, verify that your account is KYC-approved or already owns a candidate.

If needed, contact the XDC Network operations team to upload KYC.

---

## Permanent Retirement vs. Temporary Stop

### Stopped Nodes Can Return

A penalized masternode is removed from the seated set but remains a candidate.

After its penalty period expires (about 3 epochs), it rejoins the seated set automatically if it is still running and has not been resigned.

### Resign for Permanent Retirement

To permanently retire a masternode, call `resign()` on the validator contract.

Resignation removes the node from the candidate list permanently.

After resignation and the epoch switch that removes it, the node can be stopped without risk of automatic re-seating.

### Example Timelines

**Temporary stop (node returns)**:
1. Stop node (no resignation)
2. Node is penalized and removed from seat
3. After ~3 epochs, node rejoins if still a candidate
4. Node is re-seated (slowing blocks again)

**Permanent retirement (node stays down)**:
1. Call `resign()` on validator contract
2. Wait for epoch switch
3. Node is removed from candidates list
4. Stop node
5. Node will never re-seat

---

## Monitoring and Alerts

### Manual Quorum Check

Before any maintenance, run:

```bash
scripts/xdc/quorum-check.sh http://localhost:8545
```

This script reads the masternode list and your node's activity.

It prints:
- N: number of seated masternodes
- U: number of up (active) masternodes
- Threshold: minimum needed for quorum
- SAFE or UNSAFE

### Exit Codes

- Exit code 0: SAFE to stop one more node
- Exit code 1: UNSAFE; do not stop any node

### JSON Output

For automation, use:

```bash
scripts/xdc/quorum-check.sh --json http://localhost:8545
```

This outputs JSON suitable for monitoring systems.

---

## Troubleshooting

### Network Stalled After a Stop

If the network stalls, one or more seated nodes are down and the quorum is lost.

Restart the stopped nodes immediately.

The network resumes as soon as quorum is restored.

### Epoch Not Advancing

If the epoch does not advance after a stop, the network cannot form a timeout certificate.

Check that enough nodes are up. Run `quorum-check.sh`.

If quorum is not met, restart the stopped nodes.

### Block Time Increasing

If blocks take longer than 4 seconds on average, check if a seated node is down.

Run `quorum-check.sh` to see the status.

If a node is down, either restart it or resign and wait for the epoch to remove it.

---

## Summary

- A stopped masternode keeps its seat for about 3 epochs. Each of its turns costs a 10-second timeout.
- The network needs at least 2/3 of the seated set to make blocks.
- Before you stop a masternode, resign it on the validator contract and wait for the epoch switch.
- Never stop a node if the remaining up nodes fall below the quorum threshold.
- Use `quorum-check.sh` before any maintenance.
- Restart nodes one at a time and wait for each to catch up to the tip.
- Add new nodes before you stop others if the margin is thin.

---

## SkyNet Monitoring and Alerts

To prevent stalls caused by too many masternodes down, add these alert rules to SkyNet (XDCIndia/SkyNet):

### Alert 1: Seated Node Missed Turns

**Name**: `masternode_missed_turns`

**Description**: A masternode is seated but has not mined for 2 of its recent turns.

**Condition**:
- A masternode appears in `XDPoS_getMasternodesByNumber("latest")`.
- That node did not produce blocks in at least 2 of its expected turns (last 2N blocks, where N = number of seated nodes).

**Action**: Page on-call. This node may be hung or unreachable.

**Example**: With 5 seated nodes, check the last 10 blocks. If a seated node appears in 0 or 1 of them, alert.

### Alert 2: Predicted Quorum Loss

**Name**: `predicted_quorum_loss`

**Description**: The number of active masternodes is below the quorum threshold. The network is at risk of stall.

**Condition**:
- Count seated nodes (N) from `XDPoS_getMasternodesByNumber("latest")`.
- Count active nodes (U) from recent blocks (last 2N blocks).
- Calculate threshold = ceil(2N / 3).
- If U < threshold, the network cannot form timeout certificates and blocks will stall.

**Action**: Page on-call immediately. Restart down nodes or increase the seated set urgently.

**Example**: With N=5 (threshold=4), if only 3 nodes are active, trigger alert. At N=5, U=2 (like net2030 block 9,926), the network stalls.

### Implementation Note

Both alerts can be computed from the output of `scripts/xdc/quorum-check.sh`:
- Alert 1: Compare miners in recent blocks against the seated set.
- Alert 2: Alert when the script prints `UNSAFE`.

---

## Related

- [XDPoS v2 Consensus Guide](#) (link to be added)
- [Validator Contract 0x88](https://explorer.xinfin.network/address/0x0000000000000000000000000000000000000088)
- Issue #1633: Net2030 measurement evidence
