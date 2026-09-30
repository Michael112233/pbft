# Epoch-aligned vs time-aligned comparison of long runs

Long scenario runs (hundreds of epochs, one scenario per 100 epochs) do not stay in
step with each other in wall-clock time. The Adaptive run's epochs are about 1 s longer
than the baselines', so by epoch 700 it is about 11 minutes behind. Any comparison has
to pick a cut: the same **epoch** for every run, or the same **second**. The two give
different numbers, and only one of them supports the per-window weighted decomposition.

Numbers below are from `data_baselines_new/` (24 Sep 2026): Adaptive
(`run2new_20260924_022030`) and the four oracle baselines, scenario schedule
ProposalDelay → NetworkDelay → NetworkDelayFCrash → ProposalDelay → NetworkDelay →
NetworkDelayFCrash → Healthy, `scenario_generations = 100`.

## 1. Why the runs drift

An epoch is not 45 s. The epoch timer (`epochTimerInterval`, 45 s) fires, the node
sends `EpochDataMsg` to node 4, node 4 aggregates 2f+1 of them, every node asks the
agent (or the oracle), and only when the decision is applied does
`incrementGeneration` reset the epoch timer. So:

```
epoch length = 45 s + (timer expiry -> decision applied)
```

Measured from the node logs (`Epoch timer fired` to `received learning-agent
decision` for the same generation, all four nodes):

| Run | Timer → decision (median) | p10 – p90 | Epoch length (700 epochs) |
|---|---|---|---|
| Adaptive (QuadRF over gRPC) | 1.38 s | 1.06 – 1.51 s | 46.28 s |
| Baselines (local oracle, 100 ms sleep) | 0.44 s | 0.10 – 0.45 s | 45.30 – 45.32 s |

A node that has not decided yet is pulled into the new generation once f+1 nodes have
switched, so the switch follows the faster nodes and the epoch length sits below
45 s + the median delay.
Consensus keeps running during the decision; the extra second is not dead time, it
just lengthens every scenario window.

The drift accumulates:

| Epoch switch | Adaptive (s) | Baselines (s) | Adaptive lag |
|---|---|---|---|
| 100 | 4,594.0 | 4,510.4 | 84 s |
| 200 | 9,234.4 | 9,055.7 – 9,056.6 | 178 s |
| 300 | 13,883.9 | 13,601.2 – 13,609.3 | 280 s |
| 400 | 18,493.0 | 18,111.4 – 18,119.6 | 379 s |
| 500 | 23,134.3 | 22,657.5 – 22,665.9 | 474 s |
| 600 | 27,784.6 | 27,203.2 – 27,211.1 | 579 s |
| 700 | 32,395.4 | 31,713.5 – 31,721.5 | 680 s |

The four baselines stay within 8 s of each other, so the problem is Adaptive vs
baselines, not baseline vs baseline.

## 2. How switch points are located

`scenario_switches()` in `plot_tps_series.ipynb` reads the `scenario switch gen=N`
lines from a run's `node_1.log`; `gen=N` is the end of epoch `N-1`. The client's
`report.json` gives `committed_total` against `elapsed_sec`; the run's zero is the
first sample's `timestamp_unix_nano` minus its `elapsed_sec`, and each switch
timestamp minus that zero is its position on the same axis. A window's commits are
`committed_total` at its end edge minus at its start edge (`value_at`: last sample at
or before the time; samples are 0.1 s apart).

Small offsets, all negligible:

- Nodes enter gen 1 about 5.1 s before the client's elapsed 0, identically in every
  run (±0.01 s). Window 1 is therefore ~5 s short in all runs alike.
- Nodes switch up to ~2.5 s apart; only node 1's timestamps are used. That is ~0.05%
  of a 75-minute window.

## 3. The two alignments

### Epoch-aligned

Each run is cut at **its own** epoch-K switch, and window `w` of each run is that
run's own `[t(100(w-1)), t(100w)]`.

- Every run gets exactly 100 epochs of every scenario.
- Adaptive gets ~2% more wall-clock time overall (579 s more at 600, 680 s at 700),
  spread over the windows (84–105 s more per window).
- The per-window weighted decomposition (section 5) is exact.

### Time-aligned (same wall-clock cut)

Every run is cut at the same second, the earliest epoch-K switch across runs:
27,203.2 s for K = 600 and 31,713.5 s for K = 700 (`combined_end_sec`).

- Every run gets exactly the same wall-clock time.
- Adaptive is cut short of its own epoch-K switch by 581 s (600) or 682 s (700), so
  its **last** window is truncated: it has seen only part of its final scenario.
- Window edges no longer line up between runs, so the decomposition is only
  approximate.

## 4. Effect on the headline ratio

Headline number: baseline total committed ÷ Adaptive total committed.

| Baseline ÷ Adaptive | 1–600 epoch | 1–600 time | 1–700 epoch | 1–700 time |
|---|---|---|---|---|
| FixedRoundRobin | 3.75% | 3.81% | 25.48% | 26.37% |
| PerformanceRoundRobin | 54.89% | 55.77% | 64.77% | 67.00% |
| PeriodicRoundRobin | 85.64% | 87.02% | 88.49% | 91.60% |
| PeriodicElection | 87.66% | 89.07% | 89.85% | 93.00% |

Time-aligned is always higher (better for the baselines): up to 1.4 points at 600
and 3.2 points at 700. The baselines' totals barely change between the two cuts
(they end at the cut either way); what changes is Adaptive's total:

| Cut | Adaptive, epoch-aligned | Adaptive, time-aligned | Lost commits | Lost time falls in |
|---|---|---|---|---|
| 600 | 125,277,400 | 123,290,550 | 1.99 M | NetworkDelayFCrash (~3,300 tps) |
| 700 | 162,577,550 | 157,057,400 | 5.52 M | Healthy (~8,100 tps) |

The gap between the two methods depends on **which scenario the lost time falls
in**: the same ~10 minutes cost 2.0 M commits at the end of NetworkDelayFCrash but
5.5 M at the end of Healthy. The ranking of baselines is the same under both cuts.

## 5. The weighted decomposition needs epoch alignment

With `A`, `B` the totals and `A(w)`, `B(w)` the commits in window `w`:

```
B / A  =  Σ_w B(w) / A  =  Σ_w  [A(w) / A] · [B(w) / A(w)]
                                 weight(w)      gain(w)
```

This holds exactly only when the windows partition each run's total, i.e. the
epoch-aligned cut. The weights are Adaptive's share of commits per window, so
high-throughput windows count more:

| Window | weight, 1–600 | weight, 1–700 |
|---|---|---|
| ProposalDelay #1 | 25.4% | 19.6% |
| NetworkDelay #1 | 9.7% | 7.5% |
| NetworkDelayFCrash #1 | 11.4% | 8.8% |
| ProposalDelay #2 | 28.1% | 21.6% |
| NetworkDelay #2 | 13.0% | 10.1% |
| NetworkDelayFCrash #2 | 12.4% | 9.5% |
| Healthy | – | 22.9% |

Healthy gets the largest weight, and every baseline's gain there is 97–99%, so
including it pulls every ratio toward 100% (FixedRoundRobin 3.75% → 25.48%). This
dilution is a property of the metric, not of the alignment, and it appears under
both cuts.

Checked numerically: for all four baselines, Σ weight × gain equals B/A to the
second decimal at both 600 and 700 (epoch-aligned). A plain (unweighted) mean of the
window gains is a different number, e.g. PeriodicElection at 600: 96.04% plain vs
87.66% weighted.

## 6. A third option: per-window rates

Average TPS per window (commits in the window ÷ that run's window duration) is both
scenario-aligned and time-fair: each run is measured over exactly its own 100 epochs
of the scenario, and the longer epoch cancels out because it is a rate. This is what
the per-scenario grouped-bar cell plots.

Summing those rates as if every window had the same length gives a headline that
sits between the other two:

| Baseline ÷ Adaptive | 1–600 | 1–700 |
|---|---|---|
| FixedRoundRobin | 3.84% | 26.09% |
| PerformanceRoundRobin | 56.25% | 66.33% |
| PeriodicRoundRobin | 87.40% | 90.35% |
| PeriodicElection | 89.43% | 91.72% |

The same decomposition holds exactly for it, with weights = Adaptive's TPS per window
÷ the sum of its window TPS and gains = the TPS ratio per window.

## 7. In the notebook

In `plot_tps_series.ipynb`:

- The combined cumulative plot (`last_epoch = …`) cuts each series at its own
  epoch switch but sets `xlim` to `combined_end_sec`, so what it **shows** is the
  time-aligned view. Its scenario shading and separators are the mean switch time
  over all runs, which sits between Adaptive's and the baselines' true edges.
- The windowed-summary cell (`start_cutoff` / `end_cutoff`) is time-aligned. As
  written it looks up `"Adaptive (QuadRF)"`, while the combined cell labels the run
  `"Adaptive"`, so it raises a `KeyError`.
- The per-scenario grouped-bar cell uses each run's own edges: per-window rates,
  section 6.

## 8. Which to report

- **Per-scenario claims and the weighted-average argument:** epoch-aligned or
  per-window rates. Both compare the same 100 epochs of each scenario.
- **"Commits per wall-clock second" claims:** time-aligned, but state which scenario
  the cut truncates for Adaptive; the answer moves by up to 3 points depending on it.
- **Always** give the cut next to the number, and when the last window is Healthy,
  show the result with and without it.
- The drift itself goes away if the epoch timer is re-armed when it fires rather than
  when the decision is applied (fixed 45 s epochs), or if the oracle is made to take
  as long as the agent. Either stops the lag from accumulating, and the two alignments
then differ by about one decision delay instead of ~11 minutes.
