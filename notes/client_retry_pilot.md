# Client retry + intake filter: pilot results (2026-09-27)

Static runs, no epochs, 150 s each, `inject_speed` 200 per 24 ms (pacer budget
8333 tx/s), batch 50, n=4. Results in `results/pilot_retry_{A,B,C}_*`, configs in
`config/pilot_retry_*.json`.

| | A: PeriodicRR, no retry | B: PeriodicRR, backoff 50 ms / 2 s | C: FixedRR, node 1 proposal delay, fixed 50 ms |
|---|---|---|---|
| view changes installed | 15 | 15 | 0 |
| client commits / tps | 1,216,500 / 8108 | 1,215,544 / 8101 | 73,350 / 489 |
| latency avg / p99 / p99.9 | 10.4 / 13.6 / 44 ms | 11.5 / 21.6 / 182 ms | 51 s / 79 s / 80 s |
| commits that needed a retry | n/a | 5,550 (avg 160 ms, p99 276 ms) | all 73,350 |
| retry sends | 0 | 6,269 | 129,250 |
| never committed at stop | 5,700 | 200 | 35,650 |
| retries dropped at intake | 0 | 1,160 | 62,050 |
| duplicate executions | 0 | 0 | 0 |

## Findings

- **Without retry, requests hit by a view change are lost for good.** In A, about 5,500
  were lost over 14 view changes, roughly 400 per view change: the burst in flight plus
  the old leader's pending queue, which `pendingRequests.Reset()` drops. In B the 200
  left over is one burst still in flight at shutdown. `uncommitted` always includes
  in-flight requests at stop, because the client injects `period × number_of_periods`,
  not `max_tx_num`.
- **Most retries are real work, not overhead.** A retry of a lost request is its first
  execution. With the pacer saturated, retries replace fresh sends one for one (B:
  fresh 8097/s + retries 41/s = 8138/s, the same total as A), so the load delivered to
  the node is unchanged and node throughput stays at about 8100.
- **A retry is redundant only if the intake filter drops it.** B: 1,160, which is 0.09%
  of sends. C: 62,050, but there the node was the bottleneck (see below), so it cost
  nothing.
- **In C the fresh load dropped because of the node, not the retries.** The client sent
  1,435 tx/s in total, 17% of the pacer budget. With the node's pending queue full, the
  event loop stops reading, gRPC backpressure blocks the client's send, and everything
  paces to the 500 tps leader. Node and client throughput agree (489 tps, no duplicate
  executions).
- **C exercised only the "already queued or executed" part of the filter.** A slow but
  live leader never trips the Fixed trigger, so there were no view changes. B covered the
  view-change path. The post-checkpoint-jump gap (see CLAUDE.md, Intake filter) was not
  hit in either run.
- The p99.9 in B (182 ms vs 44 ms in A) is the view-change-hit requests, which are now
  counted instead of lost. p50 and p99 barely move, so the view-change cost shows up in
  the mean and far tail only.

## When retries can distort the offered load

Redundant retries take pacer budget from fresh requests only when both of these hold:

1. **the pacer is the bottleneck**, i.e. the client sends at about 100% of its budget
   (the node is not pushing back), and
2. **a large share of sends are redundant retries**. This happens when the retry interval
   is below the scenario's normal latency, so requests that were never lost get retried.
   The far-node scenario is near that edge: about 43 ms latency against a 50 ms first
   retry.

Under node backpressure (as in C), redundant retries cost only transport and intake
signature checks, and do not change what the node executes.

### How to monitor

After each run:

```bash
python3 scripts/retry_redundancy.py results/<run>
```

It reports:
- **pacer utilization:** sends per second (fresh plus retried, from
  `client_request_send_rate.csv`) over the budget `inject_speed × 1000 / 24`. At 95% or
  more the pacer is the bottleneck; lower means the node is pushing back and redundancy
  is harmless.
- **redundant share of sends:** retries dropped at intake (sum of the nodes' `INTAKE:`
  lines) over all sends.
- **useful offered load:** sends minus drops. Printed only when pacer-bound.

Not counted: retries that arrive during a view change, or at a node that is not the
leader (`eventloop.go` drops them before the intake filter). Those are sends to a leader
that isn't ready yet, so the redundant share is a lower bound on wasted sends.

It warns when the run is pacer-bound and more than 1% of sends were redundant. The fix
is a retry interval above the scenario's normal client latency. Pilot values: A 98% / 0%,
B 98% / 0.09%, C 17% / 28% (node-bound, harmless).

For a per-epoch signal later: the node already logs `INTAKE:` at every local checkpoint.
Summing them per generation gives the redundant retries for that epoch, which can go
into the epoch data next to throughput.
