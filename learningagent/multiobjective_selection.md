# Selecting the arm with throughput as a constraint and latency as the objective

## Why throughput alone is not enough

- In the FarNode scenario (node 1 is 35 ms from everyone), every arm delivers ~8,100 tps. Latency differs up to 4×: 11 ms with a near leader, 44 ms when node 1 leads.
- A throughput-only reward cannot tell these arms apart. The bandit picked FixedAware only 14–96% of the time, depending on the seed (coin flip).

## How the two objectives relate

- **Where throughput differs** (ProposalDelay, NetworkDelay, FCrash) and throughput is the deciding factor.
- **Where throughput is saturated** (Healthy, FarNode), latency is the only signal.
- So there is no real trade-off to weigh. Throughput comes first, and latency breaks ties.

## The model

- As in QuadRF, there is one random forest per (previous action, candidate action) pair. That keeps the one-step dependency: the state is measured under the previous action.
- Each forest now has two outputs: throughput and latency. Each output is scaled by its spread before fitting, so tx/s and ms carry equal weight.
- Thompson sampling: at every decision, each candidate's forest is refit on a bootstrap of its data. That gives one joint sample (T̃, L̃) per arm.

## The decision rule (throughput first, with a tolerance)

1. **Feasible arms:** those whose sampled throughput is within δ of the best, T̃ ≥ (1 − δ) · max T̃. We use δ = 5%.
2. **Choice:** the lowest sampled latency among the feasible arms.

## Properties of this rule

- **Always has a choice:** the throughput-best arm is always feasible.
- **Throughput loss is bounded:** the chosen arm is never more than δ below the best sampled throughput.
- **One knob, and it is interpretable:**
  - δ = 0 gives today's throughput-only agent;
  - δ = 1 gives a latency-only agent;
  - δ sits above epoch-to-epoch throughput noise (~2%) and below the smallest gap that throughput should decide (like 25%).
- **Explores both objectives:** an under-sampled arm can draw a high throughput or a low latency, so it gets retried on either axis.


## How it plays out per scenario (pilot data)

| Scenario | Throughput step | Latency step | Chosen |
|---|---|---|---|
| Healthy | all 6 within 1.1% → all feasible | 10.5–11.5 ms: a near tie | any of 5 tied arms |
| FarNode | all within 2.3% → all feasible | FixedAware 11 ms vs 24–44 ms | **FixedAware** |
| ProposalDelay | PerfRR 7,627; next best (PerfElection) 7,052 (−7.5%) → only PerfRR feasible | — | **PerfRR** |
| NetworkDelay | PeriodicRR 3,609, PeriodicElection 3,479 (−3.6%) → both feasible | 3.2 s vs 5.1 s | **PeriodicRR** |
| NDFCrash | PeriodicElection 3,409, PeriodicRR 2,793 (−18%) → only PeriodicElection feasible | — | **PeriodicElection** |

## Convergence over scenario cycles

### Setup

- 1,000 epochs (generations), 6 arms, 3 random seeds.
- The scenario changes every 100 epochs; each 100-epoch stretch is one "block".
- Each scenario appears twice, so blocks 6–10 show what happens when a scenario returns.
- State and reward come from 30 real pilot runs (5 scenarios × 6 actions), with some noise.

### Steady state and convergence time

| Block | Scenario | Optimal set | Seed 5 | Seed 6 | Seed 7 |
|---|---|---|---|---|---|
| 1 | Healthy | 5 tied arms | 100% · 1 | 100% · 10 | 94% · 1 |
| 2 | FarNode | FixedAware | 74% · 55 | 78% · 23 | 80% · 41 |
| 3 | ProposalDelay | PerfRR | 78% · 68 | 54% · 46 | 68% · 61 |
| 4 | NetworkDelay | PeriodicRR | 82% · 42 | 78% · 58 | 84% · 64 |
| 5 | NDFCrash | PeriodicElection | 96% · 37 | 86% · 19 | 96% · 28 |
| 6 | FarNode | FixedAware | 100% · 5 | 100% · 1 | 94% · 1 |
| 7 | ProposalDelay | PerfRR | 98% · 4 | 100% · 2 | 98% · 1 |
| 8 | Healthy | 5 tied arms | 100% · 1 | 100% · 1 | 100% · 1 |
| 9 | NDFCrash | PeriodicElection | 98% · 1 | 96% · 2 | 92% · 11 |
| 10 | NetworkDelay | PeriodicRR | 98% · 2 | 96% · 2 | 98% · 3 |

How to read a cell, "X% · N":

- **X:** the share of epochs in the second half of the block (its last 50) where the agent picked an optimal arm. This is steady-state accuracy.
- **N:** how many epochs into the block the agent first picked an optimal arm 5 times in a row. This is convergence time after a scenario change.
- **Optimal set:** what the rule selects on the noise-free pilot means.

### Whole block vs last 50 epochs

| Block | Scenario | All 100 epochs (seeds 5 / 6 / 7) | Last 50 epochs (seeds 5 / 6 / 7) |
|---|---|---|---|
| 6 | FarNode | 95 / 99 / 96% | 100 / 100 / 94% |
| 7 | ProposalDelay | 93 / 97 / 97% | 98 / 100 / 98% |
| 8 | Healthy | 100 / 100 / 100% | 100 / 100 / 100% |
| 9 | NDFCrash | 93 / 93 / 91% | 98 / 96 / 92% |
| 10 | NetworkDelay | 92 / 92 / 95% | 98 / 96 / 98% |

On a scenario's second appearance, convergence takes 1–11 epochs, so even the whole-block share (91–100%) is close to steady state. 

### What it shows

- **First encounter (blocks 1–5): 20–70 epochs to converge, 54–96% steady state.** Every (previous, candidate) pair must be tried once, and six arms give 36 pairs. The forests also need samples before their estimates settle.
- **Second encounter (blocks 6–10): 1–11 epochs, 92–100% steady state.** The forests already hold experience for that scenario, and the state tells them which scenario is running. The agent recognises it and switches almost at once.
- **FarNode:** FixedAware is picked in 74–80% of steady-state epochs the first time and 94–100% the second. The latency-aware arm is found even though every arm's throughput looks the same.
- **The remaining 0–8% of non-optimal picks** are Thompson-sampling exploration: an occasional optimistic draw for another arm. That is what keeps the agent able to notice a change.

