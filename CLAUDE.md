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
`EpochAggregateMsg` payload is still placeholder. The synthetic data comes from
`learningagent/simulate_quadrf_n7.py`: per-generation (throughput, VC rate) measured in
the n=7 scenario × action pilots, resampled with a per-worker rng
(`SYNTHETIC_RNG_SEED`), so a live run makes the same decisions as
`python -m learningagent.simulate_quadrf_n7 --seed 5` on the same scenario schedule.
Replacing the synthetic data with the real aggregate is planned work.

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
  `loopbackip` (127.0.0.<i+1>, what the scripts use, so netem can filter per node;
  client i of several at 127.0.1.<i+1>, see `client_num`), `remote` (one node per
  machine: IPs from a hosts file passed as `--hosts`, same ports as loopbackip, agent on
  its node's IP; `config.GenerateRemoteNetwork` rejects missing/duplicate IPs and a
  client on a node's IP). An unknown `-m` and a node id outside 1..n are fatal, and a
  node that cannot bind its address fails `Start` instead of running on unreachable.
  Every node dials all its peers at startup and logs `PEER READY` per peer and one
  `PEERS READY` line (`node/peerdial.go`), in every mode; `scripts/cluster/start.sh`
  waits for that line before starting the client. Cluster harness and caveats:
  `docs/multi-host.md`.
- Logs go to `logs/node_<id>.log`, `logs/client.log`, plus `_test`, `_feature`,
  `_mem` variants (`logs/client<i>.log` etc. with `client_num` > 1). Scripts wipe
  `logs/` on start.

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
also the Perf floor), and `relaxed_fixed_trigger_timeout_ms` (default 350). `timeoutForMode`
picks Periodic's or the Fixed/Perf floor, and is used both by the constructor and by
`SwitchTriggerMode`, so a mode switch never leaves a stale timeout behind.

**The Fixed/Perf floor depends on the generation's scenario** (`config.FixedFloorForScenario`,
`Node.fixedFloorForGeneration`): in scenario mode, NetworkDelay and NetworkDelayFCrash
generations use `fixed_trigger_timeout_ms` (150, so Fixed and Perf still cascade under the
170 ms delay while Periodic makes progress) and every other scenario uses
`relaxed_fixed_trigger_timeout_ms` (350, which an n=7 view change fits inside until
view-change cost is optimised). Outside scenario mode it is always
`fixed_trigger_timeout_ms`. `incrementGeneration` passes the new generation to
`SwitchTriggerMode`, which derives the scenario with `scenarioForGeneration` (as the
election pool does), so every node agrees; the constructor uses generation 1. Each switch
logs `TIMER: gen G scenario S mode M fixed floor X`. Experiment scaffolding, matching the
n=7 pilots (350 ms for Healthy/ProposalDelay/Throttle, 150 ms for the network-delay
scenarios). The values below are the defaults outside scenario mode.

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

## ViewChange and NewView messages

- **ViewChange** carries the node's P-set: a prepared cert per prepared seq above
  its last stable checkpoint. That is up to about 290 certs in steady state and 500
  at the watermark (`docs/prepared-cert-bound.md`).
  - Cert prepares go on the wire as (from, signature) only. View, seq and digest are
    rebuilt from the cert's pre-prepare (`PreparedCertToPB`/`FromPB`).
  - `buildPreparedCert` keeps at most 2f matching prepares.
  - Only the first ViewChange per sender per view counts (`appendViewChangeIfNew`).
  - Every ViewChange gets a local `Digest`: sha256 of its signed bytes, computed in
    `Deliver` and in `VC()` for the node's own.
  - `verifyVC` skips every cert prepare this node already holds byte for byte as a
    verified one, in the slot's current-view `prepares` or its `preparedProof`
    (`certsLeftToVerify` / `prepareAlreadyVerified`, `node/view.go`). Only the rest
    are signature-checked. This is safe because everything held there is this node's
    own or passed `Deliver`'s signature and sender checks. The log line
    `VC CERTS ... cached=… to_verify=…` gives the hit rate.
- **NewView names its 2f+1 ViewChanges by `(sender, digest)`** (`ViewChangeRefs`)
  instead of embedding them, as in OSDI 2000 PBFT. The primary signs it. Each
  replica resolves the refs against ViewChanges it verified itself
  (`resolveViewChangeRefs`, `node/newviewrefs.go`), so a primary can't alter one.
  - If a ref doesn't resolve, the NewView is parked as `pendingNewView` and retried
    when a ViewChange of that view arrives.
  - If the ViewChange is still missing after `view_change_fetch_delay_ms`, the
    replica sends a `ViewChangeFetchMsg` to the primary, retrying once at twice the
    delay. The primary forwards that sender's original signed ViewChange
    (`ForwardViewChange`).
  - A different ViewChange from a sender already counted (equivocation, or the
    forwarded copy) is kept in `viewChangeAlt` only if a pending NewView names it,
    and is never counted.
- Log lines: `NEWVIEW PENDING`, `NEWVIEW RESOLVED`, `NEWVIEW REJECTED`, `VC FETCH
  sent`/`served`, `VC EQUIVOCATION`, `VC CERTS`.
- **A PrePrepare, Prepare or Commit for a view above the installed one is buffered**
  (`bufferFutureViewMessage`, `node/buffer.go`), whether or not the node is in a view
  change, and replayed through its handler when that view installs. Order is not
  kept on either side: `asyncBroadCast` is one goroutine per send, and the event loop
  reads NewViews, ViewChanges and consensus messages from separate channels, so a
  new-view message can be handled before the NewView or the ViewChanges that move
  the node. One below `forView` is dropped (that view can't install here), and
  `pruneBelowForView` clears the buffer below it. The buffer is unbounded
  (`TODO(byzantine)`). Log: `Replaying N buffered consensus messages ..., M of them
  buffered outside a view change`.
- Not checked: that the NewView's sender is the view's primary. There's a
  `TODO(safety)` in `HandleNewView`.

## Node architecture

`node/node.go` builds the `Node`; `node/eventloop.go` runs it.

**Single-goroutine event loop.** `run()` is the only writer of node state
(`viewID`, `forViewID`, `currAction`, `consensusLog`, timers, ...). Everything else —
the gRPC hub, VDF workers, the learning-agent client, the oracle — hands work in over
a channel and never touches node fields. All timer operations are loop-owned, so
none of them lock. Keep it that way when adding state.

Channels feeding the loop: `receiveVerifiedClientRequestCh`, `consensusMsgChan`,
`viewChangeMsgChan`, `newViewMsgChan`, `checkpointMsgChan`, `electionMsgChan`,
`epochMsgChan`, `learningAgentDecisionCh`, `electionVDFResultCh`, plus the timer
channels (progress, new-view, perf, epoch, and `vcFetchTimerCh` for a pending
NewView). `viewChangeMsgChan` also carries `ViewChangeFetchMsg`, routed by `MsgType`.

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
**Executed ids are recorded only when the client can resend** (`client_retry` or
`complete_suite`; `intakeFilter.trackExecuted`). Every node records them in `exeLoop`,
and the first request that never commits leaves a gap no retry fills, so the watermark
stops and every later id stays in `idTracker.above` (~30 B each, per node): ~13 GB per
node after 15 h of the n=7 multi-scenario runs. With retry off nothing can arrive twice,
so nothing is recorded; with retry on the set is still unbounded (TODO in `intake.go`).

Transport is gRPC bidirectional streams (`proto/pbft_transport.proto`,
`transportpb/`), one peer stream per target, 8 MiB flow-control windows. A node keeps
one reply stream **per client name** (`clientStreams`, `node/nodeMessageHub.go`), taken
from the `pbft-client-name` stream metadata (none = `"client"`): CommitTps goes only to
the request's `ClientName` (clients match replies by id alone and all number from 0),
LeaderIdUpdate to every client. ViewChanges are large: about 600 KiB at 290 certs and
batch 50, n=7. Most of that is the per-txn digest lists. ViewChanges dominate
view-change latency. A NewView is about 514 KiB, mostly the O-set's per-txn lists,
because it names ViewChanges by digest instead of carrying them.
`node/viewchange_size_test.go` reports both sizes. `node/newview_cost_test.go` is
the older standalone cost harness.

`TestNewViewTimingBudget` (`node/newview_timing_test.go`) times each view-change
stage on real nodes, across n, P-set size and batch. Results and the new-view timer
sizing are in `docs/newview-timer-budget.md`. The short version: before the
already-verified skip, the timer at n ≥ 7 was dominated by every node re-checking
every ViewChange's cert signatures. With the skip, when a node holds the prepares,
the timer need is the NewView path: about 23 / 34 / 45 ms at n = 4 / 7 / 10 on 16
CPUs. Pass `NVTIMING_COLD=1` to measure the miss case.

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
  fault scenarios (`Healthy`, `ProposalDelay`, `NetworkDelay`, `NetworkDelayFCrash`,
  `Throttle`;
  `core/scenario.go`), one
  per `scenario_generations` generations, derived from the generation in
  `SetForViewID` → `maybeSwitchScenario` (`node/scenario.go`). A switch turns everything
  off, then enables the new fault: ProposalDelay turns on the 100 ms `tryPropose` sleep on
  every `proposal_delay_nodes` node (a map like `nodes_dead`, **exactly f** of them, node 4
  allowed since a slow leader is still live — `validateScenarioProposalDelayNodes`);
  NetworkDelay has node 4 run
  `sudo -n scripts/netem_scenario.sh up 170 <n>` from a worker goroutine (`down` on
  every switch and on `Stop`) — in `-m remote` **every** node instead runs
  `scripts/netem_egress.sh up 170 <peer ips>` on its own egress (`ownsScenarioNetem`),
  which delays each directed pair once, as on lo; NetworkDelayFCrash applies the same delay and sets `dead`
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
  **Throttle** is driven by the client's throttle manager (`client/throttlemanager.go`),
  not by node 4: nodes inject nothing, but a node's proposal gate may be on only in a
  Throttle generation — it turns its own gate off when it leaves one
  (`clearThrottleGateOnScenarioSwitch`) and refuses enables outside one
  (`throttleGateAllowed`, `node/proposalgate.go`). The client works out each
  generation's scenario from accepted leader notices with the same formula
  (`core.ScenarioForGeneration`), is active only in Throttle generations, drains its
  slots on leaving, and takes its strategy from the notice's leader policy
  (RoundRobin → `roundrobin`, otherwise `election`), switching in place when the agent
  changes policy. `LeaderIdUpdate` carries the generation's `Action` for this, and it is
  part of the client's 2f quorum key. In scenario mode `throttle.enabled` is rejected
  (list `Throttle` instead) and `throttle.strategy` is ignored; Throttle needs
  `proposal_min_interval_ms > 0`, and runs with the learning agent or the oracle.
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
- `view_change_fetch_delay_ms` (default 20). This is how long a replica holding a
  NewView waits for a referenced ViewChange before fetching it from the primary.
- **`checkpoint_fetch`** — `{enabled, delay_ms}` (`config/checkpointfetch.go`;
  `run2new.json` has it on, delay 200). Checkpoint state transfer
  (`node/checkpointfetch.go`) for a node stranded below a stable checkpoint: it left a
  view before executing up to checkpoint k, the others stabilized k after sending their
  ViewChanges, so the NewView's O-set starts below k and every other replica skips those
  slots (below its low watermark) — nobody re-votes them and nothing retransmits. Seen
  at node 4 (aggregator, decides first) in ~10% of NetworkDelay generations under the
  Periodic actions, as 500-cert ViewChanges. Trigger: a certificate (2f+1 matching
  CHECKPOINT votes) above `lastExecuted` **and** no execution for a full `delay_ms`
  window inside one installed view (not across a view change). The node asks a voter
  for that checkpoint's state and installs it only for a (seq, digest) it holds a local
  certificate for and only if the state hashes to the digest, then runs `exeLoop`.
  With no install it asks the next voter after `CheckpointFetchRetry` (5 × `delay_ms`,
  sized by the round trip, not by the stall; reasons on `checkpointFetchRetryFactor`).
  Validated in `results/ckpt_fetch_n7` (README in `config/ckpt_fetch_n7/`): all 17
  NetworkDelay strandings repaired 0.55–0.74 s after the NewView, zero fetches in
  Healthy/NetworkDelay/NetworkDelayFCrash/Throttle. A stranded **primary** (Election can
  elect node 4 right after it left early) stalls the whole system for its tenure — seen
  once with the feature off. The ProposalDelay slow leader fetches although it would
  catch up (8 in 39 generations); accepted as cheap, see the comment in
  `node/checkpointfetch.go`.
  Disabled, it neither fetches nor serves. Why 200 ms is in the comment on
  `defaultCheckpointFetchDelay`. Log: `CKPT FETCH sent / served / installed / stale /
  rejected / ignored`; `scripts/ckpt_fetch_summary.py` counts them per run. Like
  `fastPathStablizeCheckpointviaVC`, an install skips requests without a record in the
  intake filter. The ViewChange path's own checkpoint proof and state are still
  unverified (`TODO(safety)` on `verifyVC`).
- Others: `node_num`, `max_batch_size`, `inject_speed`, `parallel_workers`,
  `nodes_dead`, `gc`, `min_vdf_delay` / `max_vdf_delay` (election VDF range).
- `nodes_in_dark` is present in the JSON but has no field in `config.go`, so it is
  silently ignored.
- `max_batch_delay` is parsed (`MaxBatchDelay`) but has no effect: the batch timer
  in `node/batcher.go` is armed and immediately stopped in `node.go`, and nothing
  reads its channel. A batch is proposed only once `pendingRequests` holds
  `max_batch_size` requests, so batching wait is the fill time `B/R` and is
  unbounded at low load.
- **`client_num`** (default 1): N client processes, client i colocated with node i,
  each sending its share of `inject_speed` per 24 ms (`config.ClientInjectShare`: 200
  over 7 is 29,29,29,29,28,28,28, so the total is unchanged). `alt_run_project.sh`
  starts windows `client1..clientN` (`-r client -n i`); client i is named `client<i>`
  and sends from 127.0.1.<i+1> — a range apart from the node IPs, so the node-pair
  netem filters (which also match a node with itself) never delay a client. All clients
  share `keys/client_*.pem`. Only client 1 runs the throttle manager (a node's gate is
  one setting). Client i writes `logs/client<i>_<file>` for every client file
  (`Client.outPath`); `stop_experiment.sh` runs `scripts/merge_client_reports.py`,
  which writes the single-client names (`report.json`, `latencyreport.json` with
  percentiles from the merged `latency_hist_us` and a per-client `clients` list,
  `client_request_send_rate.csv`, client 1's `leader_timeline.jsonl`), so analysis
  scripts read either kind of run. With one client nothing changes: name `client`,
  127.0.0.1, legacy file names.
- The client paces request batches (`client/ratelimiter.go`, one batch of
  `inject_speed` per 24 ms) into **per-node send queues** (`client/sendqueue.go`, 8
  batches each); a sender goroutine per node does the blocking `stream.Send`. A full
  queue drops the batch (still registered, so it counts as uncommitted and is retried
  if `client_retry` is on); a leader change discards the old leader's queue. Counts go
  to `send_queue_dropped` / `send_queue_discarded` in `latencyreport.json` and to the
  send-rate CSV. Before this, a leader that stopped reading its stream (ProposalDelay:
  the 100 ms sleep runs on node 1's event loop) froze the whole client once the 8 MiB
  gRPC window filled, starving the next leader for ~300 ms.
- **The client never forgets an uncommitted request.** Every sent request is registered
  in `TransactionManager` (`client/transactionmanager.go`) and only a commit deletes it,
  so client memory grows with the number of requests that never commit (cascades,
  slow/throttled leaders, send-queue drops), not with run length. The signed body is
  kept only when something can resend it (`client_retry` or `complete_suite`,
  `SetKeepRequestBody`); without it an entry is ~134 B of heap instead of ~374 B.
  Without a retry path, a batch dropped on a full send queue is registered and then
  forgotten again (`ForgetUnsent`, in `pacedSendRequestTransactions`), and reported as
  `dropped_unsent` in `latencyreport.json`; `uncommitted + dropped_unsent` is every
  request that never committed. Discarded and sent-but-lost requests stay tracked.
  `latencySamples` also grows by 8 B per committed request, and `LatencySummary`
  copies it twice at export.
- `proposal_delay_ms` is parsed but unused; the proposal delay is a hardcoded 100 ms
  sleep in `tryPropose`.

## Experiment tooling

- `alt_run_project.sh` — the main harness. Sets up a netem `prio`+`netem` qdisc on
  `lo` with flower filters for every node pair, then runs a delay schedule (the
  active one raises delay for 30 s). `setup_netem` and `start_netem_schedule` at the
  bottom are commented in and out per experiment. Delay transitions are logged with
  timestamps to `logs/netem_schedule.log` — check it against node logs when
  correlating.
- `scripts/cluster/` — the multi-machine harness for `-m remote`: `setup.sh` (once per
  cluster; `--tune-tcp-lan` matches the LAN to lo's ~640 KiB first flight so lo results
  reproduce, `--tune-tcp-wan` is BBR/fq/big buffers for long links), `start.sh` / `stop.sh` / `run_experiments.sh <hosts.json> ...`
  (the counterparts of `alt_run_project.sh`, `stop_experiment.sh`, `run_experiments.sh`),
  run from the control machine over ssh; `cloudlab/profile.py` makes the cluster. Each run
  records every host's TCP/NIC counters at start and stop and the PBFT sockets' `ss -tin`;
  `scripts/tcp_counters_summary.py results/<run>` gives retransmissions, RTO timeouts and
  drops per host. See `docs/multi-host.md`.
- `docs/todo/` holds planned work not started yet, e.g. `vc-newview-size.md` (Phase 1
  leftovers, smaller checkpoint interval, BLS-aggregated prepares).
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
  candidate pool is the same size as RoundRobin's rotation. On lo the aggregator
  also owns the scenario-mode netem qdisc; that, and aggregating, is why
  `NetworkDelayFCrash` refuses to crash it. All are experiment scaffolding, not protocol.
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
  `nodeMessageHub.go`, including NewView. Prepare and Commit are also rejected when
  the payload's `From` differs from `env.From`. PrePrepare has no `From`, and its
  leader identity isn't checked (`verifyPreprepare` is a stub).
- Several `node` tests construct `&Node{}` directly and panic on a nil logger or nil
  trigger manager; they are stale, not regressions.
- Timestamps in node logs are the only cross-node clock, and all nodes run on one
  machine, so they are directly comparable. Hub logs report `elapsed=` for delivery
  and `took` for sends — subtract them from the log time to get the true start.
