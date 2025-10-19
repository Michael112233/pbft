# PBFT Implementation - How to Run

## Overview
This document provides step-by-step instructions for running the PBFT (Practical Byzantine Fault Tolerance) implementation. The system supports both local and remote execution modes with multiple deployment options.

## Table of Contents
1. [Prerequisites](#prerequisites)
2. [Project Structure](#project-structure)
3. [Configuration](#configuration)
4. [Local Execution](#local-execution)
5. [Remote Execution](#remote-execution)
6. [Scripts and Automation](#scripts-and-automation)
7. [Data Management](#data-management)
8. [Results and Analysis](#results-and-analysis)
9. [Troubleshooting](#troubleshooting)
10. [Performance Tuning](#performance-tuning)

---

## Prerequisites

You can follow `script/environment_setup.sh`

### System Requirements
- **Go**: Version 1.19 or higher
- **Python**: Version 3.7 or higher (for scripts and plotting)
- **Operating System**: Linux, macOS, or Windows
- **Memory**: At least 4GB RAM (8GB+ recommended for large datasets)
- **Storage**: At least 2GB free space for logs and results

### Required Python Packages
```bash
pip install -r requirements.txt
```
Required packages:
- `pandas>=1.3.0`
- `matplotlib>=3.5.0`
- `numpy>=1.21.0`
- `paramiko>=2.7.0`

### Go Dependencies
The project uses Go modules. Dependencies will be automatically downloaded when building:
```bash
go mod tidy
```

---

## Project Structure

```
pbft/
├── main.go                    # Main entry point
├── go.mod                     # Go module definition
├── go.sum                     # Go module checksums
├── requirements.txt           # Python dependencies
├── config/
│   ├── config.go             # Configuration management
│   ├── network.go            # Network setup
│   └── run.json              # Runtime configuration
├── core/                     # Core data structures
│   ├── message.go            # Message types
│   ├── block.go              # Block structure
│   ├── blockchain.go         # Blockchain management
│   └── transaction.go        # Transaction structure
├── node/                     # PBFT node implementation
│   ├── node.go               # Node main logic
│   ├── send.go               # Message sending
│   ├── receive.go            # Message receiving
│   ├── viewChange.go         # View change protocol
│   ├── garbageCollection.go  # Garbage collection
│   └── nodeMessageHub.go     # Network communication
├── client/                   # Client implementation
│   ├── client.go             # Client main logic
│   ├── send.go               # Request sending
│   ├── receive.go            # Reply handling
│   └── clientMessageHub.go   # Client networking
├── data/                     # Transaction data
│   ├── len3_data.csv         # Sample transaction data
│   └── data_process.go       # Data processing
├── script/                   # Automation scripts
│   ├── parallel_runner.py    # Remote execution
│   ├── plot_tps.py           # Performance plotting
│   └── download_dataset.py   # Data download
├── logs/                     # Runtime logs
├── result/                   # Performance results
└── docs/                     # Documentation
```

---

## Configuration

### Configuration File (`config/run.json`)
```json
{
    "data_dir": "data/len3_data.csv",
    "max_tx_num": 1000000,
    "inject_speed": 50000,
    "max_block_size": 10,
    "experiment_mode": "local",
    "node_num": 4,
    "run_time": 60,
    "election_method": "round_robin",
    "expire_time": 20,
    "seq_number_upper_bound": 500000,
    "seq_number_lower_bound": 1000,
    "checkpoint_interval": 4
}
```

### Configuration Parameters
- **`data_dir`**: Path to transaction data file
- **`max_tx_num`**: Maximum number of transactions to process
- **`inject_speed`**: Transactions per second injection rate
- **`max_block_size`**: Maximum transactions per block
- **`experiment_mode`**: "local" or "remote" (<u>no use</u>)
- **`node_num`**: Number of PBFT nodes (minimum 4)
- **`run_time`**: Experiment duration in seconds
- **`election_method`**: Leader election algorithm ("round_robin")
- **`expire_time`**: Request timeout in seconds
- **`seq_number_upper_bound`**: Maximum sequence number
- **`seq_number_lower_bound`**: Minimum sequence number
- **`checkpoint_interval`**: Garbage collection interval

---

## Local Execution

### Method 1: Using Shell Scripts (Recommended)

#### macOS
```bash
# Make script executable
chmod +x run_project_macos.sh

# Run the experiment
./run_project_macos.sh
```

#### Linux
```bash
# Make script executable
chmod +x run_project_linux.sh

# Run the experiment
./run_project_linux.sh
```

### Method 2: Manual Execution

#### Step 1: Build the Binary
```bash
# Build for current platform
go build -o pbft_main main.go
```

#### Step 2: Start Nodes
Open multiple terminal windows and run:

**Terminal 1 - Node 0 (Leader)**
```bash
./pbft_main -r node -m local -n 0
```

**Terminal 2 - Node 1**
```bash
./pbft_main -r node -m local -n 1
```

**Terminal 3 - Node 2**
```bash
./pbft_main -r node -m local -n 2
```

**Terminal 4 - Node 3**
```bash
./pbft_main -r node -m local -n 3
```

**Terminal 5 - Client**
```bash
./pbft_main -r client -m local
```

#### Step 3: Monitor Execution
- Check logs in `logs/` directory
- Monitor performance in `logs/result.log`
- View real-time TPS in terminal output

---

## Remote Execution

### Prerequisites for Remote Execution
- SSH access to remote servers
- Go installed on remote machines
- Network connectivity between servers
- SSH key authentication configured

### Method 1: Using Parallel Runner Script

#### Step 1: Configure Remote Servers
Edit `script/parallel_runner.py`:
```python
# CloudLab configuration
HOST = "your-server.wisc.cloudlab.us"
USERNAME = "your-username"
KEY_PATH = os.path.expanduser("~/.ssh/id_rsa")

# Server configuration: port -> (role, nodeID)
SERVER_CONFIG = {
    25410: ("client", None),      # Client
    25411: ("node", 0),          # Node 0
    25412: ("node", 1),          # Node 1
    25413: ("node", 2),          # Node 2
    25414: ("node", 3),          # Node 3
}
```

#### Step 2: Set SSH Key Passphrase
```bash
export SSH_KEY_PASSPHRASE="your-ssh-key-passphrase"
```

#### Step 3: Run Remote Execution
```bash
# Activate virtual environment
source venv/bin/activate

# Run parallel execution
python3 script/parallel_runner.py
```

### Method 2: Manual Remote Setup

#### Step 1: Upload Code to Remote Servers
```bash
# Copy code to all servers
scp -r pbft/ user@server1:/home/user/
scp -r pbft/ user@server2:/home/user/
scp -r pbft/ user@server3:/home/user/
scp -r pbft/ user@server4:/home/user/
```

#### Step 2: Setup Environment on Remote Servers
```bash
# On each remote server
cd pbft/
chmod +x script/environment_setup.sh
./script/environment_setup.sh
```


#### Step 3: Start Remote Nodes
```bash
# On each server, run the appropriate node
./pbft_main -r node -m remote -n 0  # Server 1
./pbft_main -r node -m remote -n 1  # Server 2
./pbft_main -r node -m remote -n 2  # Server 3
./pbft_main -r node -m remote -n 3  # Server 4
./pbft_main -r client -m remote     # Client server
```

---

## Scripts and Automation

### Parallel Runner Script
The `script/parallel_runner.py` script automates remote execution:

**Features:**
- Connects to multiple remote servers
- Starts nodes and client automatically
- Handles SSH authentication
- Monitors execution status
- Provides detailed logging

**Usage:**
```bash
python3 script/parallel_runner.py
```

### Remote Controller Script
The `script/remote_controller.py` provides additional remote management:

**Features:**
- Individual server control
- Status monitoring
- Log collection
- Performance analysis

### Environment Setup Script
The `script/environment_setup.sh` prepares remote servers:

**Features:**
- Installs Go 1.23.0
- Sets up Python environment
- Configures system dependencies
- Validates installation

---

## Data Management

### Transaction Data
The system uses CSV files for transaction data:

**Format:**
```csv
sender,receiver,value
0xc418969d5f8948d9a40465f0a432d510b7e80b36,0x6758d7777813a335e90c94b867f3951d2e2e2cb0,3822960000000000
0x72e5263ff33d2494692d7f94a758aa9f82062f73,0xbc22a279889f9ca5cf5b79ddc705292ba1f9c284,50000000000000000
```

### Data Processing
```bash
# Download sample data
python3 script/download_dataset.py

# Process custom data
# Place your CSV file in data/ directory
# Update config/run.json data_dir parameter
```

### Data Scaling
To increase data size for testing:
```bash
# Duplicate existing data (3x)
head -1 data/len3_data.csv > data/len3_data_temp.csv
tail -n +2 data/len3_data.csv >> data/len3_data_temp.csv
tail -n +2 data/len3_data.csv >> data/len3_data_temp.csv
tail -n +2 data/len3_data.csv >> data/len3_data_temp.csv
mv data/len3_data_temp.csv data/len3_data.csv
```

---

## Results and Analysis

### Log Files
- **`logs/node_X.log`**: Individual node logs
- **`logs/client.log`**: Client execution log
- **`logs/result.log`**: Performance metrics
- **`logs/blockchain.log`**: Blockchain operations

### Performance Metrics
The system tracks:
- **TPS (Transactions Per Second)**: Throughput measurement
- **Latency**: End-to-end transaction latency
- **Committed Transactions**: Total processed transactions
- **View Changes**: Consensus protocol overhead

### Result Export
```bash
# Results are automatically exported to:
# - tps_results.csv: Performance data
# - latency_plot.png: Latency visualization
# - tps_plot.png: Throughput visualization
```

### Plotting Results
```bash
# Generate performance plots
python3 script/plot_tps.py

# Custom analysis
python3 -c "
import pandas as pd
import matplotlib.pyplot as plt

# Load results
df = pd.read_csv('tps_results.csv')
print('Average TPS:', df['TPS'].mean())
print('Peak TPS:', df['TPS'].max())
"
```

---

## Advanced Usage

### Custom Network Topology
Modify `config/network.go` to define custom(remote or local) network addresses:
```go
func GenerateCustomNetwork() {
    ClientAddr = "192.168.1.100:20000"
    NodeAddr = map[int]string{
        0: "192.168.1.101:28000",
        1: "192.168.1.102:28000",
        2: "192.168.1.103:28000",
        3: "192.168.1.104:28000",
    }
}
```

### Custom Leader Election
Implement custom leader election in `leader_election/`:
```go
func (l *LeaderElection) GetFromCustom(viewId int64) string {
    // Custom election logic
    return config.NodeAddr[viewId%l.nodeNum]
}
```

### Custom Message Types
Add new message types in `core/message.go`:
```go
type CustomMessage struct {
    Timestamp int64
    From      string
    To        string
    Data      []byte
}
```

---

## Best Practices

### 1. Development
- Use version control (Git)
- Test with small datasets first
- Monitor logs during development
- Use debug mode for troubleshooting

### 2. Production
- Use dedicated servers for nodes
- Monitor system resources
- Set up log rotation
- Implement health checks

### 3. Security
- Use SSH key authentication
- Restrict network access
- Monitor for suspicious activity
- Keep dependencies updated

### 4. Performance
- Start with default configuration
- Gradually increase load
- Monitor bottlenecks
- Tune based on results

---

## Support and Contributing

### Getting Help
- Check logs in `logs/` directory
- Review configuration in `config/run.json`
- Test with minimal configuration first
- Use debug mode for detailed logging

### Contributing
- Follow Go coding standards
- Add comprehensive tests
- Update documentation
- Submit pull requests

### Reporting Issues
- Include system information
- Provide relevant log files
- Describe reproduction steps
- Include configuration details

---

This guide should help you successfully run the PBFT implementation in various environments. For additional help, refer to the function documentation in `docs/PBFT_Functions_Documentation.md`.
