# Epoch grid: aligning Adaptive and baseline runs in time

Long scenario runs switch scenario every 100 generations. Without the grid, an
Adaptive run falls behind its baselines by about one second per epoch, ~11 minutes by
epoch 700, because every learning-agent decision pushes all later epochs back
(measured in [`epoch-vs-time-alignment.md`](epoch-vs-time-alignment.md)). This doc
describes the fix (`timer.epoch_grid`), what is left over (δ), and how baselines are
configured so that they switch scenario at the same moments as Adaptive.

Code: [`node/epochtimer.go`](../node/epochtimer.go) (`armEpochTimer`,
`epochGridDelay`), config in [`config/timer.go`](../config/timer.go), oracle delay in
[`node/oracle.go`](../node/oracle.go), benchmark in
[`learningagent/bench_learner.py`](../learningagent/bench_learner.py).

---

## 1. Why runs drift without the grid

The epoch timer starts at seq 1. When it fires, the node sends its epoch data to node 4,
node 4 broadcasts the aggregate, each node asks its decider (agent or oracle), and when
the decision is applied `incrementGeneration` re-arms the timer **for a fresh epoch**.
So every generation lasts

```
epoch_timer_ms + δ
```

and each δ pushes every later generation back. Measured on the 4-node runs of 24 Sep
2026 (45 s epochs): δ ≈ 1.38 s for Adaptive and 0.44 s for the oracle baselines, so
Adaptive was 84 s behind at the first scenario switch and 680 s behind by epoch 700.

## 2. What δ is

δ is the time on one node **from its epoch timer firing to the decision being applied**
(`incrementGeneration`), i.e. to the start of the next generation and, at a boundary, of
the next scenario.

| Step | What happens | Adaptive | Baseline |
|---|---|---|---|
| 1 | timer fires; node sends `EpochDataMsg` to node 4 | same | same |
| 2 | node 4 waits for all n, or 2f+1 then the grace timer (`aware_grace_ms`, 500 ms); broadcasts `EpochAggregateMsg` | same | same |
| 3 | each node hands the aggregate to its decider | QuadRF over gRPC: fresh bootstrap fit + predict for every tried arm | oracle: sleep `oracle_decision_delay_ms`, return the locked action |
| 4 | decision arrives on `learningAgentDecisionCh` → `incrementGeneration` | same | same |

**δ_A** is δ in an Adaptive run, **δ_B** in a baseline run. Steps 1, 2 and 4 are the
same in both, so **δ_A − δ_B is the agent's compute time minus the oracle's sleep.**

Example, the switch from generation 100 into 101 at the end of a Healthy window
(anchor 0, 75 s epochs, grid on):

| | Adaptive | Baseline |
|---|---|---|
| timer fires | 7,500.000 | 7,500.000 |
| steps 1–2: all n data messages in, aggregate out | +0.01 | +0.01 |
| step 3: decision | QuadRF ≈ +1.0 | oracle +0.1 (default) |
| step 4: applied | +0.01 | +0.01 |
| **δ** | **≈ 1.0 s** | **≈ 0.1 s** |
| next scenario starts | 7,501.0 | 7,500.1 |

δ also depends on the scenario that is ending, but **equally in both runs**:

- ending NetworkDelay: the data message to node 4 and the aggregate back each cross the
  170 ms delay, ≈ +0.34 s to both;
- ending NetworkDelayFCrash: the f dead nodes never send, so node 4 always waits the
  500 ms grace, on top of the delay, ≈ +0.84 s to both.

The gap δ_A − δ_B stays at the agent's compute time whichever scenario is ending.
Setting `oracle_decision_delay_ms` to the agent's measured compute time (section 6)
shrinks it to the jitter of that compute time.

## 3. The grid

With `timer.epoch_grid: true`, the node records an anchor at seq 1 and generation g's
epoch timer fires at

```
anchor + g × epoch_timer_ms
```

whenever the previous decision was applied. (Precisely: `anchor + (g − g0 + 1) ×
epoch_timer_ms`, where g0 is the generation the node was in at seq 1, normally 1.)
`incrementGeneration` still re-arms the timer, but for that absolute deadline instead of
for a fresh period. The places that stop the timer early (the aggregate arriving before
a node's own timer fired) are unchanged: the next generation re-arms against the same
grid.

### 3.1 One node, first generations (Adaptive, δ = 1.4 s, 75 s epochs)

Without the grid:

| Event | Time | Next timer armed for |
|---|---|---|
| seq 1, gen 1 starts | 0 | 0 + 75 = 75.0 |
| gen 1 timer fires | 75.0 | |
| decision applied, gen 2 starts | 76.4 | 76.4 + 75 = 151.4 |
| decision applied, gen 3 starts | 152.8 | 152.8 + 75 = 227.8 |
| decision applied, gen 4 starts | 229.2 | … |

With the grid:

| Event | Time | Next timer armed for |
|---|---|---|
| seq 1, gen 1 starts | 0 | 0 + 1×75 = 75.0 |
| gen 1 timer fires | 75.0 | |
| decision applied, gen 2 starts | 76.4 | 0 + 2×75 = 150.0 (73.6 s away) |
| gen 2 timer fires | 150.0 | |
| decision applied, gen 3 starts | 151.4 | 0 + 3×75 = 225.0 |
| decision applied, gen 4 starts | 226.4 | … |

### 3.2 Generations still last 75 s

What gets shorter by δ is the span from the timer being armed to it firing, not the
generation. Consensus keeps running under generation g's action while g's own decision
is computed:

```
76.4            150.0       151.4
 |--- timer armed → fires ---|-- δ --|
 |        73.6 s             | 1.4 s |
 |<-------- gen 2 runs action 2: 75.0 s -------->|
```

| | Generation g lasts | Timer armed → fires |
|---|---|---|
| Without grid | 75 + δ_g | 75 |
| Grid | 75 − δ_(g−1) + δ_g | 75 − δ_(g−1) |

With a constant δ every generation is exactly 75 s; with jitter one generation may be
74.6 s and the next 75.4 s, and the average over a 100-generation window is exactly
75 s. Generation 1 has no earlier decision, so it runs from seq 1 to 75 + δ.

One consequence for later work: the epoch data for generation g covers the 75 − δ
seconds before its timer fired. While the agent's state and reward are synthetic this
does not matter. When the real aggregate replaces them, report rates (per second over
the window), not counts, so the ~1 s difference between runs cancels.

### 3.3 Scenario switches, Adaptive vs baseline (both with the grid)

Generations 1–100 are scenario 1, 101–200 scenario 2, … Generation 101 starts when
generation 100's decision is applied.

| Switch into gen | Without grid: Adaptive | Without grid: baseline | Gap | Grid: Adaptive | Grid: baseline | Gap |
|---|---|---|---|---|---|---|
| 101 | 100×76.4 = 7,640 | 100×75.4 = 7,540 | 100 s | 7,500 + 1.4 | 7,500 + 0.4 | 1 s |
| 201 | 15,280 | 15,080 | 200 s | 15,000 + 1.4 | 15,000 + 0.4 | 1 s |
| 601 | 45,840 | 45,240 | 600 s | 45,000 + 1.4 | 45,000 + 0.4 | 1 s |

Every scenario window lasts 7,500 s in both runs (± the jitter of δ at its two edges),
and the gap stays at one δ difference instead of growing.

### 3.4 All nodes at one boundary

Each node has its own anchor, its own seq 1; on one host these are milliseconds apart.

| Time | What happens |
|---|---|
| 0.000–0.005 | nodes execute seq 1; each records its anchor and arms anchor + 75 |
| 75.000–75.005 | each node's timer fires; `EpochDataMsg` to node 4 |
| ~75.01 | node 4 has all of them (or 2f+1 + grace); broadcasts the aggregate |
| ~75.0x | nodes whose timer had not fired stop it on the aggregate; all ask their decider |
| ~76.4 | decisions applied; each node arms **its own anchor + 2×75 ≈ 150.00x** |

All nodes fire again at ~150.00. Without the grid each node re-arms from its own
decision time, so their timers drift apart by the spread of their decision times.

### 3.5 A node that catches up

Node 6 is still in generation 5 when f+1 ViewChanges for generation 6 arrive at 376.0.

- Without the grid: amplification calls `incrementGeneration`, which arms 376.0 + 75 =
  451.0, while the others fire at ~450 — node 6 is now out of phase with them.
- Grid: it arms anchor + 6×75 = 450.0, the same as everyone else. A two-generation jump
  (5 → 7) arms anchor + 7×75 = 525.0.

### 3.6 A missed deadline

If a decision ever took longer than a whole epoch — say generation 9's decision is
applied at 9×75 + 80 = 755, after generation 10's deadline of 750 — the timer fires once,
immediately, and the node logs `EPOCH GRID: deadline for generation 10 missed by 5s`.
Generation 10 is then nearly empty and generation 11 is back on the grid at 825. At
δ ≈ 1 s this needs a 75× slowdown; it is a guard, not an expected case.

## 4. Fair baselines: native and locked

Two different questions, two different baselines:

| Question | Baseline | What it measures |
|---|---|---|
| **A. Does Adaptive beat the protocols that are actually run?** (headline) | **Native**: the action runs as plain PBFT would, no generation switch every epoch | Adaptive's whole benefit, **net of its own overhead** |
| **B. Are the learned decisions better than any one fixed decision?** (ablation) | **Locked**: identical to Adaptive, but the oracle always answers X | the value of the decisions alone, mechanics held equal |

A generation switch is a view change, and plain PBFT (FixedRoundRobin) does not do one
every 75 s. That overhead is the cost of being adaptive, so the headline comparison
charges it to Adaptive and not to the baselines; giving the baselines the same artificial
view change every epoch would handicap them. The locked baseline is still worth running:
native − locked, for the same action, is the overhead of generation switching on its own.

The runs in `results/old_multiscenario/` and the n=7 pilots in `config/pilots_n7/` are
locked baselines. That is the right choice for the pilots, which produce data for an agent
that itself runs with generation switches.

### 4.1 Native baselines: one generation per scenario

Scenario switching is tied to generations (`scenario_mode` requires `epoch_mode`), so the
native baseline keeps generations but makes each one a whole scenario:

```
Adaptive:  epoch_timer_ms 75000,    scenario_generations 100
           scenario k starts at anchor + 100k × 75 s + δ_A
Native:    epoch_timer_ms 7500000,  scenario_generations 1
           scenario k starts at anchor + k × 7500 s + δ_B
```

Both land on the same grid lines, one δ difference apart, with nothing to estimate:
without the grid the native epoch would have to be guessed as 100 × (75 + δ_A), and δ_A
varies with the model, n and the scenario. The native baseline does one generation switch
per scenario change (7 in a 7-scenario run), each at the moment the fault changes, when
Adaptive switches too.

The rule: **native `epoch_timer_ms` = Adaptive's `scenario_generations` × Adaptive's
`epoch_timer_ms`**, both runs with `epoch_grid: true` and the same `scenarios` list.

### 4.2 Config per run type

| Key | Adaptive | Native baseline | Locked baseline |
|---|---|---|---|
| `timer.epoch_grid` | true | true | true |
| `timer.epoch_timer_ms` | 75000 | 7500000 | 75000 |
| `scenario_generations` | 100 | 1 | 100 |
| `scenarios` | the schedule | same | same |
| `epoch_mode`, `scenario_mode` | true | true | true |
| `oracle_mode` | false | true | true |
| `oracle_actions`, `default_action` | — | the action | the action |
| `oracle_decision_delay_ms` | — | 100, or measured (section 6) | 100, or measured |

`default_action` of the Adaptive run is the action it starts in (gen 1 runs it before
the first decision).

## 5. Round-robin restarts at node 1 every generation

`incrementGeneration` resets the view counter to 1, and RoundRobin's leader is
`((counter − 1) mod n) + 1`, so every generation starts again at node 1. Under
PeriodicRoundRobin at n=7 with 75 s epochs a generation runs leaders 1, 2, …, 7 and node 1
again for half a tenure. With ProposalDelay on nodes 1 and 2 that is 2.5/7.5 ≈ 33% of the
time under a slow leader, against 2/7 ≈ 29% for an even rotation; under
NetworkDelayFCrash the rotation reaches dead node 3 early in every generation.

The native baseline has one generation per scenario, so it rotates evenly; Adaptive
restarts every 75 s. In the headline comparison this counts **against Adaptive**, which
is acceptable: it is how the current Adaptive design behaves. It is left as is and
reported as a known property, not changed.

## 6. Matching the oracle to the agent

`oracle_decision_delay_ms` (default 100, the old hardcoded value) is how long the oracle
sleeps. Setting it to the agent's decision compute time makes δ_B ≈ δ_A, so the remaining
gap at each switch is only jitter.

### 6.1 With the grid, matching is optional

With the grid on, the oracle's sleep only sets a **fixed offset** between the runs at each
scenario switch:

```
gap at every switch = δ_A − δ_B = agent's in-run decision time − oracle_decision_delay_ms
```

With the default 100 ms oracle:

| | Agent in-run | Gap per switch, grid on | Gap without grid |
|---|---|---|---|
| Sep 24 host (old runs) | 1.03–1.15 s | ≈ 0.93–1.05 s, the same at every switch | grows ~84 s per scenario, 680 s by epoch 700 |
| 9 Oct host (estimate) | ≈ 0.77 s | ≈ 0.67 s | ≈ 67 s per scenario |

- **It does not grow:** switch 1 and switch 7 are both ~1 s apart.
- **Windows are the same length:** each run's window starts δ after its grid line and ends
  δ after the next one, so both last 7,500 s (± jitter); only the start is shifted.
- **It is negligible:** 1 s of a 7,500 s window is 0.013%; in a Healthy window at
  ~8,000 req/s that second is ~8,000 commits of ~60 M.

So leaving the oracle at 100 ms is defensible; state in the paper that scenario edges agree
to within one decision time (~1 s). Matching the oracle shrinks that to the agent's jitter
(~0.1–0.2 s), which is cosmetic. The sleep only matters for runs **without** the grid,
where the gap builds up every epoch (analyse those as in
[`epoch-vs-time-alignment.md`](epoch-vs-time-alignment.md)).

### 6.2 Measuring the agent

The agent's time depends on the host, n (one agent per node, all deciding at once on the
same machine) and how much experience it has accumulated, so if you do match it, measure
it on each new machine:

```bash
python3 -m learningagent.bench_learner --config <adaptive run config>.json
```

It replays exactly the per-epoch work of `run_decision_worker_quadrf`
(`learningagent/server.py`) minus the gRPC hop, with one process per node started together
at each generation, over the run's whole scenario schedule, and prints the decision time
per scenario window and overall, plus the value to set (`--per-gen` also prints every
generation). The cost is about one random-forest fit per arm that already has data, so it
steps up as arms get tried (≈ 125 ms per arm on this host) and then grows slowly with the
experience in each bucket. The first generations are near zero because untried arms are
explored without fitting.

**The benchmark runs the agent alone, so it reads low.** In a real run the nodes and the
client share the cores with the agents; on this host the in-run decision time was about
**1.2×** the benchmark's (section 7). Two ways to set the oracle:

1. **Before any run on a new machine:** benchmark × 1.2 (the factor measured here; it may
   differ on other hardware).
2. **After the first Adaptive run on the machine (more accurate):** measure δ from its logs
   against a baseline run with the same schedule:

   ```bash
   python3 scripts/epoch_delta.py results/<adaptive run> results/<baseline run>
   ```

   The last column, δ_A − δ_B + the baseline's `oracle_decision_delay_ms`, is the agent's
   in-run decision time per scenario window; set the oracle to its median.

A 20% error in the matched value is ~0.1–0.2 s per switch, well inside 6.1's margin.

## 7. Checks

**Grid on vs off, real cluster** (9 Oct 2026, this host, n=4, FixedRoundRobin locked,
10 s epochs, `oracle_decision_delay_ms` 1500 to make δ obvious). Epoch timer fire times
relative to each node's seq 1, identical on all four nodes to 10 ms:

| | fires at (s) | decisions at (s) |
|---|---|---|
| grid on | 10.00 20.00 30.00 40.00 50.00 60.00 70.00 | 11.51 21.51 31.51 … 71.51 |
| grid off | 10.00 21.50 33.01 44.51 56.01 67.51 | 11.50 23.01 34.51 … 69.02 |

With the grid, the timers stay on the 10 s grid; without it, each 1.5 s decision pushes
every later epoch back, as in section 3.1.

**δ in the old 4-node runs** (`results/old_multiscenario/`, 24 Sep 2026, no grid, nodes
2–4, median per window): the oracle baseline's δ_B is 101 ms in windows without network
delay and 441 ms in NetworkDelay windows (100 ms sleep + 2 × 170 ms), as section 2
predicts; Adaptive's δ_A − δ_B + 100 ms, the agent's effective decision time, is
1.03–1.15 s. The NetworkDelayFCrash windows show no extra 500 ms because the aggregation
grace timer was only added on 27 Sep; with current code both run types pay it there.

**Benchmark vs a real run, same host** (9 Oct 2026, n=7, real QuadRF agents, Healthy,
10 s epochs, grid on, 21 decisions). The benchmark makes the same decisions as the run
(same seed), so the steps line up generation by generation; the run is ~1.2× slower at
every step:

| Generation | Arms fitted | Benchmark | Real run (fire → decision, median of 7 nodes) |
|---|---|---|---|
| 3 | 1 | 134 ms | 168 ms |
| 5 | 2 | 257 ms | 319 ms |
| 10 | 3 | 382 ms | 471 ms |
| 17 | 4 | 503 ms | 657 ms |
| 21 | 4 | 503 ms | 618 ms |

The real column also contains ~5 ms of aggregation (Healthy, no delay). Over the old run's
700-generation schedule (4 agents) the benchmark gives a median of 640 ms on this host
(633–707 ms per window), so ≈ 770 ms is the expected in-run value here.

## 8. What is left

- **δ jitter**: with the oracle matched, each switch differs between runs by the jitter
  of the agent's compute time (p10–p90 ≈ 0.45 s on the 4-node runs).
- **Start**: each run's grid starts at its own seq 1. The notebook already aligns runs at
  their start (the nodes enter gen 1 ~5.1 s before the client's elapsed 0, identically in
  every run), so this adds nothing.
- **Old runs**: runs without the grid (everything before this change, including
  `results/old_multiscenario/`) still drift; analyse them as described in
  [`epoch-vs-time-alignment.md`](epoch-vs-time-alignment.md).
