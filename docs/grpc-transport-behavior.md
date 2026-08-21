# gRPC transport behavior in this PBFT implementation

This document describes the transport as it exists in this repository. It
covers client-to-node requests, node-to-client responses, node-to-node PBFT
messages, ordering, flow control, backpressure, failure handling, signatures,
the `alt_run_project.sh` netem experiment, and the ViewChange/NewView latency
observed on 2026-08-20.

The repository currently uses `google.golang.org/grpc` v1.64.0 and Protocol
Buffers over HTTP/2 over TCP. The only PBFT transport service is declared in
[`proto/pbft_transport.proto`](../proto/pbft_transport.proto):

```proto
service PBFTTransport {
  rpc Deliver(Envelope) returns (Ack);
  rpc ClientNodeChannel(stream Envelope) returns (stream Envelope);
}
```

The running client and node implementations use the bidirectional
`ClientNodeChannel`. The unary `Deliver` method exists in the schema and is
implemented as a local dispatch function, but no production caller invokes it
as a unary RPC.

## Executive summary

- The client maintains one long-lived bidirectional gRPC stream to every node.
  It sends requests only to the current leader, but it receives execution and
  leader-update messages from all nodes on their respective streams.
- Every node lazily creates one long-lived outbound gRPC stream to every other
  node. Although the RPC is bidirectional, node-to-node traffic uses each
  stream only in its initiating direction. The reverse side carries no PBFT
  envelopes.
- For a pair of nodes A and B, normal operation therefore creates two TCP
  connections: A's outbound connection to B and B's outbound connection to A.
- All PBFT message types sent from one node to one peer share the same ordered
  stream, the same `sendMu`, the same HTTP/2 stream window, the same HTTP/2
  connection window, and the same TCP connection.
- `stream.Send()` does not mean that the remote application received the
  message. It can return after serialization and admission to the local gRPC
  transport. It may also block when gRPC has insufficient transport write
  quota or flow-control credit.
- `stream.Recv()` returns only after gRPC has received and reconstructed a
  complete protobuf message. The current receiver delivery timer starts after
  `Recv()`, so it excludes network and gRPC transfer time.
- The default HTTP/2 flow-control window in the pinned gRPC-Go version starts
  at 65,535 bytes. gRPC-Go enables dynamic bandwidth-delay-product estimation
  when no explicit window is configured, so the window may grow later. The
  first large transfer can nevertheless behave like repeated 65,535-byte
  windows.
- Under the shell netem experiment, node-to-node data and reverse-direction
  TCP acknowledgements/HTTP/2 control frames each receive 100 ms of one-way
  delay. Client-to-node traffic is not selected by those filters.
- The measured 1.080 MiB ViewChange took about 3.567 seconds to become visible
  at `Recv()`. A 4.238 MiB NewView took about 13.804 seconds. Both imply an
  effective rate close to 0.30 MiB/s, which is strongly consistent with a
  roughly 64 KiB flow-control window being replenished over a roughly 200 ms
  round trip.
- PBFT handler work was not the dominant part of those transfers. Sender-side
  preparation/enqueue was tens of milliseconds, and receiver-side protobuf
  conversion/signature verification was approximately 8-16 ms per ViewChange.
- The application still contributes to the problem through large proof
  messages, one ordered stream for both data-plane and view-change traffic,
  unbounded send waits, and no application-level delivery acknowledgement.

## Addressing and connection topology

In `loopbackip` mode, [`config/network.go`](../config/network.go) assigns:

| Participant | Address |
|---|---|
| Client | `127.0.0.1:20000` |
| Node 1 | `127.0.0.2:28100` |
| Node 2 | `127.0.0.3:28200` |
| Node 3 | `127.0.0.4:28300` |
| Node 4 | `127.0.0.5:28400` |

The client does not listen on `127.0.0.1:20000` for gRPC responses. That value
is logical application metadata. The client initiates a TCP connection to each
node, and the node sends responses back over that already-established
bidirectional stream.

For four nodes, the steady-state topology is approximately:

```text
Client
  |-- one TCP/HTTP2/ClientNodeChannel connection --> Node 1
  |-- one TCP/HTTP2/ClientNodeChannel connection --> Node 2
  |-- one TCP/HTTP2/ClientNodeChannel connection --> Node 3
  `-- one TCP/HTTP2/ClientNodeChannel connection --> Node 4

Node 1 -- outbound stream --> Node 2
Node 1 -- outbound stream --> Node 3
Node 1 -- outbound stream --> Node 4
Node 2 -- outbound stream --> Node 1
... one directed stream for every ordered pair of distinct nodes ...
```

Once all peer streams have been created, four nodes have 12 directed
node-to-node TCP connections plus four client-to-node TCP connections.

The custom TCP dialers bind the source IP:

- The client binds to the host part of `ClientAddr`, normally `127.0.0.1`, and
  lets the kernel choose an ephemeral source port.
- A node binds outgoing peer connections to its own loopback IP, such as
  `127.0.0.3`, and lets the kernel choose an ephemeral source port.

This source binding is why the `tc flower` source/destination filters in
`alt_run_project.sh` can identify a logical directed node link.

## One RPC, two channel roles

Both client and peer connections call `ClientNodeChannel`. They attach the
metadata key `pbft-channel-kind`, defined in
[`transportpb/channel.go`](../transportpb/channel.go):

- The client sends `pbft-channel-kind: client`.
- A node peer sends `pbft-channel-kind: node`.
- Missing metadata defaults to the client role.

The node's server handler in
[`node/nodeMessageHub.go`](../node/nodeMessageHub.go) reads this metadata:

```text
ClientNodeChannel
  |-- kind=client --> receive client requests and retain stream for responses
  `-- kind=node   --> receive PBFT envelopes through Deliver
```

This distinction is at the application metadata level. Both roles still use
the same protobuf service, HTTP/2 implementation, flow control, and TCP stack.

## Client startup and stream maintenance

[`client/clientMessageHub.go`](../client/clientMessageHub.go) starts one worker
per configured node. Each worker repeatedly:

1. Creates a `grpc.ClientConn` using insecure transport credentials.
2. Binds the TCP source address to the client IP.
3. Opens `ClientNodeChannel` with client-role metadata.
4. Stores the resulting stream in `streams[nodeAddr]`.
5. Runs a blocking `Recv()` loop for responses.
6. On EOF or another stream error, removes the stream, closes the connection,
   waits 500 ms, and reconnects.

Important details:

- The stream context is the message hub's background lifetime context. There
  is no per-stream request deadline.
- `clientDialTimeout` is declared as three seconds but is not currently used.
- `grpc.NewClient` is lazy; opening/using the stream drives actual connection
  establishment.
- Client startup sleeps for 100 ms before beginning transaction injection. If
  the leader stream is still unavailable, a send polls the stream map every
  50 ms for up to five seconds.
- That five-second limit only covers waiting for a stream object. Once
  `stream.Send()` begins, there is no application deadline and it can block on
  flow control or transport backpressure.

## Client-to-node request path

### 1. Client message creation and signing

[`client/send.go`](../client/send.go) creates `ClientMsg` values in a worker
pool. Each message contains:

- transaction ID and timestamp;
- sender, receiver, and amount;
- client name;
- optional configured padding.

The client deterministically serializes each `ClientMsg` and signs it with the
client Ed25519 private key. Each outbound request therefore contains a repeated
list of individually signed `ClientMsgSignature` values.

The outer `Envelope` is not itself signed by the client.

### 2. Client batching and pacing

For a non-serial client:

- `inject_speed` is also used as the client request-envelope batch size.
- With the current `inject_speed: 200`, one `RequestMessage` normally carries
  200 signed transactions.
- The request pacer uses a 22 ms interval for a full client batch.
- Normal and retry sends share one pacer mutex, so they do not intentionally
  burst past each other.
- A slow `Send()` moves the effective schedule later rather than creating
  catch-up bursts.

For `serial_client: true`, the client sends one transaction and waits for an
execution notification before sending the next one.

The node's consensus batch size is separately controlled by `max_batch_size`
(currently 30). `max_block_size` is present in the JSON configuration but is
not used by the current node batching path.

### 3. Envelope construction and stream send

The client converts the request into an `Envelope_Request`, finds the stream
for the current leader, locks that stream's `sendMu`, and calls
`state.stream.Send(env)`.

The mutex is necessary because gRPC-Go does not support concurrent writes by
multiple goroutines to the same stream. It also means:

- only one request envelope can enter a particular node stream at a time;
- a blocked send blocks all later sends to that node;
- the mutex acquisition order, rather than transaction ID, determines order
  among concurrent producers.

The client increments its `requestSentTxs` metric only after `Send()` returns.
That metric means "accepted by the local gRPC send path," not "received by the
leader."

### 4. Node client-stream receive loop

The node's client-role server handler repeatedly calls `stream.Recv()`.
For a request it:

1. Extracts the `RequestMessage` body.
2. Converts protobuf objects into core Go objects.
3. Calls `HandleRequestMessage` synchronously.
4. Deterministically serializes every client message again.
5. Verifies every client Ed25519 signature.
6. Sends each verified request into `receiveVerifiedClientRequestCh`.

`receiveVerifiedClientRequestCh` is unbuffered. The receive handler can
therefore block until the node's single event loop receives each transaction.
If the pending request queue is full, the event loop disables its receive case,
which propagates backpressure through this chain:

```text
pending request queue full
  -> event loop stops reading receiveVerifiedClientRequestCh
  -> HandleRequestMessage blocks
  -> node stops calling client stream Recv()
  -> gRPC receive buffers/flow-control credit stop advancing
  -> client stream.Send() can eventually block
  -> request pacer and injection rate slow down
```

### 5. Node event loop and proposal

The event loop ignores client requests if the node is not the leader or if a
view change is running. On the leader it enqueues requests and proposes once
at least `max_batch_size` requests are pending, subject to inflight and
watermark limits.

The leader builds a PrePrepare containing the selected client requests,
computes batch and per-request digests, signs the minimal
`{view, sequence, batch digest}` payload, records local state, and broadcasts
the PrePrepare to every other node.

## Node-to-client response path

Nodes do not dial the client. The node retains the server side of the
client-originated stream in one `clientStream` field and writes responses on
that stream.

The following message types are routed to the client stream by
`NodeMessageHub.Send`:

- `MsgCommitTpsMessage`;
- `MsgLeaderIdUpdateMessage`;
- `MsgReplyMessage` support exists, although current node code does not send
  it;
- `MsgVCRunningStatusMessage` support exists, although its current handling is
  inactive.

All response types share the client stream's `sendMu` and flow-control windows.
The `ip`/`To` argument does not select a network connection on this path; the
stored client stream is used.

After execution, every node asynchronously sends a `CommitTps` message for
each executed client transaction. The client processes incoming envelopes in
its per-node `Recv()` loop and launches a goroutine for each handler. The
transaction manager marks a transaction committed on the first accepted
`CommitTps`; later duplicates from other nodes are ignored.

After a replica accepts a NewView, it sends a leader update on its client
stream. For four nodes, the client changes leaders after matching updates reach
`2*f`, currently two messages.

Current node-to-client messages are not signed at the envelope level, and the
gRPC connection uses `insecure.NewCredentials()`. Their authenticity therefore
depends on the trusted experiment environment rather than TLS or an
application signature.

The node stores only one client stream. If multiple independent clients connect
to the same node, the newest stream replaces the previous one for all outgoing
responses. The current system is consequently designed for one client process.

## Node-to-node connection creation

Node peer streams are created lazily by the first send to a destination.
`getOrCreatePeerStream`:

1. Checks the `peerStreams` map.
2. Creates a source-bound TCP dialer.
3. Creates an insecure `grpc.ClientConn` with large message-size limits.
4. Opens `ClientNodeChannel` with node-role metadata.
5. Resolves concurrent creation races by retaining only one stream per address.
6. Starts `watchPeerStream`, which blocks on the response direction's `Recv()`.

The receiving node never sends an envelope on the reverse direction of a peer
stream. `watchPeerStream` therefore normally blocks indefinitely and is used
only to notice stream closure or errors.

Node peer streams have no proactive reconnect worker. On a send or receive
error, the stream is removed and its connection is closed. The next outbound
message attempts to create another stream. The message that failed is not
retried.

## How the unused unary `Deliver` RPC would differ

If node-to-node messages were sent through the declared unary `Deliver` RPC,
each call would use a separate HTTP/2 stream and would return the protobuf
`Ack`. That could remove application ordering behind one long-lived stream and
make receiver rejection visible to the caller.

Unary RPC does not bypass flow control. Each call still has a per-stream
HTTP/2 window, all concurrent calls on the `grpc.ClientConn` share its
connection window, and all of them share the connection's TCP congestion,
receive-window, and head-of-line behavior. One 1 MiB unary request can still
require multiple window updates. Concurrent unary calls may make aggregate
progress through separate stream windows, but a large control message is not
automatically reduced to one 100 ms transfer.

Unary calls would also change retry and completion semantics: the call would
normally remain active until the remote handler returns its Ack or the context
ends. Explicit deadlines and idempotency rules would be necessary before
enabling automatic retries for PBFT messages.

## Node-to-node send path

`asyncBroadCast` starts one goroutine per destination and skips the local node.
Each goroutine:

1. Converts the core message into a protobuf `Envelope`.
2. Sets `Envelope.From` to the local node ID.
3. Adds the supplied Ed25519 signature.
4. Applies configured in-process artificial latency, if any.
5. Gets the persistent peer stream.
6. Locks that peer stream's `sendMu`.
7. Calls `stream.Send()`.

Different destinations transfer concurrently because each has its own
connection and mutex. Messages to the same destination are serialized. Since
broadcast uses goroutines, order across different peers is not guaranteed, and
concurrent message types race for the per-peer mutex.

There is no priority distinction between:

- normal PrePrepare/Prepare/Commit traffic;
- checkpoints;
- ViewChange traffic;
- NewView traffic.

A NewView can therefore be queued behind older consensus data on the same
ordered stream. A separate control-plane stream or connection would be needed
for genuine ViewChange/NewView priority.

## Node-to-node receive path

Every incoming peer stream has its own server goroutine. It repeatedly:

1. Calls `stream.Recv()` and waits for one complete `Envelope`.
2. Starts the current `node stream delivery` timer.
3. Calls `Deliver` locally.
4. Logs local rejection or processing errors.
5. Calls `Recv()` for the next envelope.

`Deliver` validates the envelope, verifies the message-specific signature,
converts the protobuf payload, and sends a typed value to an internal channel.
Those internal channels are:

| Traffic | Channel | Capacity |
|---|---|---:|
| PrePrepare, Prepare, Commit | `consensusMsgChan` | `consensus_chan_size`, currently 5000 |
| ViewChange | `viewChangeMsgChan` | 100 |
| Checkpoint | `checkpointMsgChan` | 100 |
| NewView | `newViewMsgChan` | 20 |
| Verified client request | `receiveVerifiedClientRequestCh` | unbuffered |

If one of these channels fills, `Deliver` blocks. That incoming gRPC stream
then stops calling `Recv()`, creating transport backpressure for subsequent
messages from that sender.

After enqueue, one event-loop goroutine serially handles all consensus,
ViewChange, checkpoint, NewView, and timer events. Expensive handlers therefore
delay every other event source, including timer observation. Incoming gRPC
streams can still enqueue concurrently until their channel capacities are
reached.

## Message routing and verification

| Message | Normal direction | Verification before internal enqueue | Event-loop destination |
|---|---|---|---|
| Request | Client -> current leader | Each client message is deterministically marshalled and Ed25519-verified | Unbuffered client-request channel |
| PrePrepare | Leader -> replicas | Node signature over view, sequence, and batch digest; replicas later verify each client signature and recompute the batch digest | Consensus channel |
| Prepare | Every node -> other nodes | Node signature over the protobuf Prepare | Consensus channel |
| Commit | Every node -> other nodes | Node signature over the protobuf Commit | Consensus channel |
| Checkpoint | Every node -> other nodes | Envelope sender must match body sender; node signature verified | Checkpoint channel |
| ViewChange | Every node -> other nodes | Envelope sender must match body sender; signature over the full ViewChange verified | ViewChange channel |
| NewView | New primary -> replicas | Envelope sender must match body sender; signature over the full NewView verified | NewView channel |
| CommitTps | Node -> client on client stream | No node signature checked by client | Client transaction manager |
| LeaderIdUpdate | Replica -> client on client stream | No node signature checked by client | Client leader-vote tracking |

The receiver creates an `Ack` value inside `Deliver`, but `receiveNodeStream`
does not send that result back on the bidirectional stream. Consequently:

- the sender has no application-level proof of receipt;
- the sender is not told about signature failure, malformed payload, a stale
  view, or event-loop rejection;
- a successful `Send()` means only that the local gRPC transport accepted the
  send operation;
- the protobuf unary `Deliver` RPC would return an Ack, but the active
  node-to-node path does not call it.

## Ordering and head-of-line blocking

Within one gRPC stream, message order is preserved according to the order in
which the protected `Send()` calls succeed. This produces several kinds of
head-of-line blocking:

1. **Application mutex:** one blocked `Send()` holds or waits around the
   per-peer send path and prevents later messages from entering that stream.
2. **gRPC write quota:** a large earlier message can consume transport quota,
   causing a later, tiny message's `Send()` to block.
3. **HTTP/2 stream order:** later DATA for that stream cannot overtake earlier
   DATA.
4. **TCP order:** lost or delayed TCP data blocks later bytes on the same
   connection.
5. **Receiver dispatch:** the server handles envelopes from one peer stream
   sequentially; a blocked `Deliver` prevents the next `Recv()`.
6. **Single PBFT event loop:** after transport delivery, handlers themselves are
   serialized.

The latest logs demonstrate this distinction. Most initial 1.080 MiB
ViewChange `Send()` calls returned in 4-10 ms, while receipt occurred about 3.5
seconds later. Later, even a 1,075-byte ViewChange had a `Send()` duration of
about 2.88 seconds because the transport was already backpressured by earlier
large messages. A tiny message is therefore fast only when the stream and
connection have available quota and no earlier backlog.

## Message-size limits are not flow-control windows

The node configures:

```text
MaxRecvMsgSize = 1000 * 1024 * 1024 bytes
MaxSendMsgSize = 1000 * 1024 * 1024 bytes
```

The client configures per-call send and receive limits of 256 MiB.

These settings answer: "May this complete protobuf message be accepted?" They
do not answer: "How many bytes may be in flight right now?"

A 4 MiB NewView is below the configured node message limit and is valid. It is
still fragmented into gRPC framing and HTTP/2 DATA frames, which are carried in
TCP segments. The sender must obey both HTTP/2 and TCP flow control during that
transfer.

`proto.Size(env)` reports the encoded protobuf envelope size. It excludes the
five-byte gRPC message header, HTTP/2 frame headers, TCP/IP headers, link-layer
overhead, retransmissions, and acknowledgements.

## HTTP/2 and gRPC flow control

Flow control prevents a fast sender from filling receiver memory without a
bound. HTTP/2 maintains two relevant credits:

1. A per-stream window.
2. A per-connection window shared by all streams on the connection.

The sender may transmit DATA only while both windows have credit. Receiving
`WINDOW_UPDATE` frames grants more credit. This is separate from TCP's own
receive window and congestion window.

In gRPC-Go v1.64.0, the default initial HTTP/2 window is 65,535 bytes. This
project now explicitly sets both `InitialWindowSize` and
`InitialConnWindowSize` to 8 MiB on the node server, node-to-node client
connections, and standalone client-to-node connections. This lets the current
4.238 MiB NewView fit within the initial stream and connection credits.

Conceptually, a large transfer can behave like:

```text
sender has flow-control credit
  -> sends a portion of the message
  -> portion crosses the delayed link
  -> receiver consumes bytes
  -> WINDOW_UPDATE crosses the delayed reverse link
  -> sender receives more credit
  -> sender transmits the next portion
```

This does not mean gRPC has a maximum message size of 64 KiB. It means the
initial amount of uncredited stream data is approximately 64 KiB.

## TCP behavior beneath gRPC

HTTP/2 DATA and control frames are ordinary bytes in a TCP connection. TCP adds
its own behavior:

- reliable ordered delivery;
- congestion-window growth and reduction;
- receiver-window flow control;
- retransmission after loss;
- acknowledgement traffic in the reverse direction;
- head-of-line blocking after a lost or delayed byte range.

The observed delay should therefore be described as gRPC/HTTP2 flow control
over delayed TCP, not as pure PBFT computation and not as a configured bandwidth
rate. The current logs do not individually timestamp each HTTP/2 window update
or TCP segment, so they cannot assign every millisecond between HTTP/2, TCP,
kernel qdisc scheduling, and queued earlier messages.

## What `stream.Send()` means in this code

For gRPC-Go, `SendMsg` first prepares/serializes the protobuf and checks the
maximum send-message size. It then submits the operation to the current stream
attempt. Depending on available write quota and flow-control state, the call
may return quickly or block.

It does not wait for this repository's remote `Deliver`, signature
verification, typed channel enqueue, or PBFT event-loop handler. There is no
stream-level application Ack sent back.

The current node sender timer:

- starts after `buildEnvelope` and after in-process artificial latency;
- includes `proto.Size` for ViewChange/NewView;
- includes peer stream creation if needed;
- includes waiting for `sendMu`;
- includes the `stream.Send()` call;
- excludes time after `Send()` returns and before the remote `Recv()` completes.

Thus a log saying `Send took 6.9ms` is an enqueue/admission measurement, not an
end-to-end latency measurement.

## What `stream.Recv()` and the receiver timer mean

The receiver's `stream.Recv()` blocks until one complete protobuf Envelope is
available or the stream fails. Only after it returns does the code start the
`node stream delivery` timer.

For ViewChange and NewView, the timed `Deliver` work includes:

- body/sender checks;
- deterministic protobuf serialization for signature verification;
- Ed25519 verification;
- conversion into nested core Go structures;
- a `proto.Size` traversal;
- the `HUB: Received` log;
- waiting to enqueue on the typed internal channel.

It excludes:

- sender-side construction;
- sender-side queueing after a prior message;
- gRPC serialization before transport admission;
- HTTP/2 flow-control waits before `Recv()` returns;
- TCP and netem time;
- later PBFT event-loop verification and state changes.

## The 100 ms netem experiment

`alt_run_project.sh` configures a root `prio` qdisc on `lo`, adds a netem child,
and installs flower filters for every ordered pair of configured node IPs. The
schedule changes the child from 0 ms to 100 ms after three seconds.

The filters require both source and destination to be node IPs in the range
`127.0.0.2` through `127.0.0.5`. Therefore:

- node-to-node DATA is delayed;
- reverse node-to-node TCP ACKs and HTTP/2 control frames are delayed;
- client `127.0.0.1` to node traffic is not selected;
- node to client `127.0.0.1` responses are not selected.

The script injects delay, not a configured bandwidth rate. It does not add
explicit packet loss, duplication, reordering, or rate shaping. Nevertheless,
delay can drastically reduce the effective throughput of window-controlled
protocols because replenishing a window requires reverse-path feedback.

The netem queue limit is 100,000 packets, which permits a large backlog rather
than applying early backpressure or drops. This can make queueing latency and
memory use less visible while allowing old data to remain ahead of control
messages.

The JSON `netem.enabled` setting is false in the current configuration, but
that does not disable the shell script's independent `tc` commands. The
in-process `ArtificialLatency` path is separate; the client call is commented
out, and the node path is active only when `far_node_id` and
`far_node_delay_ms` select a node.

## Case study: the measured ViewChange and NewView

The measured ViewChange path was:

```text
Node 1 Send returned:       17:25:42.583449
Node 3 complete receive:    17:25:46.150359
Difference:                 3.566910 seconds
Envelope size:              1,132,571 bytes = 1.080 MiB
Receiver Deliver work:      approximately 8.04 ms
```

Its effective application payload rate was approximately:

```text
1.080 MiB / 3.5669 s = 0.303 MiB/s
```

The corresponding NewView was:

```text
Send returned:              approximately 17:25:46.324
Complete receive:           approximately 17:26:00.128
Difference:                 approximately 13.804 seconds
Envelope size:              4,443,928 bytes = 4.238 MiB
Effective payload rate:     approximately 0.307 MiB/s
```

The nearly identical effective rates are important. They indicate a
size-proportional transport constraint rather than a four- or fourteen-second
PBFT handler pause.

A 65,535-byte window replenished over an approximately 200 ms feedback loop
has a theoretical scale of:

```text
65,535 bytes / 0.2 s = approximately 320 KiB/s = 0.3125 MiB/s
```

That is very close to the observed 0.303-0.307 MiB/s. This is strong evidence
that initial HTTP/2 flow-control behavior, interacting with 100 ms delay in
both directions, dominated the large-message transfer. It is still an
inference from end-to-end timing; confirming individual window transitions
would require gRPC channel/transport instrumentation or a packet trace.

The first ViewChange round also broadcast 12 large transfers concurrently:
each of four nodes sent a 1.080 MiB ViewChange to three peers. Each directed
transfer had its own connection, but all shared the host CPU and loopback
qdisc.

## Why ViewChange is 1.080 MiB

On view 2, every node reported 241 prepared slots after stable checkpoint 750.
`createVCContent` includes a prepared certificate for each qualifying slot.
Each certificate contains:

- a PrePrepare mini record;
- individual request digests;
- the original PrePrepare signature;
- a Prepare log with signatures;
- when `carry_state: true`, the actual signed client requests.

The ViewChange also contains checkpoint proof and checkpoint balances.

Consequently its size grows approximately with the number of prepared slots,
the consensus batch size, client padding, the number of Prepare signatures, and
checkpoint state.

## Why NewView is 4.238 MiB

The new primary constructs a NewView containing both:

- the computed `PreprepareLog`/O set; and
- the complete collected `ViewChangeLog`.

For four nodes with `f=1`, the quorum normally includes three ViewChange
messages. Much of the prepared-certificate and actual-request material is
therefore represented in the three proofs and again in the O set. In the
observed run, three roughly 1.080 MiB ViewChanges plus the O set and envelope
overhead produced a 4.238 MiB NewView.

The implementation's `appendViewChangeIfNew` currently has its sender
deduplication code commented out, while `uniqueViewChangeCount` simply returns
the slice length. Duplicate ViewChanges can therefore inflate both the counted
quorum and NewView payload. That is a correctness and size risk independent of
gRPC flow control.

## Timers and the view-change cascade

Both `leaderProgressTimeout` and `newViewTimeout` are currently seven seconds.
A 4.238 MiB NewView taking approximately 13.8 seconds cannot arrive before the
NewView timer expires.

The resulting sequence is:

```text
replica receives 2f+1 ViewChanges
  -> starts seven-second NewView timer
  -> primary creates and locally queues NewView
  -> NewView transfer exceeds seven seconds
  -> replica enters the next view
  -> old NewView finally arrives
  -> replica rejects it because its pending view is now higher
  -> view changes repeat
```

Increasing the timeout can stop this specific cascade but does not solve the
large proof or shared-stream behavior. A robust timeout should exceed the p99
time for construction, queueing, transport, verification, and event-loop
acceptance under the intended impairment.

## Failure and shutdown behavior

### Client streams

- The client reconnects every 500 ms after a receive-side failure.
- A send waits up to five seconds for a stream to appear.
- A send error removes and closes the stream.
- There is no request-envelope retransmission inside `ClientMessageHub`; any
  transaction retry is handled at the transaction-manager layer.
- Normal retry startup is currently commented out in `Client.Start`; retry is
  started after injection only when `complete_suite` is enabled.

### Node peer streams

- An error detected by `watchPeerStream` removes and closes the stream.
- A send error also removes and closes it.
- The next send lazily reconnects.
- The failed PBFT envelope is not retried.
- There is no per-send deadline, dial timeout, keepalive configuration, or
  application acknowledgement.

### Shutdown

- Client shutdown cancels the shared context, waits for stream workers, and
  closes all connections.
- Node shutdown cancels outbound peer contexts, calls `grpc.Server.Stop()`
  rather than graceful stop, closes its listener, and closes outbound peer
  connections.
- Close envelopes are supported by the protobuf/builders, but ordinary
  production shutdown closes transports rather than broadcasting them.

### Simulated dead nodes

- A node marked dead refuses outbound sends.
- The client-role receive loop ignores incoming client messages while dead.
- The dead check in the node-to-node `Deliver` path is currently commented
  out, so a dead node can continue receiving, verifying, and enqueueing peer
  messages.

## Security properties of the current transport

- gRPC uses plaintext/insecure credentials: there is no TLS confidentiality,
  server authentication, or transport integrity.
- Client transactions have application-level Ed25519 signatures.
- PBFT PrePrepare, Prepare, Commit, Checkpoint, ViewChange, and NewView messages
  have application signatures checked before internal enqueue.
- ViewChange, Checkpoint, and NewView explicitly require the envelope sender ID
  to match the body sender ID.
- Node-to-client execution and leader-update messages are not currently signed
  or verified.
- The active streaming path does not return application-level rejection
  results to senders.

These choices may be acceptable for a controlled single-host experiment but
should not be treated as an authenticated production transport.

## Current observability and its limits

The current ViewChange/NewView logs provide:

- sender target;
- protobuf envelope size in bytes and MiB;
- duration of local size calculation, stream acquisition/mutex wait, and
  `Send()`;
- receiver sender ID, view, and protobuf envelope size;
- receiver post-`Recv()` `Deliver` duration when it exceeds five milliseconds.

They do not provide:

- a globally unique message ID;
- sender creation time inside the envelope;
- time of first byte written or read;
- time spent waiting for HTTP/2 stream credit;
- time spent waiting for connection credit;
- TCP congestion window, receive window, RTT, retransmissions, or queued bytes;
- netem queue occupancy/drops at the moment of transfer;
- a remote application acknowledgement;
- time from typed channel enqueue to completion of the PBFT handler.

For unambiguous end-to-end measurement, add a transport message ID and sender
monotonic/wall timestamp, log immediately before `Send`, immediately after
`Recv`, before and after `Deliver`, and before and after event-loop handling.
For correctness, return an explicit application Ack containing the message ID
after the required acceptance point.

System-level experiments should also capture:

```bash
tc -s qdisc show dev lo
ss -tinp
```

Packet captures can confirm DATA, WINDOW_UPDATE, ACK, and retransmission timing.
gRPC channelz or a gRPC stats handler can expose connection and RPC activity
without inferring everything from application logs.

## Mitigation options

### 1. Reduce ViewChange and NewView payloads

This is the most fundamental fix. Options include:

- restore actual sender deduplication for ViewChange messages;
- include only the required `2f+1` unique ViewChanges in NewView;
- avoid carrying full actual client requests in every prepared certificate;
- transfer missing request bodies separately by digest;
- avoid repeating checkpoint balances in every proof;
- use checkpoint/state-transfer references rather than embedding full state;
- prune prepared certificates as soon as a stable checkpoint makes them
  unnecessary;
- consider compression only after measuring CPU cost and ensuring compressed
  message limits are safe.

### 2. Configure explicit gRPC windows

The current configuration sets both windows to 8 MiB:

```go
const grpcFlowControlWindowSize = 8 * 1024 * 1024
```

Server options:

```go
grpc.NewServer(
    grpc.InitialWindowSize(grpcFlowControlWindowSize),
    grpc.InitialConnWindowSize(grpcFlowControlWindowSize),
    grpc.MaxRecvMsgSize(maxGRPCMsgBytes),
    grpc.MaxSendMsgSize(maxGRPCMsgBytes),
)
```

Client-connection options:

```go
grpc.NewClient(
    addr,
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithInitialWindowSize(grpcFlowControlWindowSize),
    grpc.WithInitialConnWindowSize(grpcFlowControlWindowSize),
    // existing options...
)
```

All nodes act as both gRPC servers and clients, so configure both sides. The
standalone client should also use compatible options for both request and
response directions.

In gRPC-Go v1.64.0, explicitly setting an initial stream window at or above the
default disables dynamic window estimation for that endpoint. Explicit windows
should therefore be benchmarked rather than assumed to be universally better.
Larger windows permit more buffered in-flight data and can increase memory use
and the effect of an oversized-message attack.

### 3. Separate control and data traffic

Use a dedicated stream, or preferably a dedicated connection, for ViewChange
and NewView messages. This prevents old PrePrepare/Prepare/Commit DATA and
their stream window from blocking view-change control messages. Separate HTTP/2
streams on one connection remove stream-level ordering but still share the
connection window and TCP head-of-line behavior; a separate connection gives
stronger isolation.

### 4. Add bounded sends and reconnection policy

- Give connection establishment and sends explicit deadlines.
- Define whether a failed consensus message is retried, dropped, or causes a
  peer to be marked unavailable.
- Avoid letting `Send()` block an application goroutine indefinitely.
- Track queue depth and send wait separately from protobuf serialization time.

### 5. Add application acknowledgements

The current local `Ack` is discarded on the streaming path. Send a response
Envelope containing message ID, sender, receiver, status, and acceptance stage,
or use a small unary control RPC where appropriate. Decide whether Ack means:

- bytes decoded;
- signature verified;
- internal channel enqueue completed;
- PBFT event-loop handler accepted the message.

These meanings have different correctness and latency implications.

### 6. Revisit timeouts after transport fixes

Measure p50/p95/p99 end-to-end control-message latency under the intended RTT,
loss, queue, and payload sizes. Set NewView timeouts from that distribution.
Increasing the timeout before reducing or isolating the large transfers is a
temporary guard against cascading views, not a transport solution.

## Practical interpretation checklist

When reading a future log, use these meanings:

| Log/event | What it proves | What it does not prove |
|---|---|---|
| Client/node `Send()` returned | Local gRPC send operation completed | Remote `Recv`, verification, enqueue, or PBFT acceptance |
| `HUB: Received ...` | Full envelope arrived, signature passed, and protobuf conversion completed | Event-loop handler accepted the view/message |
| `node stream delivery took ...` | Post-`Recv` local `Deliver` duration | Wire time |
| `received vc as round robin ...` | Event loop began handling that ViewChange | Quorum or NewView acceptance |
| `Received 2f+1 ...` | Current counted slice length reached quorum condition | Uniqueness, because deduplication is currently disabled |
| `Received and accepted new view ...` | NewView proof/O-set verification passed and state transition began | Client has already learned the new leader |
| `size_bytes` | Protobuf Envelope encoded size | Full network bytes or memory allocated |

The main conclusion from the current experiment is that the delay is not all
"TCP" and not all "the application." The measured multi-second portion is in
the gRPC/HTTP2-over-TCP transport path under netem delay. The PBFT application
creates the conditions—large proofs, one ordered peer stream, and no priority
or Ack—while HTTP/2/TCP flow control and reverse-path latency determine how
quickly those bytes can actually reach `Recv()`.
