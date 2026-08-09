# Why Removing Netem Delay Creates a Throughput Burst

## Summary

In the current loopback experiment, enabling netem slows PBFT consensus while
the client continues sending requests to Node 1 at the normal rate. Work
therefore accumulates in the leader, the PBFT pipeline, and the netem queues.
When the node-to-node delay is removed, that accumulated work can complete in a
short interval. This produces a temporary throughput burst above the normal
sustainable rate.

The burst is real, but it is a catch-up effect. Removing delay does not
permanently increase the system's capacity.

## Network paths affected by netem

The loopback-IP configuration uses these addresses:

```text
Client: 127.0.0.1
Node 1: 127.0.0.2
Node 2: 127.0.0.3
Node 3: 127.0.0.4
Node 4: 127.0.0.5
```

`setup_netem` installs filters only for source and destination pairs belonging
to the nodes (`127.0.0.2` through `127.0.0.5`). Consequently:

```text
Client -> Node 1 leader: not delayed
Node 1 -> replicas: delayed
Replica -> replica: delayed
Replica -> Node 1: delayed
```

The client continues delivering approximately 1,200--1,300 transactions per
second to the leader while netem delays PrePrepare, Prepare, and Commit
traffic between replicas.

## Netem queues delayed packets rather than rate-limiting them

The schedule uses a command of this form:

```text
netem limit 100000 delay 100ms
```

This is a delay configuration, not a bandwidth limit. Netem holds matching
packets in its queue until their scheduled delivery time. No packet-loss rule
is configured, and the queue limit is 100,000 packets, so packets are expected
to be queued rather than dropped as long as that limit is not exceeded.

This distinction is important. A bandwidth limiter would restrict how quickly
the backlog could leave the queue. A delay-only qdisc can release traffic at
the underlying interface's much higher rate once packets become eligible.
Changing the delay back to zero also removes the additional wait for new
node-to-node packets. Previously delayed traffic and newly transmitted traffic
can consequently arrive close together.

Netem can still drop packets if its queue limit is exceeded. The statement
that packets are not dropped here depends on the configured high queue limit
and the absence of a loss rule; it is not an unconditional property of netem.

## How work accumulates in PBFT

Because the client-to-leader path is not delayed, Node 1 continues receiving
requests during the node-to-node slowdown. However, Node 1 cannot create an
unbounded number of PrePrepares. The current configuration contains:

```json
"max_inflight_seq": 400,
"retry_sleep": 200
```

The leader calculates its in-flight work as:

```text
inflight = preprepareSeqNumber - lastExecuted
```

Once `inflight` reaches 400, the leader pauses and retries instead of
continuing to allocate sequence numbers without limit. During the measured
delay, `node_1.log` shows this gate being reached repeatedly:

```text
19:01:21.621 seq 7715 reached max inflight 400
19:01:21.831 seq 7803 reached max inflight 400
19:01:22.044 seq 8006 reached max inflight 400
```

Work therefore accumulates in several places:

- client requests waiting in or before the leader's verified-request channel;
- up to 400 proposed sequence slots at different PBFT stages;
- PrePrepare, Prepare, and Commit packets waiting in netem queues;
- committed or partially committed slots waiting for an earlier sequence so
  ordered execution can advance.

When the delay is removed, consensus messages arrive faster and many
pipelined slots can reach quorum close together. Ordered execution advances,
which reduces the in-flight count and reopens the leader's 400-slot proposal
window. Node 1 can then turn more queued client requests into PrePrepares. The
combined effect is a temporary catch-up burst:

```text
undelayed client input
        -> delayed node-to-node consensus
        -> queued packets and pending PBFT slots
        -> delay removed
        -> quorums and executions complete close together
        -> proposal window reopens quickly
        -> backlog drains faster than the normal input rate
```

## Evidence from the current run

`logs/netem_schedule.log` records:

```text
2026-08-07T19:01:21-06:00 delay=100ms
2026-08-07T19:01:22-06:00 delay=0ms
```

The client-side commit samples around that transition are:

| Sample time | New commits in 0.5 s | Window TPS | Interpretation |
| --- | ---: | ---: | --- |
| 19:01:21.193 | 501 | 1,002 | slowdown begins |
| 19:01:21.693 | 135 | 270 | consensus is delayed |
| 19:01:22.193 | 367 | 734 | transition/recovery begins |
| 19:01:22.693 | 1,071 | 2,142 | catch-up after removal |
| 19:01:23.193 | 1,026 | 2,052 | catch-up continues |
| 19:01:23.693 | 638 | 1,276 | returns near normal |

The node execution timestamps independently show the same behavior on all
four replicas:

| Sequence interval | Observed execution rate |
| --- | ---: |
| 7250 to 7750, across the delayed period | 470--474 TPS |
| 7750 to 10000, after delay removal | 1,977--2,003 TPS |
| 10000 to 12000, after recovery | 1,247--1,251 TPS |

This replica-side evidence shows that the client peak is not solely caused by
client notification batching. The nodes themselves advance execution faster
while draining the backlog. The view remains 1, so this particular burst is
also not the result of a leader change.

## Why `node_1_throughput.csv` appears not to show the burst

The `tput_measurement` column in `node_1_throughput.csv` is not fixed-window
throughput. It is calculated as:

```text
executed_slots = current_seq - throughput_interval_start_seq
elapsed_seconds = now - throughput_interval_start_time
tput_measurement = executed_slots / elapsed_seconds
```

It is therefore a cumulative average from the beginning of the observation
interval. The low-throughput delay and the high-throughput recovery are mixed
with all earlier executions. A short recovery burst only moves this cumulative
average gradually, so a plot of `tput_measurement` does not display a 2,000 TPS
spike.

Rows are also emitted only when the executed sequence reaches a checkpoint
boundary, currently every 250 sequences. This is execution-triggered sampling,
not a periodic wall-clock measurement. A stall appears as a long gap between
rows rather than as explicit zero-TPS samples.

The CSV nevertheless contains enough information to reconstruct the burst
from adjacent sequence and timestamp values:

```python
import pandas as pd

node = pd.read_csv("logs/node_1_throughput.csv")
node["time"] = pd.to_datetime(node["time"])
node["interval_sec"] = node["time"].diff().dt.total_seconds()
node["executed_delta"] = node["seq"].diff()
node["interval_tps"] = node["executed_delta"] / node["interval_sec"]

print(
    node[["time", "seq", "interval_sec", "interval_tps"]]
    .sort_values("interval_tps", ascending=False)
    .head(10)
)
```

This derived `interval_tps` measures the rate between two adjacent checkpoint
observations. It exposes the approximately 2,000 TPS recovery seen across all
four node files.

## Why `ch.json` shows the burst

`ch.json` is exported from the client's TPS series. Its `window_tps` is
calculated every 500 ms as:

```text
window_tps =
    (committed_total_now - committed_total_at_previous_sample)
    / (now - previous_sample_time)
```

Unlike the node's cumulative `tput_measurement`, this calculation retains the
local behavior of each short window. For example:

```text
1,071 newly committed transactions / 0.500005 seconds = 2,141.98 TPS
```

That is why `ch.json` clearly shows the temporary recovery burst.

The client metric has a different semantic meaning from the node execution
metric. It counts when the client first accepts a unique `CommitTps`
notification for a transaction. It is therefore a client-observed commit
arrival rate, not a direct timestamp of state-machine execution. Network and
client scheduling can bunch notifications together. In this run, however,
the adjacent node sequence timestamps corroborate the same recovery burst.

If a five-sample rolling mean is calculated from `ch.json`, its peak is a
trailing 2.5-second rate:

```text
(2141.977 + 2052.008 + 1276.000 + 1324.009 + 1199.996) / 5
    = 1598.798 TPS
```

This smoothing changes the magnitude and time at which the plotted peak
appears, but it does not create the underlying burst.

## Interpreting the measurements

For this run, the relevant values describe different time scales:

- approximately 2,142 TPS: highest 500 ms client-observed commit-arrival rate;
- approximately 2,000 TPS: node-side checkpoint-to-checkpoint execution rate
  while the backlog drains;
- approximately 1,599 TPS: highest trailing 2.5-second client rate;
- approximately 1,240 TPS: normal sustainable/whole-run throughput.

The peak should therefore be described as **transient recovery throughput**,
not as a new steady-state capacity. For future experiments, add a periodic
node-side execution counter using fixed 250 or 500 ms windows and emit
zero-execution windows explicitly. That will make stalls and catch-up bursts
directly visible without reconstructing rates from checkpoint timestamps.
