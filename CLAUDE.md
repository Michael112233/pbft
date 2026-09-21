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
sequence of fault scenarios (`Healthy`, `ProposalDelay`, `NetworkDelay`) and a
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

Planned scenario — **network delay combined with `f` crashed nodes** — expected to
converge to `PeriodicElection`, because it is the one action that answers both halves:
the 10 s period keeps the system making progress under delay (as above), and Election
skips the crashed nodes for free — candidacy requires broadcasting RequestVote after
the VDF race (`node/election.go`), which a crashed node never does, so it can never be
elected. RoundRobin has no such filter and burns a full timeout every time the rotation
lands on a dead node.

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

- Config path is hardcoded to `config/run2new.json` in `main.go`.
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

Constants in `node/triggerManager.go`; timer plumbing in `node/viewtimers.go`.
`timeoutForMode` gives Periodic 10 s and everything else 150 ms, and is used both by
the constructor and by `SwitchTriggerMode`, so a mode switch never leaves a stale
timeout behind.

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
  `0.90 * maxRecentThroughput` (`targetThroughputMaxFactor`), where the max is taken
  over a window of the last `3f+1` views (`maxRecentViewThroughput`).
- A 1 s timer (`perfTimerInterval`) samples throughput since the window opened.
  Above the bar → the bar is multiplied by `1.01` (`perfTimedTargetGrowth`) and the
  timer re-arms. At or below the bar → immediate view change.
- Because the bar starts at 90% and compounds 1% per second, it passes 100% of the
  achievable throughput after 11 raises. **Every leader, however healthy, is removed
  after about 12 s.** This is intended: it forces rotation and yields a throughput
  sample per node.
- The measurement window does not open at the NewView itself but
  `THROUGHPUTINTERVAL_DELAY` (3) slots later, so the post-view-change burst is not
  counted.

There is also an older sequence-driven variant in
`observeExecutedSlotForThroughput` that fires at `CHECKPOINT_INTERVAL` boundaries;
its view-change path is commented out in `node/execution.go` and only its logging and
`viewThroughputs` bookkeeping still matter.

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

Transport is gRPC bidirectional streams (`proto/pbft_transport.proto`,
`transportpb/`), one peer stream per target, 8 MiB flow-control windows. ViewChange
and NewView messages are large (1 MiB and several MiB with a few hundred prepared
certs) and dominate view-change latency; `node/newview_cost_test.go` is a standalone
harness for measuring that cost.

## Epoch → learning agent → generation switch

1. `epochTimerInterval` (45 s, `node/epochtimer.go`) fires; the node sends an
   `EpochDataMsg` to **node 4** (hardcoded aggregator).
2. Node 4 collects 2f+1 of them and broadcasts an `EpochAggregateMsg`
   (`epochManager.go`). Payload fields are still placeholder zeros.
3. Every node calls `SendLearningDataToAgent`, which either goes over gRPC to the
   Python agent (`learningagent/`, one process per node, ports 29000+) or, when
   `oracle_mode` is set, into `node/oracle.go` — a local stand-in that sleeps 100 ms
   and picks from `oracle_actions` either cyclically or seeded-randomly.
4. The decision arrives on `learningAgentDecisionCh`; `handleLearningAgentDecision`
   (`node/switch.go`) drops it unless its generation matches `forViewID.Generation`,
   then calls `incrementGeneration(action)` and enters a view change.
5. Nodes that have not yet switched catch up through the f+1 amplification rule in
   `HandleViewChangeRoundRobin`: f+1 ViewChanges for generation+1 pull a node into
   the new generation with the action carried in those messages.

Epoch timers only start when `epoch_mode` is true (`node/execution.go`, at seq 1).
With it off, the node stays in its initial action for the whole run.

## Config

`config/run2new.json`, parsed by `config/config.go`.

- `default_action` — the action every node starts in, e.g. `"FixedRoundRobin"`.
  `Config.InitialAction()` feeds both `currAction` and the trigger manager, so the
  two can never disagree at startup. An unknown name is a fatal config error.
- `oracle_mode`, `oracle_actions`, `oracle_random`, `oracle_seed` — replace the
  learning agent with the local oracle.
- `epoch_mode` — enable the epoch/generation machinery at all.
- `scenario_mode`, `scenarios`, `scenario_generations` (default 100) — cycle through
  fault scenarios (`Healthy`, `ProposalDelay`, `NetworkDelay`; `core/scenario.go`), one
  per `scenario_generations` generations, derived from the generation in
  `SetForViewID` → `maybeSwitchScenario` (`node/scenario.go`). A switch turns everything
  off, then enables the new fault: ProposalDelay turns on the 100 ms `tryPropose` sleep on
  `proposal_delay_node`; NetworkDelay has node 4 run
  `sudo -n scripts/netem_scenario.sh up 170 <n>` from a worker goroutine (`down` on
  every switch and on `Stop`). Requires `epoch_mode`; incompatible with `netem.enabled`
  and with the script's `netem_delay`.
- `leader_type` (`roundrobin` | `election` | `wrr`) — legacy `VCType`, separate from
  the per-action `Policy`; prefer `default_action`.
- `performance`, `performance_trigger`, `performance_timed_trigger` — throughput
  bookkeeping and the perf trigger.
- `peak_tps_test` — suppresses all timer-driven view changes; used to measure the
  ceiling.
- `netem` — event-driven delay injection via `cmd/netem-controller`.
- Others: `node_num`, `max_batch_size`, `inject_speed`, `parallel_workers`,
  `nodes_dead`, `gc`, `min_vdf_delay` / `max_vdf_delay` (election VDF range).
- `nodes_in_dark` is present in the JSON but has no field in `config.go`, so it is
  silently ignored.

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

## Gotchas

- **Hardcoded node ids.** Node 4 is the epoch aggregator (`epochtimer.go`); node 3 is
  forced to be the only election candidate to avoid split votes
  (`handleElectionVDFResult`); node 4 also owns the scenario-mode netem qdisc
  (`scenarioNetemNodeID`). All are experiment scaffolding, not protocol.
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
