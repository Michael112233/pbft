# Compare leader rotation timers

> do you think in case of network congestion it would be better to go to peridic changing leader rather than fixed timer which is sensitive and of 100ms
>
> see the way i introduce congestion i am thinking that by congestion i can trip timer and fixed one will keep running in view change and periodic one only run view change when its time come

<details><summary>6 previous messages</summary>

> I’ll trace the view-change and congestion logic in the repo first, because the answer depends on whether the 100 ms timer is measuring missing progress, message delay, or simply rotating the leader. Then I’ll compare the two policies against the exact congestion pattern you inject.
>
> <details><summary>Explored 5 files, 2 searches</summary>
>
> - Searched for `100 ?ms|100ms|view.?change|leader|timeout|timer|congestion|periodic|primary` in `.`
> - Read `./node/timer.go`
> - Read `./node/periodicTimer.go`
> - Read `./node/periodic.go`
> - Read `./run_project_linux_netem.sh`
> - Read `./docs/node-congestion-metrics.md`
> - Searched for `defaultPBFTRequestTimeout|defaultPeriodicTimerTimeout|PeriodInterval|periodInterval|Periodic:` in `.`
>
> </details>
>
> The key interaction is already visible: your “fixed” progress timer is hard-reset to exactly 100 ms, while the netem experiment adds 100 ms to every node-to-node packet. A normal PBFT decision needs multiple message legs, so that timeout is expected to fire even when the leader is healthy; it is detecting delay, not leader failure. I’m checking how your periodic trigger is defined, because it appears to be tied to executed sequence numbers rather than elapsed time.
>
> <details><summary>Explored 6 files, 2 searches, ran a command</summary>
>
> <details><summary>Explored 7 files, 2 searches</summary>
>
> - Read `./node/node.go`
> - Read `./node/execution.go`
> - Read `./node/viewIntent.go`
> - Searched for `Fixed|fixed|Periodic|periodic|Period|period` in `config*`
> - Read `./node/execution.go`
> - Read `./node/node.go`
> - Read `./config/config.go`
> - Read `./config/run2new.json`
> - Searched for `PeriodicReq|periodicReq|ReadPeriodicTrigger|EpochReqExecuted|periodicTrigger` in `node`
>
> </details>
>
> - Ran `rg -n "trackPreprepareRequest|forceResetPBFTTimer|forceStopPBFTTimer|startPeriodicTimerForNewView|startPeriodicTimerForReqExe" node --glob '*.go'; sed -n '390,460p' node/epochManager.go; git status --short`
>
> <details><summary>Explored 4 files</summary>
>
> - Read `./node/node.go`
> - Read `./node/node.go`
> - Read `./node/node.go`
> - Read `./node/roundrobin.go`
>
> </details>
>
> </details>
>
> There is one more important distinction: your netem rule delays every node-to-node pair equally. Rotating the leader cannot remove that congestion; it can only prevent the 100 ms failure detector from reacting badly to it. If you want to demonstrate that leader rotation helps, the injected impairment should be leader-specific. I’m also verifying the current script defaults, since its stated baseline and actual qdisc delay differ.
>
> <details><summary>Explored 8 files</summary>
>
> - Read `./node/timer.go`
> - Read `./node/periodicTimer.go`
> - Read `./node/execution.go`
> - Read `./run_project_linux_netem.sh`
> - Read `./config/run2new.json`
> - Read `./run_project_linux_netem.sh`
> - Read `./node/node.go`
> - Read `./node/viewIntent.go`
>
> </details>
</details>

Yes—for your experiment, periodic leader rotation will probably behave better than the fixed 100 ms timer. But the conclusion would be: “periodic rotation avoids false view changes caused by an undersized timeout,” not necessarily “periodic rotation is generally superior.”

With 100 ms one-way netem delay, PBFT roughly needs:

```text
PrePrepare: 100 ms
Prepare:    100 ms
Commit:     100 ms
-----------------
Execution: ≈300+ ms
```

Your fixed watchdog is exactly 100 ms in [timer.go](./node/timer.go:218), so it can expire before a healthy leader could possibly complete consensus. It is interpreting ordinary network delay as leader failure.

Your intuition about behavior is mostly right:

- The fixed timer will fire during congestion whenever requests remain pending.
- It does not initiate another view change while `viewChangeRunning` is true, due to the check in [timer.go](./node/timer.go:304).
- Once normal processing resumes in the new view, the next PrePrepare starts the 100 ms timer again, so repeated view changes are likely while the delay remains.
- The periodic mechanism waits for its scheduled trigger—currently five seconds for the wall-clock version in [periodicTimer.go](./node/periodicTimer.go:11)—and stops that timer when view change begins. Therefore, it avoids the view-change loop.

However, periodic-only rotation has two weaknesses:

- If the leader genuinely crashes, recovery waits until the next scheduled rotation.
- If you use the request-count trigger (`periodic_req`), it may never rotate while consensus is completely stalled, because the trigger depends on `lastExecuted` reaching the configured period in [execution.go](./node/execution.go:271).

My preferred design would be a hybrid:

- Periodic rotation for fairness or planned leader changes.
- A progress watchdog for actual failure recovery.
- Set the watchdog above expected end-to-end consensus time—perhaps start around 500–750 ms for a 100 ms one-way delay experiment.
- Apply exponential backoff after unsuccessful view changes, for example 500 ms → 1 s → 2 s.
- Reset it only when real consensus progress occurs.

Also note three experiment details:

- Your netem rule affects every node pair, so changing leaders cannot escape the impairment. Periodic rotation only avoids the bad timer response. To show a benefit from changing leaders, selectively impair the current leader’s links.
- The script currently installs `delay 50ms` while claiming the initial delay is 0 ms in [run_project_linux_netem.sh](./run_project_linux_netem.sh:28).
- The 0→100→0 ms schedule is currently commented out in [run_project_linux_netem.sh](./run_project_linux_netem.sh:118).
- This netem setup injects latency, not actual bandwidth congestion. For a congestion claim, add constrained `rate`, queue limits, or loss.

One more configuration issue: `peak_tps_test` is currently `true`, so timer expiry is explicitly prevented from initiating a view change. That happens in [timer.go](./node/timer.go:307), based on [run2new.json](./config/run2new.json:24).

So: use periodic rotation if your goal is to demonstrate resistance to premature view changes, but compare it against a properly sized/adaptive timeout too. Otherwise the experiment mostly demonstrates that 100 ms is too short.

> what are some problems with my timer
> does it ensure that some request executed every 100ms that was my intention for my timer

<details><summary>2 previous messages</summary>

> I’m checking the exact timer lifecycle against request-pool semantics. The short answer is no: it is a watchdog that reacts after 100 ms without observed execution; it cannot guarantee an execution deadline. There are also a couple of race and measurement issues that make it weaker than the intended “one execution per 100 ms while work is pending” invariant.
>
> <details><summary>Explored 3 files, a search, ran a command</summary>
>
> - Ran `sed -n '1,280p' node/pool.go 2>/dev/null || rg -n "type Pool|func \(.*Pool.*PendingRequests|func \(.*Pool.*Add|func \(.*Pool.*Delete" node; rg -n "func \(.*Pool.*PendingRequests|type Pool" node -g '*.go'; rg -n "onRequestExecuted\(" node -g '*.go'; rg -n "trackPreprepareRequest\(" node -g '*.go'`
>
> <details><summary>Explored 3 files, a search</summary>
>
> - Searched for `\.pool\.Add\(|pool.Add\(` in `node`
> - Read `./node/node.go`
> - Read `./node/execution.go`
> - Read `./node/pool.go`
>
> </details>
>
> </details>
</details>

Your timer is intended to detect “no execution progress for 100 ms while requests are pending.” It does not ensure that a request executes every 100 ms.

Its approximate behavior is:

1. First PrePrepare starts the timer.
2. An execution resets it to 100 ms if more requests remain.
3. If no execution happens before expiry, it may initiate view change.

So conceptually it measures the gap between executions, not throughput over fixed 100 ms windows.

Several problems prevent it from reliably implementing even that rule.

### 1. A timer cannot guarantee execution

The timer can only detect that execution did not happen and initiate recovery. A view change itself may take hundreds of milliseconds or fail during congestion.

Under your 100 ms network delay:

```text
t=0 ms    leader sends PrePrepare and starts timer
t=100 ms  backups receive PrePrepare; timer expires
t=200 ms  Prepare messages arrive
t=300 ms  Commit messages can arrive and execution occurs
```

The timer can therefore destroy a view that would otherwise make progress.

### 2. It is hardcoded to 100 ms

Both start and reset use a literal 100 ms in [timer.go](./node/timer.go:218). They ignore the existing `pbftTimeout`, jitter, and five-second defaults.

This makes experiments difficult to configure and makes the timeout much shorter than the expected PBFT execution latency under netem.

### 3. It measures the request pool, not execution progress directly

At expiry, it checks only:

```go
n.pool.PendingRequests() > 0
```

It does not verify whether `lastExecuted` has changed since the timer was armed.

A clearer implementation would capture:

```text
executedSequenceWhenTimerStarted
currentViewWhenTimerStarted
```

At expiry, trigger only when:

```text
pending requests exist
AND lastExecuted has not advanced
AND the view has not changed
```

### 4. There is a stale-expiry race

This sequence is possible:

1. Timer fires and the worker receives the event.
2. A request executes and resets the timer.
3. The old expiry handler runs afterward.
4. It sees pending requests and triggers view change despite the recent execution.

The timer has no generation/epoch value for distinguishing the old expiry from the new reset. Your new-view timers use epochs, but the PBFT progress timer does not.

### 5. It becomes inactive after expiry

The expiry handler sets `pbftTimerInitiated = false` in [timer.go](./node/timer.go:301). If it does not initiate a view change, it does not immediately re-arm itself even when requests remain pending.

Also, `forceResetPBFTTimer()` only resets an already-active timer. Despite the name, it does nothing when the timer is inactive.

Therefore, after the first missed deadline, continued monitoring depends on another PrePrepare arriving and calling `trackPreprepareRequest()`.

### 6. `peak_tps_test` disables its recovery action

Your current configuration has `"peak_tps_test": true`. In that mode, expiry explicitly does not initiate view change in [timer.go](./node/timer.go:307).

So currently it is mainly an intermittent logging timer, not an enforcement mechanism.

### 7. No-op execution also resets it

`onRequestExecuted()` is called for normal requests and no-op slots in [execution.go](./node/execution.go:237). Therefore, a no-op can count as “execution progress” even though no client request was completed.

If your invariant specifically means client-request execution, reset the progress timestamp only for `!pending.noOp`.

### 8. All replicas use the same deadline

Replicas receive the PrePrepare at slightly different times, but they all use the same 100 ms timeout. During congestion they can expire close together and produce a synchronized view-change burst.

Timeout jitter and exponential backoff would help avoid repeated synchronized changes.

### 9. It does not keep running during view change

When expiry occurs during an existing view change, it logs the condition and does not trigger another change. It also leaves the timer inactive. So your previous statement that it “keeps running in view change” is not quite accurate.

A better definition for your intention would be:

```text
While client requests are pending:
    if no non-no-op sequence has executed for T milliseconds:
        initiate at most one view change for the current view
```

Implement that with a monotonic `lastClientExecutionTime`, current view, and timer generation. For the 100 ms one-way-delay experiment, `T` must be comfortably above the roughly 300 ms PBFT message path—probably at least 500–750 ms initially, followed by exponential backoff.

> if we have 100ms timer and in congestion request take 300ms will my timer expire in the case i have pipelining
> when i add latency my throughput stays same so i am thinking will timer expire

Pipelining means a 300 ms per-request latency does not automatically make your 100 ms timer expire.

Your timer expires when:

```text
time since the most recent execution > 100 ms
AND local request pool is non-empty
```

It does not measure how long an individual request takes.

For example, with pipelining:

```text
Requests proposed:  0, 10, 20, 30, ... ms
Requests executed: 300,310,320,330, ... ms
```

Every request takes 300 ms, but after the pipeline fills, executions occur every 10 ms. Therefore the timer keeps resetting and does not expire.

There are two important exceptions:

1. At startup, the first execution occurs around 300 ms, so a timer started with the first proposal expires at 100 ms.

2. When congestion is introduced suddenly, the pipeline may develop a temporary output gap. If that gap exceeds 100 ms, the timer expires—even if steady-state throughput later returns to the same level.

Also, unchanged average throughput does not prove the timer will not expire. For example:

```text
30 requests execute together every 300 ms
average throughput = 100 req/s
maximum execution gap = 300 ms
```

The average throughput is still 100 req/s, but your timer expires between bursts.

The exact condition to measure is therefore:

```text
max(inter-execution gap while PendingRequests > 0)
```

If that maximum is below 100 ms, the timer normally will not expire. If it exceeds 100 ms, it will.

For your experiment, I would use two timeouts:

- Initial/proposal timeout: at least 500–750 ms, allowing the high-latency pipeline to fill.
- Steady-state progress timeout: possibly 100 ms, but only if normal measurements show execution gaps safely below 100 ms.

Finally, with `"peak_tps_test": true`, the timer can expire and log the expiry, but it will not initiate view change. So distinguish “timer fired” from “view change happened.”

> what improvement can i do to timer to be close to pbft like timer

To make your timer closer to a PBFT failure detector, make it a view-scoped pending-request timer with adaptive timeout and stale-expiry protection.

Classic PBFT-like behavior is:

```text
A replica knows a client request is pending
        ↓
Start a request timer
        ↓
Request executes → cancel/re-arm for next pending request
        ↓
Timer expires → suspect the primary and send one ViewChange
        ↓
No NewView → increase timeout and move to a higher view
```

The most useful improvements are:

1. Do not hardcode 100 ms. For your 100 ms one-way network delay, start around 500–750 ms:

```text
T0 = 500 ms
T1 = 1 second
T2 = 2 seconds
T3 = 4 seconds
```

Use exponential backoff:

```go
timeout = min(maxTimeout, baseTimeout*2^consecutiveTimeouts)
```

This is important for PBFT liveness: after the network becomes stable, the timeout must eventually become longer than the real communication delay.

2. Attach the timer to a view and generation. When resetting:

```text
generation++
capture current view
capture current generation
```

On expiry, verify:

```text
captured generation == current generation
captured view == current view
view change is not already running
pending request still exists
```

This prevents an old timer event from triggering view change after recent execution.

3. Trigger at most once per view. Keep something like:

```go
timedOutView int64
```

Then:

```go
if timedOutView == currentView {
    return
}
timedOutView = currentView
```

Otherwise several concurrent expiries can repeatedly call the view-change code.

4. Track the oldest pending client request. Your current pool stores only requests that have already been proposed or accepted. A faulty leader could ignore one client request while continuing to execute others, and your current timer would keep resetting.

Record:

```go
type PendingRequest struct {
    Digest     [32]byte
    ReceivedAt time.Time
}
```

Arm the timer against the oldest pending request:

```text
deadline = oldestRequest.ReceivedAt + timeout
```

When another request executes, do not give the oldest request a fresh full timeout unless it was the request that executed.

5. Do not count no-ops as client progress. Currently [execution.go](./node/execution.go:237) calls the timer update for no-op and client executions.

For client-request liveness:

```go
if !pending.noOp {
    watchdog.RequestExecuted(pending.digestClientMsg)
}
```

6. Re-arm after installing a new view. The current `forceResetPBFTTimer()` only resets when the timer is already active. After expiry, `pbftTimerInitiated` is false, so “force reset” may do nothing.

After NewView:

```text
if pending requests exist:
    unconditionally arm timer for new view
else:
    stop timer
```

Include carried/replayed requests, not only newly received PrePrepares.

7. Use a separate NewView timeout. You need two different watchdogs:

- Request timer: current primary is not executing a pending request.
- NewView timer: replicas entered view change but the next leader did not complete NewView.

Both should use view-scoped generations and backoff. A scheduled periodic rotation should not necessarily increase the failure backoff; only an actual timeout should.

8. Decide which property you want to detect.

A PBFT-like oldest-request timer detects:

```text
Some known client request has waited longer than T.
```

A pipeline-friendly progress timer detects:

```text
No client request has executed for T while work is pending.
```

They are different. With 300 ms request latency and executions every 10 ms:

- The progress timer does not expire.
- A 100 ms per-request timer expires.
- A 500 ms per-request timer does not expire.

For your system, I would use both measurements but only one view-change trigger:

```text
trigger view change when:
    oldest_pending_age > request_timeout
    AND no_execution_progress > progress_grace_period
```

For example:

```text
request_timeout = 750 ms initially
progress_grace_period = 250–500 ms
new_view_timeout = 1 second initially
maximum timeout = 8 seconds
```

Finally, log why every timeout fired:

```text
view, timeout_ms, pending_count, oldest_pending_age_ms,
last_executed, no_progress_ms, timer_generation
```

That will let you distinguish a genuinely stalled leader from a healthy but high-latency pipelined leader.

> think deeply and tell me scenario where periodically changing leader after like 5s would be better than fixed sensistive timer of 100ms
> think in term of anything congestion loss delay network size anything is fine

The strongest scenario is a network that is slow or bursty but still capable of maintaining good throughput. In that situation, the 100 ms timer can turn temporary delay into a self-inflicted view-change storm, while a five-second periodic policy gives the pipeline time to operate.

### Concrete scenario: 100 ms network delay with pipelining

Assume four replicas, 100 requests/second, and 100 ms one-way node delay.

```text
PrePrepare reaches backups: ≈100 ms
Prepare quorum:            ≈200 ms
Commit quorum/execution:   ≈300 ms
```

After warming up, pipelining can produce:

```text
Request latency:     300 ms
Execution interval:   10 ms
Throughput:           100 requests/second
```

But at startup or immediately after introducing congestion:

```text
t=0 ms     leader proposes; 100 ms timer starts
t=100 ms   timer expires; ViewChange starts
t=200 ms   original Prepare messages arrive
t=300 ms   original view could have executed, but replicas are changing view
```

The next leader repeats the same pattern:

```text
New leader proposes
100 ms later → another timeout
```

The network was capable of processing requests, but the timeout prevents the pipeline from filling.

With five-second periodic rotation:

```text
t=0–300 ms    pipeline fills
t=300 ms      first execution
t=310 ms      second execution
t=320 ms      third execution
...
t≈5 s         one planned view change
```

Periodic rotation wins here because it provides a long stable view. Its main benefit is rate-limiting view changes, not the fact that it chooses another leader.

### Other scenarios where periodic rotation can be better

| Scenario | Why the 100 ms timer performs badly | Why five-second rotation can perform better |
|---|---|---|
| Global network congestion | Every leader has the same delayed paths, so changing leader cannot help | Avoids repeatedly changing leaders for a leader-independent problem |
| Queueing microbursts | Execution may stop for 150–300 ms and then catch up in a burst | Allows queues to drain and the pipeline to recover |
| Correlated packet loss | TCP retransmission or quorum recovery can take more than 100 ms | Does not confuse a short transport recovery with leader failure |
| Large replica set | Prepare/Commit traffic and signature verification can exceed 100 ms | Amortizes view-change cost across a five-second stable period |
| Checkpoint or GC pause | All replicas may briefly pause for more than 100 ms | Avoids replacing a healthy leader for a system-wide pause |
| Adversarial timing | An attacker only needs to create gaps slightly above 100 ms to force repeated view changes | Limits induced view changes to approximately one every five seconds |
| Multi-region deployment | Normal consensus latency may already be hundreds of milliseconds | Allows slow but healthy leaders to make progress |

### Same average throughput can still trip the timer

Suppose congestion causes bursty execution:

```text
t=300 ms: 30 requests execute
t=600 ms: 30 requests execute
t=900 ms: 30 requests execute
```

Average throughput is still:

```text
30 / 0.3 seconds = 100 requests/second
```

But the inter-execution gap is 300 ms, so the 100 ms timer fires between every burst.

Therefore, whole-run throughput staying the same does not prove the timer did not expire. Measure:

```text
maximum inter-execution gap while requests are pending
```

### Positive-feedback congestion

PBFT view change creates additional broadcasts and certificate processing. With \(N\) replicas, normal Prepare and Commit communication is already approximately \(O(N^2)\).

A sensitive timer can produce:

```text
network congestion
→ 100 ms timeout
→ ViewChange broadcasts
→ more congestion
→ slower NewView
→ another sensitive timeout
```

Periodic rotation breaks this feedback loop by placing a minimum amount of stable time between view changes.

### When periodic rotation is worse

Periodic-only rotation performs badly when:

- The leader actually crashes: recovery takes up to five seconds.
- Only the leader’s links are congested: changing immediately would help.
- The leader is Byzantine and stops proposing.
- The network is healthy and execution gaps are always below 100 ms: periodic rotation adds unnecessary overhead.
- The initial leader fails before executing sequence 1. In your implementation, the five-second periodic timer starts after the first execution, so it may never start in this case.
- The periodic intent message is lost and no new view is installed; the timer is currently one-shot unless restarted.

Therefore, periodic rotation should not be the only liveness mechanism.

### Best design

I would combine:

```text
Scheduled rotation: every 5 seconds, if you want fairness
Failure watchdog: adaptive, initially around 500–750 ms
New-view watchdog: around 1 second, with backoff
```

After unsuccessful views:

```text
500 ms → 1 s → 2 s → 4 s
```

After a new view, also provide a pipeline warm-up grace period before enabling the shorter steady-state progress detector.

The most defensible experimental claim would be:

> Periodic rotation performs better than a fixed 100 ms timeout under global delay, burst loss, or queueing because it avoids false suspicions and view-change amplification. An adaptive PBFT timeout should retain that stability while recovering faster from genuine leader failures.

For evaluation, compare the three policies under both global congestion and leader-specific failure:

1. Fixed 100 ms.
2. Periodic five seconds.
3. Adaptive timeout with backoff.

I would expect periodic to beat fixed under global congestion, while adaptive should beat both across global congestion and actual leader failure.

> what policy does my current 100ms timer guarantee right now

Right now, your timer implements this approximate local policy:

> After a replica creates or accepts a PrePrepare, if its local request pool remains non-empty and no consensus slot executes for 100 ms, that replica attempts to suspect the current view.

Its state machine is:

```text
PrePrepare created/accepted
        ↓
Start one-shot 100 ms timer
        ↓
Any slot executes
        ├─ requests still pending → reset to 100 ms
        └─ pool empty → stop timer
        ↓
Timer expires
        ├─ pool empty → do nothing
        ├─ view change running → do nothing
        ├─ peak_tps_test=true → log only
        └─ otherwise → initiate ViewChange
```

With your current configuration:

```json
"fixed": true,
"peak_tps_test": true
```

the timer can expire, but it does not initiate view change. It only logs the expiry. After expiry it becomes inactive until another PrePrepare starts it again.

It does not guarantee:

- One client request executes every 100 ms.
- Request latency is below 100 ms.
- Minimum throughput of 10 requests/second.
- Every pending request eventually executes.
- Continuous checking every 100 ms after the first expiry.
- Detection of a leader that never sends a PrePrepare.
- Successful application execution.

The last point matters because the timer is updated whenever a consensus slot advances, including no-op slots and operations whose application execution reports failure.

Ignoring races and with `peak_tps_test=false`, its closest policy is:

```text
While the timer is armed and locally known work is pending,
attempt one view change if the gap between local slot executions
exceeds 100 ms.
```

It is therefore a one-shot local progress-gap detector—not a per-request deadline, periodic execution guarantee, or complete PBFT failure detector.

> does it guarantee that one transaction execute every 100ms

No. It does not guarantee that one transaction executes every 100 ms.

It only attempts to detect when approximately 100 ms passes without local execution while requests are pending. After expiry, it may initiate view change—but a view change does not make a transaction execute within that deadline.

It also counts consensus progress that is not necessarily a successful transaction:

- No-op execution can reset the timer.
- A transaction whose application execution fails can still advance the executed sequence.
- With your current `peak_tps_test=true`, expiry only logs; it does not initiate view change.
- After expiry, the timer is inactive until another PrePrepare starts it.

So the policy is closer to:

> “If locally known work is pending and no slot executes for roughly 100 ms, report a progress timeout and possibly suspect the leader.”

It provides neither a 100 ms transaction-latency guarantee nor a guaranteed minimum rate of 10 transactions per second.

> how can i guarantee that timer ensure that some request execute in 100ms and if there is no execution in past 100ms then expire

You cannot guarantee that the network and replicas execute a request within 100 ms. You can guarantee that your watchdog detects a violation:

> While client work is pending, expire when 100 ms has passed since either the first request became pending or the most recent real request execution.

Use this reference time:

```text
progressReference =
    max(
        lastClientExecutionTime,
        timePendingWorkBecameNonempty,
        currentViewStartTime
    )
```

Then expire when:

```text
pendingClientRequests > 0
AND now - progressReference >= 100 ms
AND timeout has not already fired for this view
```

### Required changes

1. Record real client execution time

Update it only when a real client transaction executes—not for a no-op:

```go
if !pending.noOp {
    watchdog.RecordExecution(view)
}
```

Decide whether failed application operations count. Usually they count as consensus execution but not as successful transactions.

2. Start when work becomes pending

When pending requests transition from zero to one:

```go
watchdog.WorkBecamePending(view)
```

Do not wait only for a PrePrepare if you want to detect a leader that accepts a request but never proposes it.

3. Reset after every real execution

Conceptually:

```go
func (w *Watchdog) RecordExecution(view int64) {
    w.mu.Lock()
    defer w.mu.Unlock()

    w.lastExecution = time.Now()
    w.generation++

    if w.pendingRequests > 0 {
        w.armLocked(view, w.generation, 100*time.Millisecond)
    } else {
        w.stopLocked()
    }
}
```

4. Protect against stale timer callbacks

Every reset increments a generation:

```go
func (w *Watchdog) expire(view int64, generation uint64) {
    w.mu.Lock()

    if generation != w.generation ||
        view != w.currentView ||
        w.timedOutView == view ||
        w.pendingRequests == 0 {
        w.mu.Unlock()
        return
    }

    elapsed := time.Since(w.progressReferenceLocked())

    if elapsed < 100*time.Millisecond {
        remaining := 100*time.Millisecond - elapsed
        w.armLocked(view, generation, remaining)
        w.mu.Unlock()
        return
    }

    w.timedOutView = view
    w.mu.Unlock()

    w.triggerViewChange(view)
}
```

The generation check prevents this race:

```text
Old timer expires
→ transaction executes and resets timer
→ old callback runs
→ old callback is ignored
```

5. Fire once per view

After detecting the violation, do not keep firing every 100 ms:

```go
if w.timedOutView == view {
    return
}
```

Use a separate NewView timeout while view change is running.

6. Re-arm correctly after NewView

When a new view is installed:

```text
currentViewStartTime = now
timedOutView = -1

if requests remain pending:
    start a fresh 100 ms grace period
```

Do not depend on another PrePrepare arriving.

### Exact property achieved

Assuming the process itself is scheduled normally, this implementation provides:

```text
If client work remains pending and no real client request execution
is recorded for at least 100 ms, the replica triggers one timeout
for the current view.
```

It still cannot provide:

```text
At least one transaction will execute every 100 ms.
```

The timer can only report the violation and initiate recovery.

Also, with 100 ms one-way network delay, PBFT may need roughly 300 ms before its first execution. Therefore, a correctly implemented 100 ms watchdog will correctly—but undesirably—expire during pipeline startup. A practical design would use a 500–750 ms initial/view-start timeout, then optionally a shorter steady-state progress timeout after the pipeline is warm.

> in my current state of timer is it possible that my timer rearms and in the fresh 100ms gap some request execute but my timer still expire
>
> tell me all possible cases for that

<details><summary>2 previous messages</summary>

> I’m tracing the races at the exact point your code records execution, resets `time.Timer`, and handles `<-timer.C>`. The main risk is not usually the newly reset timer firing early; it is an expiry from the previous timer generation continuing after the reset because the PBFT timer has no epoch/generation check.
>
> <details><summary>Ran 2 commands</summary>
>
> - Ran `go version; sed -n '1,40p' go.mod; nl -ba node/timer.go | sed -n '155,320p'; nl -ba node/execution.go | sed -n '228,265p'; rg -n "type Node struct|commitSerializedRoutine|commitChan" node/node.go node/execution.go | head -n 80`
> - Ran `go doc time.Timer.Reset; go doc time.Timer.Stop`
>
> </details>
</details>
