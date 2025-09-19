# PBFT Configuration

This directory contains the configuration file for the PBFT (Practical Byzantine Fault Tolerance) consensus algorithm implementation.

## Configuration File: run.json

The `run.json` file defines the parameters for running the PBFT consensus system:

### Data Configuration
- **data_dir**: Path to the dataset file containing transactions to be processed
  - Current value: `"data/len3_data.csv"`
  - This CSV file contains the transaction data that will be injected into the system

### Transaction Processing
- **max_tx_num**: Maximum number of transactions to be injected into the system
  - Current value: `240000`
  - This limits the total number of transactions that will be processed

- **inject_speed**: Rate at which transactions are injected (transactions per time unit)
  - Current value: `1000`
  - Controls the throughput of transaction injection

- **max_block_size**: Maximum number of transactions per block
  - Current value: `1000`
  - Determines the block size limit for consensus

### Network Configuration
- **node_num**: Total number of nodes in the PBFT network
  - Current value: `4`
  - This defines the size of the consensus network

- **node_id**: Identifier for the current node instance
  - Current value: `0`
  - Each node should have a unique ID (0 to node_num-1)

## Usage

To run the PBFT system, ensure that:
1. The dataset file exists at the specified `data_dir` path
2. Each node instance has a unique `node_id` (0, 1, 2, 3 for a 4-node network)
3. All nodes use the same `node_num` value
4. The configuration parameters are appropriate for your use case

## Example Configuration

```json
{
    "data_dir": "data/len3_data.csv",
    "max_tx_num": 240000,
    "inject_speed": 1000,
    "max_block_size": 1000,
    "node_num": 4,
    "node_id": 0
}
```