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

### 4.1 `thr_perfrr_n7` — PerformanceRoundRobin: a limit cycle, settling at τ ≈ 2 s

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

Second, independent leak in the same direction: `sendLeaderIdUpdate` fires only on a
NewView **install**. If a 150 ms new-view timer expires and the counter is skipped, the
controller never hears about that counter at all, so its `{c, c+1}` window lags the
real rotation and coverage drops below the law in §2. Perf's fast rotation makes those
skips more likely (§5.2).

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
| `thr_perfrr_n7` | PerformanceRoundRobin | ~50% | ~82 | ~4 100 | 8.2× |
| `thr_pelec_n7` | PeriodicElection | ~79% | ~43 | ~2 140 | 4.3× |
| `thr_prr_n7` | PeriodicRoundRobin | **100%** | ~10 | ~500 | 1× |

**The finding, if the runs bear it out: the perf trigger beats the throttle not by
catching a slow leader but by rotating faster than the controller can re-aim.** τ
self-tunes to just under `prep_ms`, so `prep >= 2τ` holds and §2's coverage law returns
zero. The trigger plane therefore dominates the policy plane here, and the policy plane
only re-ranks *within* a trigger (Election > RoundRobin in both, for the reactive-prep
reason).

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
