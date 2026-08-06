# Node-Side Congestion Metrics for Netem Experiments

This document recommends node-side metrics for distinguishing a normal PBFT
run from a run using the `start_netem_schedule` delay injection.

The current schedule applies 400 ms of node-to-node delay for two 2-second
periods. This is induced network latency, not bandwidth saturation or packet
loss. The most useful measurements are therefore peer RTT, loss of consensus
progress, fixed-window execution throughput, and view-change behavior.

## Current experimental evidence

The execution-triggered throughput CSVs already show a clear difference on
every node. The largest gap between recorded execution measurements is:

| Node | Normal maximum gap | Congestion maximum gap |
| --- | ---: | ---: |
| 1 | 0.287 s | 2.820 s |
| 2 | 0.302 s | 2.820 s |
| 3 | 0.319 s | 2.823 s |
| 4 | 0.311 s | 2.820 s |

The two congestion gaps align with the netem transitions and the view changes
from view 1 to view 2 and from view 2 to view 3. This makes consensus progress
stall the strongest existing majority-node signal.

Whole-run latency ratios are less reliable. Only a small fraction of requests
are affected by two short delay periods, and each node starts latency tracking
only after it accepts a request into consensus. Requests ignored during view
change or accepted later through retry do not contain the complete waiting
period on every replica. The median also changes independently and can make a
ratio rise even when the absolute percentile falls.

## Recommended metrics

### 1. Consensus progress-stall duration

Use this as the primary protocol-impact metric.

Record a monotonic timestamp whenever `lastExecuted` advances. On a periodic
100 ms ticker, calculate:

```text
no_progress_ms = now - last_execution_time
```

Only classify the value as a stall while work is pending. Without that gate,
an idle system after workload completion would look congested.

Export at least:

- `no_progress_ms`
- `max_no_progress_ms`
- `stall_event_count`
- `total_stall_duration_ms`
- `time_above_stall_threshold_ms`
- `last_executed_seq`
- `highest_observed_seq`
- `pending_seq_count`

For the current workload, use 500 ms as an initial stall threshold. The normal
runs remain below approximately 320 ms, while both induced stalls last about
2.8 seconds. A baseline-relative alternative is five times the normal
progress-gap p99.

The execution timestamp should be updated immediately after `lastExecuted`
changes in `node/execution.go`.

### 2. Fixed-window execution throughput

The existing throughput CSV is execution-triggered. When execution stops, it
produces an absence of rows instead of an explicit zero. Add a wall-clock
ticker independent of execution, using a 250 or 500 ms window:

```text
window_tps = (executed_now - executed_at_previous_tick) / window_seconds
```

Export each window, including zero-execution windows:

- `window_tps`
- `executed_in_window`
- `minimum_window_tps`
- `zero_tps_window_count`
- `longest_zero_tps_run_ms`
- `time_below_50_percent_baseline_ms`
- `throughput_deficit`

`throughput_deficit` can be calculated as the area below the normal baseline:

```text
sum(max(0, baseline_tps - window_tps) * window_seconds)
```

Normal runs should remain near their steady execution rate. Congestion runs
should contain several consecutive zero-TPS windows during both delay periods
on all nodes.

### 3. Peer heartbeat RTT

Use peer RTT as the direct measurement that netem changed the network path.
Every node should periodically send a lightweight probe to every other node,
and the receiving node should reply immediately.

Recommended probe behavior:

- Send one probe per peer every 250 ms.
- Allow only one outstanding probe per peer to prevent probe accumulation.
- Add small randomized scheduling jitter so all nodes do not probe at once.
- Use the sender's monotonic clock to measure request/reply RTT.
- Use a 2-second probe timeout for the current schedule.
- Ensure probes use the same source addresses and node-to-node path selected by
  the netem filters.

Export:

- `peer_id`
- `peer_rtt_ms`
- `peer_rtt_window_median_ms`
- `peer_rtt_window_max_ms`
- `peer_timeout_count`
- `peer_unreachable_duration_ms`

Because netem applies 400 ms in both directions, an application-level
request/reply should be approximately 800 ms during an active delay period,
plus application scheduling overhead. Normal loopback RTT should be much
smaller. Use windowed statistics rather than one whole-run p99 so that two
short events are not diluted.

Do not use the duration of a buffered gRPC `Send` call as a substitute for RTT.
The call may return after local buffering and before the peer receives the
message.

### 4. View-change metrics

View changes are a strong measurement of protocol impact, although they show a
consequence of network delay rather than measuring the network directly.

Record:

- `view_change_count`
- `view_change_start_time`
- `view_change_end_time`
- `view_change_duration_ms`
- `maximum_view_change_duration_ms`
- `view_change_trigger`
- `new_view_timeout_count`
- `messages_ignored_during_view_change`

Start the duration when a node sets `viewChangeRunning` and end it when the new
view is installed. For this experiment, the expected result is zero view
changes in the normal run and two view changes on every node in the congestion
run.

### 5. Oldest pending consensus age and backlog

Completed-request latency omits requests that are currently stuck. Track the
age and size of incomplete consensus work so that an active stall remains
visible.

Record when each node first observes a slot, then periodically export:

- `oldest_pending_age_ms`
- `pending_slot_count`
- `highest_observed_seq`
- `last_executed_seq`
- `sequence_backlog`, calculated as `highest_observed_seq - last_executed_seq`
- `inflight_limit_utilization`

Normal pending age should remain small. During netem delay, the oldest pending
age should grow into seconds and the sequence backlog may approach the
configured maximum in-flight limit.

### 6. Consensus phase timing

For deeper diagnosis, attach timestamps to these state transitions for each
sequence:

1. proposal or accepted `PrePrepare`;
2. prepare quorum reached;
3. commit quorum reached;
4. execution completed.

Derive:

- `preprepare_to_prepare_ms`
- `prepare_to_commit_ms`
- `commit_to_execute_ms`
- `preprepare_to_execute_ms`
- count and current age of phases that timed out before completion

Incomplete phases must be counted. Reporting only successfully completed phase
latencies creates the same selection problem as the existing node latency
summary. Prefer one-second windowed values, maximum phase age, and timeout
counts over only whole-run percentiles.

## Recommended detector

Combine direct network evidence with protocol impact:

```text
network_delayed =
    peer_rtt_window_median_ms > 200
    OR peer_rtt_window_median_ms > 5 * normal_peer_rtt_p99

consensus_stalled =
    pending_seq_count > 0
    AND no_progress_ms > 500

congestion_detected = network_delayed AND consensus_stalled
```

The fixed thresholds are suitable starting points for the current 400 ms
netem schedule. Production thresholds should be derived from repeated normal
runs and should account for the deployment network.

If only one metric can be added, use `max_no_progress_ms` gated by pending
work. If the goal is specifically to prove that netem caused the behavior, add
peer RTT as well.

## Suggested output files

Write health samples on a periodic ticker rather than only when execution
occurs:

```text
logs/node_<id>_health.csv
```

Suggested columns:

```text
time,view,leader_id,last_executed_seq,highest_observed_seq,pending_seq_count,no_progress_ms,executed_in_window,window_tps,oldest_pending_age_ms,view_change_running
```

Write peer probes separately:

```text
logs/node_<id>_peer_rtt.csv
```

Suggested columns:

```text
time,peer_id,rtt_ms,timed_out
```

Write one row per completed view-change event:

```text
logs/node_<id>_view_changes.csv
```

Suggested columns:

```text
view,start_time,end_time,duration_ms,trigger,ignored_message_count
```

Keeping these event streams separate avoids mixing network health, consensus
progress, and control-plane behavior into one ambiguous latency ratio.

## Experiment comparison procedure

Use the same transaction count, injection rate, batching, retry configuration,
node configuration, and experiment duration for normal and congestion runs.
Then compare:

1. maximum and total progress-stall duration;
2. count and duration of zero-TPS windows;
3. windowed peer RTT and timeout counts;
4. view-change count and duration;
5. oldest pending age and maximum sequence backlog;
6. absolute latency percentiles and ratios as secondary measurements.

Align all time-series data with `netem_schedule.log`. Report each node
individually and also report how many nodes crossed the selected threshold.
Repeat each condition several times and summarize the run-level median and
spread rather than drawing conclusions from a single run.

## Metrics that should not be primary signals

- **Whole-run p99/median alone:** sparse congestion events are diluted, and a
  changing median can reverse or inflate the ratio.
- **Completed latency alone:** ignored, retried, timed-out, and still-pending
  requests can be absent.
- **No-progress time without a pending-work gate:** normal idle time would be
  misclassified as congestion.
- **Packet loss or TCP retransmissions:** the current netem configuration adds
  delay and does not intentionally add loss.
- **CPU and memory usage:** useful for ruling out local resource pressure, but
  they do not directly detect the configured network delay.

The three recommended headline measurements for this experiment are
`max_no_progress_ms`, `zero_tps_window_count`, and
`peer_rtt_window_median_ms`.
