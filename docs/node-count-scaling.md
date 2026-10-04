# Running beyond 4 nodes: what adapts, what it costs, what the numbers mean

The protocol runs unmodified at 7 and 10 nodes. Quorums, the `f+1` amplification
threshold, the perf trigger's window, the election candidate pool and the epoch
aggregation threshold all derive from `node_num` and scale on their own. One real bug
and one config-validation gap had to be fixed first; both are described below.

The throughput and latency costs of going from 4 to 7 nodes are **smaller than a naive
reading of the raw numbers suggests**, because every node in this testbed shares one
machine. The headline figures:

| Quantity | 4 → 7 nodes | Confidence |
|---|---|---|
| Throughput at equal per-node CPU | **≈ 84 %** (a ~16 % cost) | Medium — stable across two budgets and two load regimes, but pessimistically biased (§7) |
| Throughput, no CPU limits | 61 % | **Do not report** — co-location artifact (§3) |
| Consensus latency, backup-side | **+ ~2 ms** | High — node-side clock, zero-drop runs only (§4) |

Numbers below are from runs on 3–4 Oct 2026, saved under `results/`. Every figure says
which run directory and which formula produced it, because several of the obvious
readings of this system's own metrics are wrong (§4, §6).

---

## 1. The machine, which conditions every number

| Fact | Value | Source |
|---|---|---|
| CPU | Intel Xeon E5-2660 v2 @ 2.20 GHz (Ivy Bridge-EP, 2013) | `/proc/cpuinfo` |
| Topology | 2 sockets × 10 cores × 2 threads | `lscpu` |
| **Logical CPUs** | **40** | `lscpu` |
| **Physical cores** | **20** | distinct `topology/thread_siblings_list` values |
| cpufreq governor | `performance` on all 40 | `scaling_governor` |
| Turbo | **disabled** (`no_turbo = 1`) | `intel_pstate/no_turbo` |
| Clock | pinned at 2.2 GHz (range 1.2–2.2) | `scaling_cur_freq` = 2200503 kHz |
| NUMA | 2 nodes (0–9/20–29, 10–19/30–39) | `lscpu` |

Two consequences worth holding onto:

**Frequency is not a confound.** The governor is `performance` with turbo off, so the
clock never moves between runs. `schedutil` is available but unused. Anything in
`logs/freq_trace.csv` (see `freq_trace_analysis.md`) will be flat for these runs.

**"40 cores" is 40 hyperthreads over 20 real cores.** A sibling delivers roughly
1.2–1.3× a single core, not 2×. This matters for the CPU-normalised comparison and is
the main open weakness in §3 — see §7.

---

## 2. What adapts on its own

### Code audit

A sweep of every non-test Go file found **no fixed-size arrays and no literal loop
bounds** over node counts. The only `[N]byte` in the tree is `[8]byte` in
[`vrf.go`](../vr/vrf.go), a counter.

Everything node-count dependent derives from `f`:

- [`node.go`](../node/node.go) sets `fNodes = (cfg.NodeNum - 1) / 3`, and
  `QuorumSize() = 2*fNodes + 1`.
- All broadcasts enumerate `config.NodeAddr` (`asyncBroadCast`,
  `asyncBroadcastCommit`), never a literal count.
- `electionCandidateForView(view, nodeNum, excluded)` in
  [`election.go`](../node/election.go) draws from `1..nodeNum` minus `nodes_dead`.
- The perf trigger's window is `3*fNodes + 1` views ([`perfTrigger.go`](../node/perfTrigger.go)).
- Keys ([`crypto_main.go`](../setup_crypto/crypto_main.go)), addresses
  ([`network.go`](../config/network.go)), tmux windows and netem flower filters
  ([`alt_run_project.sh`](../alt_run_project.sh)) all loop on `node_num`.

The only literal node id is **4**, the epoch aggregator and netem qdisc owner. It is
documented scaffolding and node 4 exists at any `n ≥ 4`. It is now defined once as
`config.EpochAggregatorNodeID` and aliased by `epochAggregatorNodeID`
([`epochtimer.go`](../node/epochtimer.go)) and `scenarioNetemNodeID`
([`scenario.go`](../node/scenario.go)); `config` cannot import `node` (import cycle),
so the constant lives in `config`.

### Observed quorum scaling

Read from the `votes=N` field on `Stable checkpoint advanced` lines:

| n | f = (n−1)/3 | Expected 2f+1 | Observed | Run |
|---|---|---|---|---|
| 4 | 1 | 3 | **3** (4 when extras arrive) | `results/tps_n4` |
| 7 | 2 | 5 | **5** (6, 7) | `results/tps_n7` |
| 10 | 3 | 7 | **7** (8, 9) | `results/gmp3_n10` |

The perf window was confirmed from a node log at n=7, which reports *"Recent view
throughputs for the 7 views before (1,5)"* — `3f+1 = 7`, up from 4 at n=4.

### Functional runs at n=7

| Exercised | Evidence |
|---|---|
| Healthy, FixedRoundRobin | 2750+ sequence numbers executed, **zero `[ERROR]` lines on all 7 nodes** |
| PeriodicElection, nodes 2 and 3 dead | Node 5 elected with GrantVotes from 6, 4, 7, 1 — four grants plus its own = **5 = 2f+1** |
| Election fairness | `electionCandidateForView` reproduced in Python: uniform over 10 000 views (1981 / 2073 / 2012 / 1959 / 1975 across the five live nodes) |
| Epoch aggregation, 2 dead | `Epoch data quorum reached` at **5**, grace timer fired, `aggregate … with 5/7` |
| Live learning agents | 7 Python QuadRF servers, real gRPC decisions at generations 1 and 2, scenario switch Healthy → ProposalDelay → Healthy |
| n = 10 | 10 nodes, quorum 7, no stalls; only startup dial errors before all listeners were up |

---

## 3. Throughput

### Method

All runs via `./run_experiments.sh <seconds> <configs>`, which calls
`alt_run_project.sh` (rebuild, regenerate keys, one tmux window per node, then the
client), waits, then `stop_experiment.sh` to export metrics and move `logs/` into
`results/<name>/`. All with `peak_tps_test: true` so timer-driven view changes do not
interfere, `max_batch_size: 50`, `carry_state: false`, healthy, no faults.

| Quantity | Computed as |
|---|---|
| **offered tps** | `inject_speed / 0.024` — the client paces one batch of `inject_speed` per 24 ms ([`ratelimiter.go`](../client/ratelimiter.go)) |
| **steady tps** | From `report.json`: drop samples with `elapsed_sec < 15`, then `(committed_total_last − committed_total_first) / (elapsed_last − elapsed_first)` |
| **drop %** | `send_queue_dropped / (count + uncommitted)` from `latencyreport.json` |

Per-node CPU budgets were set with `NODE_GOMAXPROCS`, which
[`alt_run_project.sh`](../alt_run_project.sh) prefixes onto each node's command line.
Verified to take effect by reading `GOMAXPROCS` from `/proc/<pid>/environ` of the live
processes, and by a live `/proc/<pid>/stat` sample showing nodes pinned at 2.88–2.93 of
a 3-core budget with the client left uncapped at 4.39 cores.

### Raw results

| Run | n | cores/node | `inject` | Offered | Steady tps | Drop % | Over-driven |
|---|---|---|---|---|---|---|---|
| `tps_n4` | 4 | uncapped | 200 | 8 333 | 8 147 | 0.0 | 1.02× |
| `tps_n7` | 7 | uncapped | 200 | 8 333 | 8 152 | 0.0 | 1.02× |
| `sat_n4_i400` | 4 | uncapped | 400 | 16 667 | 16 399 | 0.0 | 1.02× |
| `sat_n7_i400` | 7 | uncapped | 400 | 16 667 | 13 660 | 7.7 | 1.22× |
| `sat_n4_i800` | 4 | uncapped | 800 | 33 333 | **21 763** | 30.1 | 1.53× |
| `sat_n7_i800` | 7 | uncapped | 800 | 33 333 | **13 262** | 54.0 | 2.51× |
| `gmp3_n4` | 4 | 3 | 800 | 33 333 | 12 123 | 58.6 | 2.75× |
| `gmp3_n7` | 7 | 3 | 800 | 33 333 | 10 235 | 64.8 | 3.26× |
| `gmp3_n10` | 10 | 3 | 800 | 33 333 | 8 226 | 70.9 | 4.05× |
| `gmp4_n4` | 4 | 4 | 800 | 33 333 | 14 797 | 50.5 | 2.25× |
| `gmp4_n7` | 7 | 4 | 800 | 33 333 | 12 287 | 58.4 | 2.71× |
| `g4knee_n4` | 4 | 4 | 400 | 16 667 | 15 036 | 0.9 | 1.11× |
| `g4knee_n7` | 7 | 4 | 400 | 16 667 | 12 593 | 16.8 | 1.32× |
| `g4sub_n4` | 4 | 4 | 250 | 10 417 | 10 288 | 0.0 | 1.01× |
| `g4sub_n7` | 7 | 4 | 250 | 10 417 | 10 250 | 0.0 | 1.02× |

### The ratios

| Comparison | n=4 | n=7 | n=7 / n=4 | Runs |
|---|---|---|---|---|
| Uncapped ceiling | 21 763 | 13 262 | **61 %** | `sat_*_i800` |
| 3 cores/node | 12 123 | 10 235 | **84 %** | `gmp3_*` |
| 4 cores/node | 14 797 | 12 287 | **83 %** | `gmp4_*` |
| 4 cores, tuned load | 15 036 | 12 593 | **84 %** | `g4knee_*` |
| n=10, 3 cores/node | 12 123 | 8 226 | 68 % | `gmp3_n4` vs `gmp3_n10` |

### Why uncapped reads 61 % and capped reads 84 %

Per-process CPU, from `utime + stime` in `/proc/<pid>/stat` over a 10 s window during
live runs:

| Run | cores per node | nodes total | client | Total |
|---|---|---|---|---|
| `sat_n4_i800` | **6.17** | 24.67 | 4.90 | 29.57 |
| `sat_n7_i800` | **4.56** | 31.90 | 3.29 | 35.18 |

Uncapped, each n=4 node got 6.17 cores while each n=7 node got 4.56 — a 26 % cut at
exactly the moment its own workload doubled. Seven processes each wanting ~6 cores is
43, on a box with 40 logical CPUs. **The 61 % is substantially the machine running out,
not the protocol.** Giving both sizes the same budget removes that, and the answer
settles at 83–84 % across two budgets and two load regimes.

### Messages per batch: the structural reason

Verified by reading the send paths. Prepare is broadcast only from `HandlePrePrepare`
([`node.go`](../node/node.go)), so **the leader never sends one** — which is why the
prepare quorum is `QuorumSize()-1`. Commit is broadcast by every node
(`asyncBroadcastCommit`).

Per sequence number:

- Backup receives `1 PrePrepare + (n−2) Prepares + (n−1) Commits` = `2(n−1)`
- Leader receives `0 + (n−1) Prepares + (n−1) Commits` = `2(n−1)`

| n | per node | system-wide `n·2(n−1)` |
|---|---|---|
| 4 | 6 | 24 |
| 7 | 12 (2.0×) | 84 (3.5×) |
| 10 | 18 (3.0×) | 180 (7.5×) |

Per-node growth is **linear**; system-wide growth is **quadratic**. In a real deployment
each machine pays only the linear column. On a single box the one machine pays the
quadratic column. That asymmetry is the entire gap between 61 % and 84 %.

---

## 4. Latency, and the two clocks that are easy to confuse

### The client clock includes client-side queueing

`AddTransaction` sets `startTimestamp` in
[`transactionmanager.go`](../client/transactionmanager.go), and
[`send.go`](../client/send.go) calls it **before** the send queue:

```go
if beforeSend != nil { beforeSend(batch) }   // -> AddTransaction: clock starts here
c.messageHub.EnqueueRequest(leader, ...)     // -> THEN into the 8-batch queue
```

So `p50_ms` in `latencyreport.json` includes the time a batch waits in the per-node send
queue ([`sendqueue.go`](../client/sendqueue.go), `sendQueueBatches = 8`). Its own
comment concedes the scope: *"latency includes send/stream wait, network, consensus and
reply."*

The only thing excluded is the **pacer** wait, reported separately as `queue_*`. This is
why `queue_p50_ms` reads a flat ~23.8 ms in every run — that is the 24 ms ratelimiter
tick, not queue depth. **Send-queue wait has no metric at all.**

At n=4 below saturation the client reported 21.0 ms while actual consensus was 5.0 ms:
**16 of those 21 ms were not consensus.**

### The node clock does not

`RecordStartTime` fires in `tryPropose` for the leader and in `HandlePrePrepare` for
backups ([`node.go`](../node/node.go)); `RecordEndTime` fires at execution
([`execution.go`](../node/execution.go)). Output lands in
`logs/node_<id>_latencylog.json`.

The leader's clock starts earlier in the round, which is why node 1 reads roughly double
the backups. **Compare leader-to-leader and backup-to-backup only.** Node 1 is the
leader in every run cited here (verified: zero view-change quorums, `peak_tps_test` plus
FixedRoundRobin means the genesis leader never rotates).

### The valid comparisons — zero-drop runs only

| Condition | n=4 leader | n=7 leader | n=4 backup | n=7 backup | Client p50 |
|---|---|---|---|---|---|
| Uncapped, 8 333 offered (`tps_*`) | 5.34 ms | 9.89 ms | **2.71 ms** | **5.08 ms** | 10.7 → 16.0 ms |
| 4 cores, 10 417 offered (`g4sub_*`) | 10.19 ms | 13.53 ms | **5.00 ms** | **6.69 ms** | 21.0 → 27.9 ms |

| Condition | Leader | Backup | Client | Absolute |
|---|---|---|---|---|
| Uncapped, 8.3k | +85 % | **+87 %** | +50 % | +2.37 ms |
| 4 cores, 10.4k | +33 % | **+34 %** | +33 % | +1.69 ms |

**Quote the ~2 ms, not the percentage.** The percentage swings from +87 % to +34 %
purely because the baseline moved (2.71 vs 5.00 ms). The extra wait is close to a fixed
cost, so it looks large against a small baseline and modest against a large one.

Mechanism: `prepared` needs the `2f`-th Prepare — 4 at n=7 instead of 2 — and
`committed` needs the `2f+1`-th Commit — 5 instead of 3. Each node waits on
progressively slower peers.

The client-measured ratio is **not** a reliable proxy for the consensus ratio. It agreed
in one case (+33 % vs +34 %) and was off by 37 points in the other (+50 % vs +87 %).

### Latency numbers that must not be used

**Every saturated run.** In `gmp3_n4` the backup median is 141.65 ms; in `gmp3_n10` it
is 52.72 ms, making n=10 look three times faster. That ordering is an artifact of
internal queueing against `max_inflight_seq`, not a latency result. **Any run with
drop % > 0 is unusable for latency.**

`g4knee` is the trap worth naming: 1643 ms → 5198 ms looks like a 3.16× penalty, but
n=4 sat at 0.9 % drop (barely saturated) and n=7 at 16.8 % (queue full). Two different
regimes, so the comparison is meaningless.

---

## 5. Why the cost is only ~16 %

### What is measured

Benchmarks in [`permsg_cost_bench_test.go`](../node/permsg_cost_bench_test.go)
(`go test ./node -run XXX -bench PerMessage -benchtime 2s`):

| Operation | Measured |
|---|---|
| `ed25519.Verify`, one consensus message | **128.5 µs** |
| `marshalDeterministic` | 0.37 µs (negligible) |
| Full per-message receive path | 129.8 µs |
| `ed25519.Verify`, one client transaction | **131.1 µs** |

Plus a structural fact: **every node verifies all 50 client signatures in a batch**, not
just the leader. The leader does it on intake (`verifyAndForwardRequests` in
[`receive.go`](../node/receive.go)); every backup repeats it in `HandlePrePrepare` via
`verifyPreprepareClientMessages` ([`node.go`](../node/node.go)). That is
`50 × 131 µs = 6.55 core-ms per batch, independent of n`.

| n | total/batch | 50 client sigs | 2(n−1) consensus sigs | crypto total | everything else |
|---|---|---|---|---|---|
| 4 | 12.37 | **6.55** | 0.78 | 7.33 (59 %) | 5.04 |
| 7 | 14.66 | **6.55** | 1.56 | 8.11 (55 %) | 6.54 |
| 10 | 18.23 | **6.55** | 2.34 | 8.89 (49 %) | 9.34 |

(`total/batch` = `cores / (tps / 50)`, from the 3-core runs; crypto columns from the
benchmark.)

**53 % of a node's CPU per batch at n=4 is client-signature verification that does not
depend on n at all.** `max_batch_size: 50` amortises the n-dependent message work across
50 transactions. That is why the penalty is ~16 % rather than something near the 2×
message growth.

### What is only inferred, and is weak

An earlier version of this analysis fitted `cost = F + M · 2(n−1)` from two points:

```
C(4) = F +  6M = 12.373
C(7) = F + 12M = 14.656
  ->  M = 0.380 core-ms,  F = 10.091 core-ms
```

**Two equations, two unknowns — zero degrees of freedom.** It fits exactly by
construction and validates nothing. The n=10 run then falsified its central assumption:

| messages/node | marginal cost per message |
|---|---|
| 6 → 12 | 0.380 core-ms |
| 12 → 18 | **0.597 core-ms** |

Not constant. Least squares over all three points leaves convex residuals
(+0.22, −0.43, +0.22), so a line is the wrong shape, and the "fixed share" becomes
82 % / 80 % / 76 % depending on which fit you pick. **Do not quote `F` or `M`.**

The fit also mis-attributed the cause: it blamed message handling at 0.380 core-ms each,
but measured crypto is only 0.130. The growth is dominated by non-crypto per-peer
overhead (gRPC streams, protobuf, channel handoff, scheduler), and that term grows
**super-linearly**: +1.50 core-ms for six extra messages from n=4→7, +2.80 for the same
six from n=7→10. Hence the linear model predicted 8 856 tps for n=10 and the run
measured 8 226 — 7.1 % low.

Two caveats. 128 µs for Ed25519 is slow (modern x86 is ~50 µs); this is 2.2 GHz Ivy
Bridge with turbo off and no AVX2, so crypto is a *larger* share here than it would be
on current hardware. And **the ~16 % figure is specific to `max_batch_size: 50`** — a
smaller batch makes the n-dependent part a larger share and should worsen the penalty.
That prediction is untested.

---

## 6. Readings of this system's metrics that are wrong

Recorded because each one was initially believed during this investigation.

| Belief | Reality |
|---|---|
| `consensus_chan_size: 5000` causes backpressure at n=7 | **No.** Inferred from `nodeMessageHub.go` sending without a `select`, never measured. Measured: 2 120 msgs/s into 5 000 slots = **2.36 s of buffer**, worst observed delivery delay 10.2 ms. Raising it is unnecessary |
| The `elapsed=` in `delivery took too long` is channel wait | **No.** `Deliver` runs Ed25519 verification on the stream goroutine *before* the channel send, so that figure is verification + send |
| `send_queue_dropped` means nodes lost messages | **No.** The client discards the batch before it reaches the wire ([`sendqueue.go`](../client/sendqueue.go)). It adds no node-side load, which is why over-driving did not distort the throughput ratios |
| `queue_*` is send-queue wait | **No.** It is pacer wait only — a flat ~23.8 ms, i.e. the 24 ms tick |
| n=7 stalls permanently under load | **Config, not n.** With `max_inflight_seq: 400` the leader deadlocks at `Cannot propose: … exceed the high watermark`, because the consensus-log window is `2 × CHECKPOINT_INTERVAL = 500` ([`consensuslog.go`](../node/consensuslog.go)) and only advances on a stable checkpoint. At the default 40: zero stalls in 15 runs. Still a real failure mode to avoid |
| Client-measured latency ratio ≈ consensus latency ratio | **Unreliable.** +50 % vs +87 % in one run |
| System-wide messages go 28 → 91 (3.25×) | **Wrong count.** It is 24 → 84 (3.5×); the leader receives no PrePrepare and does not send a Prepare |

---

## 7. Open weakness: hyperthreading undermines the CPU normalisation

The `NODE_GOMAXPROCS` comparison treated the box as having 40 cores. It has **20
physical cores with 2-way hyperthreading**, and a sibling is worth roughly 1.2–1.3× a
core, not 2×:

| Run | node threads | + client | Total | vs **20 physical** |
|---|---|---|---|---|
| n=4 @ GOMAXPROCS=3 | 12 | ~5 | 17 | **0.85× — fits, near-dedicated cores** |
| n=7 @ GOMAXPROCS=3 | 21 | ~5 | 26 | **1.30× — sibling sharing forced** |
| n=10 @ GOMAXPROCS=3 | 30 | ~5 | 35 | **1.75× — heavy sharing** |
| n=4 uncapped | 25 | ~5 | 30 | 1.49× |
| n=7 uncapped | 32 | ~5 | 37 | 1.86× |

n=4 ran nearly hyperthread-free while n=7 and n=10 did not, so **the budgets were not
equal in physical terms** and the bias runs against higher n. Two NUMA nodes add a
smaller bias in the same direction (more processes → more cross-socket traffic).

Therefore **84 % is an upper bound on the cost: the true equal-footing figure is
probably better than 84 %, not worse.** The 68 % for n=10 is the most contaminated
number here and should not be reported.

The clean experiment is two threads per node with a capped client: n=4 → 8 + 4 = 12
threads, n=7 → 14 + 4 = 18 threads, both under 20 physical cores so neither touches a
sibling (`config/ht2_n4.json`, `config/ht2_n7.json`, run with
`NODE_GOMAXPROCS=2 CLIENT_GOMAXPROCS=4`). **This has not been run.** n=10 cannot be done
hyperthread-free on this box at all, since `10 × 2 + client > 20`.

---

## 8. Changes made

### Fixed

`validateScenarioDeadNodes` in [`config.go`](../config/config.go) now requires
**exactly f** dead nodes for `NetworkDelayFCrash`, not `1..f`. A count below f is a
weaker scenario under the same name, so a `nodes_dead` set written for `node_num: 4` and
carried to 7 is now a config error instead of a quietly different experiment. Also added
an `f == 0` guard (the old `len(dead) == 0` check incidentally rejected `n < 4`;
exactly-f would have passed it silently) and `sort.Ints` for a stable error message,
since `NodesDead` is a map. 16 tests in
[`config/config_test.go`](../config/config_test.go).

Verified against the binary: n=7 with 1 dead rejected, 2 dead accepted, 3 dead rejected,
node 4 rejected; n=4 unchanged (1 accepted, 2 rejected). All 16 existing configs still
parse — the pre-existing ones have `scenario_mode: false` and never reach this path.

### Outstanding

**`client/receive.go` leader-update threshold — a real bug.** The client accepts a new
leader at `== 2*c.fNodes` matching reports. The correct threshold for "at least one
honest reporter" is `f+1`. At f=1 they coincide (both 2), which is why this has never
surfaced. At f=2 it demands 4 where 3 suffices.

`sendLeaderIdUpdate` has exactly one call site, in `HandleNewView`
([`view.go`](../node/view.go)) — the **replica** path. The incoming primary's own
install path, `newview()`, never calls it. So senders = live nodes − 1. Under
`NetworkDelayFCrash` at n=7 that is 5 − 1 = 4 against a threshold of 4: **zero slack.**
One slow replica and the client keeps addressing the old leader for a whole tenure. Fix
is `c.fNodes + 1`.

Lower priority:

- `SelectElection` in [`roundrobin.go`](../node/roundrobin.go) hardcodes
  `expectedLeader := 3`. Unreachable (its call site is commented out), but misleading.
- The per-sender ViewChange dedup in [`roundrobin.go`](../node/roundrobin.go) is
  commented out, so `uniqueViewChangeCount` can double-count a duplicate sender.
  `verifyNewView` does dedup by sender, so the blast radius is a rejected NewView rather
  than a safety break.
- `GenerateRemoteNetwork` in [`network.go`](../config/network.go) loops
  `i := 0; i < nodeNum`, so `NodeAddr` is keyed `0..n-1` while node ids are `1..n`; node
  n has no address. Broken at n=4 too; only `remote` mode is affected.
- The 1..8 node cap in [`netem.go`](../config/netem.go), `setup_netem` and
  `scripts/netem_scenario.sh` passes at 7 but blocks n ≥ 9 when netem is in use.

---

## 9. What this does not tell you about the learning agent

`generate_state` in [`simulate_quadrf.py`](../learningagent/simulate_quadrf.py) returns
hardcoded `(vc_rate, proposal_interval, inactive_nodes)` keyed only on
`(scenario, protocol)`. The live agent logs confirm it:

```
synthetic_state [0. 3.76 0.] synthetic_reward 258.00 (ignored node_reward 0.00 node_state [0. 0. 0.])
```

The agent never observes the system, so **convergence at n=7 will be identical to n=4 by
construction**, and `inactive_nodes` stays 0/1 when the truth at n=7 is 2. Until the real
`EpochAggregateMsg` payload replaces the placeholder zeros, running the bandit at 7 nodes
exercises the plumbing, not the learning.

---

## Appendix: reproducing

```bash
# 7-node functional checks
NO_ATTACH=1 ./alt_run_project.sh config/n7_basic.json          # healthy
NO_ATTACH=1 ./alt_run_project.sh config/n7_elec_fcrash.json    # election + 2 dead
NO_ATTACH=1 ./alt_run_project.sh config/n7_epoch_fcrash.json   # epoch + 2 dead
NO_ATTACH=1 ./alt_run_project.sh config/n7_agent_scenario.json # live agents + scenarios

# equal-CPU throughput comparison
NODE_GOMAXPROCS=3 ./run_experiments.sh 60 config/gmp3_n4.json config/gmp3_n7.json
NODE_GOMAXPROCS=4 ./run_experiments.sh 60 config/gmp4_n4.json config/gmp4_n7.json

# unsaturated latency comparison (the only valid one)
NODE_GOMAXPROCS=4 ./run_experiments.sh 60 config/g4sub_n4.json config/g4sub_n7.json

# per-message and per-transaction crypto cost
go test ./node -run XXX -bench "PerMessage|ClientTx" -benchtime 2s

# the hyperthread-free comparison (NOT YET RUN, see section 7)
NODE_GOMAXPROCS=2 CLIENT_GOMAXPROCS=4 ./run_experiments.sh 60 config/ht2_n4.json config/ht2_n7.json
```

Verify a CPU cap actually took effect:

```bash
for pid in $(pgrep -x pbft_main); do tr '\0' '\n' < /proc/$pid/environ | grep ^GOMAXPROCS=; done
```
