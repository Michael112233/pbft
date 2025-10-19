# PBFT Implementation Functions Documentation

## Overview
This document provides comprehensive documentation for all functions used in the PBFT (Practical Byzantine Fault Tolerance) implementation. The system consists of multiple components including nodes, clients, blockchain, message handling, view change, and utility functions.

## Table of Contents
1. [Core Components](#core-components)
2. [Node Functions](#node-functions)
3. [Client Functions](#client-functions)
4. [Blockchain Functions](#blockchain-functions)
5. [Message Hub Functions](#message-hub-functions)
6. [View Change Functions](#view-change-functions)
7. [Leader Election Functions](#leader-election-functions)
8. [Utility Functions](#utility-functions)
9. [Data Processing Functions](#data-processing-functions)
10. [Result Management Functions](#result-management-functions)
11. [Garbage Collection Functions](#garbage-collection-functions)

---

## Core Components

### Message Types
- `RequestMessage`: Client transaction requests
- `PreprepareMessage`: Leader's proposal message
- `PrepareMessage`: Replica's prepare vote
- `CommitMessage`: Replica's commit vote
- `ReplyMessage`: Final response to client
- `ViewChangeMessage`: View change initiation
- `NewViewMessage`: New view establishment
- `CheckpointMessage`: Garbage collection checkpoint

---

## Node Functions

### Node Initialization and Management

#### `NewNode(nodeID int64, cfg *config.Config) *Node`
**Purpose**: Creates a new PBFT node instance
**Parameters**:
- `nodeID`: Unique identifier for the node
- `cfg`: Configuration object containing system parameters
**Returns**: Initialized Node struct
**Description**: Initializes all node data structures including message counters, locks, and message hub.

#### `Start()`
**Purpose**: Starts the node's message hub and garbage collection
**Description**: Begins listening for incoming messages and starts background garbage collection process.

#### `Stop()`
**Purpose**: Gracefully shuts down the node
**Description**: Stops all timers, closes network connections, and cleans up resources.

#### `GetAddr() string`
**Purpose**: Returns the node's network address
**Returns**: String representation of the node's address
**Description**: Maps node ID to network address using configuration.

### Message Sending Functions

#### `SendPreprepareMessage()`
**Purpose**: Sends preprepare messages to all replicas
**Description**: 
- Generates blocks from mempool transactions
- Creates preprepare messages with sequence numbers
- Broadcasts to all other nodes
- Stops if view change is in progress

#### `SendPrepareMessage(data core.PreprepareMessage)`
**Purpose**: Sends prepare messages after receiving preprepare
**Parameters**:
- `data`: The received preprepare message
**Description**: 
- Increments prepare message counter
- Creates and broadcasts prepare messages to all replicas
- Includes digest and request message data

#### `SendCommitMessage(data core.PrepareMessage)`
**Purpose**: Sends commit messages after receiving enough prepare messages
**Parameters**:
- `data`: The prepare message that triggered commit
**Description**: 
- Increments commit message counter
- Broadcasts commit messages to all replicas
- Final step before sending reply to client

#### `SendReplyMessage(data core.CommitMessage)`
**Purpose**: Sends reply message back to client
**Parameters**:
- `data`: The commit message containing transaction data
**Description**: 
- Creates reply message for client
- Stops expiration timer
- Sends final confirmation to client

#### `SendCheckpointMessage(sequenceNumber int64, digest string)`
**Purpose**: Sends checkpoint messages for garbage collection
**Parameters**:
- `sequenceNumber`: Sequence number of committed block
- `digest`: Digest of the committed block
**Description**: Broadcasts checkpoint messages to establish stable checkpoints.

### Message Handling Functions

#### `HandleRequestMessage(data core.RequestMessage)`
**Purpose**: Handles incoming transaction requests from clients
**Parameters**:
- `data`: Request message containing transactions
**Description**: 
- Validates node is not in view change
- Starts expiration timer
- Adds transactions to mempool
- Triggers preprepare message sending

#### `HandlePreprepareMessage(data core.PrepareMessage)`
**Purpose**: Handles preprepare messages from leader
**Parameters**:
- `data`: Preprepare message from leader
**Description**: 
- Validates digest, view number, and sequence number
- Checks sequence number is within bounds
- Stores preprepare message
- Triggers prepare message sending

#### `HandlePrepareMessage(data core.PrepareMessage)`
**Purpose**: Handles prepare messages from replicas
**Parameters**:
- `data`: Prepare message from replica
**Description**: 
- Validates message integrity
- Increments prepare message counter
- Triggers commit when threshold reached (2f+1 messages)

#### `HandleCommitMessage(data core.CommitMessage)`
**Purpose**: Handles commit messages from replicas
**Parameters**:
- `data`: Commit message from replica
**Description**: 
- Validates message integrity
- Increments commit message counter
- Triggers reply when threshold reached (2f+1 messages)
- Initiates garbage collection

### Sequence Number Management

#### `SetPreprepareSequenceNumber(seqNumber int64, preprepareMessage *core.PreprepareMessage)`
**Purpose**: Stores preprepare message for a sequence number
**Parameters**:
- `seqNumber`: Sequence number
- `preprepareMessage`: Message to store
**Description**: Thread-safe storage of preprepare messages.

#### `GetPreprepareSequenceNumber() int64`
**Purpose**: Returns the last preprepare sequence number
**Returns**: Last sequence number processed

#### `SetPrepareSequenceNumber(seqNumber int64)`
**Purpose**: Updates the last prepare sequence number
**Parameters**:
- `seqNumber`: New sequence number

#### `GetPrepareSequenceNumber() int64`
**Purpose**: Returns the last prepare sequence number
**Returns**: Last sequence number that received enough prepare messages

#### `SetCommitSequenceNumber(seqNumber int64)`
**Purpose**: Updates the last commit sequence number
**Parameters**:
- `seqNumber`: New sequence number

#### `GetCommitSequenceNumber() int64`
**Purpose**: Returns the last commit sequence number
**Returns**: Last sequence number that received enough commit messages

### Message Counter Functions

#### `GetPrepareMessageNumber(seqNumber int64) int32`
**Purpose**: Returns count of prepare messages for a sequence
**Parameters**:
- `seqNumber`: Sequence number to query
**Returns**: Number of prepare messages received

#### `GetCommitMessageNumber(seqNumber int64) int32`
**Purpose**: Returns count of commit messages for a sequence
**Parameters**:
- `seqNumber`: Sequence number to query
**Returns**: Number of commit messages received

#### `AddPrepareMessageNumber(seqNumber int64)`
**Purpose**: Increments prepare message counter
**Parameters**:
- `seqNumber`: Sequence number to increment

#### `AddCommitMessageNumber(seqNumber int64)`
**Purpose**: Increments commit message counter
**Parameters**:
- `seqNumber`: Sequence number to increment

### Timer Management Functions

#### `StartExpireTimer(timerID string)`
**Purpose**: Starts an expiration timer for request timeout
**Parameters**:
- `timerID`: Unique identifier for the timer
**Description**: Creates timer that triggers view change on expiration.

#### `StopExpireTimer(timerID string)`
**Purpose**: Stops a specific expiration timer
**Parameters**:
- `timerID`: Timer identifier to stop

#### `StopAllExpireTimers()`
**Purpose**: Stops all running expiration timers
**Description**: Cleanup function for graceful shutdown.

#### `monitorTimer(timerID string, timer *time.Timer)`
**Purpose**: Monitors timer expiration and triggers view change
**Parameters**:
- `timerID`: Timer identifier
- `timer`: Timer object to monitor
**Description**: Goroutine that handles timer expiration and view change initiation.

### Digest Management

#### `AddSeq2Digest(seqNumber int64, digest string)`
**Purpose**: Associates sequence number with its digest
**Parameters**:
- `seqNumber`: Sequence number
- `digest`: Associated digest

#### `SnapshotPreprepareMessages() map[int64][]*core.PreprepareMessage`
**Purpose**: Creates thread-safe snapshot of preprepare messages
**Returns**: Copy of preprepare messages map
**Description**: Used during view change to avoid concurrent access issues.

---

## Client Functions

### Client Initialization

#### `NewClient(addr string, config *config.Config) *Client`
**Purpose**: Creates a new client instance
**Parameters**:
- `addr`: Client's network address
- `config`: System configuration
**Returns**: Initialized Client struct

#### `Start()`
**Purpose**: Starts the client's message hub and transaction injection
**Description**: Begins listening for replies and starts sending transactions.

#### `Stop()`
**Purpose**: Stops the client and waits for completion
**Description**: Waits for all transactions to complete before stopping.

#### `GetAddr() string`
**Purpose**: Returns the client's network address
**Returns**: Client's address string

### Transaction Management

#### `AddTxs(txs []*core.Transaction)`
**Purpose**: Adds transactions to client's queue
**Parameters**:
- `txs`: Array of transactions to process

#### `InjectTxs()`
**Purpose**: Sends transactions to the current leader
**Description**: 
- Batches transactions according to inject speed
- Sends to current view's leader
- Handles rate limiting

### Message Handling

#### `HandleReplyMessage(data core.ReplyMessage)`
**Purpose**: Handles reply messages from replicas
**Parameters**:
- `data`: Reply message containing committed transaction
**Description**: 
- Creates block from reply data
- Adds block to blockchain
- Records committed node

---

## Blockchain Functions

### Blockchain Management

#### `NewBlockchain(cfg *config.Config)`
**Purpose**: Initializes the global blockchain
**Parameters**:
- `cfg`: System configuration
**Description**: Creates singleton blockchain instance.

#### `AddBlock(block *Block)`
**Purpose**: Adds a committed block to the blockchain
**Parameters**:
- `block`: Block to add
**Description**: 
- Handles duplicate blocks by adding committed nodes
- Records performance metrics
- Triggers result reporting

#### `GetBlock(index int64) (*Block, bool)`
**Purpose**: Retrieves a block by sequence number
**Parameters**:
- `index`: Sequence number to search for
**Returns**: Block and existence flag
**Description**: Thread-safe block retrieval.

#### `GetLastBlock() *Block`
**Purpose**: Returns the most recently added block
**Returns**: Last block in the chain

### Block Management

#### `NewBlock(sequenceNumber int64, txs []*Transaction, leader string, proposedTimestamp int64) *Block`
**Purpose**: Creates a new block
**Parameters**:
- `sequenceNumber`: Block's sequence number
- `txs`: Transactions in the block
- `leader`: Node that proposed the block
- `proposedTimestamp`: When the block was proposed
**Returns**: New Block instance

#### `AddTransaction(txs []*Transaction)`
**Purpose**: Adds transactions to a block
**Parameters**:
- `txs`: Transactions to add

#### `AddCommittedNode(node string)`
**Purpose**: Records which node committed this block
**Parameters**:
- `node`: Node address that committed

---

## Message Hub Functions

### Node Message Hub

#### `NewNodeMessageHub() *NodeMessageHub`
**Purpose**: Creates a new node message hub
**Returns**: Initialized message hub

#### `Start(node *Node, wg *sync.WaitGroup)`
**Purpose**: Starts the message hub
**Parameters**:
- `node`: Node instance
- `wg`: Wait group for synchronization

#### `Close()`
**Purpose**: Closes all network connections
**Description**: Cleanup function for graceful shutdown.

#### `Send(msgType string, ip string, msg interface{}, callback func(...interface{}))`
**Purpose**: Sends messages to other nodes
**Parameters**:
- `msgType`: Type of message to send
- `ip`: Target IP address
- `msg`: Message data
- `callback`: Optional callback function

#### `Dial(addr string) (net.Conn, error)`
**Purpose**: Establishes TCP connection to address
**Parameters**:
- `addr`: Target address
**Returns**: Connection and error

#### `listen(addr string, wg *sync.WaitGroup)`
**Purpose**: Listens for incoming connections
**Parameters**:
- `addr`: Address to listen on
- `wg`: Wait group for synchronization

#### `handleConnection(conn net.Conn, ln net.Listener)`
**Purpose**: Handles individual client connections
**Parameters**:
- `conn`: Client connection
- `ln`: Listener object

#### `packMsg(msgType string, data []byte) []byte`
**Purpose**: Serializes and packages messages for transmission
**Parameters**:
- `msgType`: Message type
- `data`: Serialized message data
**Returns**: Packaged message bytes

#### `unpackMsg(packedMsg []byte) *core.Message`
**Purpose**: Deserializes received messages
**Parameters**:
- `packedMsg`: Packed message bytes
**Returns**: Deserialized message

### Client Message Hub

#### `NewClientMessageHub() *ClientMessageHub`
**Purpose**: Creates a new client message hub
**Returns**: Initialized client message hub

#### `Start(client *Client, wg *sync.WaitGroup)`
**Purpose**: Starts the client message hub
**Parameters**:
- `client`: Client instance
- `wg`: Wait group for synchronization

#### `Close()`
**Purpose**: Closes client message hub connections

#### `Send(msgType string, ip string, msg interface{}, callback func(...interface{}))`
**Purpose**: Sends messages from client
**Parameters**:
- `msgType`: Message type
- `ip`: Target IP
- `msg`: Message data
- `callback`: Optional callback

#### `sendRequestMessage(msg interface{})`
**Purpose**: Sends request messages to leader
**Parameters**:
- `msg`: Request message data

#### `sendCloseMessage(msg interface{})`
**Purpose**: Sends close messages to all nodes
**Parameters**:
- `msg`: Close message data

#### `handleReplyMessage(dataBytes []byte)`
**Purpose**: Handles reply messages from replicas
**Parameters**:
- `dataBytes`: Serialized reply message

---

## View Change Functions

### View Change Management

#### `NewViewChanger(cfg *config.Config) *ViewChanger`
**Purpose**: Creates a new view changer
**Parameters**:
- `cfg`: System configuration
**Returns**: Initialized ViewChanger

#### `StartViewChange(currentView int64, currentSequenceNumber int64)`
**Purpose**: Initiates view change process
**Parameters**:
- `currentView`: Current view number
- `currentSequenceNumber`: Current sequence number
**Description**: Sets view change state and clears old messages.

#### `ResetViewChanger()`
**Purpose**: Resets view changer to normal state
**Description**: Clears view change state and messages.

#### `ActivateViewChange()`
**Purpose**: Activates view change mode
**Description**: Sets view change flag to true.

#### `IsInViewChange() bool`
**Purpose**: Checks if view change is active
**Returns**: True if in view change, false otherwise

### View Change Messaging

#### `SendViewChangeMessage()`
**Purpose**: Sends view change messages to all nodes
**Description**: 
- Creates view change message with current state
- Broadcasts to all other nodes
- Includes prepared list and preprepare messages

#### `SendNewViewMessage()`
**Purpose**: Sends new view messages after view change
**Description**: 
- Increments view number
- Filters preprepare messages to active window
- Broadcasts new view to all nodes

#### `SendMempoolSnapshot(toIp string)`
**Purpose**: Sends mempool to new leader
**Parameters**:
- `toIp`: Target IP address
**Description**: Transfers pending transactions to new leader.

#### `HandleViewChangeMessage(data core.ViewChangeMessage)`
**Purpose**: Handles incoming view change messages
**Parameters**:
- `data`: View change message
**Description**: 
- Validates sender is expected leader
- Collects view change messages
- Triggers new view when threshold reached

#### `HandleNewViewMessage(data core.NewViewMessage)`
**Purpose**: Handles new view messages
**Parameters**:
- `data`: New view message
**Description**: 
- Updates view number
- Resets view changer
- Replays preprepare messages
- Transfers mempool if needed

#### `HandleMempoolMessage(data core.MempoolMsg)`
**Purpose**: Handles mempool transfer messages
**Parameters**:
- `data`: Mempool message
**Description**: Receives and processes mempool from previous leader.

---

## Leader Election Functions

### Leader Election Management

#### `NewLeaderElection(config *config.Config) *LeaderElection`
**Purpose**: Creates a new leader election instance
**Parameters**:
- `config`: System configuration
**Returns**: Initialized LeaderElection

#### `GetLeader(viewId int64) string`
**Purpose**: Determines leader for a given view
**Parameters**:
- `viewId`: View number
**Returns**: Leader's address
**Description**: Delegates to specific election method.

#### `GetFromRoundRobin(viewId int64) string`
**Purpose**: Round-robin leader selection
**Parameters**:
- `viewId`: View number
**Returns**: Leader's address
**Description**: Selects leader using round-robin algorithm.

---

## Utility Functions

### Digest Generation

#### `GetDigest(data *core.RequestMessage) string`
**Purpose**: Generates digest for request message
**Parameters**:
- `data`: Request message to digest
**Returns**: Digest string
**Description**: Creates deterministic hash for message integrity.

### Sequence Number Generation

#### `GenerateRandomSequenceNumber(upperBound int64, lowerBound int64) int64`
**Purpose**: Generates random sequence number within bounds
**Parameters**:
- `upperBound`: Maximum sequence number
- `lowerBound`: Minimum sequence number
**Returns**: Random sequence number
**Description**: Creates random sequence number for block proposals.

---

## Data Processing Functions

### Transaction Data Management

#### `ReadData(maxTxNum int64) []*core.Transaction`
**Purpose**: Reads transaction data from CSV file
**Parameters**:
- `maxTxNum`: Maximum number of transactions to read
**Returns**: Array of transactions
**Description**: 
- Reads CSV file line by line
- Parses sender, receiver, and amount
- Handles large files efficiently
- Skips invalid lines

---

## Result Management Functions

### Performance Metrics

#### `CalculateTPS() float64`
**Purpose**: Calculates transactions per second
**Returns**: TPS value
**Description**: Computes throughput based on committed transactions and time.

#### `SetStartTime(t time.Time)`
**Purpose**: Records experiment start time
**Parameters**:
- `t`: Start timestamp

#### `SetEndTime(t time.Time)`
**Purpose**: Records experiment end time
**Parameters**:
- `t`: End timestamp

#### `AddCommittedTransactionNum(n int64)`
**Purpose**: Increments committed transaction counter
**Parameters**:
- `n`: Number of transactions to add

#### `GetCommittedTransactionNum() int64`
**Purpose**: Returns total committed transactions
**Returns**: Number of committed transactions

#### `PrintResult()`
**Purpose**: Prints current performance results
**Description**: Outputs TPS, time, and transaction count.

#### `AddLatency(latency float64)`
**Purpose**: Records transaction latency
**Parameters**:
- `latency`: Latency value in seconds

#### `ExportToCSV(filename string) error`
**Purpose**: Exports results to CSV file
**Parameters**:
- `filename`: Output CSV filename
**Returns**: Error if export fails
**Description**: Creates CSV with time, TPS, and latency data.

---

## Garbage Collection Functions

### Checkpoint Management

#### `StartGarbageCollection()`
**Purpose**: Initializes garbage collection system
**Description**: Sets up checkpoint tracking and counters.

#### `TriggerGarbageCollection(seqNumber int64, digest string)`
**Purpose**: Triggers garbage collection for a sequence
**Parameters**:
- `seqNumber`: Sequence number to checkpoint
- `digest`: Block digest
**Description**: 
- Checks if sequence is checkpoint interval
- Sends checkpoint messages
- Increments checkpoint counter

#### `SendCheckpointMessage(sequenceNumber int64, digest string)`
**Purpose**: Sends checkpoint messages to all nodes
**Parameters**:
- `sequenceNumber`: Sequence number
- `digest`: Block digest
**Description**: Broadcasts checkpoint for garbage collection.

#### `HandleCheckpointMessage(data core.CheckpointMessage)`
**Purpose**: Handles incoming checkpoint messages
**Parameters**:
- `data`: Checkpoint message
**Description**: 
- Validates checkpoint digest
- Updates checkpoint counter
- Establishes stable checkpoint when threshold reached

---

## Controller Functions

### System Control

#### `runNode(nodeID int64, cfg *config.Config)`
**Purpose**: Runs a PBFT node
**Parameters**:
- `nodeID`: Node identifier
- `cfg`: System configuration
**Description**: Creates, starts, and manages node lifecycle.

#### `runClient(cfg *config.Config)`
**Purpose**: Runs the PBFT client
**Parameters**:
- `cfg`: System configuration
**Description**: 
- Initializes blockchain
- Creates and starts client
- Loads transaction data
- Manages experiment lifecycle

#### `Main(nodeID int64, role, mode, cfgPath string)`
**Purpose**: Main entry point for PBFT system
**Parameters**:
- `nodeID`: Node identifier
- `role`: System role (node/client)
- `mode`: Execution mode (local/remote)
- `cfgPath`: Configuration file path
**Description**: 
- Reads configuration
- Sets up network topology
- Starts appropriate component

---

## Configuration Functions

### Network Configuration

#### `GenerateLocalNetwork(nodeNum int)`
**Purpose**: Creates local network configuration
**Parameters**:
- `nodeNum`: Number of nodes
**Description**: Sets up localhost addresses for all nodes.

#### `GenerateRemoteNetwork(nodeNum int)`
**Purpose**: Creates remote network configuration
**Parameters**:
- `nodeNum`: Number of nodes
**Description**: Sets up remote IP addresses for distributed deployment.

#### `ReadCfg(filename string) *Config`
**Purpose**: Reads configuration from JSON file
**Parameters**:
- `filename`: Configuration file path
**Returns**: Configuration object
**Description**: Parses JSON configuration and calculates derived values.

---

## Message Types and Structures

### Core Message Types

#### `RequestMessage`
- `Timestamp`: Message creation time
- `From`: Sender address
- `To`: Recipient address
- `Txs`: Array of transactions
- `Id`: Request identifier

#### `PreprepareMessage`
- `Timestamp`: Message creation time
- `From`: Leader address
- `To`: Recipient address
- `SequenceNumber`: Block sequence number
- `ViewNumber`: Current view number
- `Digest`: Request message digest
- `RequestMessage`: Original request

#### `PrepareMessage`
- `Timestamp`: Message creation time
- `From`: Replica address
- `To`: Recipient address
- `SequenceNumber`: Block sequence number
- `ViewNumber`: Current view number
- `Digest`: Request message digest
- `RequestMessage`: Original request

#### `CommitMessage`
- `Timestamp`: Message creation time
- `From`: Replica address
- `To`: Recipient address
- `SequenceNumber`: Block sequence number
- `ViewNumber`: Current view number
- `Digest`: Request message digest
- `RequestMessage`: Original request

#### `ReplyMessage`
- `Timestamp`: Message creation time
- `From`: Replica address
- `To`: Client address
- `SequenceNumber`: Block sequence number
- `ViewNumber`: Current view number
- `RequestMessage`: Original request

#### `ViewChangeMessage`
- `Timestamp`: Message creation time
- `From`: Replica address
- `To`: Recipient address
- `CheckpointSeqNumber`: Last stable checkpoint
- `ViewNumber`: New view number
- `CheckpointMsgNumber`: Checkpoint message count
- `HavePreparedList`: Map of prepared sequences
- `PreprepareMessages`: Preprepare message snapshots
- `Mempool`: Pending transactions

#### `NewViewMessage`
- `Timestamp`: Message creation time
- `From`: New leader address
- `To`: Recipient address
- `ViewChangeMessages`: Collected view change messages
- `ViewNumber`: New view number
- `PreprepareMessages`: Filtered preprepare messages

#### `CheckpointMessage`
- `Timestamp`: Message creation time
- `From`: Replica address
- `To`: Recipient address
- `SequenceNumber`: Checkpoint sequence number
- `Digest`: Block digest

#### `CloseMessage`
- `Timestamp`: Message creation time
- `From`: Sender address
- `To`: Recipient address

---

## Error Handling and Logging

### Logger Functions

#### `NewLogger(nodeID int64, role string) *Logger`
**Purpose**: Creates a new logger instance
**Parameters**:
- `nodeID`: Node identifier
- `role`: Component role
**Returns**: Logger instance
**Description**: Creates role-specific log files.

#### `Info(format string, args ...interface{})`
**Purpose**: Logs informational messages
**Parameters**:
- `format`: Message format string
- `args`: Format arguments

#### `Debug(format string, args ...interface{})`
**Purpose**: Logs debug messages
**Parameters**:
- `format`: Message format string
- `args`: Format arguments

#### `Warn(format string, args ...interface{})`
**Purpose**: Logs warning messages
**Parameters**:
- `format`: Message format string
- `args`: Format arguments

#### `Error(format string, args ...interface{})`
**Purpose**: Logs error messages
**Parameters**:
- `format`: Message format string
- `args`: Format arguments

---

## Performance Optimization Features

### Message Compression
- Automatic gzip compression for large messages (>64KB)
- Transparent compression/decompression
- Reduces network bandwidth usage

### Concurrent Processing
- Per-target asynchronous message sending
- Prevents blocking on slow connections
- Improves overall system throughput

### Memory Management
- Thread-safe message snapshots
- Efficient garbage collection
- Checkpoint-based state cleanup

### Network Optimization
- Connection pooling and reuse
- Automatic reconnection on failures
- Write deadline management

---

## Security Features

### Message Integrity
- Digest-based message validation
- Sequence number verification
- View number consistency checks

### Byzantine Fault Tolerance
- 2f+1 message thresholds
- View change on failures
- Leader election mechanisms

### State Consistency
- Checkpoint synchronization
- Garbage collection coordination
- View change state transfer

---

This documentation covers all major functions in the PBFT implementation. Each function is designed to work together to provide a robust, fault-tolerant distributed consensus system.
