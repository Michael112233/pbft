# Pipeline window vs. throughput: who is the bottleneck, and when

How the in-flight window (`max_inflight_seq`) and batch size interact with round
latency, and at what round latency the window, not the clients or the CPU, starts to
limit throughput. Written while evaluating a bad-positioned leader (node 4 under
NetworkDelay) and whether AWARE-style latency-aware leader selection is worth adding
as a policy.

## 1. How the window is gated (code)

- `tryPropose` (`node/node.go:381`) refuses to propose when
  `inflight = sequenceNumber - lastExecuted >= AllowedMaxInFlight()`.
  `AllowedMaxInFlight()` is `cfg.MaxInflightSeq` (`max_inflight_seq`, currently 10).
- `lastExecuted` only advances in `exeLoop` (`node/execution.go`) when slot
  `lastExecuted+1` is committed. Execution is strictly in order, so the window slides
  when the **head** slot commits, and one slow head slot blocks the window even if
  later slots already committed.
- A batch is proposed only when `pendingRequests.Len() >= batch size`
  (`max_batch_size`, currently 30).
- `tryPropose` is called from the event loop when a client request arrives and from
  `exeLoop` after `lastExecuted` advances.
- Also gated by the high watermark (`consensusLog.high`), which must stay above the
  window.

Defaults in `config/run2new.json`: `max_inflight_seq = 10`, `max_batch_size = 30`, so
`W*B = 300` transactions in flight.

## 2. The model

Notation:

| Symbol | Meaning |
|---|---|
| `W` | in-flight window (`max_inflight_seq`) |
| `B` | batch size (`max_batch_size`) |
| `W*B` | transactions the pipeline can hold in flight |
| `L` | round latency: leader sends PrePrepare until the **leader** commits the slot |
| `R` | client rate (tx/s offered) |
| `C` | CPU / crypto / gRPC ceiling (tx/s) |

Steady-state throughput (Little's law on the pipeline):

```
T = min( R, C, W*B / L )
```

- `W*B / L` is the most the pipeline can move: `W*B` transactions per round of
  length `L`.
- The window slides on the leader's own commit, so `L` is the leader's own
  PrePrepare-to-commit time, not the replicas'.

## 3. Crossover round latency `L*`

The window becomes the bottleneck when its limit drops below the other limits.

Against clients (CPU treated as infinite, or not saturated):

```
W*B / L  <  R
L        >  W*B / R
L*       =  W*B / R
```

Against the CPU ceiling: `L* = W*B / C`.

In general `L*` is set by whichever of `R` and `C` is smaller, i.e. the effective
supply limit `min(R, C)`:

```
L* = W*B / min(R, C)
```

- `L < L*`: the window is not the constraint. `T = min(R, C)`. Extra latency is
  hidden by the pipeline and only per-request latency rises.
- `L > L*`: the window is the constraint. `T = W*B / L`, falling as `1/L`.

`L*` is just the break-even point where the two limits are equal. Equivalently, the
transactions arriving during one round (`L*R`) equal what the window can hold (`W*B`).

### Example A: `W*B = 300`, `R = 8000` tx/s, CPU infinite

```
L* = 300 / 8000 = 0.0375 s = 37.5 ms
```

| `L` | Throughput |
|---|---|
| 5 ms | 8000 (clients-limited; window limit would be 60,000) |
| 37.5 ms | 8000 (exactly the crossover) |
| 75 ms | 300 / 0.075 = 4000 |
| 342 ms | 300 / 0.342 = ~880 |

### Example B: same, but the window is 900 (the hypothetical that started the discussion)

```
L* = 900 / 8000 = 0.1125 s = 112.5 ms
```

Three times the window tolerates three times the round latency before throughput
drops.

### Example C: the crossover moves with `R`

`W*B = 300`:

| `R` (tx/s) | `L*` |
|---|---|
| 1,000 | 300 ms |
| 3,000 | 100 ms |
| 8,000 | 37.5 ms |
| 10,000 | 30 ms |

The same delay can be harmless at a low client rate and costly at a high one. Results
depend on how `R` compares with `C`, not only on the delay.

### Example D: `C` is the constraint (no client limit)

`W*B = 300`, `C = 6000` tx/s: `L* = 300 / 6000 = 50 ms`. With `C = 3000` it is 100 ms;
with `C = 10000` it is 30 ms.

## 4. What `L` is: healthy vs. a badly placed leader

Let `eps` be the healthy per-hop time (about 1 ms on loopback) and `d` the extra
delay on node 4's links, per direction.

**Healthy** (or node 4 is a follower):

```
L ~= 3*eps      (PrePrepare, Prepare, Commit)
```

For `eps = 1 ms`, `L ~= 3 ms`, so the window limit is `300 / 0.003 = 100,000 tx/s`.
The window is nowhere near binding. `T = min(R, C)`.

**Node 4 is the leader, with delay `d` on its links:**

1. PrePrepare reaches the replicas after `d + eps`.
2. The three non-leaders commit among themselves without the leader: they need 2
   Prepares and 3 Commits, and the three of them can supply both. About `2*eps` more.
3. Their Commits travel back to node 4 (`+d`), which needs 3 Commits (its own plus 2).

```
L_slow ~= 2d + 2*eps
```

The leader pays the slow link twice: once outbound on PrePrepare, once inbound on
Commit.

**Node 4 as a follower is harmless**: quorums of 3 out of 4 form without it and it
never gates a window. The same node as leader costs `2d`, because the window gates on
its own commit. This asymmetry is why leader placement matters.

### Example E: node 4 leader, `W*B = 300`, `R = 8000` tx/s, CPU infinite

Crossover: `L* = 37.5 ms`, so `2d + 2*eps = 37.5 ms`, giving `d ~= 17.75 ms` per
direction. Beyond that the window binds.

| `d` (per direction) | `L ~= 2d + 2 ms` | Throughput `300 / L` (capped at 8000) |
|---|---|---|
| 5 ms | 12 ms | 8000 (window not binding) |
| 17.75 ms | 37.5 ms | 8000 (crossover) |
| 50 ms | 102 ms | ~2940 |
| 85 ms | 172 ms | ~1740 |
| 170 ms | 342 ms | ~880 (about 11% of the client rate) |

To restore 8000 tx/s at `L = 342 ms` the window must hold
`W*B = 8000 * 0.342 ~= 2740`, i.e. about `W = 91` at `B = 30`.

Sizing rule: `W*B >= min(R, C) * L_worst`, with `L_worst ~= 2d + 2*eps`.

## 5. Caveats

- **`L` is not purely network.** It includes queueing on the single-goroutine event
  loop and signature verification. Under CPU saturation, `L` rises by itself until
  `W*B / L = C`: the window and `C` become the same constraint (Little's law), and
  the window does no extra work. Measure the healthy `L` and compare with `W*B / C`:
  if it is close, the window is oversized; if `L` is much smaller, there is spare
  pipeline capacity.
- **Per-direction vs. per-hop delay is unconfirmed.** Whether the 170 ms in
  `scripts/netem_scenario.sh up 170 <n>` applies per direction or per hop depends on
  how the netem filters match on `lo`. That decides whether `d` is 170 ms or about
  85 ms. Check the script before quoting exact numbers.
- **Below the crossover, latency still rises.** A bad leader still adds `L` to every
  request even when throughput stays at `R`. That matters if the agent's reward uses
  latency and not only throughput.
- **The perf trigger is blind below the crossover.** It samples throughput. With `R`
  below the crossover, a bad leader looks healthy (`T = R` either way), so the 90%
  bar does not fire until `W*B / L` actually drops under `R`.
- **Timer interaction.** With a 150 ms fixed timeout, replicas commit their first slot
  about `d + 2*eps` after the leader's first PrePrepare. Around `d ~= 150 ms` the
  progress timer expires first and the system cascades (the NetworkDelay behaviour in
  CLAUDE.md). The window binds much earlier (in Example E from `d ~= 18 ms`), so
  between those two thresholds the system stays live but slow, and only leader
  placement or a larger `W*B` helps.
- **Proposal delay is a different mechanism.** The 100 ms sleep in `tryPropose` runs
  on the event loop and blocks all message handling, so it is not a latency term in
  `L`. It caps release rate at one batch per 100 ms and is excluded from this model.

## 6. Relevance to AWARE

AWARE (Berger et al., TDSC 2020) measures per-link latency with a one-sided
challenge-response (WRITE-RESPONSE), takes a moving median over a window, orders the
vectors through consensus so every replica holds the same matrix, and sanitizes with
`max(M[i,j], M[j,i])` before predicting the leader's consensus latency. The quantity
that matters here is the leader's `L`, which the round-trip measurement approximates
directly. A one-way link figure is only half of what the leader pays (`2d`).

An AWARE-style policy would help only when per-node latency is asymmetric (node 4
slow, others fast) **and** the pipeline is window-bound (`L > L*`). With a uniform
delay on all links there is nothing to optimize, and below the crossover the
throughput effect is hidden.
