# Periodic vs. Fixed View Change Under 100 ms Network Spikes

## Experiment

This report compares two PBFT runs subjected to the same repeating network
disturbance:

- `logs/`: request-count periodic view change.
- `singleepoch_fixed/`: fixed 100 ms execution-gap suspicion timer.

The client injects 63,000 transactions in batches of 100 at a configured pace
of approximately 2,500 transactions per second. The netem loop adds 100 ms of
node-to-node delay for approximately 100--115 ms, restores zero added delay,
and waits approximately two seconds before repeating. The filters cover every
node-to-node pair; the client-to-node path is not delayed.

The current configuration rotates the periodic leader every **21,000
requests**, not 20,000, because `config/run2new.json` sets `period` to 21,000.
The periodic run therefore changes view at sequence 21,000 and sequence 42,000.

## Result

The periodic run performs better because the fixed 100 ms timer repeatedly
interprets transient network delay as leader failure. Periodic mode lets the
delayed PBFT pipeline finish and produce a catch-up burst. Fixed mode interrupts
that recovery with a view change, rejects old-view traffic, redirects the
client, and loses requests because client retry is effectively disabled.

| Metric | Periodic | Fixed 100 ms |
| --- | ---: | ---: |
| Committed at last progress, approximately 25.6 s | 62,300 | 55,015 |
| Average TPS at last progress | 2,433.6 | 2,149.0 |
| Fraction of 63,000 injected requests committed | 98.9% | 87.3% |
| Final installed view | 3 | 52 |
| Logged dropped client batches | 7 | 58 |
| Transactions in logged dropped batches | 700 | 5,800 |

These values come from `logs/ch.json`, `singleepoch_fixed/ch.json`, and the
four node logs in each directory. The client-level average at the final log
sample is lower because sampling continues after execution progress has
stopped. The values in the table compare the runs at their last progress point.

## Behavior During a Delay Spike

A PBFT decision crosses several network phases. A 100 ms delay on node-to-node
messages can therefore create an execution-output gap longer than 100 ms even
when the leader is healthy. Pipelining preserves steady-state capacity, but it
does not prevent a transient gap when delay is introduced abruptly.

During the first spike, the periodic client's approximate 100 ms throughput
samples were:

```text
2,169 -> 360 -> 470 -> 4,740 -> 4,260 TPS
```

This is a freeze followed by a catch-up burst. The timer expires in periodic
mode, but the expiration is only recorded as a shadow suspicion. Consensus
continues in the same view, so delayed PrePrepare, Prepare, and Commit messages
remain valid and complete after the impairment is removed.

The corresponding fixed samples were:

```text
2,080 -> 270 -> 100 -> 4,990 -> 0 -> 0 -> 0 TPS
```

A catch-up burst begins, but the timeout-induced view change interrupts it.
Eleven of the first twelve delay spikes cause a fixed-mode view change. The
periodic run performs only two planned changes over the entire workload.

## Why Periodic Does Not React to Each Spike

The view timer is a 100 ms execution-gap detector. Every execution resets its
deadline. When the deadline expires, `handleViewTimerExpiry` increments the
shadow-suspicion count.

In periodic mode, `ReadFixedTrigger()` is false, so the expiry is marked as a
shadow timeout and returns without initiating view change. A later execution
re-arms the detector in the same view. This allows the old pipeline to drain
after the network returns to normal.

In fixed mode, `ReadFixedTrigger()` is true, so the same expiration calls
`handleViewChangeTimeoutDummy`. That immediately sets `viewChangeRunning`,
advances `forView`, and begins the round-robin view-change protocol.

Consequently, the relevant advantage is not that periodic mode selects a
better leader. Netem delays every node-to-node pair, so changing the leader
cannot bypass the impairment. Periodic performs better because it rate-limits
view changes and does not treat every short, global delay as a leader failure.

## What Happens to Old-View Work

The hypothesis that in-flight old-view work is rejected is substantially
correct, but not all old-view work is necessarily lost.

While `viewChangeRunning` is true, the node handlers return without processing
current-view PrePrepare, Prepare, and Commit messages. Future-view messages are
buffered, but equal-view traffic is ignored. Once the new view is installed,
messages from the previous view also fail the normal view-number checks.

Prepared work can survive. `createVCContent` includes slots that have a
PrePrepare and have reached `commitSent`, and the new leader reconstructs those
slots in the NewView suffix. For example, the first fixed NewView carries 244
entries with a maximum sequence of 2,744.

The vulnerable work consists of:

- delayed consensus traffic that has not reached a prepare quorum;
- client requests queued inside the old leader;
- requests accepted while the leader is changing;
- batches sent by a client that has not yet learned the new leader;
- requests for which `preprepare` observes that the node is no longer leader
  and returns without proposing them.

Thus, a better description than "all old-view work is lost" is:

> Prepared work may be carried into the new view, but unprepared and queued
> work can be abandoned, while old-view consensus messages arriving during
> view change are ignored.

## Client-Side Amplification

The client sends batches of 100 transactions approximately every 40 ms. It
reads its current leader immediately before each batch is sent. The client only
updates that leader after receiving the required leader-update messages for a
new view.

If a batch reaches a node that is in view change or is no longer the leader,
`HandleRequestMessage` logs the condition and drops the entire batch.

The logs contain:

- Periodic: 7 dropped batches, representing 700 transactions.
- Fixed: 58 dropped batches, representing 5,800 transactions.

The periodic run finishes 700 transactions short of 63,000, exactly matching
its seven logged dropped batches. The fixed run finishes 7,985 short. Its 5,800
explicitly dropped transactions explain most of the deficit; the remaining
2,185 are consistent with work abandoned in an old leader's queue or slots
that had not reached the prepared stage when view change began.

These losses are permanent in the current setup:

- `complete_suite` is false, so retry is not started after injection.
- The normal retry-timer start in `Client.Start` is commented out.
- Even if the retry worker runs, its call to `sendTransactions` is commented
  out, so it identifies candidates without retransmitting them.

This means the measured throughput difference includes both protocol recovery
cost and permanent client-request loss. It is not a pure measurement of PBFT
pipeline throughput.

## Fixed-Mode View-Change Storm After Progress Stops

The fixed run reaches sequence 55,015 and then makes no additional execution
progress. Nevertheless, it continues installing views 13 through 52 at
approximately 0.3-second intervals.

This loop happens because every installed view starts a fresh 300 ms grace
timer. The timer is not gated on whether client work is pending. If no
execution occurs during that grace period, the node suspects the new leader
and starts another view change, including when the workload is idle or the
remaining requests have already been lost.

There is also an experiment-specific cutoff in `handleViewTimerExpiry`: once
the last stable checkpoint reaches 61,000, timer expiration no longer initiates
view change. Periodic reaches this threshold; fixed stalls below it. This
explains why fixed continues cycling while periodic eventually stops reacting.

The post-workload view storm lowers the final average TPS reported by the
client sampler, but it does not explain the entire difference. Before the
storm, the fixed run already has more recovery gaps, more dropped batches, and
a lower commit rate.

## Causal Chain

The behavior observed in these runs is:

```text
100 ms global node-to-node delay spike
    -> execution gap exceeds the 100 ms suspicion threshold
    -> periodic records a shadow suspicion and preserves the view
    -> delayed traffic completes and produces a catch-up burst

100 ms global node-to-node delay spike
    -> execution gap exceeds the 100 ms suspicion threshold
    -> fixed mode falsely suspects a healthy leader
    -> old-view pipeline is interrupted
    -> some consensus traffic and queued work are abandoned
    -> client continues briefly sending to the old leader
    -> batches are dropped and not retried
    -> pipeline must be reconstructed in a new view
    -> average committed throughput falls
```

## Conclusion

Under these periodic global delay spikes, request-count leader rotation
outperforms a fixed 100 ms suspicion timer because the fixed timeout is
undersized for the disturbance. It causes false view changes, pipeline
disruption, client rerouting delays, and permanent request loss.

The result supports the following narrow claim:

> For this workload and repeating global-delay pattern, periodic view change
> achieves higher committed throughput because it tolerates transient delay
> spikes that the fixed 100 ms detector misclassifies as leader failure.

It does not establish that periodic view change is generally superior to a
properly sized failure detector. The current comparison is primarily evidence
that 100 ms is too aggressive under 100 ms injected one-way delay.

## Follow-up Run: One-Second New-View Grace and Client Retry

The retry-enabled fixed-mode run recorded in `logs/` from 11:18:55 through
11:20:29 uses a one-second deadline in `ViewTimerManager.StartView`, followed
by the normal 100 ms execution-gap deadline after execution is observed. The
netem schedule applies 150 ms delay for about 150 ms, then zero delay for 1.5
seconds. Including command overhead, spikes begin about 1.677 seconds apart,
not two seconds apart.

The long no-progress view-change chain is no longer present. Through view 55,
the node-1 throughput CSV has rows for every view except 19 and 35. Views 19
and 35 are isolated gaps, not a consecutive chain. The other replicas also
have no throughput rows for view 19. In view 35, however, nodes 2, 3, and 4
record four throughput samples and advance through sequence 45,750. Therefore,
view 35 is only a node-1 observation gap; the replica group does make progress
in that view. Views 56 and 57 occur at the end of the process lifetime, and
view 57 visibly reaches sequence 75,000 before shutdown, so missing/flushed
tail CSV rows must not be classified as a no-progress chain.

The remaining view-19 gap exposes an important timer interaction. Installing
view 19 arms the intended one-second grace. The NewView suffix has maximum
sequence 25,221 while replicas are at 25,220 or 25,221. A replica that is one
slot behind executes the carried/replayed sequence 25,221. `RecordExecution`
then immediately replaces the one-second deadline with
`executedAt + 100 ms`. All replicas expire view 19 at approximately 11:19:26.625
and enter view 20, only about 170 ms after the leader creates view 19. No
throughput is recorded in view 19, and view 20 resumes progress.

The same mechanism is especially clear on node 1 in view 35:

```text
11:19:51.686  node 1 installs view 35; NewView max sequence is 44,746
               while node 1 last executed 44,745
~11:19:51.739 node 1 executes the one carried suffix slot
11:19:51.839  its re-armed 100 ms timer expires; it requests view 36
11:19:51.919  the other replicas begin measuring progress in view 35
11:19:52.873  nodes 2--4 reach sequence 45,750 in view 35
```

This means the configured grace is not guaranteed to remain in force for one
second. The first execution of any kind ends it, including one replayed or
carried old-view slot. Such a slot proves that execution is technically
possible, but it does not prove that the new leader has established sustained
current-view progress or that a retried request has reached it.

The log message `View timer expired ... after 100ms` is accurate for these
early expiries: `RecordExecution` has changed the active deadline back to 100
ms. It does not show the one-second StartView deadline that was initially
armed.

To make the new-view grace mean a true minimum warm-up period, execution should
not be allowed to move the deadline earlier than `viewStartedAt + grace`.
Equivalent policies are to preserve
`max(viewStartedAt + grace, executedAt + steadyTimeout)`, or to leave warm-up
only after fresh current-view progress (rather than NewView-suffix replay) has
been observed. Requiring several executions or a short sustained-progress
window would be more robust than treating one carried slot as recovery.

## Recommended Follow-Up Experiment

To isolate the protocol effect more cleanly:

1. Restore actual client retry and start it during injection, not only after
   injection completes.
2. Gate the execution-gap detector on pending client or consensus work.
3. Remove the hard-coded 61,000 stable-checkpoint cutoff.
4. Run fixed timeout values of 100, 300, and 500 ms against the same periodic
   policy.
5. Align the first netem spike to the same experiment-relative time in every
   run.
6. Compare a fixed wall-clock interval rather than stopping based on workload
   completion.
7. Repeat each condition at least five times and report the mean, standard
   deviation, and confidence interval.
8. Record explicit view-change start/end times, ignored consensus-message
   counts, dropped client transaction counts, and prepared versus unprepared
   suffix sizes.
9. Include a no-delay baseline and a leader-specific impairment. The current
   global impairment demonstrates false suspicion avoidance; a leader-specific
   impairment is needed to test whether changing leaders itself is beneficial.

## Relevant Code and Data

- Netem schedule: `alt_run_project.sh`
- Experiment configuration: `config/run2new.json`
- View timer: `node/viewTimer.go`
- Fixed versus shadow timeout decision: `node/node.go`
- Periodic trigger: `node/execution.go` and `node/periodic.go`
- Old/current/future-view message handling: `node/node.go`
- Client request rejection: `node/receive.go`
- Client pacing and leader selection: `client/send.go`
- Retry behavior: `client/client.go` and `client/transactionmanager.go`
- Periodic results: `logs/`
- Fixed results: `singleepoch_fixed/`
