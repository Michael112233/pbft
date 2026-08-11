# Adaptive vs. Fixed Experiment Design

## Objective

Evaluate whether the adaptive strategy commits more client requests than the
fixed strategy under the same transient network-delay workload.

The comparison should measure cumulative committed client requests over a
fixed experiment-relative time window, such as the first 800 seconds. The
primary result should be the difference in committed requests at the end of
that window, supported by the cumulative-commit curves and their gradients
(throughput).

## Current Timing Relationships

The current experiment uses:

| Setting | Value | Approximate wall-clock interpretation |
|---|---:|---:|
| Netem delay | 100 ms | Equal to the fixed execution timeout |
| Spike duration | 100 ms | Delay remains enabled for 100 ms |
| Gap between spikes | 3 s | One cycle approximately every 3.1 s |
| Fixed execution timeout | 100 ms | A spike is exactly on the threshold |
| Periodic interval | 20,000 requests | Approximately 16 seconds at 1,200–1,250 TPS |
| Learning epoch | 60,000 requests | Approximately 49–53 seconds |
| Learning watermark | 30,000 requests | Approximately 24–26 seconds |

At the current throughput, each periodic interval experiences approximately
five delay spikes, and each learning epoch experiences approximately sixteen
spikes.

Because the current 100 ms delay equals the 100 ms execution timeout, whether
fixed mode reacts can depend on scheduler and timer jitter. A spike slightly
above the timeout provides a more reliable experimental stimulus.

## Recommended Next Configuration

Use a 150 ms network delay, keep it enabled for 150 ms, and retain a 3-second
gap:

```bash
sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
    netem limit "$NETEM_LIMIT" delay 150ms
sleep 0.15

sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
    netem limit "$NETEM_LIMIT" delay 0ms
sleep 3
```

This should cross the fixed timeout reliably while remaining a short,
transient disturbance that periodic mode can potentially tolerate.

If this condition does not produce enough separation, reduce the gap to two
seconds while keeping the delay and duration at 150 ms:

```bash
delay 150ms
sleep 0.15
delay 0ms
sleep 2
```

Increasing spike frequency should be done carefully. Eventually both
strategies will be dominated by network disruption, at which point the
experiment no longer measures useful adaptation.

## Suggested Netem Sweep

Run a small predefined matrix instead of selecting only the condition that
produces the largest difference:

| Condition | Added delay | Duration | Gap |
|---|---:|---:|---:|
| Baseline | 0 ms | — | — |
| Current | 100 ms | 100 ms | 3 s |
| Reliable threshold crossing | 150 ms | 150 ms | 3 s |
| Frequent spikes | 150 ms | 150 ms | 2 s |
| Severe | 250 ms | 200 ms | 3 s |

This sweep shows whether the adaptive benefit is robust across disturbance
levels rather than specific to one hand-selected schedule.

## Learning Epoch Recommendation

The decision worker forces the initial protocol coverage before using a model
prediction. With the current 60,000-request epoch, the first model-generated
decision is applied around the beginning of epoch five, approximately 200
seconds into the experiment.

To adapt sooner, use:

```go
const (
    EPOCH_INTERVAL     int64 = 40000
    WATERMARK_INTERVAL int64 = 20000
)
```

At the current throughput, this should apply the first learned decision after
approximately 130–140 seconds. A 40,000-request epoch should still contain
roughly 10–12 netem spikes, providing a reasonably stable reward measurement.

Avoid reducing the epoch below 30,000 requests without first checking reward
variance. Shorter epochs adapt faster but produce noisier throughput rewards.

## Periodic Interval Recommendation

Keep the periodic interval at 20,000 requests for the next experiment. It is
currently long enough to avoid reacting to every transient spike while still
performing planned view changes regularly.

The configuration field `Period` also affects the total client workload:

```go
TotalTxnsToInject = Period * NumberOfPeriods
```

If the periodic interval changes, adjust `number_of_periods` so every
experimental condition injects the same total number of requests.

## Evidence From the Current Runs

The existing fixed and adaptive runs already show different system behavior
across the four nodes:

| Event | Fixed run | Adaptive run |
|---|---:|---:|
| Fixed-timeout view-change triggers | 597 | 138 |
| Planned periodic view-change triggers | 0 | 155 |
| State transfers | 29 | 11 |
| No-op executions | 168 | 32 |

At approximately 799.9 seconds:

| Metric | Fixed run | Adaptive run |
|---|---:|---:|
| Committed client requests | 913,259 | 966,889 |
| Client requests sent by 800 s | 992,800 | 993,200 |

The adaptive run committed 53,630 more requests, a 5.87% improvement over the
fixed run. The offered workloads differed by only 400 requests at 800 seconds.

A suitable interpretation is:

> Under the matched workload and netem conditions in these runs, the adaptive
> strategy committed approximately 5.9% more requests during the first 800
> seconds than the fixed strategy.

This is evidence consistent with the hypothesis that selecting periodic mode
improves throughput under transient delay spikes. It is not yet sufficient for
a broad causal claim because there is only one run per condition.

## Machine Considerations

The current machine reports 20 logical CPUs but has 10 physical cores on an
Intel Xeon E5-2640 v4. Four Go nodes, one Go client, four Python learning-agent
processes, signing workers, and netem all share the same host.

A newer machine may improve absolute throughput and reduce shared CPU
contention. It will not necessarily increase the adaptive advantage:

- Faster processing may make network-delay effects easier to distinguish from
  CPU contention.
- Faster view-change recovery may improve fixed mode and reduce the relative
  difference.
- If both strategies reach the client injection-rate ceiling, their cumulative
  curves may converge.

Always compare fixed and adaptive modes on the same machine. Changing hardware
between the two conditions would confound protocol strategy with machine
performance.

For cleaner single-host experiments:

- Keep background workloads off the machine.
- Use the same CPU governor for every run.
- Consider pinning node, client, and agent processes to consistent CPU sets.
- Keep the code revision and configuration identical except for the strategy
  under test.

## Fairness and Reproducibility Checklist

For each fixed/adaptive pair:

1. Use the same machine and code revision.
2. Inject the same number of requests at the same configured rate.
3. Use the same netem delay, duration, gap, interface, and filters.
4. Start the netem schedule relative to client experiment time so both runs
   receive the same spike phase.
5. Compare the same experiment-relative window, such as 0–800 seconds.
6. Alternate run order to reduce warm-cache, temperature, and background-load
   bias.
7. Run each condition at least five times.
8. Report the mean committed count at 800 seconds, standard deviation, and a
   confidence interval.
9. Also report state transfers, no-ops, view changes, and offered request rate
   to explain why throughput differs.

## Recommended Immediate Experiment

For the next fixed/adaptive comparison:

- Set netem delay to 150 ms.
- Hold each spike for 150 ms.
- Keep a 3-second gap between spikes.
- Set `EPOCH_INTERVAL` to 40,000 requests.
- Set `WATERMARK_INTERVAL` to 20,000 requests.
- Keep the periodic interval at 20,000 requests.
- Keep the total workload at 1,000,000 client requests.
- Run fixed and adaptive conditions at least five times each.

This configuration should make fixed-mode reactions more deterministic and
allow the learning agent to settle on periodic mode earlier without making the
network condition unrealistically severe.
