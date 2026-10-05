---
name: cpu-baseline
description: Check and record the CPU baseline of an experiment host — governor, turbo, and the real clock measured under load. Use when setting up a new/remote machine for PBFT runs, before the first experiment on a box, when asked "what is the CPU frequency / governor here", or when run-to-run timing results differ between machines and clock speed is a suspect.
---

# CPU baseline for an experiment host

Timing is the dependent variable in this testbed — 150 ms trigger timeouts, the
10 s Periodic window, VDF delays, throughput bars. A host whose clock moves, or
whose clock differs from the last machine, invalidates comparisons against results
in `docs/` and `notes/`. Establish the baseline once per machine, then record it.

## Run this

```bash
scripts/cpu_check.sh              # topology, governor, turbo, clock under load
scripts/cpu_check.sh --cores 8    # all-core turbo bin (use the node count of the run)
scripts/cpu_check.sh --fix        # pin all CPUs to performance (needs sudo)
```

Do not hand-roll the check with `cat .../scaling_cur_freq` or `lscpu` alone. See
the trap below — that is the whole reason this script exists.

## The trap: idle frequency readings are stale, not low

With the `intel_pstate` / `intel_cpufreq` driver, `scaling_cur_freq` is **not** the
requested P-state. It is an APERF/MPERF average that refreshes only when the
scheduler runs on that CPU. So:

- An **idle** core returns a frozen leftover value. A quiet 64-core box reads
  ~1 GHz on most cores even with the `performance` governor pinning every one of
  them to max. Reading this as "the governor isn't working" is wrong.
- A core that was busy a moment ago keeps reporting its **old high** value after
  going idle.
- Repeated identical readings from an idle core are the tell: the sample is frozen,
  not stable.
- `/proc/cpuinfo`'s `cpu MHz` column is unreliable here too — on this hardware it
  reported 3700 while no core could exceed 3300.

**Only a reading taken from a core that is busy right now means anything.** That is
what `scripts/cpu_check.sh` does: it pins a spin loop, then samples.

The same caveat bounds `logs/freq_trace.csv` (`start_freq_trace` in
`alt_run_project.sh`): it is trustworthy *during* a run because the cores are
loaded, and meaningless in the idle stretches before and after. See
`scripts/freq_trace_analysis.md` for reading that file.

## Interpreting the result

- **Governor** should be `performance` on every CPU. Anything else → `--fix`.
  That writes the old value to `/tmp/prev_governor` first.
- **Spread** under 2% of median is what you want; the run's timings are then
  comparable with other runs on this host. A larger spread means thermal or power
  capping, and timing results from that box need a caveat.
- **Sustained below the advertised max is normal.** The `cpuinfo_max_freq` ceiling
  is the 1–2 core turbo bin; a loaded machine runs the all-core bin. Quote the
  measured number, never the advertised one.

## Record it

Append the measured baseline to `notes/cpu_note.txt` with the hostname and date, so
results in `docs/` can be traced back to the clock they were produced on. Reference
hosts measured so far:

| Host | CPU | Logical CPUs | Governor | Sustained |
|---|---|---|---|---|
| CloudLab (2026-10-04) | 2× Xeon Gold 6142 @ 2.60 GHz | 64 (2×16×2) | performance | 3.30 GHz, 1.1% spread |
