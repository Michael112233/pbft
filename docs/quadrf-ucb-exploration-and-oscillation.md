# QuadRF UCB Exploration: What It Fixes and Why It Oscillates

## Background

`QuadRF` (`learningagent/server.py`) keeps a separate `RandomForestRegressor`
per `(prev_protocol, action)` bucket. With `RESAMPLE_ON_PREDICT = False`, a
bucket's model is only refit when that specific `(prev_protocol, action)` pair
is actually selected and its reward is recorded
(`record_state_action_reward` + `train()`). Every other bucket keeps whatever
fit it had from its own last selection.

This write-only training exposed a lock-in bug: in a 500-step run that cycles
through `healthy -> proposal_delay -> network_delay -> healthy -> ...`
(`learningagent/simulate_quadrf.py`), the `(performance_election,
fixed_round_robin)` bucket was trained exactly twice -- once during `healthy`
(reward 266) and once during `proposal_delay` (reward 14) -- on two nearly
identical feature vectors (`[0.10, 4.2, 0]` vs `[0.12, 4.4, 0]`). A
`max_depth=5` forest fit on two nearly-collinear points with opposite targets
blends toward a low prediction and never gets a chance to fit again, because
nothing reselects a bucket whose point estimate already looks bad. When the
scenario rotated back to `healthy` at step 301, `fixed_round_robin` stayed
locked out for the rest of the run even though it was the true best action.

`predict()` now adds a UCB-style bonus to each candidate's point estimate when
`RESAMPLE_ON_PREDICT` is `False`:

$$
\text{score}(a) = \hat{r}(a) + c \sqrt{\frac{\ln(N)}{n_a}}
$$

- $\hat{r}(a)$ is the forest's point-estimate prediction for candidate $a$.
- $n_a$ is the number of training samples in that `(prev_protocol, a)` bucket.
- $N$ is the total samples across all candidates in the current
  `prev_protocol` bucket (`total_pulls` in the code).
- $c$ is `UCB_EXPLORATION_CONSTANT` (currently `50.0`, chosen to be on the
  same order of magnitude as this workload's throughput rewards).

## What UCB gets us today

The bonus term is largest for buckets with few samples and shrinks as
$n_a$ grows, but it never reaches zero as long as $N$ keeps growing. That is
the property that fixes the lock-in: a bucket that looks bad from a stale,
sparse fit still keeps a standing incentive to be re-tried, so it eventually
gets enough fresh samples to correct itself. Re-running the same 500-step
scenario with the bonus enabled, step 301 (the return to `healthy`)
immediately re-selects `fixed_round_robin` instead of staying stuck on
`performance_election`, and the second `proposal_delay` window (steps
401-500) correctly re-converges to `performance_round_robin` /
`performance_election`, the true top two for that scenario. Without the
bonus, none of that recovery happens.

## The cost: the oscillation never fully stops

The same run shows the tradeoff. Under `proposal_delay`, the true rewards are
`performance_round_robin = 258` and `performance_election = 254` -- a fixed
4-point gap. Once both buckets are well sampled, the run keeps alternating
between them instead of settling on the better one:

| Step | $N$ (total pulls) | $n_a$, PRR | $n_a$, PE | bonus, PRR | bonus, PE | score, PRR | score, PE | score gap |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 410 | 106 | 37 | 25 | 17.75 | 21.59 | 275.75 | 275.59 | 0.16 |
| 450 | 131 | 53 | 34 | 15.16 | 18.93 | 273.16 | 272.93 | 0.23 |
| 490 | 158 | 71 | 43 | 13.35 | 17.16 | 271.35 | 271.16 | 0.19 |

(PRR = `performance_round_robin`, PE = `performance_election`; point estimates
are exactly 258 and 254 at every row, so all movement in the score gap comes
from the bonus terms.)

At every sampled step, the bonus differential (`bonus(PE) - bonus(PRR)`, about
3.8 in each row) very nearly cancels the true 4-point reward gap, leaving a
score difference under 0.25. This is not noise or a bug -- it is UCB1's
intended equilibrium. The algorithm self-balances the visit ratio between two
arms so that the bonus gap tracks the reward gap, which is exactly what
bounds its regret. But it means the "loser" arm keeps getting pulled at a
rate that shrinks only like $O(\ln N)$, never like $O(1/N)$ or faster --
absolute visits to the inferior arm grow without bound, just slower than
visits to the better one.

In classic bandit theory this is fine, because only cumulative reward matters.
Here it is more expensive than that: each selection is a live BFT protocol
switch, and switching has its own real cost (the epoch/view-change machinery
described in `docs/adaptive-vs-fixed-experiment-design.md` and
`docs/newview-cost-and-viewchange-storm.md`). A policy that is
reward-optimal in the limit but switches protocols on a near-coin-flip every
few steps is paying a cost that plain regret analysis does not charge for.

## Fix 1: Decay the exploration constant

Shrinking $c$ over time makes the bonus differential eventually fall below a
real, persistent reward gap, letting the run settle. A simple form:

$$
c(N) = \frac{c_0}{1 + N / K}
$$

where $K$ is a decay horizon (in samples) chosen so $c(N)$ is still large
early (to prevent exactly the lock-in this bonus was added for) but small
once a bucket has accumulated enough data to be trusted. For example, with
$c_0 = 50$ and $K = 200$: at $N = 106$ (step 410 above), $c(N) \approx 32.7$,
about two-thirds of today's flat 50 -- not yet enough to change the outcome
at that step, but by $N = 500$, $c(N) \approx 14.3$, roughly a third of
today's constant, which would shrink both bonus terms proportionally and
widen rather than close the score gap between PRR and PE (since PRR's smaller
$n_a$ still gets more bonus, decay does not by itself resolve the tie --
see the caveat below).

**Caveat:** decaying $c$ uniformly does not break the tie by itself, because
it scales both arms' bonuses down by the same factor -- the *ratio*
`bonus(PE)/bonus(PRR)` (driven by `sqrt(n_a)`) is unchanged, so the
differential shrinks in proportion to the gap it's competing with, not faster
than it. What decay does buy is a smaller absolute bonus everywhere, which
reduces how large a genuinely stale bucket's temporary advantage can be
relative to a wide true gap (as in the `fixed_round_robin` case, where the
true gap between 266 and second place is large). It should be paired with a
floor (never decay $c$ to zero) so a bucket that has gone untouched for a
very long time still keeps enough bonus to be worth revisiting -- decaying
all the way to zero reintroduces the original lock-in bug.

## Fix 2: Switching hysteresis (a margin to beat the incumbent)

A more direct fix for oscillation: only switch away from the
currently-selected protocol if a challenger's score beats it by more than a
margin $M$, rather than by any positive amount.

Looking at the table above, the actual score gaps produced by noise-level UCB
fluctuation are tiny: 0.16, 0.23, 0.19. A margin as small as $M = 1$ (in
throughput units) would suppress every one of these flips while a genuine
regime change still clears it easily -- e.g. at step 301 in the earlier test,
`fixed_round_robin` was being compared against buckets with `training_size ==
0` (`float('inf')`) or a stale, badly-blended low estimate, producing score
gaps of hundreds of points, not fractions of one.

Concretely, this changes the selection rule from "pick the argmax" to "pick
the argmax only if it exceeds the current incumbent's score by more than
$M$; otherwise keep the incumbent." This directly targets the switching cost
this system actually pays, independent of how the underlying point estimates
or bonuses are computed -- it works whether or not decay (Fix 1) is also
applied.

**Caveat:** $M$ needs to be picked relative to the reward scale and to how
close two genuinely different arms can legitimately be (in this workload,
254 vs 258 is a real, distinguishable gap). Too large an $M$ makes the policy
sluggish to adopt a real improvement, similar in spirit to a purely greedy
policy's blind spots.

## Recommendation

Use both together: decay $c$ with a floor so long-idle buckets never fully
lose their revisit incentive (preventing a return of the original bug), and
add a small switching margin $M$ on top so that near-tied scores -- which
decay alone does not eliminate -- don't translate into a protocol switch.
Tune $K$ (decay horizon) and $M$ (margin) empirically against this
simulation before touching the live system, using the same before/after
comparison method used here: log `total_pulls`, `n_a`, and the computed
bonus/score per candidate at a few checkpoints, and confirm both that (a) the
`fixed_round_robin`-style lock-in does not return, and (b) the
`performance_round_robin` / `performance_election`-style oscillation drops
out once one of them is genuinely ahead.

## Relevant Code

- Bonus computation and the `RESAMPLE_ON_PREDICT` / `UCB_EXPLORATION_CONSTANT`
  switch: `learningagent/server.py`, `QuadRF.predict()`
- Write-only per-bucket training: `learningagent/server.py`,
  `QuadRF.train()` / `QuadRF.record_state_action_reward()`
- Scenario rotation and synthetic reward/state generation used for all
  numbers in this doc: `learningagent/simulate_quadrf.py`
