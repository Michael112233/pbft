# Reading `freq_trace.csv`

`alt_run_project.sh`'s `start_freq_trace` function samples every logical CPU's
current frequency every 0.2s for the duration of a run, into `logs/freq_trace.csv`.
This doc explains how to read that file and what conclusions each pattern
supports. Use `scripts/analyze_freq_trace.py` to compute all of this instead
of doing it by hand.

## File format

```
<unix_timestamp>,<cpu_khz>,<cpu_khz>,...,<cpu_khz>,
```

One row per 0.2s sample, one column per logical CPU, read straight from
`/sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq`.

**Gotcha:** the columns come from a shell glob (`cpu*`), which expands in
**lexicographic** order — `cpu0, cpu1, cpu10, cpu11, ..., cpu19, cpu2, cpu20,
...` — not numeric order. Column 3 is not "cpu2". This is fine for aggregate
questions (how many cores, at this instant, are above X) but means you can't
trust a column index as "this specific physical core" without separately
capturing the glob's expansion order.

## The four questions worth asking

### 1. What's the *typical* frequency, not the peak?

A single "it hit 3.3 GHz once" tells you almost nothing — brief turbo spikes
happen under any governor. What matters is the **percentile distribution
across every reading** (all cores × all samples, flattened into one list):
p50, p90, p99. If p50 sits at the machine's frequency floor, the CPU is
idling most of the time by volume, no matter how good the max looks.

```python
allvals = sorted(x for row in rows for x in row[1:])
p50 = allvals[len(allvals) // 2]
p90 = allvals[int(len(allvals) * 0.9)]
```

### 2. How many cores are doing something at any given instant, on average?

Pick a threshold above the floor (default here: 1.5 GHz) and count, per
sample row, how many columns exceed it — then average that count across all
rows. This is the best single number for telling "bursty, single-threaded
work" apart from "sustained, parallel work": a value near 1-2 (out of 64)
means essentially one core is ever active at a time; a value near 40 means
real, wide parallelism.

```python
boosted_per_sample = [sum(1 for x in row[1:] if x > 1_500_000) for row in rows]
avg_boosted = sum(boosted_per_sample) / len(boosted_per_sample)
```

This is the number that separated a `schedutil` run (avg **1.73** boosted
cores) from the same workload under `performance` (avg **41.76**) — and,
separately, showed that a code change which more than doubled throughput
(parallelizing signature verification) still only averaged **1.36** boosted
cores, proving that particular win had nothing to do with frequency at all.

### 3. Does it change over time, and does that match what the experiment was doing?

Print `max(row)` and boosted-count **per sample**, with elapsed time, instead
of collapsing to one number. Look for a decay shape: many cores hot for the
first ~5s while tmux windows and processes spin up concurrently, then
collapsing toward the floor once steady-state consensus begins. That decay
means the *resting* behavior — after startup — is what characterizes the
workload, not the transient.

```python
for ts, row in rows[:15]:
    print(ts - t0, max(row[1:]), sum(1 for x in row[1:] if x > 1_500_000))
```

`analyze_freq_trace.py` reports both a whole-run summary and a "steady-state"
summary that excludes the first 5 seconds, for exactly this reason.

### 4. Compare two runs' distributions — don't eyeball one number in isolation

Every conclusion drawn from this data came from running the same four stats
on two `freq_trace.csv` files side by side (schedutil vs performance;
full-crypto vs parallelized-verification) and reading the delta. A lone
"p50 = 1.0 GHz" doesn't prove anything by itself until you have a comparison
point that differs. `analyze_freq_trace.py` accepts multiple files and prints
a comparison table for this reason — pass it every run you want to compare in
one invocation.

## What each pattern implies

| Pattern | What it means |
|---|---|
| p50 pinned at floor, avg boosted ~1-2 | Workload is a thin, serialized chain — one goroutine bursts briefly, everything else idles. The governor has no reason to ramp. |
| p50 pinned at floor, avg boosted ~1-2, **but throughput/latency changed between two runs anyway** | The change had nothing to do with frequency — look at the code, not the CPU. |
| p50 near max, avg boosted near core-count | Real parallel utilization; the frequency governor is no longer the limiting factor, so any remaining bottleneck is elsewhere. |
| High max, low avg-boosted, decaying over the first few seconds | Startup transient (many processes launching concurrently) — not representative of steady state; exclude it when characterizing "resting" behavior. |

## Usage

```bash
# one run
python3 scripts/analyze_freq_trace.py logs/freq_trace.csv

# compare two or more runs
python3 scripts/analyze_freq_trace.py results/run_a/freq_trace.csv results/run_b/freq_trace.csv

# adjust the boost threshold if your machine's floor differs
python3 scripts/analyze_freq_trace.py logs/freq_trace.csv --boost-threshold-ghz 2.0
```

## Known context from this machine (2026-09-05)

Recorded here as a reference point, not a claim that will hold on other
hardware — always re-derive rather than assume:

- Frequency floor ~1.0 GHz, `scaling_driver=intel_cpufreq` (intel_pstate
  passive), default governor `schedutil`.
- Under `schedutil`, this PBFT workload's bursty, many-thread-spread pattern
  almost never justified a ramp: whole-run p50 = 1.0 GHz, avg boosted cores
  ~1.7 out of 64.
- Switching to the `performance` governor alone (no code change) took
  steady-state throughput from ~111 to ~272 batches/s (~2.45x).
- Parallelizing the leader-side client-signature verification (a code
  change, governor left on `schedutil`) took steady-state throughput from
  ~111 to ~271 batches/s (~2.44x) — an almost identical multiplier reached by
  a completely different mechanism, while `freq_trace.csv` for that run still
  showed the CPU pinned near the floor (avg boosted cores 1.36, *lower* than
  before). That comparison is what proved the two levers were independent.
