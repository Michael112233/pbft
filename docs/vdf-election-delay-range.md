# VDF Election Delay Range, Split Votes, and Election Time

## Purpose

This note describes how the VDF delay range affects:

- the probability of a split vote;
- the average time at which the first node finishes its VDF; and
- the tradeoff between a slower successful election attempt and fewer failed
  election attempts.

The analysis models each node's VDF completion time as an independent uniform
random variable. The current implementation obtains a uniform step count from
the node's verified VRF output and the configured inclusive range
`[min_vdf_delay, max_vdf_delay]`.

## Model and Assumptions

Let:

- `N` be the number of election participants;
- `T_min` be the minimum VDF completion time;
- `T_max` be the maximum VDF completion time;
- `W = T_max - T_min` be the wall-clock width of the VDF range; and
- `L` be the time between the first node finishing its VDF and the other nodes
  receiving, verifying, and acting on its vote request.

Let `X_(1)` and `X_(2)` be the earliest and second-earliest VDF completion
times. Under the current immediate-self-vote behavior, a split is avoided when
the winner's request reaches the other candidates before the second candidate
finishes:

```text
no split  <=>  X_(2) - X_(1) > L
```

The model assumes:

- nodes begin their VDFs at approximately the same time;
- VDF runtime is approximately proportional to `DelaySteps`;
- nodes have similar processing speeds;
- completion times are independent and uniform over a range of width `W`; and
- `L` is approximately fixed.

These assumptions make the equations useful for choosing an initial range.
Real measurements are still required because CPU scheduling, unequal hardware,
message queues, and network jitter make `L` and VDF runtime variable.

## Probability of Avoiding a Split Vote

Suppose the first node finishes at time `x`. Every other node must finish after
`x + L` to avoid a split:

```text
0 -------- x -------- x + L ---------------- T_max
           winner     safe region begins
```

For one of the other nodes, the probability of finishing in the safe region is:

```text
(W - x - L) / W
```

For all `N - 1` other nodes, it is:

```text
((W - x - L) / W)^(N - 1)
```

Accounting for all `N` possible winners and integrating over every possible
winning time gives:

```text
P(no split) = N * integral[0, W-L] {
    (1/W) * ((W - x - L)/W)^(N-1)
} dx
```

Therefore:

```text
P(no split) = (1 - L/W)^N       when 0 <= L <= W
P(split)    = 1 - (1 - L/W)^N
```

If `L >= W`, the model gives `P(no split) = 0`: the entire VDF range is no
wider than the winner-announcement interval.

For four nodes:

```text
P(no split) = (1 - L/W)^4
P(split)    = 1 - (1 - L/W)^4
```

### Example

With `W = 1 second` and `L = 100 ms`:

```text
P(no split) = (1 - 0.1/1)^4
            = 0.9^4
            = 0.6561

P(split)    = 1 - 0.6561
            = 0.3439
```

The approximate probabilities are therefore 65.6% for no split and 34.4% for
a split.

With `W = 4 seconds` and the same `L`:

```text
P(no split) = (1 - 0.1/4)^4
            = 0.975^4
            = 0.9037
```

The approximate split probability falls to 9.6%.

## Choosing a Range for a Target Split Probability

If `epsilon` is the desired maximum split probability, solve:

```text
(1 - L/W)^N >= 1 - epsilon
```

This gives:

```text
W >= L / (1 - (1 - epsilon)^(1/N))
```

For four nodes:

| Maximum split probability | Required `W/L` | `W` when `L = 100 ms` | `W` when `L = 500 ms` |
|---:|---:|---:|---:|
| 20% | 18.43 | 1.84 s | 9.22 s |
| 10% | 38.47 | 3.85 s | 19.23 s |
| 5% | 78.48 | 7.85 s | 39.24 s |

This table demonstrates why reducing request propagation and processing time
is important. If `L` is five times larger, the required VDF range is also five
times larger.

## Average Winning VDF Time

For `N` independent uniform completion times, the expected earliest completion
time is:

```text
E[T_first] = T_min + W/(N + 1)
```

For four nodes:

```text
E[T_first] = T_min + W/5
```

If the minimum remains fixed, increasing the wall-clock range by one second
increases the average winning time by 200 ms in a four-node election.

| Wall-clock range width `W` | Average winning time above `T_min` |
|---:|---:|
| 1 s | 0.2 s |
| 4 s | 0.8 s |
| 8 s | 1.6 s |
| 20 s | 4.0 s |

The average total time for a successful election attempt is approximately:

```text
E[T_election]
    = T_min
    + W/(N + 1)
    + T_request
    + T_grant_votes
    + T_new_view
```

The last three terms cover vote-request delivery, collection of a quorum of
granted votes, and dissemination and acceptance of the new-view message.

## Converting the Result to VDF Steps

The configuration uses VDF steps, not wall-clock seconds. If VDF runtime is
approximately linear and one step takes `k` seconds, then:

```text
W_time  ~= k * (max_vdf_delay - min_vdf_delay)
W_steps ~= W_time / k
```

The expected winning step count is:

```text
E[S_first]
    = min_vdf_delay
    + (max_vdf_delay - min_vdf_delay)/(N + 1)
```

For four nodes with the current `5,000-70,000` configuration:

```text
E[S_first] = 5,000 + (70,000 - 5,000)/5
           = 18,000 steps
```

For a proposed `5,000-1,000,000` configuration:

```text
E[S_first] = 5,000 + (1,000,000 - 5,000)/5
           = 204,000 steps
```

The expected winning work would be approximately `204,000 / 18,000 = 11.3`
times the current value. This does not automatically imply exactly 11.3 times
the wall-clock election latency because fixed overhead and CPU contention also
contribute.

To convert a target wall-clock range accurately:

1. Log both `DelaySteps` and elapsed VDF evaluation time.
2. Benchmark several step counts on the same hardware used by the experiment.
3. Fit the approximate cost per step `k`.
4. Measure the p99 completion-to-vote-request processing time and use it as
   `L`.
5. Select a target split probability and calculate `W` and the corresponding
   step range.

Increasing `min_vdf_delay` and `max_vdf_delay` by the same amount does not
reduce splits because it does not change the width. To reduce splits while
keeping the minimum fixed, increase `max_vdf_delay`.

## Successful Attempts Versus Retries

A wider range makes one successful election attempt slower, but it also lowers
the probability that an attempt fails due to a split. If failed elections are
retried independently, a simplified estimate is:

```text
p_success = (1 - L/W)^N
expected attempts = 1/p_success
```

If a successful attempt costs `A_success` and a failed attempt costs
`A_failure`, including its timeout, then:

```text
E[T_total]
    ~= A_success
     + ((1 - p_success)/p_success) * A_failure
```

Consequently, increasing `W` can reduce total average election time when a
split causes a long timeout, even though it increases the VDF time of each
individual attempt. The best range is an empirical tradeoff between average
winner time, split frequency, and failure-recovery cost.

## Interpretation for the Current Run

In the inspected four-node run, the first VDF completed only about 26 ms before
the next candidate, while the first candidate's request was observed by other
nodes roughly 100 ms after its VDF completed. Under immediate self-voting, the
runner-up could therefore vote for itself before learning about the winner.

The same run also contained much longer completion-to-request observations for
another candidate, reaching several hundred milliseconds. The system should
therefore measure a distribution of `L` rather than select a range from a
single minimum-latency observation. Using p99 `L` is conservative, but the
table above shows that a large `L` can require an impractically wide range.

## Limitations

Changing the delay range reduces split votes probabilistically; it cannot
eliminate them. The model also does not establish an objective global
wall-clock winner. Replicas can observe messages in different orders, and a
VDF proves sequential work rather than a trusted completion timestamp.

The range-only approach is most effective when:

- VDF start times are closely aligned;
- candidate machines execute VDF steps at similar rates;
- election messages are delivered promptly; and
- the system has an election timeout and retry mechanism for the remaining
  split-vote cases.
