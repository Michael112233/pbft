"""Measure δ (epoch timer fired -> decision applied) per scenario window from run logs.

Usage: python3 scripts/epoch_delta.py <run dir> [<baseline run dir>]

A run dir is what run_experiments.sh / run_long_experiment.sh save under results/: node
logs plus config.json. For each generation it takes the median over the nodes of
"Epoch timer fired for view (g,..)" -> "received learning-agent decision for generation
g" (nodes pulled into the next generation by f+1 ViewChanges before their own decision
never log the second line and are skipped), then reports the median per scenario window.

With a baseline (oracle) run as the second argument it also prints
δ_A − δ_B + oracle_decision_delay_ms of the baseline: the agent's effective decision time
in the run, i.e. the value that makes the oracle match it (docs/epoch-grid.md §6). This is
the in-run number; learningagent/bench_learner.py measures the agent alone, which on the
9 Oct 2026 host came out about 1.2x lower than in a run.
"""

import datetime as dt
import glob
import json
import os
import re
import statistics
import sys

TS = re.compile(r"^\[\w+\] (\S+ \S+)")
FIRE = re.compile(r"Epoch timer fired for view \((\d+),")
DECISION = re.compile(r"received learning-agent decision for generation (\d+)")


def ts(line):
    return dt.datetime.strptime(TS.match(line).group(1), "%Y-%m-%d %H:%M:%S.%f").timestamp()


def deltas(run):
    """generation -> median δ in seconds over the nodes that logged both lines."""
    per_gen = {}
    for log in glob.glob(os.path.join(run, "node_[0-9]*.log")):
        if not re.search(r"node_\d+\.log$", log):
            continue
        fired = {}
        for line in open(log, errors="ignore"):
            m = FIRE.search(line)
            if m:
                fired.setdefault(int(m.group(1)), ts(line))
                continue
            m = DECISION.search(line)
            if m and int(m.group(1)) in fired:
                g = int(m.group(1))
                per_gen.setdefault(g, []).append(ts(line) - fired[g])
    return {g: statistics.median(v) for g, v in per_gen.items()}


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p / 100 * (len(xs) - 1))))]


def windows(run):
    cfg = json.load(open(os.path.join(run, "config.json")))
    names = cfg.get("scenarios") or ["Healthy"]
    span = int(cfg.get("scenario_generations") or 100) if cfg.get("scenario_mode") else 10**9
    return cfg, names, span


def main():
    if len(sys.argv) not in (2, 3):
        sys.exit(__doc__)
    run = sys.argv[1]
    cfg, names, span = windows(run)
    a = deltas(run)
    b, oracle_ms = {}, 0
    if len(sys.argv) == 3:
        bcfg = json.load(open(os.path.join(sys.argv[2], "config.json")))
        b = deltas(sys.argv[2])
        oracle_ms = int(bcfg.get("oracle_decision_delay_ms") or 100)
    if not a:
        sys.exit(f"no epoch decisions found in {run}")

    last = max(a)
    head = f"{'generations':>13s} {'scenario':>20s} {'δ ms':>8s} {'p10':>7s} {'p90':>7s}"
    if b:
        head += f" {'δ_B ms':>8s} {'δ_A−δ_B+oracle':>15s}"
    print(head)
    start = 1
    while start <= last:
        end = min(start + span - 1, last)
        # generation 1 has no experience yet (no fits), so it is left out of window 1
        gens = [g for g in range(max(start, 2), end + 1) if g in a]
        if gens:
            xs = [a[g] * 1000 for g in gens]
            scenario = names[((start - 1) // span) % len(names)] if span < 10**9 else names[0]
            row = (f"{f'{start}-{end}':>13s} {scenario:>20s} {statistics.median(xs):8.0f}"
                   f" {pct(xs, 10):7.0f} {pct(xs, 90):7.0f}")
            common = [g for g in gens if g in b]
            if common:
                da = statistics.median(a[g] for g in common) * 1000
                db = statistics.median(b[g] for g in common) * 1000
                row += f" {db:8.0f} {da - db + oracle_ms:15.0f}"
            print(row)
        start = end + 1


if __name__ == "__main__":
    main()
