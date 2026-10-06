# Targeted proposal throttling: what the trigger plane does to a slow-to-retarget throttle

Experiment design + pre-registered predictions for the four configs in
`config/throttle_testing/`. Written before the runs; every number marked
**predicted** is a derivation from code, not a measurement.

The question the four arms answer is *not* "which policy evades the throttle" — it is
**which plane of the action decides**. The headline claim derived below is that under
these parameters the **trigger** plane dominates the **policy** plane, because the perf
trigger drives the tenure length down to just under `prep_ms` and the controller can
never finish re-aiming.

---

## 1. Parameters and what each one pins

All four configs are identical except `default_action` and `throttle.strategy`.

| Knob | Value | Where it bites |
|---|---|---|
| `node_num` | 7 | f = 2, quorum 5, perf window `3f+1` = 7 views, election pool 1..7 |
| `inject_speed` | 200 | offered 200/24 ms = **8 333 req/s** = **166.7 slots/s** at `max_batch_size` 50 |
| `proposal_min_interval_ms` | 100 | gated leader proposes 1 batch/100 ms = **10 slots/s** = **500 req/s** |
| `throttle.slots` | 2 | k = 2 (= f; set explicitly so a later `node_num` change cannot move it) |
| `throttle.prep_ms` | 3000 | a retargeted slot's gate goes live 3 s after the retarget |
| `throttle.tenure_ms` | 10000 | **inert at k ≥ 2** — only `onNoticeRoundRobin`'s `k == 1` branch reads it |
| `performance` | true | **required**: `newviewUpdatePerf` / `resetTimedPerfWindow` are gated on `cfg.Performance`, so without it the perf arms have no bar at all |
| `carry_state` | false | keeps NewView ~1.35 MiB / ~25 ms instead of 5.3 MiB / 135 ms (see §5.2) |
| `client_retry` | false | committed TPS stays the clean metric; drops land in `send_queue_dropped` |
| `epoch_mode`, `scenario_mode` | false | one fixed action for the whole run, no generation switching |

Two derived rates used throughout:

- **F = 163 slots/s** — unthrottled. The system is *not* saturated at `inject_speed`
  200: `docs/node-count-scaling.md` measures 8 152 committed tps at n = 7, 0 drops,
  1.02× over-driven. So F is the offered load, not a ceiling.
- **R = 10 slots/s** — throttled. A 16× cut, which is the whole point: it is far below
  any plausible perf bar, so a gated leader is always evicted on the next perf tick.

The severity (`proposal_min_interval_ms`) is deliberately *not* part of
`ThrottleConfig` (`config/throttle.go` header): it is identical across all four arms so
RoundRobin and Election are compared under the same gate.

### The perf bar, restated

`resetTimedPerfWindow` on each NewView sets

```
bar = 0.92 * max(viewThroughputs over the last 3f+1 = 7 views)     # targetThroughputMaxFactor
```

then the window opens `THROUGHPUTINTERVAL_DELAY` = 3 slots past `maxSeq`, and every
`perfTimerInterval` = 1 s `handlePerfTimerTimeout` compares the **average since the
window opened** against the bar: above → `bar *= 1.01`, at-or-below → immediate view
change. With recent views at F, **bar ≈ 0.92 × 163 ≈ 150 slots/s**.

Convenient robustness: `observeExecutedSlotForThroughput` only writes
`viewThroughputs[view]` at `seq % CHECKPOINT_INTERVAL == 0` boundaries. A throttled
view executes 10–20 slots, so it almost never crosses a 250 boundary and records
**nothing** — which is why the bar stays near 0.92 F instead of decaying toward R. And
if all 7 window views record nothing, `maxRecentViewThroughput` falls back to
`defaultTargetThroughput` = 0.92 × 160 = **147.2**, essentially the same number. The
prediction does not depend on which path is taken.

---

## 2. The coverage law for RoundRobin at k = 2

From `onNoticeRoundRobin` → `assignToDesired`, the desired set at the notice for
counter *c* is `{c, c+1}`. Tracing three consecutive notices:

- notice *c*: slot holding *c* keeps it (`heldWanted`); the other slot retargets to
  *c+1*, `readyAt = now + prep`.
- notice *c+1*: *c+1* is still wanted → kept, prep **not** restarted; the stale slot
  retargets to *c+2*.
- notice *c+2*: *c+1* leaves the window → released.

So node *c+1* owns a slot for the whole interval `[notice_c, notice_{c+2}]` = **2τ**,
and it leads during the second half of it. Writing τ for the tenure length:

```
gate goes live (prep - τ) into c+1's own tenure
  prep <= τ    -> gated for 100% of the tenure
  τ < prep < 2τ -> gated for (2τ - prep) of τ
  prep >= 2τ   -> never gated at all
```

**This single inequality is the experiment.** `prep` is fixed at 3 s; τ is set by the
trigger plane. Periodic pins τ = 10 s > prep. Perf lets τ float — and, as §4 shows, it
floats to ≈ 2 s, where 2τ ≈ prep.

---

## 3. The periodic arms (the hypothesis as stated)

### 3.1 `thr_prr_n7` — PeriodicRoundRobin

τ = `PeriodicTriggerTimeout` = 10 s, pinned: under `PeriodicTrigger`,
`ResetOnExecution` resets the leader-progress timer only at `seq == 1`, so the timer
runs the full 10 s from `acceptNewViewTimers`. prep 3 s ≤ τ → the successor's gate is
live **7 s before it even takes over**.

> **Predicted: 100% of tenures throttled, ~10 slots/s ≈ 500 req/s.** Confirms the
> hypothesis. VC dead time is negligible against a 10 s tenure.

Startup transient (2 tenures, ~20 s), from the `sendLeaderIdUpdate` gotcha: there is no
leader update for genesis view (1,1), so node 1's first 10 s is **completely ungated**
and absent from `leader_timeline.jsonl`; the first notice is for (1,2) → node 2, whose
prep starts only once it is already sitting, so it is gated for 7 s of 10. Node 3
onward is steady state. `scripts/analyze_throttle.py --skip-tenures 1` already drops
exactly the right window (its default; see that script's docstring).

### 3.2 `thr_pelec_n7` — PeriodicElection

`electionCandidateForView` hashes `view-<gen>-<counter>` and draws uniformly from 1..7
(no `nodes_dead` here), independently per view. `onNoticeElection` is reactive: camp if
a slot `holds()` the new leader, else evict the stalest. In steady state the two slots
hold the last two **distinct** leaders.

| Draw | P | Tenure profile | Slots executed |
|---|---|---|---|
| leader already held | 2/7 | gate already live → 10 s at R | 100 |
| leader not held | 5/7 | retarget → 3 s at F, then 7 s at R | 3·163 + 7·10 = 559 |

> **Predicted: 78.6% of tenure time throttled, ≈ 42.8 slots/s ≈ 2 140 req/s** —
> **≈ 4.3× PeriodicRoundRobin.** This is the user's "2/7 compromised, 5/7 we get high
> tput while the slot preps", quantified.

No backlog burst inflates that 3 s: `pendingRequests` is cleared on every new-view
install, and the client discards the old leader's send queue (`DiscardQueued`), so the
fresh leader simply sees the offered 8 333 req/s.

Variance source: a repeat draw onto a slot that is still *preparing* camps without
restarting prep (`onNoticeElection` leaves `readyAt` alone, by design), giving a
partially-gated tenure that is neither row above. Notices arrive every ~10 s and only
5/7 evict, so a slot lives ~35 s ≫ prep — this should be rare.

---

## 4. The perf arms — the part that was open, and the result is a reversal

Both perf arms keep the 150 ms leader-progress floor underneath. A gated leader
proposes every 100 ms and replicas reset on **every** executed slot
(`ResetOnExecution` under `PerfTrigger`), so **the 100 ms gate survives the 150 ms
floor with 50 ms of margin** — exactly as expected. The bar, not the floor, does the
evicting. (That margin is thin; see §5.1.)

### 4.1 `thr_perfrr_n7` — PerformanceRoundRobin: τ ≈ 2 s, fixed point or 4-cycle

Start from a long tenure (the ~12 s a healthy perf leader gets: the bar starts at 92%
and compounds 1%/s, so it passes 100% of achievable after ~11 raises). With τ = 12 s >
prep = 3 s, §2 says the **successor is gated for its entire tenure** → it runs at R =
10 against a bar of ~150 → evicted on its first tick. Its 3-slot window takes 0.3 s at R,
so **τ collapses to 1.3 + d**.

But after a 1.3 s tenure, the next leader's gate lands 3 − 1.3 = 1.7 s into its own
tenure. It runs at full F until then, beats the bar at the 1 s tick, and survives, so τ
grows again. The system cannot stay in either regime. The rest of this section works
out where it ends up.

#### Solving for x

Notation, all times measured from the leader's NewView install:

- *u* = prep − τ_prev — when this leader's gate goes live. By §2, its slot was
  retargeted at the previous leader's notice, `τ_prev` before this install. If *u* ≤ 0
  the leader is gated from the start.
- *d* — VC dead time plus noise (including the ~0.02 s the 3-slot window takes at F),
  lumped into one number and added to every tenure.
- **Flat 0.3 s** — only for a leader gated from the start: its 3-slot window
  (`THROUGHPUTINTERVAL_DELAY`) takes 3/R = 0.3 s, so its ticks come 0.3 s later. This
  one is not noise. It decides which steady state the system reaches.
- Perf ticks at 1 s, 2 s, 3 s, … (plus 0.3 s if gated from the start).
- *B* = 0.92 · 163 = 150 at the first tick, × 1.01 after every tick the leader survives
  (150 → 151.5 → 153.0).

The tick at time *x* compares the average since install against B: the leader runs at F
for *u* seconds, then at R for the rest. Set the average equal to the bar and solve:

```
u·F + (x − u)·R = B·x
u·(F − R)       = x·(B − R)
x = u · (F−R)/(B−R) = u · 153/140 = 1.093·u
```

Since B is 92% of the way from R up to F, the average falls to the bar only about
0.09·u seconds after the gate lands. The leader is effectively dead as soon as it is
gated, and is evicted at the first whole-second tick at or after *x*. The next tenure is
then:

```
τ = (eviction tick) + d             ungated at install
τ = 0.3 + 1 + d                     gated from the start (always evicted at tick 1)
```

#### Steady state 1: fixed point τ = 2 + d (d < 0.085 s)

| step | τ_prev | u = 3 − τ_prev | x = 1.093·u | tick 1 | tick 2 | evicted | τ |
|---|---|---|---|---|---|---|---|
| 1 | 12 | gated from start | — | 10 ≤ 150 | — | tick 1 | **1.3 + d** |
| 2 | 1.3 + d | 1.7 − d | ≈ 1.86 | 163 > 150 | ≈ 140 ≤ 151.5 | tick 2 | **2 + d** |
| 3 | 2 + d | 1 − d | ≈ 1.09 | 163 − 153·d vs 150 | 86.5 − 76.5·d ≤ 151.5 | tick 2 | **2 + d** |

Step 3 survives tick 1 only if the gate lands late enough in the first second:

```
(1 − d)·163 + d·10 > 150   →   163 − 153·d > 150   →   d < 0.085 s
```

If it survives, the leader is evicted at tick 2 and τ = 2 + d repeats. Each tenure is
1 − d s ungated and 1 + d s gated:

```
throttled = (1 + d)/(2 + d) ≈ 50–52%
slots/s   = [(1 − d)·163 + (1 + d)·10] / (2 + d) = (173 − 153·d) / (2 + d)
```

#### Steady state 2: 4-cycle 1.3 → 2 → 1 → 3 (0.085 < d < 0.15 s)

With d over 0.085, step 3 fails tick 1. That 1 s tenure gives the next leader 2 − d s
ungated, so it survives to tick 3. That 3 s tenure makes the leader after it gated from
the start, which brings the system back to step 1:

| leader | τ_prev | u = 3 − τ_prev | gated for | evicted | τ |
|---|---|---|---|---|---|
| A | 3 + d | −d (gated from start) | all 1.3 s | tick 1 | **1.3 + d** |
| B | 1.3 + d | 1.7 − d | 0.3 + d | tick 2 | **2 + d** |
| C | 2 + d | 1 − d | d | **tick 1** | **1 + d** |
| D | 1 + d | 2 − d | 1 + d | tick 3 | **3 + d** |

```
cycle time = 7.3 + 4d       gated = 2.6 + 3d     →  throttled ≈ 38%
slots      = 13 + 163·(4.7 − 3d) + 10·(1.3 + 3d) = 792.1 − 459·d
```

The cycle holds while D survives tick 2: (2 − d)·163 + d·10 > 2 · 151.5, so d < 0.15 s.

Trace from the 12 s start at d = 0.10 (bold rows are where it leaves the fixed point):

| step | τ_prev | u = 3 − τ_prev | x = 1.093·u | tick 1 avg / bar | tick 2 avg / bar | tick 3 avg / bar | evicted | τ |
|---|---|---|---|---|---|---|---|---|
| 1 | 12.00 | −9.00 (gated from start) | — | 10.0 ≤ 150.0 | — | — | tick 1 | 0.3 + 1 + 0.10 = **1.40** |
| 2 | 1.40 | 1.60 | 1.75 | 163.0 > 150.0 | 132.4 ≤ 151.5 | — | tick 2 | 2 + 0.10 = **2.10** |
| **3** | **2.10** | **0.90** | **0.98** | **147.7 ≤ 150.0** | — | — | **tick 1** | 1 + 0.10 = **1.10** |
| **4** | **1.10** | **1.90** | **2.08** | 163.0 > 150.0 | 155.3 > 151.5 | 106.9 ≤ 153.0 | **tick 3** | 3 + 0.10 = **3.10** |
| 5 | 3.10 | −0.10 (gated from start) | — | 10.0 ≤ 150.0 | — | — | tick 1 | 0.3 + 1 + 0.10 = **1.40** |
| 6 | 1.40 | 1.60 | 1.75 | 163.0 > 150.0 | 132.4 ≤ 151.5 | — | tick 2 | 2 + 0.10 = **2.10** |

This is the path §7.3 measured for views (1,3) → (1,4) → (1,5): gated from slot one and
evicted at tick 1, then evicted at tick 2, then at tick 1 (126.9 < 152.4).

#### By d

| d | Steady state | Tenures | Mean τ | Throttled | Slots/s | req/s |
|---|---|---|---|---|---|---|
| 0 | fixed | 2.00 | 2.00 | 50% | 86.5 | 4 325 |
| 0.01 | fixed | 2.01 | 2.01 | 50% | 85.3 | 4 265 |
| 0.05 | fixed | 2.05 | 2.05 | 51% | 80.7 | 4 033 |
| 0.08 | fixed | 2.08 | 2.08 | 52% | 77.3 | 3 864 |
| 0.09 | 4-cycle | 1.39 → 2.09 → 1.09 → 3.09 | 1.92 | 37% | 98.0 | 4 901 |
| 0.10 | 4-cycle | 1.40 → 2.10 → 1.10 → 3.10 | 1.92 | 38% | 96.9 | 4 845 |
| 0.14 | 4-cycle | 1.44 → 2.14 → 1.14 → 3.14 | 1.97 | 38% | 92.6 | 4 630 |

Mean τ stays about 2 s (< prep) in both regimes, so the headline in §4.3 holds: the perf
trigger rotates faster than the controller can re-aim. Crossing d = 0.085 s drops the
throttled fraction from ~50% to ~38%.

> **Predicted: mean τ ≈ 2 s; ~50% throttled, ≈ 77–86 slots/s ≈ 3 900–4 300 req/s if VC
> dead time is under 85 ms; ~38% throttled, ≈ 93–98 slots/s ≈ 4 600–4 900 req/s if it
> is 85–150 ms** — **≈ 8–10× PeriodicRoundRobin and ≈ 2× PeriodicElection.** In
> `leader_timeline.jsonl`, a steady ~2 s tenure means the fixed point; a repeating
> 1.3 / 2 / 1 / 3 s pattern means the 4-cycle.

Second, independent leak in the same direction: `sendLeaderIdUpdate` fires only on a
NewView **install**. If a 150 ms new-view timer expires and the counter is skipped, the
controller never hears about that counter at all. The window does not stay behind — the
next notice resyncs it — but the leader right after the skip was never prepped. Suppose
notice *c+2* is skipped:

- after notice *c+1* the slots hold `{c+1, c+2}` (*c+1* live, *c+2* preparing);
- notice *c+2* never arrives, and *c+2* never leads, so its prep was wasted;
- at notice *c+3*, `desired = {c+3, c+4}`. Neither slot holds a wanted node
  (`heldWanted` → 0; with n = 7 there is no wrap-around), so both are free and both are
  retargeted, `readyAt = now + prep`, while `reconcile` disables *c+1* and *c+2*.

| Leader | Normal (§2 law) | After the skip |
|---|---|---|
| *c+3* | prepped since notice *c+2*, gated `prep − τ` into its tenure | **not prepped ahead**, so it runs ungated for its first `prep` = 3 s |
| *c+4* | prepped since notice *c+3* | unchanged, so the law holds again |

So each skip costs exactly one tenure, the one right after it. Under Periodic (τ = 10 s)
that tenure falls from fully gated to 3 s at F + 7 s at R (the Election "not held" row).
Under Perf (τ ≈ 2 s < prep), it falls from about 1 s gated to **never gated**. Perf's
fast rotation also makes skips more likely (§5.2).

#### 4.1.1 Original model (no d)

The derivation as first written. It is the model above at d = 0 (fixed point τ = 2), and
it does not cover the 4-cycle.

Start from a long tenure (the ~12 s a healthy perf leader gets: the bar starts at 92%
and compounds 1%/s, so it passes 100% of achievable after ~11 raises). With τ = 12 s >
prep = 3 s, §2 says the **successor is gated for its entire tenure** → it runs at R =
10 against a bar of ~150 → evicted on its first tick. Window opens 3 slots = 300 ms
after install at R, so **τ collapses to ~1.3 s**.

But at τ = 1.3 s, 2τ = 2.6 s < prep = 3 s → §2's third case → **the gate never goes
live at all**. The next leader runs at full F, beats the bar, and survives — τ grows
again. The system cannot sit in either regime, so solve for the fixed point.

A leader ungated for its first *u* = prep − τ_prev seconds, then gated, is evicted at
the first 1 s tick where the running average drops below the bar B:

```
u*F + (x-u)*R = B*x   ->   x = u*(F-R)/(B-R) = u*153/140 = 1.093*u
```

τ is that *x* rounded up to the next tick. Iterating: τ_prev = 2 → u = 1 → x = 1.09 →
τ = 2. **Self-consistent at τ ≈ 2 s with u ≈ 1 s.** Sanity-check the eviction at τ = 2:
tick at 1 s sees avg = 163 > bar → survive, bar → 151.5; tick at 2 s sees
(1·163 + 1·10)/2 = 86.5 < 151.5 → evicted. ✓

> **Predicted: ~50% of tenure time throttled, ≈ 86.5 slots/s gross; minus one VC per
> 2 s (~100 ms of dead time, ~5%) → ≈ 82 slots/s ≈ 4 100 req/s** —
> **≈ 8× PeriodicRoundRobin and ≈ 2× PeriodicElection.**

### 4.2 `thr_perfelec_n7` — PerformanceElection: reactive prep never lands

Reactive controller, so the 2/7 ÷ 5/7 split of §3.2 applies, but now the tenure is set
by the bar rather than pinned at 10 s:

| Draw | P | Tenure profile | τ | Slots |
|---|---|---|---|---|
| not held (5/7) | 5/7 | retarget → prep 3 s. Ungated, so it beats the bar through the 1 s, 2 s and 3 s ticks (bar 150 → 154.5, actual 163). Gate lands at +3 s; the 4 s tick sees (3·163 + 1·10)/4 = 124.8 < 156 → evict | **4 s** | 499 |
| already held (2/7) | 2/7 | gate live from the start → R from slot one → evicted on the first tick, window opens 300 ms in | **~1.3 s** | ~13 |

Per 7 tenures: 22.6 s, 2 521 slots.

> **Predicted: ~33.6% of tenure time throttled, ≈ 111.6 slots/s gross; minus one VC per
> 3.2 s (~3%) → ≈ 108 slots/s ≈ 5 400 req/s. The best of the four.**

### 4.3 Predicted ranking

| Config | Action | Throttled fraction | Slots/s | req/s | vs PRR |
|---|---|---|---|---|---|
| `thr_perfelec_n7` | PerformanceElection | ~34% | **~108** | ~5 400 | **10.8×** |
| `thr_perfrr_n7` | PerformanceRoundRobin, d < 85 ms (fixed point) | ~50% | ~77–86 | ~3 900–4 300 | 7.7–8.6× |
| `thr_perfrr_n7` | PerformanceRoundRobin, d = 85–150 ms (4-cycle) | ~38% | ~93–98 | ~4 600–4 900 | 9.3–9.8× |
| `thr_pelec_n7` | PeriodicElection | ~79% | ~43 | ~2 140 | 4.3× |
| `thr_prr_n7` | PeriodicRoundRobin | **100%** | ~10 | ~500 | 1× |

d is the VC dead time (§4.1). PerfElec ranks first in both PerfRR regimes, but in the
4-cycle its lead shrinks from ~25% to 10–14%.

**The finding, if the runs bear it out: the perf trigger beats the throttle not by
catching a slow leader but by rotating faster than the controller can re-aim.** Under
Periodic, τ = 10 s ≥ prep and §2's law gates RoundRobin for 100% of every tenure. Under
Perf, τ settles near 2 s, inside §2's middle case (τ < prep < 2τ), where the law gives
(2τ − prep)/τ ≈ 50%, or ~38% if VC dead time pushes PerfRR into the 4-cycle. The trigger
plane therefore dominates the policy plane here, and the policy plane only re-ranks
*within* a trigger. Election > RoundRobin in both, because Election's prep can only start
at the leader's own notice, giving every fresh leader the full 3 s ungated, while
RoundRobin's prep starts one notice early and leaves only 3 − τ_prev (0 s under Periodic,
~1 s under Perf).

The measured ranking (§8.5) puts PerfRR above PerfElec. That flip comes from the
`viewThroughputs` outlier artifact that pushes PerfRR's bar above F (§8.4), not from this
model.

### 4.4 Consequence for the testbed's framing

This scenario, at `prep_ms` 3000, does **not** hand PeriodicElection a unique win — it
hands it third place. If the goal is a scenario whose unique optimum is
`PeriodicElection`, the discriminating variable is `prep_ms` relative to the perf
trigger's self-tuned τ ≈ 2 s, and 3000 is on the wrong side of it. The natural
follow-up is a `prep_ms` sweep on these same four configs, nothing else changed:

| `prep_ms` | vs periodic τ = 10 s | vs perf τ ≈ 2 s | Expected shape |
|---|---|---|---|
| 500 | ≤ τ → 100% gated | ≤ τ → ~100% gated | all four crushed; isolates VC overhead |
| 1500 | 100% gated | ≈ τ → partial | perf's edge appears |
| **3000** | 100% gated | ≥ 2τ → ~0% gated | the four configs here |
| 8000 | still ≤ τ → 100% gated | ≫ 2τ → 0% gated | widest perf/periodic gap |

That sweep is the actual contribution — a single scenario where the optimal action
*moves* as one adversary parameter moves is worth more to the bandit story than a
scenario with one fixed winner.

---

## 5. Risks to check in the logs before trusting any of the above

### 5.1 The 150 ms progress floor has only 50 ms of margin (perf arms)

The gate spaces proposals 100 ms; `FixedTriggerTimeout` = 150 ms. Consensus jitter, a
GC pass or a checkpoint pause pushing one inter-execution gap past 150 ms removes the
leader by the **progress timer** instead of the bar, which breaks the attribution in
§4. Count the two causes:

```bash
grep -c "Leader progress timer expired" logs/node_*.log
grep -c "below timed target" logs/node_*.log
```

If progress-timer VCs are not a small minority, the gate severity has to move — and it
must move in **all four** configs together, since `config/throttle.go` keeps severity
policy-independent on purpose.

### 5.2 The 150 ms new-view timer at n = 7 (perf arms)

`docs/newview-cost-and-viewchange-storm.md` is the cautionary case: a 90 ms timer could
not fit one view-change round trip and the system self-sustained a storm. `carry_state:
false` is the mitigation and is set — 253 certs cost 1.35 MiB / 24.8 ms instead of
5.28 MiB / 134.7 ms. But that table is n = 4, where the VC log carries 3 ViewChanges;
at n = 7 it carries 2f+1 = 5, so expect roughly 2× that. ~50 ms against a 150 ms timer
is fine, but it is the same margin that produced the storm. Check:

```bash
grep -c "new view timer" logs/node_*.log
```

A storm would also show up as `leader_timeline.jsonl` counters advancing by more than 1
between rows.

### 5.3 Drops are expected and are not an error

At 8 333 offered vs 500 drained, the 8-batch per-node send queue fills in ~24 ms and
the client drops the rest. With `client_retry: false` those are simply lost. **Compare
`committed_total` rates from `report.json`, never offered load**, and read
`send_queue_dropped` / `send_queue_discarded` in `latencyreport.json` as a sanity check
rather than a failure.

### 5.4 Run length: the binding constraint is `thr_pelec_n7`, not the clock

The tenure count is trivially `duration / τ`, minus the 2 startup tenures of §3.1. The
unit that matters is not tenures, though — it is **how many Bernoulli draws the
prediction rests on**, and that differs per arm:

| Arm | τ | Prediction rests on | Needs many tenures? |
|---|---|---|---|
| `thr_prr_n7` | 10 s | §2's coverage law — **deterministic**, 100% gated | No |
| `thr_perfrr_n7` | ~2 s | the τ ≈ 2 s fixed point — **deterministic** | No, and gets ~120 |
| `thr_perfelec_n7` | ~3.2 s avg | the 2/7 ÷ 5/7 election draw | Yes, and gets ~73 |
| `thr_pelec_n7` | 10 s | the 2/7 ÷ 5/7 election draw | **Yes, and gets only ~22** |

Only `thr_pelec_n7` is both draw-dependent *and* slow-rotating. Its rate is linear in
the realised held-fraction *q*: `rate = 55.9 − 45.9q` slots/s, so sampling error on *q*
passes straight through to the headline number.

| Duration | Usable tenures | Rotations at n = 7 | sd(*q*) | sd(rate) | 2σ band |
|---|---|---|---|---|---|
| 240 s | 22 | **3.1** | 9.6 pp | 4.4 (10% of mean) | **34–52 slots/s** |
| 900 s | 88 | 12.6 | 4.8 pp | 2.2 (5%) | 38–47 slots/s |

At 240 s the 2σ band on PeriodicElection is 34–52 slots/s. That is wide enough to be
embarrassing but still nowhere near `thr_perfrr_n7` at ~82, so **the ranking in §4.3
survives 240 s; only the PeriodicElection *number* does not**. Reaching 5% 1σ needs ~94
tenures ≈ **16 min**.

Three rotations is also too few to separate the draw from per-leader asymmetry — the 7
nodes share one box and are not interchangeable, and at ~3 tenures per node a single
unlucky leader moves the mean.

So: **900 s for all four**, which costs an hour and makes the arms directly comparable.
`thr_prr_n7` and `thr_perfrr_n7` would be fine at 240 s, but uniformity is cheap (logs
run ~0.5–1 MiB per run; `results/tps_n7` is 488 K). Drop to 240 s only for a smoke test
that the gate engages at all.

Even at 900 s these are single runs per arm. The PeriodicElection ↔ PerformanceRR gap
(~43 vs ~82) is the one that could close under variance; if those two land within ~20%
of each other, that pair needs repeats before either is reported.

---

## 6. Running them

```bash
./run_experiments.sh 900 \
  config/throttle_testing/thr_prr_n7.json \
  config/throttle_testing/thr_pelec_n7.json \
  config/throttle_testing/thr_perfrr_n7.json \
  config/throttle_testing/thr_perfelec_n7.json

for a in thr_prr_n7 thr_pelec_n7 thr_perfrr_n7 thr_perfelec_n7; do
  echo "== $a"; python3 scripts/analyze_throttle.py --logs "results/$a"
done
```

900 s per arm, 1 h total — set by `thr_pelec_n7`, which at 10 s tenures gets only 3.1
rotations of the 7-node schedule in 240 s (§5.4). The other three arms do not need it.
`run_experiments.sh` starts its sleep immediately after the client window opens, so the
duration is consensus time, not wall-clock setup.

Results land in `results/<config basename>/`, with the config copied in beside the
logs.

---

## 7. Results (runs of 5 Oct 2026)

Four parallel 1 h 30 m runs via `scripts/run_isolated.sh`, placement A = `thr_prr_n7`,
B = `thr_pelec_n7` (socket 0), C = `thr_perfrr_n7`, D = `thr_perfelec_n7` (socket 1).
Data in `results/thr_*_n7_20261005_*/`. Headline numbers from
`scripts/analyze_throttle.py --logs <dir>`; the rest from the node logs as noted.

### 7.1 Headline

| Arm | Predicted req/s | **Measured req/s** | Pred. throttled | **Meas. throttled** | Tenures | Verdict |
|---|---|---|---|---|---|---|
| PeriodicRoundRobin | ~500 | **506** | 100% | **100.0%** | 529 | ✓ exact |
| PeriodicElection | ~2 140 | **2 150** | 78.6% | **78.2%** | 526 | ✓ exact |
| PerformanceRoundRobin | ~4 100 | **26** | ~50% | (88.8%, meaningless) | 278 | ✗ **livelocked at t ≈ 100 s** |
| PerformanceElection | ~5 400 | **3 044** | ~34% | **23.8%** | 3 564 | ranking ✓, level 56% of predicted |

Preconditions held:

- **F under the pinned budget is the same as the uncapped baseline.** Ungated views
  measured 162–169 slots/s (PerfRR genesis 162.2, view (1,4) 168.9), vs 163 assumed. 8
  physical cores per run sustain the offered 8 333 req/s even with both perf arms
  sharing socket 1.
- **The gate is precise, so risk §5.1 did not materialise.** Gated proposal spacing
  (`GATE_PROPOSE since_last`) has p50 100.6 / p99 101.1 ms in PRR, PElec and PerfElec;
  > 150 ms in 0.03–0.26% of proposals.
- **Election held-fraction matches 2/7.** PElec 139/527 = 26.4%, PerfElec 1 017/3 565 =
  28.5%, vs 28.6%.
- Controller notice lag ≤ 7 ms, 0 gate command failures in all four.

### 7.2 Periodic arms: hypothesis confirmed

PeriodicRoundRobin is throttled for 100.0% of every tenure on every node (75–76 tenures
per node), committing 487–490 req/s flat across all 18 five-minute buckets. §2's coverage
law at `prep ≤ τ` holds without exception.

PeriodicElection lands within 1% of the 2/7 ÷ 5/7 model on both throttled fraction and
throughput, and per-node throttled fraction is uniform (77.3–79.0%). Five-minute buckets
range 1 793–2 548 req/s; the 1 h 30 m mean is the number to quote. **Election beats
RoundRobin 4.2× under the periodic trigger.**

### 7.3 PerformanceRoundRobin: modelled correctly, then killed by a view-change storm

**The first five views follow §4.1 exactly** (node 3's log):

| View | Leader | Observed | Model |
|---|---|---|---|
| (1,1) | 1 | ungated, 162 slots/s, evicted at 11 s when the bar crossed | ~12 s |
| (1,2) | 2 | gate on 3 s after install (first notice), evicted at 4 s | as §3.1 transient |
| (1,3) | 3 | gated from slot one, **8.99 slots/s**, evicted on the first tick | Regime A, τ ≈ 1.3 s |
| (1,4) | 4 | ungated (prep could not finish), evicted at the 2 s tick, 133.9 < 151.8 | **τ ≈ 2 s fixed point** |
| (1,5) | 5 | evicted at 1 s, 126.9 < 152.4 | |
| (1,6) | 6 | **never installs: first new-view timer expiry** | §5.2 |

From (1,6) on, the run never recovers. Counter reaches 9 625, but only 279 views install
in 1 h 30 m. Node 3 alone logs 6 662 new-view timer expiries, 489 progress timeouts, and
**5** perf evictions. Commits stop at **2 755 slots = 137 750 requests**, the run's total.
Execution is wedged at seq ≈ 2750 from 00:34:06 onward, re-proposed view after view.

The storm's trigger, from node 6's log at (1,6):

1. Every ViewChange carries `lastExecuted − stableCheckpoint` prepared certs. At view 6
   that was **208 certs, 0.46 MiB per VC**: the stable checkpoint was 2500 and execution
   had reached ~2710, near the top of the 250-slot checkpoint sawtooth.
2. Node 6 had all VCs by 56.635. Processing them took 90 ms; the NewView sends took
   11–36 ms each. **VC → NewView ≈ 250 ms, against a 150 ms new-view timer.** Replicas
   moved on before it landed.
3. Once storming, execution cannot reach 2750, so **the stable checkpoint stays at 2500
   for the rest of the run**. Every later VC carries ~255 certs (0.57 MiB), the maximum,
   which keeps the round trip over budget. Self-sustaining.

This is the mechanism in `docs/newview-cost-and-viewchange-storm.md` (stable checkpoint
pinned → maximal VC/NewView → timer can never fit a round trip), reached here at n = 7
with `carry_state: false`. §5.2 flagged it as the open risk; it fired on the sixth view.

**Not yet established:** why views that *do* install still fail. Installed leaders hit the
progress timer 150–300 ms after install without committing 2750. Leader 3 at (1,304) got
client requests 34 µs after becoming leader and never proposed. The candidates are the
two **silent** early returns in `tryPropose` (`node/node.go:437–445`):
`pendingRequests < max_batch_size` and `inflight >= max_inflight_seq`. Per-node latency
counts split into two groups ~40 apart (nodes 1–3 ≈ 2 708, nodes 5–7 ≈ 2 746), which
fits a lagging leader blocked by `inflight >= 40`. But latency-count is not
`lastExecuted`, and that value is never logged. Confirming it needs a log line on those
returns.

**This arm's 26 req/s is a liveness failure, not a throttle result, and must not be
reported as one.** The throttle's role is indirect: it causes the fast rotation that puts
a VC near the top of the checkpoint sawtooth.

### 7.4 PerformanceElection: the best arm, held back by dead-on-arrival views

PerfElec ranks first as predicted: **6.0× PRR, 1.4× PElec**, with the lowest throttled
fraction of the four (23.8%; per node 22.6–25.2%). It also survived 272 new-view timer
expiries without locking up. But it reaches only 56% of the predicted throughput, and
mean tenure is 1.5 s rather than 3.2 s.

The view-end causes on node 3 are 2 106 progress timeouts, 792 perf evictions and 272
new-view expiries. The bar was supposed to be the main evictor, but the progress floor
evicts 2.7× more often, **and not because of the gate** (see §7.1). Timing each progress
timeout from that node's install:

| ms since install | share of 2 106 progress timeouts |
|---|---|
| 150–200 | 78.3% |
| 200–300 | 17.6% |
| 300–500 | 3.7% |
| > 500 | 0.4% |

**96% are dead-on-arrival views**: the new leader never commits one slot inside the 150 ms
budget. The cause is client redirection latency. The client switches to a new leader only
after 2f = 4 `LeaderIdUpdate`s (`client/receive.go`), so the first client request reaches
the new leader **p50 94 ms, p90 162 ms** after it becomes leader (48% > 100 ms). Add
batching, three phases at n = 7 and execution, and roughly half of all installs run out of
budget. The periodic arms see the same 60–90 ms redirection delay, but with a 10 s budget
it costs them nothing.

This is an interaction between the **trigger plane and the client**, independent of the
throttle. Throttling only raises the view-change rate, so DOA views happen more often.
Each DOA view is also a leader notice, which retargets a slot, so the extra churn pushes
the throttled fraction *down* (23.8% vs 34% predicted). That partly offsets the lost
throughput.

Also not established: why PerfElec recovered from its 272 new-view expiries while PerfRR
never did. The candidates are luck in where VCs land on the checkpoint sawtooth, or the
Election VC path's different timing. One run per arm cannot tell them apart.

### 7.5 What this means for the claim

- **The periodic result is solid and is the clean publishable comparison**: under a 3 s
  prep throttle, PeriodicElection beats PeriodicRoundRobin 4.2×. The 2/7 reactive-camping
  model predicts both arms to within 1%.
- **The perf "rotate faster than the controller can re-aim" mechanism is real.** It
  appears directly in PerfRR's views 1–5 (τ ≈ 2 s fixed point) and in PerfElec having
  the lowest throttled fraction. **But at n = 7 with 150 ms timers the perf trigger is
  fragile.** One perf arm livelocked, and the other lost ~44% of its throughput to
  dead-on-arrival views. Neither failure is caused by the throttle.
- From the bandit's point of view, PerfRR at ~0 tps and PerfElec at 3 044 req/s are
  legitimate arm outcomes in this scenario. As evidence about *throttling*, though, the
  perf numbers are confounded until the control runs below are in.

### 7.6 Next runs that would settle the open points

1. **Instrument the silent `tryPropose` returns** (pending-short and inflight-full) with a
   rate-limited log line. This confirms or refutes the lagging-leader explanation in §7.3.
2. **Control: perf arms with the throttle off** (`throttle.enabled: false`,
   `proposal_min_interval_ms: 0`). This separates perf-trigger fragility at n = 7 from
   throttle-induced churn. Bucket by DOA count, new-view expiries and whether a storm
   locks in. A healthy perf leader rotates every ~12 s, so the expected DOA rate is far
   lower.
3. **Repeat PerfRR** (2–3 seeds or start offsets). The storm may hinge on where the first
   fast VC falls on the checkpoint sawtooth, so one run cannot say whether livelock is
   typical.

---

## 8. Rerun with 300 ms Fixed/Perf timers (5 Oct 2026, new host)

`FixedTriggerTimeout` raised from 150 to 300 ms (`node/triggerManager.go`). This sets both
the leader-progress and the new-view timer for Fixed and Perf. Otherwise the configs are
identical: `config/throttle_testing/thr_{perfelec,perfrr}_n7_t300.json`, which differ only
by a `_note` key. Run sequentially, 1 h 30 m each, unpinned, via `run_long_experiment.sh`.
Data in `results/thr_perf{elec,rr}_n7_t300_20261005_*/`.

**Confounds against §7:**

- **Different host.** This run is on an AMD EPYC 9354P, firmware-held 3.80 GHz with 0%
  spread and no cpufreq driver (see `notes/cpu_note.txt`). §7 ran on 2× Xeon Gold 6142
  at 3.30 GHz.
- **No CPU pinning and no neighbour run.** §7 pinned each arm to 8 physical cores and
  shared a socket.

F is unchanged at ~163 slots/s, because it is the offered load. But view-change latency
is faster on this box, so **part of the improvement may be the host, not the timer**. A
150 ms run on this host would separate the two.

### 8.1 Headline

| Arm | §4 predicted | §7 @150 ms | **§8 @300 ms** |
|---|---|---|---|
| PerformanceElection req/s | ~5 400 | 3 044 | **5 510** |
| PerformanceElection throttled | ~34% | 23.8% | **34.1%** |
| PerformanceElection mean tenure | ~3.2 s | 1.5 s | **3.3 s** |
| PerformanceRoundRobin req/s | ~4 100 | 26 (livelock) | **6 514** |
| PerformanceRoundRobin throttled | ~50% | n/a | **19.2%** |
| PerformanceRoundRobin mean tenure | ~2 s | n/a | **1.47 s** |

Both arms are steady across all 18 five-minute buckets (PerfElec 5 404–5 644, PerfRR
5 834–7 018). There is no storm and no collapse.

### 8.2 Timers: the cascade is gone

| | progress TO (all nodes) | new-view TO (all nodes) | perf evictions | installs | final counter |
|---|---|---|---|---|---|
| PerfElec @150 (node 3 only) | 2 106 | 272 | 792 | 3 565 | |
| PerfRR @150 (node 3 only) | 489 | 6 662 | 5 | 279 | 9 625 |
| **PerfElec @300** | **0** | **0** | 1 617 (node 3) | 1 634 | 1 635 |
| **PerfRR @300** | **5** | **0** | 3 638 (node 3) | 3 679 | 3 680 |

A final counter of installs + 1 means **every view change installed; none were skipped**.
PerfRR's 5 progress timeouts are one event: a single view at 18:47:29, seen by 5 replicas
1.34 s into its tenure. That is a mid-tenure stall, not a dead-on-arrival view. Both
failure modes in §7 are gone:

- the **NewView round trip** (~250 ms in §7.3) now fits inside the new-view timer;
- the **client redirection delay** (p50 94 ms, p90 162 ms in §7.4) now fits inside the
  progress timer, so dead-on-arrival views disappear.

The perf bar is again the only thing removing leaders, as designed.

### 8.3 PerformanceElection: the model holds to the decimal

Eviction tick is read from the `Perf timer ... below timed target` lines:

| Case | Model (§4.2) | Measured |
|---|---|---|
| leader already held → gated from start, evicted on tick 1 | P = 2/7 = 28.6%, 10 slots/s | **458 / 1 634 = 28.0%, 9.0 slots/s** |
| fresh leader → 3 s at F, 1 s at R, evicted on tick 4 | (3·163 + 10)/4 = **124.8**, bar ≈ 156 | **1 175 evictions, 124.4 slots/s, bar 155.1** |

Throughput, throttled fraction and mean tenure all land on §4.2. This is the cleanest
confirmation in the experiment: **reactive camping + 3 s prep + the 1 %/s compounding
bar fully determines PerfElec's behaviour.**

### 8.4 PerformanceRoundRobin: beats the model, partly through a measurement artifact

PerfRR came in at 6 514 req/s, 1.6× the §4.1 prediction, with only 19.2% of tenure time
throttled. Eviction ticks:

| Tick | Evictions | Measured tput p50 | Bar p50 |
|---|---|---|---|
| 1 s | 2 545 (69%) | **160.9** (ungated, ≈ F) | **166.5** |
| 2 s | 864 | 152.8 | 157.8 |
| 3 s | 268 | 109.0 | 156.9 |

§4.1 assumed bar = 0.92 F < F, so an ungated leader survives the first tick. Here the
**bar sits above F**, so 69% of leaders are removed at 1 s while healthy. Tenure drops to
~1.47 s, 2τ ≈ 2.9 s < prep, and the controller rarely gets a gate on in time. The
throttle is evaded even harder than modelled, but **for the wrong reason**.

Why the bar exceeds F: the bar is `0.92 × max(viewThroughputs over the last 7 views)`.
In PerfRR, those recorded per-view values include outliers **up to 16 460 slots/s**, and
13% of them exceed 177 (= F/0.92). With a 7-view max, P(at least one outlier in the
window) ≈ 1 − 0.87⁷ ≈ 62%, which matches the 69%. PerfElec's record is clean
(p90 164.4, max 170.0).

The source is `observeExecutedSlotForThroughput` (`node/throughputperformance.go`). It
writes `viewThroughputs[view] = (seq − startSeq)/elapsed` at every 250-slot boundary
**for any elapsed > 0**. The `elapsed > 1 s` check only gates the legacy `belowTarget`
path, not the recording. A boundary that lands milliseconds after the observation start
records a near-infinite rate. PerfRR's shorter, more numerous views make that more likely.

**Consequence:** PerfRR's 6 514 is real for the code as it stands, but it partly measures
this artifact. It is not purely the "rotate faster than the controller can re-aim"
mechanism. Before quoting it, either:

- apply the grace rule to the recording too: skip
  `viewThroughputs[view] = throughput` when `elapsed ≤ 1 s`, as the timed trigger already
  does by sampling only once a second; or
- report PerfRR with this caveat attached.

### 8.5 Updated ranking (300 ms, new host)

| Arm | req/s | vs PeriodicRR @150 (506) |
|---|---|---|
| PerformanceRoundRobin | 6 514* | 12.9× |
| PerformanceElection | 5 510 | 10.9× |
| PeriodicElection (§7, 150 ms, old host) | 2 150 | 4.2× |
| PeriodicRoundRobin (§7, 150 ms, old host) | 506 | 1× |

\* inflated by the §8.4 artifact.

The periodic arms were not rerun. Their 10 s timers do not depend on `FixedTriggerTimeout`,
but they ran on the other host.

With the 300 ms timers, the §4 claim stands: under a 3 s prep throttle the **trigger plane
dominates**, and both perf arms beat both periodic arms by more than 2.5×.
