# Client–node stream behaviour

How requests and replies move between the clients and the nodes, and where a slow
reader can block a sender. It covers the multi-client setup (`client_num` > 1) and the
ProposalDelay stall. Code references are to the `viewid` branch after the multi-client
change. For connection setup and the address table, see `grpc-transport-behavior.md`.

## Streams

- **Connections:** each client opens one gRPC connection to each node. On it, the client
  keeps one long-lived bidirectional stream: `ClientNodeChannel`, with metadata
  `pbft-channel-kind=client`.
- **Client → node:** request batches, written by a sender goroutine per node from an
  8-batch queue (`client/sendqueue.go`).
- **Node → client:** CommitTps, LeaderIdUpdate (and the unused Reply / VCRunningStatus).
- **Gate commands:** the throttle manager's gate commands use the unary `Deliver` RPC on
  the same connection, not the stream.

### One reply stream per client name

A node keeps one reply stream per client: `clientStreams` in `node/nodeMessageHub.go`.
- **Keyed by name:** the client announces its name in the `pbft-client-name` stream
  metadata. A stream without it is registered as the legacy single client, `"client"`.
- **Reconnects:** a reconnecting client replaces only its own entry.
- **Clear only if same:** a stream is removed only if it is still the registered one
  (`clearClientStream`).

`NodeMessageHub.Send` picks the target by message type:

| Message | Sent to | Why |
|---|---|---|
| CommitTps, Reply | only `clientStreams[ClientMsg.ClientName]` (`sendEnvelopeToClient`) | A client matches a reply by request id alone (`TransactionManager.CommitTps`), and every client numbers its ids from 0. If client 3 received client 5's CommitTps for id 42, it would mark its own request 42 committed. |
| LeaderIdUpdate, VCRunningStatus | every client stream (`broadcastEnvelopeToClients`), each on its own goroutine | Every client counts its own 2f leader-update quorum. Separate goroutines mean a backed-up stream does not delay the others. |

## Order of CommitTps sends on a node

After execution, `exeLoop` starts `go n.postActions(...)` (`node/execution.go`).
`postActions` loops over the batch's executed requests **in execution order** and calls
`sendCommitTps` for each. That call goes through `Send` → `sendEnvelopeToClient` →
`clientStreamState.send`, which holds that stream's `sendMu` around `stream.Send`.

- **Within one batch, sends are sequential.** The CommitTps for one request (say,
  client 3's) is sent before the next one starts, which may be for client 5. They are not
  grouped by client: a batch goes client3, client3, client5, client1, … in the order the
  requests executed.
- **Different batches can overlap.** Each `exeLoop` call starts its own `postActions`
  goroutine.
  - Two goroutines writing to the same client wait for each other on that stream's
    `sendMu`.
  - Writes to different clients run in parallel.
- **`stream.Send` normally returns at once.** It returns when the message is in the gRPC
  transport buffer, not when the client has read it. It blocks only when the receiver's
  flow-control window for that stream is used up (8 MiB, `grpcFlowControlWindowSize` on
  the client), meaning the client has stopped reading.
- **WAN delay is not a reason to block.**
  - A client receives about 1,160 CommitTps/s from each node at `client_num` 7, roughly
    8 KB per 70 ms round trip at 35 ms one way.
  - That is three orders of magnitude below the window, so a far client that keeps
    reading never fills it.

### Known gap: a stalled client delays other clients' replies

If one client process stops reading (hung, or starved of CPU), its stream window fills,
and the next `stream.Send` to it blocks. That blocks the **whole `postActions` loop** for
that batch. Every CommitTps after it in the batch is delayed, including those for other
clients, which then show extra latency. Later batches' goroutines block too, at the first
request for the stalled client.

With a single client this was the same (everything went to one stream). With N clients,
one stuck client can inflate the others' latency. It does not occur in normal runs.

**Future fix if it shows up:** give each client stream its own queue and sender goroutine,
the node-side mirror of `client/sendqueue.go`.
- `postActions` would only enqueue, without blocking.
- A full queue would drop the CommitTps for that client only, and count the drop.
- Only the stalled client would lose or delay its replies.

Signs that it is needed, from a run's logs:
- one client's latency jumps;
- other clients' latency rises at the same moments;
- node CommitTps sends are slow.

## Sending blocked does not block receiving (ProposalDelay)

A bidirectional gRPC stream carries two independent HTTP/2 data flows, client→node and
node→client. Each direction has its own stream-level flow-control window, so one direction
filling up does not stop the other.

What happens under ProposalDelay when node 1 leads:

1. **Node 1 stops reading client requests.** Its stream handler (`ClientNodeChannel` Recv
   loop) hands each verified request into the event loop through
   `ReceiveVerifiedClientRequestCh`. The event loop is sleeping 100 ms in `tryPropose`, so
   the handler waits there and stops calling `Recv`.
2. **The client→node 1 window fills.** Node 1 stops returning window to the client. Once
   the 8 MiB stream window is used up, the client's `stream.Send` to node 1 blocks.
3. **Node 1→client keeps flowing.**
   - Node 1 sends CommitTps from the `postActions` goroutine, which does not wait on the
     event loop.
   - The client drains that direction in `recvLoop`, a goroutine separate from its sender.
   - So CommitTps and LeaderIdUpdate from node 1 still arrive.
4. **Only the goroutine calling `Send` is stuck.**
   - **Old client:** that goroutine held the pacer lock, so sends to every node froze. The
     next leader got its first request ~318 ms after takeover, and a 150 ms-timer leader
     was starved.
   - **With `client/sendqueue.go`:** only node 1's sender goroutine blocks. Its 8-batch
     queue drops the overflow (`send_queue_dropped`), and the pacer keeps feeding the
     current leader.

Other traffic is not affected either:
- **Other nodes:** each node is a separate connection, so node 2's stream is untouched.
- **Other streams to node 1:** for example, the throttle's unary `Deliver`.
  - grpc-go returns connection-level window as soon as data arrives off the wire.
  - Only the stream-level window waits for the application to read.
  - So one unread stream does not use up the connection window.
  - This is from grpc-go's transport design and has not been tested in this repo.

**With N clients:** each client has its own stream to node 1, with its own 8 MiB window.
Each fills and blocks on its own, about N× more slowly than the single client's, since
each sends 1/N of the load. One client's full stream does not affect another client's
stream, and none of them blocks receiving.
