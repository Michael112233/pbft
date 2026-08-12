# Client Retry and Closed-Loop Execution Review

## Summary

The current source has a connected retry transport path. An uncommitted
transaction becomes retry-eligible, the transaction manager selects it, and
the client resends it to the currently known leader as a
`RetryRequestMessage`.

Serial-client bookkeeping also admits only one normal transaction at a time:
the injection goroutine sends a request and waits on `reqExecutedCh` before it
adds and sends the next request.

However, the loop is not strictly execution-gated. A replica currently sends
`CommitTps` as soon as the request becomes locally committed, before the
serialized state machine has necessarily executed it. Consequently, the
client can send request `N+1` while request `N` is still waiting for an earlier
sequence in `pendingExecutions`.

The desired invariant is:

```text
send request N
    -> retry N until it reaches the active leader
    -> locally commit N
    -> execute N in sequence order
    -> notify the client that N executed
    -> send request N+1
```

## Current end-to-end path

### 1. Register and send the normal request

With `serial_client` enabled, `Client.InjectTxs` sets its batch size to one.
For each request, it first calls `TransactionManager.AddTransaction`, then
sends the request through `altSendRequestTransactionsForSerialClient`.

Relevant code:

- [`client/send.go`](../client/send.go), `InjectTxs`
- [`client/transactionmanager.go`](../client/transactionmanager.go),
  `AddTransaction`

Registration before sending is the correct ordering because an immediate
completion notification can then find the transaction in the manager.

After sending, the injection goroutine prefetches the next signed transaction
but does not register or send it. It then blocks on:

```go
<-c.reqExecutedCh
```

Prefetching the signature does not violate the closed-loop network behavior;
only the current transaction is in the transaction manager and on the wire as
a normal request.

### 2. Select a request for retry

The retry timer starts in `Client.Start`. Newly registered transactions have
an initial `nextRetryTime` 50 ms in the future. The retry worker polls every
50 ms and selects entries for which:

```go
!txn.committed && now.After(txn.nextRetryTime)
```

For a selected transaction, the worker:

1. copies its signed client message into the candidate list;
2. increments `retryCount`;
3. calculates the next retry time with exponential backoff and jitter; and
4. calls `tm.client.sendTransactions(candidates)`.

This call is enabled in the current source. The earlier failed run, whose log
reported retry sends completing in tens of nanoseconds, used the disabled
version in which this call was commented out.

### 3. Send the retry to the active leader

`Client.sendTransactions` now distinguishes serial and non-serial operation.
In serial mode it calls:

```go
c.altSendRequestTransactionsForSerialClient(txs, retryRequestMessageType)
```

That method reads `leaderAddr` under `leaderMu` and sends a request envelope
whose internal type is `RetryRequestMessage`.

The message hub waits up to five seconds for a stream to the selected node. A
successful stream send increments the request-sent metric. A failed send is
logged, while the transaction remains in the manager and becomes eligible
again at its next retry time.

Relevant code:

- [`client/send.go`](../client/send.go), `sendTransactions`
- [`client/clientMessageHub.go`](../client/clientMessageHub.go), `Send` and
  `sendToNodeStream`
- [`client/receive.go`](../client/receive.go), `HandleLeaderUpdate`

The target can temporarily remain the old leader until the client receives
the configured quorum of matching leader-update messages. This is expected;
later retries should use the updated address.

### 4. Accept or drop the retry at a node

`Node.HandleRequestMessage` recognizes `RetryRequestMessage` for logging. It
drops the request if the recipient is in a view change or is no longer the
leader. Otherwise, it queues each signed client message for proposal.

Relevant code:

- [`node/receive.go`](../node/receive.go), `HandleRequestMessage`
- [`node/node.go`](../node/node.go), `VerifiedClientMessageHandler` and
  `preprepare`

A dropped request is not fatal because it remains uncommitted in the client
transaction manager and is retried later. Duplicate suppression prevents the
same request from being proposed twice in the same view. A retry in a later
view can be proposed again for recovery.

### 5. Deduplicate completion at the client

Every replica may send a `CommitTps` notification for the same transaction.
`TransactionManager.CommitTps` makes the first notification effective by
setting `txn.committed`, removing the transaction from its shard, and
incrementing the committed counter. Later notifications either do not find
the transaction or observe that it is already committed and return `false`.

`Client.HandleCommitTpsMessage` writes to `reqExecutedCh` only when
`CommitTps` returns `true`. Therefore, duplicate notifications from the other
replicas do not normally leave extra tokens in the serial client's channel.

Relevant code:

- [`client/transactionmanager.go`](../client/transactionmanager.go),
  `CommitTps`
- [`client/receive.go`](../client/receive.go), `HandleCommitTpsMessage`

At the bookkeeping level, this correctly releases the serial loop once per
transaction.

## Primary correctness gap: commit is not execution

The name `reqExecutedCh` does not match the event that currently drives it.

In `Node.tryExecute`, a slot is considered locally committed after it has a
PrePrepare, has sent its own commit, and has `2f+1` matching commit votes. At
that point the node currently performs these operations in this order:

```go
go n.sendCommitTps(executedMsg)
n.queueCommittedExecution(seq, slot, executedMsg, noOp, missingData, digest)
```

The state machine has not necessarily executed the request at this point.
`queueCommittedExecution` only places it on `commitChan`.

Actual execution occurs later in `newcollectReadyExecutions`. That routine
waits until every preceding sequence is available and then performs:

```go
result = n.executionMachine.Apply(pending.msg)
pending.slot.executed = true
n.lastExecuted = nextSeq
```

Relevant code:

- [`node/node.go`](../node/node.go), `tryExecute`
- [`node/execution.go`](../node/execution.go), `queueCommittedExecution`,
  `commitSerializedRoutine`, and `newcollectReadyExecutions`

This creates the following current ordering:

```text
request N becomes locally committed
    -> replica sends CommitTps
        -> client removes N and sends N+1
    -> replica queues N for ordered execution
        -> N executes when all earlier sequences are present
```

If an earlier sequence is missing, request `N` can remain in
`pendingExecutions` even though the client has already advanced to `N+1`.
Thus, the current client is local-commit-gated rather than execution-gated.

## No-op and missing-data false completion risk

`tryExecute` calls `sendCommitTps` for every locally committed slot, including
no-op slots and slots whose client data is missing. In those cases,
`executedMsg` can be the zero value of `core.ClientMsg`, whose ID is zero.

If client transaction ID 0 is outstanding when such a notification arrives,
the transaction manager can incorrectly match that notification by ID and
release the serial loop. A completion notification must only be sent for a
real client request whose data is available and which has actually passed
through ordered execution.

## Execution-result semantics

`CommitTps` contains client identity fields but does not contain:

- the executed sequence number;
- execution success or failure; or
- the execution error.

The existing `ReplyMessage` type already carries these fields, and
`Node.sendReply` constructs such a message. However, the calls to `sendReply`
after actual execution are currently commented out. `ReplyTxn` only sets a
`done` flag and does not release the serial loop.

The implementation should choose one authoritative completion path:

1. Move and extend `CommitTps` so it is sent only after ordered execution and
   contains the execution result; or
2. use `ReplyMessage` as the execution acknowledgement and make the
   transaction manager complete/release the request from that path.

Two partially overlapping completion mechanisms make it easy for commit,
execution, TPS accounting, and serial-loop release to diverge.

For deterministic execution failure, infinite retry is generally incorrect:
the same transaction will fail again on every replica. A failed execution
should be recorded as a terminal result and should release or stop the serial
workload according to an explicit experiment policy.

## Recommended minimal correction

Remove the `sendCommitTps` call from `Node.tryExecute`. Emit the client
completion only inside the ordered execution loop, after:

1. `executionMachine.Apply` returns;
2. `slot.executed` is set;
3. `lastExecuted` advances; and
4. the message is known not to be a no-op and its client data is present.

Conceptually:

```go
result := n.executionMachine.Apply(pending.msg)

pending.slot.executed = true
n.lastExecuted = nextSeq

if !pending.noOp {
    go n.sendExecutionResult(pending.msg, result, nextSeq)
}
```

After this change, the serial-client release path becomes:

```text
ordered execution notification
    -> transaction manager atomically completes and removes request N
    -> exactly one reqExecutedCh notification for N
    -> injection goroutine sends request N+1
```

For Byzantine-client semantics, consider waiting for `f+1` matching execution
replies rather than trusting a single replica notification. A single honest
replica's matching result is sufficient to establish that at least one honest
replica executed the request, whereas the current unauthenticated single
`CommitTps` notification is primarily suitable as an experimental metric.

## Concurrency and lifecycle observations

### A stale retry can race with completion

The retry worker copies a candidate while holding the transaction lock, then
releases the lock before performing the network send. The original request
can complete in between those operations. The worker may consequently send
one final stale retry after the transaction manager has completed and removed
the transaction.

Node-side duplicate handling should normally absorb it, but a strict
closed-loop design should either revalidate immediately before sending or
associate all sends and completions with the expected outstanding transaction
generation.

### Completion tokens do not carry transaction IDs

`reqExecutedCh` is a channel of empty structs. Current map deletion and
deduplication make one token per known transaction likely, but the injection
goroutine cannot verify that a received token belongs to the specific ID it is
waiting for. A channel carrying the completed transaction ID or a per-request
completion channel would express and enforce the invariant more directly.

### Client shutdown can block indefinitely

`Client.Stop` waits for the injection goroutine before it stops the retry
timer or message hub. If the serial client is waiting for a transaction and
the cluster never recovers, the injection goroutine never exits and shutdown
cannot progress. The wait on `reqExecutedCh` should also select on a client
cancellation channel or context.

### Retry timing comments are stale

The current backoff starts from one second and doubles before applying equal
jitter. After the first approximately 50 ms eligibility point, subsequent
retry delays are approximately:

```text
retry 1 -> 1 to 2 seconds
retry 2 -> 2 to 4 seconds
retry 3 -> 4 to 8 seconds
later   -> 5 to 10 seconds
```

The comments in [`client/util.go`](../client/util.go) describing 10, 20, and
30-second delays do not match the implementation.

## Test status and missing coverage

The existing tests do not validate the end-to-end invariant that request
`N+1` is sent only after actual ordered execution of request `N`.

At the time of this review:

- client tests do not compile because calls to `requestPacer.pace` in
  `client/send_test.go` use an older signature that lacks the logger argument;
- node tests, when run with vet disabled, panic in
  `TestNewViewStoresLeaderForView` inside `Node.createO`; and
- default node test execution also reports pre-existing non-constant logging
  format vet errors in `node/receive.go`.

Required focused tests include:

1. an uncommitted serial request is selected and sent as
   `RetryRequestMessage`;
2. a retry uses the leader address current at send time;
3. a locally committed but execution-blocked request does not release the
   client;
4. actual ordered execution releases the client exactly once;
5. duplicate completion notifications from multiple replicas do not release
   more than once;
6. no-op and missing-data slots never complete client transaction ID 0;
7. a stale retry racing with completion cannot corrupt the next request;
8. serial shutdown cancels an outstanding wait; and
9. execution failure follows the chosen terminal failure policy.

## Final assessment

The current retry transport wiring is correct enough to recover a request
that was dropped during a view change: it remains tracked, is selected again,
and is resent to the client's current leader. The transaction manager also
deduplicates replica notifications so the serial loop normally receives only
one release token.

The remaining blocking issue is the meaning and placement of the completion
notification. Until it is emitted after `executionMachine.Apply` and ordered
`lastExecuted` advancement, the implementation does not guarantee “execute
one request, then send the next.” It guarantees only “locally commit one
request, then send the next.”
