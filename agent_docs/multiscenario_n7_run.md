# n=7 multi-scenario run: Adaptive vs 5 baselines (~18.75 h each)

Context for analysing the results of six long runs. Read the repo's `CLAUDE.md` first for
protocol background (actions, triggers, ViewID, epochs). This file covers what this
particular experiment is, what to expect, and where the data and pitfalls are.

## Question

Does the adaptive agent (QuadRF bandit choosing one action per 75 s epoch) commit at least
**10% more requests** over the whole schedule than the **best single-action baseline**?
The secondary question is whether each scenario's expected best action actually wins, and
whether the agent converges to it.

## Runs

Six runs, one per machine (CloudLab, 125 GiB each), all n=7 (f=2), same scenario schedule.
Configs in `config/multiscenario_n7/`. `run_long_experiment.sh <duration> <config>` writes
each run to `results/new_multiscenario/<config name>/`.

| Run | Config | Action | Generation length |
|---|---|---|---|
| Adaptive | `adaptive.json` | QuadRF picks among all 5, starts in FRR | 75 s (`epoch_timer_ms` 75000, `scenario_generations` 100) |
| FRR | `baseline_frr_locked.json` | FixedRoundRobin (oracle pinned) | 75 s, like Adaptive: RR resets to node 1 every generation, like plain PBFT restarting |
| PRR | `baseline_prr_native.json` | PeriodicRoundRobin | One generation per window (`epoch_timer_ms` 7500000, `scenario_generations` 1) |
| PE | `baseline_pe_native.json` | PeriodicElection | One generation per window |
| PFRR | `baseline_pfrr_native.json` | PerformanceRoundRobin | One generation per window |
| PFE | `baseline_pfe_native.json` | PerformanceElection | One generation per window |

Common settings: `inject_speed` 200 (offered ~8,333 req/s), `max_batch_size` 50,
`client_retry` false, `latency_probe` false, `epoch_grid` true (window boundaries line up
across runs to within about 1 s), timers: periodic 10 s, fixed 150 ms in ND/NDF windows and
350 ms elsewhere.

## Schedule: 9 windows × 7,500 s = 18 h 45 min

| # | Window | Fault |
|---|---|---|
| 1, 5 | ProposalDelay | 100 ms sleep before every proposal on nodes **1, 2** (slow but live leaders) |
| 2, 6 | NetworkDelay | 170 ms one-way delay on every link (netem, run by node 4) |
| 3, 7 | NetworkDelayFCrash | 170 ms delay **plus** nodes **3, 5** dead |
| 4, 8 | Throttle | Client-side adversary gates the leader to 1 proposal per 100 ms; k=2 slots, `prep_ms` 3000. Pre-aims at the RR successor; only reacts after install under Election |
| 9 | Healthy | No fault |

Window k (0-based) covers generations `k×100+1 … (k+1)×100` for Adaptive and FRR, or
generation k+1 for the natives. In node logs the switch is
`scenario switch gen=G <old> -> <new> ...`, and every generation logs
`TIMER: gen G scenario S mode M fixed floor X`.

## Expected results (from the n=7 pilots)

Per-scenario throughput (req/s), pilot means (`results/all_new_scenario_pilots/`, 75 s
generations; Throttle PFE from the 30-min run):

| Scenario | FRR | PRR | PE | PFRR | PFE | Expected best |
|---|---|---|---|---|---|---|
| ProposalDelay | 485 | 5,544 | 6,167 | 6,969 | **7,539** | PFE (Perf trigger) |
| NetworkDelay | 0 | **3,514** | 3,483 | 0 | 0 | PRR ≈ PE (0.9% apart) |
| NetworkDelayFCrash | 0 | 2,563 | **3,440** | 0 | 0 | PE (Election skips dead nodes) |
| Throttle | 545 | 488 | 2,094 | 4,949 | **5,322** | PFE (Election is unpredictable) |
| Healthy | **8,127** | 7,973 | 7,911 | 7,978 | 7,913 | FRR (1.9%, near tie) |

Projected totals over the schedule (pilot means × 7,500 s per window; Adaptive from
`python -m learningagent.simulate_quadrf_n7 --seed 5 --span 100 --order pdelay,ndelay,ndfcrash,throttle,pdelay,ndelay,ndfcrash,throttle,healthy`):

| Run | Projected total | Adaptive's lead |
|---|---|---|
| Oracle (best action every window, no exploration) | 358.2 M | — |
| **Adaptive** | **339.5 M** | — |
| PE | 287.1 M | **+18.3%** (the binding baseline) |
| PFE | 252.3 M | +34.6% |
| PRR | 241.4 M | +40.6% |
| PFRR | 238.6 M | +42.3% |
| FRR | 76.4 M | +344% |

Natives run one generation per window, the pilots ran 75 s generations, so natives may do
slightly better than projected (RR no longer restarts at node 1 every 75 s).

## The agent uses synthetic data

Adaptive's decisions do **not** depend on what happens in the run. The epoch aggregate's
state and reward are placeholder zeros (logged as `ignored`); `learningagent/server.py`
draws state and reward from the pilot data (`learningagent/simulate_quadrf_n7.py`,
`SYNTHETIC_RNG_SEED` 6, one rng per node agent). So:

- Adaptive's action sequence should match the simulation command above, epoch for epoch.
  Check that first: a mismatch means an agent bug or a skipped/duplicated generation.
- If the real system behaves differently from the pilots, the agent will not notice. Compare
  each window's real per-action throughput against the pilot table.

## Data in each results directory

| File | Use |
|---|---|
| `report.json` | Client TPS series every 100 ms: `timestamp_unix_nano`, `committed_total`. Primary throughput source |
| `latencyreport.json` | Client latency summary, `uncommitted`, `dropped_unsent`, `send_queue_dropped` / `send_queue_discarded` |
| `node_<i>.log` | Scenario switches, TIMER lines, view changes, decisions (`received learning-agent decision for generation G: <action>`), `Stable checkpoint advanced ... seq=` |
| `node_<i>_mem.log`, `client_mem.log` | Memory over time |
| `leader_timeline.jsonl` | One row per leader notice seen by the client: gen, counter, leader, time |
| `learning-agent-node-<i>.log` | Agent decisions per generation (Adaptive only) |
| `long_run.out` (repo root of that machine) | Launch, stop and export progress |

Use node 4 as the reference node: it is never dead, never proposal-delayed, and is the
epoch aggregator.

Existing helpers: `scripts/scenario_pilot_summary.py` (per-generation tput, VC rate; written
for one-action pilots, adapt the window cutting), `scripts/epoch_delta.py` (epoch timer
fired → decision applied), `scripts/perf_tick_misses.py` (Perf trigger behaviour),
`scripts/analyze_throttle.py`.

## Known issues and pitfalls

- **A client may die before export.** In the previous attempt the PFRR client died and
  `report.json` was missing. Throughput can then be rebuilt from node 4's
  `Stable checkpoint advanced ... seq=` lines (seq × 50 requests per slot); this matched the
  client to within 0–4% where both existed.
- **Previous attempt ran out of memory** at ~15.7 h (before the Healthy window). Fixed since:
  client keeps no request bodies without retry (`SetKeepRequestBody`), forgets batches never
  sent (`ForgetUnsent`, counted as `dropped_unsent`), node intake filter records no executed
  ids without retry. **Still open:** during view-change cascades (ND/NDF windows under Fixed
  or Perf) the request pool grows ~1.8 GB/h per node and is freed at the next stable
  checkpoint. Projected peak 65–100 GB per machine. Check the `_mem` logs.
- **PFE run (`baseline_pfe_native_20261010_175337`) has valid data for window 1 only**
  (ProposalDelay, 57.26 M committed, ~7,630 req/s); it committed nothing after 19:58:44.
  In NetworkDelay, Election never installed a view, so the ViewChange log (pruned only on
  install) kept every 0.49 MiB ViewChange: ~33 GB/h per node. At 20:29 node 1 died (most
  likely OOM-killed: 7 nodes ≈ 126 GiB). The next election candidate, for view (2,2864),
  was node 1, and Election arms the new-view timer only on the candidate or a voter, so
  the cluster froze. In gen 3 (NetworkDelayFCrash) 3 nodes were down (> f), so the epoch
  aggregate never reached quorum and no later generation started. FRR/PFRR were unaffected:
  the RoundRobin primary installs locally every n views, which prunes. Fixed afterwards by
  pruning per-view state below `forViewID` (`pruneBelowForView`); verified in
  `results/verify_vcprune_pfe_20261011_145919` (flat heap over 1,093 view changes). The
  Election liveness gap (dead candidate outside `nodes_dead` → no timer) is still open.
- **Startup transient:** node 1 leads the genesis view and the client gets no leader notice
  for it, so the first two tenures of a run are not representative (throttle controller is
  blind to them). Only matters if a run starts in Throttle; this one starts in ProposalDelay.
- **Perf bar trap (PFE in Throttle):** an earlier pilot got stuck with the Perf bar at the
  default 145.6 (`PERF BAR DEFAULT` in logs), evicting fresh leaders at the first 1 s tick,
  for ~4.5 min. Attributed to a slow host. Look for long runs of `PERF BAR DEFAULT` in the
  Throttle windows of PFE and Adaptive.
- **Election draws are deterministic:** candidate = sha256("view-g-c"), no run seed, so every
  run with the same generations sees the same leader sequence. Run-to-run Election variance
  is not sampled.
- **Agent sync:** `DECIDER SYNC: ... agents have diverged` in a node log means per-node agents
  disagreed. Should not happen; report any.
- **Election and dead nodes:** the dead-node exclusion is experiment scaffolding (the
  candidate formula skips `nodes_dead` in NetworkDelayFCrash generations), standing in for
  "only live nodes finish the VDF race".

## What to produce

1. Per run, per window: committed requests and mean req/s. Table runs × windows, and the
   per-window winner against the "Expected best" column.
2. Totals per run and Adaptive's lead over each baseline; flag whether it clears 10% over the
   best baseline (expected: PE).
3. A cumulative committed-requests plot, all six runs, window boundaries marked.
4. Adaptive's chosen actions per window: share of epochs on the expected best action, first
   visit vs second visit of each scenario, and epochs to convergence. Compare to the
   simulation's choices.
5. Real vs pilot: per window, the run's throughput against the pilot mean for that action.
   Flag gaps above ~5%.
6. Anomalies: memory peaks, client drops (`send_queue_dropped`, `dropped_unsent`), long VC
   cascades outside ND/NDF, `DECIDER SYNC` lines, any run that ended early.
