# Adaptive PBFT

A PBFT implementation used as a research testbed for **adaptive leader rotation**. The
protocol switches at runtime between *actions*, where an action is a pair of
independent planes:

```
Action = (TriggerMode, Policy)
         |             |
         |             +-- how the next leader is chosen  (RoundRobin | Election)
         +-- what removes the current leader              (Fixed | Perf | Periodic)
```

Both planes always switch **together**, once per generation. A learning agent (or a
local oracle) picks the next action at the end of each epoch; there is no path that
changes only one plane.

Defined in `core/message.go`: `Action`, `TriggerMode`, `Policy`, and the five named
combinations (`FixedRoundRobin`, `PeriodicRoundRobin`, `PeriodicElection`,
`PerformanceElection`, `PerformanceRoundRobin`) with `ActiontoString` /
`StringtoAction`.

## Scope

The research question is: **no single (trigger, policy) action is best in every
condition, so let the protocol learn which one to run.** The system is put through a
sequence of fault scenarios (`Healthy`, `ProposalDelay`, `NetworkDelay`,
`NetworkDelayFCrash`) and a
contextual multi-armed bandit picks, once per generation, which of the five actions to
switch to; the goal is convergence to the scenario's best action and fast
re-convergence when the scenario changes. The live agent is `QuadRF`
(`learningagent/server.py`): a CMAB with one `RandomForestRegressor` per
**(previous action, candidate action)** pair — so the context is the current state
*plus* a one-step dependency on the action just run — selected by Thompson sampling
(a fresh bootstrap resample of every candidate arm at predict time; untried pairs get
`+inf` to force exploration). CMAB is the current approach; other ML approaches are
expected to follow, which is why the agent sits behind a gRPC boundary and the
protocol side only ever receives an action name.

Expected convergence per scenario:

| Scenario | Expected action | Why |
|---|---|---|
| `Healthy` | `FixedRoundRobin` | nothing is wrong; cheapest rotation, leader removed only when it stalls |
| `ProposalDelay` | `PerformanceRoundRobin` | a slow-but-live leader passes the fixed timer, only the throughput bar catches it |
| `NetworkDelay` | `PeriodicRoundRobin` | the 150 ms timers expire faster than a view change completes, so the system cascades and no leader is ever installed; the 10 s period gives each leader time to make progress |

`NetworkDelayFCrash` — **network delay combined with `f` crashed nodes** — expected to
converge to `PeriodicElection`, because it is the one action that answers both halves:
the 10 s period keeps the system making progress under delay (as above), and Election
skips the crashed nodes for free — candidacy requires broadcasting RequestVote after
the VDF race (`node/election.go`), which a crashed node never does, so it can never be
elected. RoundRobin has no such filter and burns a full timeout every time the rotation
lands on a dead node. In experiments the race winner is not used: to avoid split votes
the candidate comes from a deterministic per-view formula (`electionCandidateForView`)
that picks randomly among the nodes except the crashed `nodes_dead`, standing in for
"only live nodes finish the race" (see Gotchas).

The agent currently decides on a **synthetic** state and reward; the real
`node_reward`/`node_state` arrive as zeros and are logged as `ignored`, because the
`EpochAggregateMsg` payload is still placeholder. Replacing the synthetic data with
the real aggregate is planned work.

## Build and run

```bash
go build -o pbft_main main.go          # nodes and client share one binary
./pbft_main -r node -m loopbackip -n 1 # role, network mode, node id
./pbft_main -r client -m loopbackip

./alt_run_project.sh                   # full experiment: keys, build, tmux, netem

go test ./node -count=1                # some learning-agent tests are currently broken
```

- Config path is not hardcoded to `config/run2new.json` it uses --config
- `main.go` → `controller/controller.go` → `node.NewNode` / `client`.
- `setup_crypto/crypto_main.go` regenerates `keys/*.pem`; the run scripts do this
  every time, so keys always show as modified in git.
- Network modes (`config/network.go`): `local` (127.0.0.1, ports 28000+100i),
  `loopbackip` (127.0.0.<i+1>, what the scripts use, so netem can filter per node),
  `remote`.
- Logs go to `logs/node_<id>.log`, `logs/client.log`, plus `_test`, `_feature`,
  `_mem` variants. Scripts wipe `logs/` on start.

## ViewID: generation and counter

`core.ViewID{Generation, Counter}` — every view is a pair, not a single integer.

- **Counter** increments on an ordinary view change (leader removed by the current
  trigger). The action does not change. `incrementCounter()` in `node/view.go`.
- **Generation** increments only on an action switch, and resets Counter to 1.
  `incrementGeneration(action)` sets the new action, calls `SwitchTriggerMode`, and
  resets the epoch timer.

A node tracks two of these: `viewID` (installed) and `forViewID` (the view it is
currently trying to reach). They differ while a view change is running.

## Triggers

Mode logic in `node/triggerManager.go`; timer plumbing in `node/viewtimers.go`. The
timeouts come from the config's `timer` block (`config/timer.go`):
`periodic_trigger_timeout_ms` (default 10000), `fixed_trigger_timeout_ms` (default 150,
also the Perf floor), and `relaxed_fixed_trigger_timeout_ms` (default 300, parsed but not
read by the protocol yet). `timeoutForMode` picks Periodic's or the fixed one, and is
used both by the constructor and by `SwitchTriggerMode`, so a mode switch never leaves a
stale timeout behind. The values below are the defaults.

| Mode | Progress / new-view timeout | Reset on execution | Effect |
|---|---|---|---|
| `FixedTrigger` | 150 ms | every executed slot | leader removed only when it stalls |
| `PerfTrigger` | 150 ms (floor) | every executed slot | floor + throughput bar on top |
| `PeriodicTrigger` | 10 s | never (only at seq 1) | leader removed every 10 s, unconditionally |

Two timers share the timeout value:

- **leader progress timer** — armed on a replica when it accepts a NewView
  (`acceptNewViewTimers`). Expiry means "the leader is not making progress" and
  starts a view change. **The leader never arms this on itself**
  (`acceptNewViewTimersLeader`, `node/view.go`), so a leader is only ever removed by
  the replicas.
- **new-view timer** — armed when a node collects 2f+1 ViewChange messages
  (`SelectRoundRobin` / `ElectionLogic`). Expiry means the incoming primary failed to
  deliver a valid NewView, and moves to the next view.

`ResetOnExecution` (`node/triggerManager.go`) is the only reset path, and it skips the
leader.

### Perf trigger

`node/perftimer.go`, state in `node/throughputperformance.go`. The 150 ms progress
timer stays armed underneath as a floor; on top of it:

- On every NewView, `resetTimedPerfWindow` sets the bar to
  `bar_factor * maxRecentThroughput` (`performance.bar_factor`, default 0.91), where
  the max is over the view records of the last `3f+1` views that have one
  (`maxRecentViewThroughput`). With no record in that window the max is
  `performance.default_max_throughput` (160) and the logs say `source=default` /
  `PERF BAR DEFAULT`.
- A 1 s timer (`perfTimerInterval`) samples throughput since the window opened.
  Above the bar → the bar is multiplied by `1.01` (`perfTimedTargetGrowth`) and the
  timer re-arms. At or below the bar → immediate view change.
- Because the bar starts at ~91% and compounds 1% per second, it passes 100% of the
  achievable throughput after about 10 raises. **Every leader, however healthy, is
  removed after about 11 s.** This is intended: it forces rotation and yields a
  throughput sample per node.
- The window opens `performance.window_delay_slots` (3) slots after the NewView's maxSeq.
  That does not skip the post-view-change burst (~20 slots in the first ~100 ms).

**View records** (`observeExecutedSlotForViewThroughput`, `node/throughputperformance.go`),
Aardvark-style: the anchor is the first slot executed `performance.grace_ms` (1 s)
after the window opens, so the burst is never measured. From the anchor, every
`performance.interval_slots` (250) executed slots close an interval; only complete
intervals count, so an unfinished (e.g. throttled) tail never does. The view's
record is the fastest complete interval (`view_strategy: "max"`, default) or total
slots over total time of the complete intervals (`"mean"`). A view with no complete
interval (one that ends less than ~2.7 s after install at 160 slots/s: ~0.17 s to the window open, the 1 s grace, then 250 slots) has no record. Nothing executes during
a view change, so a record only covers its own view. `PERF RECORD:` log lines show
each interval.

## Leader policies

Selected in `maybeHandleViewChangeQuorum` (`node/roundrobin.go`) once 2f+1
ViewChanges for a view are in.

- **RoundRobin** (`SelectRoundRobin`) — `primaryForView(counter)` is
  `((counter-1) % nodeNum) + 1`. The expected primary builds and broadcasts the
  NewView; everyone else just arms the new-view timer.
- **Election** (`node/election.go`) — each node derives a VRF proof from the view
  seed, converts `beta` into a VDF delay in `[min_vdf_delay, max_vdf_delay]`, and
  evaluates the VDF on a worker goroutine. The first to finish broadcasts
  RequestVote; others verify the VRF/VDF and grant. The VDF is the leader-election
  lottery: a shorter delay means an earlier candidacy.

## Node architecture

`node/node.go` builds the `Node`; `node/eventloop.go` runs it.

**Single-goroutine event loop.** `run()` is the only writer of node state
(`viewID`, `forViewID`, `currAction`, `consensusLog`, timers, ...). Everything else —
the gRPC hub, VDF workers, the learning-agent client, the oracle — hands work in over
a channel and never touches node fields. All timer operations are loop-owned, so
none of them lock. Keep it that way when adding state.

Channels feeding the loop: `receiveVerifiedClientRequestCh`, `consensusMsgChan`,
`viewChangeMsgChan`, `newViewMsgChan`, `checkpointMsgChan`, `electionMsgChan`,
`epochMsgChan`, `learningAgentDecisionCh`, `electionVDFResultCh`, plus the four timer
channels (progress, new-view, perf, epoch).

Sub-managers, each with a narrow interface onto `Node`:

| File | Responsibility |
|---|---|
| `triggerManager.go` | trigger mode + the two timeout values |
| `epochManager.go` | epoch data aggregation, hands off to the learning agent |
| `electionManager.go` (in `election.go`) | votes, VRF/VDF election state |
| `checkpoint.go` | checkpoints, stable-checkpoint proof, GC |
| `nodeMessageHub.go` | gRPC transport, envelope build/parse, signatures |

**Intake filter** (`node/intake.go`). Before `Enqueue`, the leader drops a client
request whose (`ClientName`, `Id`) is already queued or proposed in this view (the
set is cleared with the pending queue on every new-view install, then seeded with
the O-set) or already executed (per-client watermark + sparse set, updated in
`exeLoop`). It is leader-local and not checkpointed, so execution stays
at-least-once: a node that jumps to a checkpoint (`fastPathStablizeCheckpointviaVC`)
has no record of the skipped requests. `exeLoop` logs `INTAKE:` counts (retries
dropped, duplicate executions) at each local checkpoint. `Pool.executed` is not a
dedup record: GC drops it and checkpoint transfer does not carry it.

Transport is gRPC bidirectional streams (`proto/pbft_transport.proto`,
`transportpb/`), one peer stream per target, 8 MiB flow-control windows. ViewChange
and NewView messages are large (1 MiB and several MiB with a few hundred prepared
certs) and dominate view-change latency; `node/newview_cost_test.go` is a standalone
harness for measuring that cost.

## Epoch → learning agent → generation switch

1. The epoch timer (`timer.epoch_timer_ms`, default 45 s, `node/epochtimer.go`) fires; the node sends an
   `EpochDataMsg` to **node 4** (hardcoded aggregator).
2. Node 4 collects 2f+1 of them and broadcasts an `EpochAggregateMsg`
   (`epochManager.go`). Payload fields are still placeholder zeros.
3. Every node calls `SendLearningDataToAgent`, which either goes over gRPC to the
   Python agent (`learningagent/`, one process per node, ports 29000+) or, when
   `oracle_mode` is set, into `node/oracle.go` — a local stand-in that sleeps
   `oracle_decision_delay_ms` (default 100; measure the agent with
   `learningagent/bench_learner.py` and match it) and picks from `oracle_actions` either
   cyclically or seeded-randomly.
4. The decision arrives on `learningAgentDecisionCh`; `handleLearningAgentDecision`
   (`node/switch.go`) drops it unless its generation matches `forViewID.Generation`,
   then calls `incrementGeneration(action)` and enters a view change.
5. Nodes that have not yet switched catch up through the f+1 amplification rule in
   `HandleViewChangeRoundRobin`: f+1 ViewChanges for generation+1 pull a node into
   the new generation with the action carried in those messages.
6. **Every generation reaches the decider exactly once, in order** — the agent rejects
   everything after a gap in sequence ids. A node that catches up before its aggregate
   arrives drops that aggregate as stale, so `incrementGeneration` calls
   `fillDeciderGap` (`node/decidersync.go`): if the generation being left was never
   sent (`deciderSentGen`), it sends placeholder data for it (the agent ignores the
   node's numbers). The agent's decision for it arrives late and is dropped; if it
   differs from the action the node caught up into, `DECIDER SYNC: … agents have
   diverged` is logged. NewView jumps of more than one generation are only logged.

Epoch timers only start when `epoch_mode` is true (`node/execution.go`, at seq 1).
With it off, the node stays in its initial action for the whole run.

**Epoch grid** (`timer.epoch_grid`, default false). Off, `incrementGeneration` re-arms
the epoch timer for a fresh `epoch_timer_ms`, so every generation lasts epoch + δ (δ =
timer fire → decision applied) and runs with different deciders drift apart (Adaptive
~11 min behind the oracle baselines by epoch 700). On, generation g's timer fires at
anchor + g × `epoch_timer_ms` (anchor = the node's seq 1), so generations average
exactly one epoch and scenario switches line up across runs to within δ_A − δ_B.
Native baselines use the grid with `scenario_generations: 1` and `epoch_timer_ms` =
Adaptive's `scenario_generations` × epoch, one generation per scenario. See
`docs/epoch-grid.md`; older runs without it: `docs/epoch-vs-time-alignment.md`.

## Config

`config/run2new.json`, parsed by `config/config.go`.

**`config/run2new.json` is the base config: every new config param must be added to it**
(with its default value written out) in the same change that adds the field to
`config.go` or a `config/*.go` file. New experiment configs are generated from it, so a
key missing there is missing from every experiment built afterwards.

- `default_action` — the action every node starts in, e.g. `"FixedRoundRobin"`.
  `Config.InitialAction()` feeds both `currAction` and the trigger manager, so the
  two can never disagree at startup. An unknown name is a fatal config error.
- `oracle_mode`, `oracle_actions`, `oracle_random`, `oracle_seed`,
  `oracle_decision_delay_ms` — replace the learning agent with the local oracle.
- `epoch_mode` — enable the epoch/generation machinery at all.
- `scenario_mode`, `scenarios`, `scenario_generations` (default 100) — cycle through
  fault scenarios (`Healthy`, `ProposalDelay`, `NetworkDelay`, `NetworkDelayFCrash`;
  `core/scenario.go`), one
  per `scenario_generations` generations, derived from the generation in
  `SetForViewID` → `maybeSwitchScenario` (`node/scenario.go`). A switch turns everything
  off, then enables the new fault: ProposalDelay turns on the 100 ms `tryPropose` sleep on
  every `proposal_delay_nodes` node (a map like `nodes_dead`, **exactly f** of them, node 4
  allowed since a slow leader is still live — `validateScenarioProposalDelayNodes`);
  NetworkDelay has node 4 run
  `sudo -n scripts/netem_scenario.sh up 170 <n>` from a worker goroutine (`down` on
  every switch and on `Stop`); NetworkDelayFCrash applies the same delay and sets `dead`
  on every `nodes_dead` node (**exactly f** of them, never node 4 — validated in
  `validateScenarioDeadNodes` in `config.go`, tested in `config/config_test.go`; both
  validators share `exactlyFNodes`). Each set is checked only if its scenario is in
  `scenarios`. Exactly f, not 1..f: a count below f is a weaker scenario under the same
  name, so a set written for `node_num: 4` and carried over to 7 is a config error
  rather than a quietly different experiment. The forbidden id is
  `config.EpochAggregatorNodeID`, which `epochAggregatorNodeID` and
  `scenarioNetemNodeID` both alias, so the validation cannot drift from the node that
  actually aggregates. In scenario mode the scenario owns both `proposalDelay` and `dead`, so
  `proposal_delay_nodes` and `nodes_dead` only take effect inside their scenario.
  The old int key `proposal_delay_node` is gone and silently ignored if present.
  Requires `epoch_mode`; incompatible with `netem.enabled` and with the script's
  `netem_delay`.
- `leader_type` (`roundrobin` | `election` | `wrr`) — legacy `VCType`, separate from
  the per-action `Policy`; prefer `default_action`.
- `performance` — `true`/`false` (legacy) or an object: `enabled`, `view_strategy`
  (`max` | `mean`), `window_delay_slots`, `interval_slots`, `grace_ms`, `bar_factor`,
  `default_max_throughput` (`config/performance.go`). Enables throughput
  measurement and the bar; the perf trigger itself only acts in a Perf-mode action.
  `performance_trigger` / `performance_timed_trigger` are gone (ignored if present).
- `peak_tps_test` — suppresses all timer-driven view changes; used to measure the
  ceiling.
- `netem` — event-driven delay injection via `cmd/netem-controller`.
- `carry_state` — when false, VC certs and the NewView O-set carry no request bodies
  (`ActualMsg`); bodies come only from the pool, filled in `HandlePrePrepare` for
  every PrePrepare received. `some req for batch not found` in a node log should
  never appear then; if it does, a node is missing a request body for a seq it has
  to execute.
- Others: `node_num`, `max_batch_size`, `inject_speed`, `parallel_workers`,
  `nodes_dead`, `gc`, `min_vdf_delay` / `max_vdf_delay` (election VDF range).
- `nodes_in_dark` is present in the JSON but has no field in `config.go`, so it is
  silently ignored.
- `max_batch_delay` is parsed (`MaxBatchDelay`) but has no effect: the batch timer
  in `node/batcher.go` is armed and immediately stopped in `node.go`, and nothing
  reads its channel. A batch is proposed only once `pendingRequests` holds
  `max_batch_size` requests, so batching wait is the fill time `B/R` and is
  unbounded at low load.
- The client paces request batches (`client/ratelimiter.go`, one batch of
  `inject_speed` per 24 ms) into **per-node send queues** (`client/sendqueue.go`, 8
  batches each); a sender goroutine per node does the blocking `stream.Send`. A full
  queue drops the batch (still registered, so it counts as uncommitted and is retried
  if `client_retry` is on); a leader change discards the old leader's queue. Counts go
  to `send_queue_dropped` / `send_queue_discarded` in `latencyreport.json` and to the
  send-rate CSV. Before this, a leader that stopped reading its stream (ProposalDelay:
  the 100 ms sleep runs on node 1's event loop) froze the whole client once the 8 MiB
  gRPC window filled, starving the next leader for ~300 ms.
- `proposal_delay_ms` is parsed but unused; the proposal delay is a hardcoded 100 ms
  sleep in `tryPropose`.

## Experiment tooling

- `alt_run_project.sh` — the main harness. Sets up a netem `prio`+`netem` qdisc on
  `lo` with flower filters for every node pair, then runs a delay schedule (the
  active one raises delay for 30 s). `setup_netem` and `start_netem_schedule` at the
  bottom are commented in and out per experiment. Delay transitions are logged with
  timestamps to `logs/netem_schedule.log` — check it against node logs when
  correlating.
- `docs/` holds the written-up analyses (view-change storms, NewView cost, netem
  behaviour, timer comparisons, election robustness). Read the relevant one before
  re-deriving an experiment result.
- `plot_tps_series.ipynb`, `scripts/analyze_freq_trace.py` for plots.
- `scripts/epoch_delta.py <run> [<baseline run>]` — δ (epoch timer fired → decision
  applied) per scenario window from a run's logs; with a baseline, the agent's in-run
  decision time to put in `oracle_decision_delay_ms`. `python3 -m
  learningagent.bench_learner` measures the agent alone before any run (reads ~1.2× low).

## Gotchas

- **Hardcoded node ids.** Node 4 is the epoch aggregator, defined once as
  `config.EpochAggregatorNodeID` and aliased by `epochAggregatorNodeID`
  (`epochtimer.go`) and `scenarioNetemNodeID` (`scenario.go`); only one
  node per view may stand as election candidate, to avoid split votes — picked
  uniformly at random by hashing the view from 1..n **minus the ids set in
  `nodes_dead`** (`electionCandidateForView(view, nodeNum, excluded)`, checked in
  `handleElectionVDFResult`), since a crashed candidate never broadcasts
  RequestVote. The exclusion applies only while those nodes are crashed
  (`electionExcludedForGeneration`): in scenario mode only in generations whose
  `scenarioForGeneration` is `NetworkDelayFCrash`, outside scenario mode always.
  With no crashed nodes the draw is uniform over all n, so Election's
  candidate pool is the same size as RoundRobin's rotation. The aggregator also
  owns the scenario-mode netem qdisc, which is why `NetworkDelayFCrash` refuses to
  crash it. All are experiment scaffolding, not protocol.
- **Node 1 leads the genesis view and no leader update is sent for it.** The
  genesis view is `(1,1)` and `primaryForView(1) == 1`, reached with no view
  change. `sendLeaderIdUpdate` has a single call site, in the NewView install path
  (`node/view.go`), so the client gets **no** `LeaderIdUpdate` for the genesis
  tenure — and it initialises `currentView = {1,1}` and drops any update
  `LessThanOrEqual` to that anyway (`client/receive.go`). Consequences: the first
  row of `logs/leader_timeline.jsonl` is the leader of view `(1,2)`, not node 1;
  anything client-side keyed on leader notices (the throttle controller in
  `client/throttlemanager.go`) is blind for the whole genesis tenure and then
  reacts late to the *second* leader, so the first two tenures of a run are
  startup transients — see the `--skip-tenures` note in
  `scripts/analyze_throttle.py`.
- `viewtimers.go` still defines unused `leaderProgressTimeout` / `newViewTimeout`
  constants; the live values come from `TriggerManager`.
- A lot of superseded logic is commented out rather than deleted. Check whether a
  path is actually reachable before reasoning about it.
- Envelope signature verification is live on every receive path in
  `nodeMessageHub.go` **except NewView**, where it is commented out (line ~520).
- Several `node` tests construct `&Node{}` directly and panic on a nil logger or nil
  trigger manager; they are stale, not regressions.
- Timestamps in node logs are the only cross-node clock, and all nodes run on one
  machine, so they are directly comparable. Hub logs report `elapsed=` for delivery
  and `took` for sends — subtract them from the log time to get the true start.
