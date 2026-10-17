# LAN timers: why progress 150 ms and new-view 50 ms

On the 7-host LAN cluster the Fixed/Perf **leader-progress timeout is 150 ms** and the
**new-view timeout is 50 ms**. Both come from measuring what each timer has to wait through in
a healthy view change, then applying one rule: **1.5 × the p99.9 need, rounded up to a multiple
of 50 ms**.

| Timer | Worst need measured (p99.9 = max) | × 1.5 | Value | Headroom |
|---|---|---|---|---|
| leader progress (`fixed_trigger_timeout_ms`) | 81 ms | 121 ms | **150 ms** | 69 ms |
| new-view (`fixed_new_view_timeout_ms`) | 30 ms | 45 ms | **50 ms** | 20 ms |

Both stay well under what the 170 ms NetworkDelay scenario takes, so Fixed and Perf still
cascade there as intended: first progress after a NewView takes ≥ 345 ms under that delay, and
a NewView can't arrive within 50 ms.

Code: the timers in [`node/triggerManager.go`](../node/triggerManager.go) and
[`node/viewtimers.go`](../node/viewtimers.go), keys in [`config/timer.go`](../config/timer.go),
the measurement in [`node/progressstats.go`](../node/progressstats.go), the summary in
[`scripts/timer_budget_summary.py`](../scripts/timer_budget_summary.py). Data:
`results/t1000_run` (configs and run notes in [`config/timer_lan_n7/`](../config/timer_lan_n7/)).

## How it was measured

- **Runs:** five 10-min runs on the LAN (branch `timer-model`, 2026-10-16) with both timers at
  1 s, so no timer evicted a leader and no need was cut off at the timeout. The values come from
  the three Healthy runs (FRR, PFRR, PFE); the ProposalDelay and Throttle runs were used only to
  check the slow leaders fit under them.
- **What each node logs per view** (`node/progressstats.go`):
  - `PROGRESS NV` at install: `nv_timer_to_install_ms`, from the last start of this node's
    new-view timer to its install;
  - `PROGRESS VIEW` when it leaves the view: `first_exec_ms` (install → first executed slot) and
    `max_gap_ms` (the largest gap between two executed slots).
- **One sample** = one node in one installed view. The progress timer is a replica's timer (the
  leader never arms it on itself), so only replicas count for it. Healthy FRR changes view only at
  the 7 generation switches (7 views × 6 replicas = 42); PFRR and PFE have ~63 views per run (the
  Perf bar removes every leader after ~10 s): 792 replica views in all.
- **With 792 samples p99.9 is the largest one,** so in practice the rule sizes each timer on the
  worst view seen.

## Leader-progress timer: 150 ms

### What it waits through

A replica arms the timer when it installs a NewView and resets it on every executed slot
(`ResetOnExecution`). It expires if any single interval without an execution is longer than the
timeout:

```
install ──first_exec──▶ exec ─gap─▶ exec ─gap─▶ exec ··· exec ──tail──▶ view ends
   ▲ timer armed           ▲ every execution resets the timer
```

So per view the need is **max(first_exec, max_gap)**.

### First execution after install

Two cases, depending on whether the replica was behind when the view changed:

| First slot the replica executes after install | Views | p50 | p99 | max |
|---|---|---|---|---|
| a new slot (it had already executed the O-set) | 668 | 37 | 67 | 75 |
| a slot carried over in the NewView's O-set (it was behind) | 142 | 29 | 80 | 81 |

The new-slot case, broken into steps (from the per-slot execution traces and the leaders'
`STALL:` lines, same clock across hosts):

```
leader installs ─┬─ ~11 ms ─▶ replica installs           ← replica's timer armed here
                 └─ ~17 ms ─▶ leader gets its first client request
                                  └─ 7–89 ms ─▶ first new slot executed at the replica
```

| Step | p50 | p99 | max | What it is |
|---|---|---|---|---|
| replica installs after the leader | 11 | 24 | 28 | NewView transfer and processing; comes off the replica's wait |
| leader → first client request | 17 | 32 | 34 | the client learns the leader from the replicas' notices, waits for its next 24 ms tick, sends |
| first request → first new slot executed | 26 | 74 | 89 | batch, PrePrepare, prepare, commit, and waiting for the O-set re-run |
| **= replica install → first new slot** (17 + third row − 11, per view) | **33** | **70** | **81** | |

The third step is the slow one, and it grows with the O-set. Slots execute in order, so a new
slot can't execute until the O-set slots have been re-prepared and re-committed in the new view:

| O-set size (slots) | Views | first request → first new slot, p50 / p90 / max |
|---|---|---|
| 0–49 | 132 | 7 / 10 / 15 ms |
| 50–149 | 294 | 19 / 29 / 43 ms |
| 150–249 | 294 | 51 / 68 / 89 ms |
| 250+ | 6 | 67 / 78 / 78 ms |

The O-set holds the slots prepared since the last stable checkpoint (every 250 slots), so its
size depends on where in the checkpoint interval the view change lands.

### Largest gap between executions

The client sends 200 requests every 24 ms, which is 4 batches of 50. So slots execute in bursts
of about 4 per tick with nothing in between: an idle gap of ~20–27 ms between ticks is normal at
this load.

| max_gap of a view | Views |
|---|---|
| 25–30 ms | 722 (91%) |
| 30–40 ms | 34 |
| 40–60 ms | 18 |
| 60–78 ms | 18 |

No view has a gap under 25 ms: every tenure has at least one ordinary between-tick pause. None of
the 36 views with a gap of 40 ms or more had it in the first 2.5 s after install, so these are
later in the tenure, not part of the view change. Their cause (a late client tick, a slow round, a
process stall) isn't pinned down: the line records the largest gap, not when it happened. They
stay under the first-execution maximum, so they don't set the timeout.

### Per run and the value

| Run | Views | First exec p50 / max | Max gap p50 / p99 / max | Need p50 / p99 / max |
|---|---|---|---|---|
| healthy_frr (generation switches, 75 s tenures) | 42 | 44 / 65 | 28 / 61 / 61 | 50 / 65 / 65 |
| healthy_pfrr | 378 | 33 / 80 | 27 / 77 / 78 | 33 / 77 / 80 |
| healthy_pfe | 372 | 35 / 81 | 27 / 62 / 62 | 36 / 69 / 81 |
| **all** | **792** | **35 / 81** | **27 / 62 / 78** | **36 / 77 / 81** |

1.5 × 81 = 121 ms, rounded up to **150 ms**: 69 ms above the worst view. The margin covers what
three 10-min runs don't contain, mainly bigger O-sets (up to ~500 slots at the watermark, against
at most ~290 here) and rarer events.

### What it does not have to cover

- **The leader's own views:** the leader never arms the progress timer on itself.
- **The tail** (last execution → end of view): healthy views end by the Perf bar or a generation
  switch, not by stalling.
- **NetworkDelay:** there the timer is meant to expire; under 170 ms first progress takes ≥ 345 ms.
- **Slow leaders** are tuned to fit under it rather than the other way round. ProposalDelay
  (`proposal_delay_interval_ms`) and the Throttle gate (`proposal_min_interval_ms`) both space a
  leader's proposals at most one per 100 ms and let a tenure's first proposal go at once, so they
  bound only the gap: about 100 + 2–7 ms, under 150 with ≥ 43 ms to spare. A slow leader passes
  Fixed, and only the Perf bar removes it, which is what gives ProposalDelay and Throttle a Perf
  winner.

## New-view timer: 50 ms

### When it starts

The timer covers "the incoming primary delivers a valid NewView". When it is armed depends on the
policy:

- **RoundRobin:** at the replica's 2f+1 ViewChanges (`SelectRoundRobin`).
- **Election:** `ElectionLogic` only starts the VDF. The timer is armed on the candidate when its
  VDF finishes (`handleElectionVDFResult`) and on every other node when it grants its vote
  (`HandleRequestVoteMsg`).

It stops at install. `nv_timer_to_install_ms` is exactly that interval.

### Per run and the value

| Run | Replica samples | p50 | p99 | max |
|---|---|---|---|---|
| healthy_frr | 48 | 15.4 | 27.7 | 27.7 |
| healthy_pfrr | 384 | 11.9 | 26.3 | 28.3 |
| healthy_pfe | 378 | 13.5 | 24.7 | **30.1** |
| pdelay_pfrr (check) | 546 | 10.9 | 25.1 | 27.1 |
| throttle_pfe (check) | 1,128 | 10.9 | 22.9 | 23.9 |

The leader's and candidate's own timers are shorter (max 12 ms). Over the Healthy runs p99.9 =
max = 30.1 ms; 1.5 × 30.1 = 45 ms, rounded up to **50 ms**.

### RoundRobin: replica quorum → install (healthy_pfrr, 384 replica views)

| Step | p50 | p99 | max |
|---|---|---|---|
| primary reaches its quorum after this replica | 0.2 | 5.9 | 7.0 |
| primary builds and signs the NewView | 4.0 | 9.3 | 9.3 |
| NewView transfer (up to ~270 KB on the LAN) | 4.4 | 11.5 | 12.9 |
| replica resolves the ViewChange refs and runs `HandleNewView` | 3.0 | 7.4 | 8.0 |
| **total** | **11.9** | **26.4** | **28.3** |

### Election: vote grant → install (healthy_pfe, 378 voter views)

| Step | p50 | p99 | max |
|---|---|---|---|
| vote round: RequestVote arrives → candidate has 2f+1 grants | 2.2 | 4.4 | 4.6 |
| new leader builds and signs the NewView | 4.5 | 8.0 | 8.0 |
| NewView transfer | 5.2 | 11.4 | 15.6 |
| resolve refs + `HandleNewView` | 3.5 | 7.2 | 8.1 |
| **total** | **13.5** | **24.7** | **30.1** |

Step maxima fall in different views, so they don't add up to the total maximum.

**The VDF race is not in the Election timer.** Before the RequestVote there is the VDF: the
candidate's took 35 ms at the median and up to 73 ms here (`VDF evaluation ... completed in` debug
lines). It happens before any timer is armed, so Election's new-view need is the same size as
RoundRobin's. Measured from the 2f+1 ViewChanges instead (`quorum_to_install_ms`), Election takes
48–50 ms at the median and up to 96 ms. That figure includes the race, but nothing is timing it.

### What 50 ms does not cover

- **A ViewChange fetch retry.** If a replica is missing a ViewChange the NewView names, it waits
  `view_change_fetch_delay_ms` (20 ms), fetches it from the primary, and retries at 40 ms. The
  first fetch fits in 50 ms, a retry doesn't. No fetch happened in ~3,800 view changes; 32 NewViews
  waited briefly and all resolved within the 20 ms. A retry would cost one extra view change.
- **NetworkDelay:** at 170 ms one way a NewView can't arrive within 50 ms, so it cascades, as
  intended.

## If Election started its new-view timer at 2f+1, as RoundRobin does

The timer would then also have to cover the VDF race and the RequestVote round. That is the
`quorum_to_install_ms` figure:

| Run | Replica samples | p50 | p99 | max | longer than 50 ms |
|---|---|---|---|---|---|
| healthy_pfe | 378 | 47.8 | 89.6 | 95.6 | 178 (47%) |
| throttle_pfe | 1,128 | 49.6 | 86.6 | 90.2 | 555 (49%) |

- **At 50 ms, about half of PerformanceElection's view changes would time out.** Each timeout
  starts the next view, with a new VDF race that is equally likely to time out, so PFE would spend
  much of its time in view changes in every scenario and could not win Throttle. PeriodicElection
  is not affected: under Periodic both timers are the 10 s period.
- **To fit, the timeout would have to cover the race:** 1.5 × 96 ms → 150 ms. There is one
  new-view timeout for all policies (`fixed_new_view_timeout_ms`), so RoundRobin would then also
  wait 150 ms instead of 50 for a primary that never delivers its NewView.
- **The value would depend on the VDF settings and the CPU.** `min_vdf_delay` / `max_vdf_delay`
  are step counts (100–20,000 here), so the race takes 35–95 ms on these hosts and something else
  on others. It would have to be re-measured whenever either changes.
- **What it would gain: liveness when the candidate is silent.** Today, if the designated
  candidate never sends RequestVote (crashed, partitioned, or Byzantine), no node arms a new-view
  timer, and the progress timer was already stopped when the view change began, so every node
  waits forever. The experiments avoid this only because crashed nodes are never chosen as
  candidates (`electionCandidateForView`). Starting the timer at 2f+1 would turn a silent
  candidate into one timeout. Getting both would need a second, longer timer started at 2f+1
  just for the election phase, with the 50 ms timer kept for the NewView once votes are granted.
  Not implemented.

## Caveats

- **One 10-min run per arm** on this cluster's hosts; the values hold for this hardware and this
  load (200 requests per 24 ms, batch 50, n = 7).
- **The O-set re-run grows with the O-set,** and the runs saw at most ~290 slots against a cap
  of ~500 at the watermark; that is most of what the 1.5× margin is for.
- **The ProposalDelay runs here used the old blocking sleep;** ProposalDelay is now a
  non-blocking spacing, checked in [`config/timer_lan_n7_validate/`](../config/timer_lan_n7_validate/).

## Reproduce

```bash
python3 scripts/timer_budget_summary.py results/t1000_run/healthy_{frr,pfrr,pfe}_t1000
python3 scripts/timer_budget_summary.py results/t1000_run/{pdelay_pfrr,throttle_pfe}_t1000
```
