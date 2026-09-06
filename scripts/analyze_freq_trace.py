#!/usr/bin/env python3
"""Analyze a CPU frequency trace produced by alt_run_project.sh's start_freq_trace.

Usage:
    python3 scripts/analyze_freq_trace.py logs/freq_trace.csv
    python3 scripts/analyze_freq_trace.py results/run_a/freq_trace.csv results/run_b/freq_trace.csv

With one file: prints the distribution, boosted-core count, and a startup/
steady-state timeline for that run.

With two or more files: also prints a side-by-side comparison table, so you
can see the delta between e.g. a schedutil run and a performance-governor
run, or a before/after code change, without re-deriving the numbers by hand.

See scripts/freq_trace_analysis.md for what each number means and why.
"""

import sys
import argparse
from pathlib import Path

BOOST_THRESHOLD_KHZ = 1_500_000  # 1.5 GHz; adjust if your machine's floor differs
STARTUP_WINDOW_SEC = 5.0         # exclude this much from the "steady state" view


def load_trace(path):
    """Parse one freq_trace.csv into a list of (timestamp, [khz, khz, ...])."""
    rows = []
    with open(path) as f:
        for line in f:
            line = line.strip().rstrip(",")
            if not line:
                continue
            parts = line.split(",")
            ts = float(parts[0])
            vals = [int(v) for v in parts[1:] if v]
            if vals:
                rows.append((ts, vals))
    if not rows:
        raise ValueError(f"{path}: no samples found")
    return rows


def percentile(sorted_vals, p):
    idx = min(int(len(sorted_vals) * p), len(sorted_vals) - 1)
    return sorted_vals[idx]


def summarize(rows, boost_threshold=BOOST_THRESHOLD_KHZ):
    """Compute the four core stats described in freq_trace_analysis.md."""
    t0 = rows[0][0]
    duration = rows[-1][0] - t0

    all_vals = sorted(x for _, v in rows for x in v)
    boosted_per_sample = [sum(1 for x in v if x > boost_threshold) for _, v in rows]

    # Steady-state = excluding the initial startup transient, where many
    # unrelated processes (learning-agent servers, node startup) briefly spin
    # up several cores at once and would otherwise skew the "resting" picture.
    steady_rows = [(ts, v) for ts, v in rows if ts - t0 > STARTUP_WINDOW_SEC]
    steady_vals = sorted(x for _, v in steady_rows for x in v) if steady_rows else all_vals
    steady_boosted = (
        [sum(1 for x in v if x > boost_threshold) for _, v in steady_rows]
        if steady_rows else boosted_per_sample
    )

    return {
        "samples": len(rows),
        "cpus": len(rows[0][1]),
        "duration_sec": duration,
        "p50_ghz": percentile(all_vals, 0.50) / 1e6,
        "p90_ghz": percentile(all_vals, 0.90) / 1e6,
        "p99_ghz": percentile(all_vals, 0.99) / 1e6,
        "max_ghz": all_vals[-1] / 1e6,
        "avg_boosted_cores": sum(boosted_per_sample) / len(boosted_per_sample),
        "steady_p50_ghz": percentile(steady_vals, 0.50) / 1e6,
        "steady_avg_boosted_cores": sum(steady_boosted) / len(steady_boosted),
    }


def print_summary(label, path, stats):
    print(f"=== {label} ({path}) ===")
    print(f"  samples={stats['samples']}  cpus={stats['cpus']}  duration={stats['duration_sec']:.1f}s")
    print(f"  whole-run:   p50={stats['p50_ghz']:.2f}GHz  p90={stats['p90_ghz']:.2f}GHz  "
          f"p99={stats['p99_ghz']:.2f}GHz  max={stats['max_ghz']:.2f}GHz  "
          f"avg_boosted_cores={stats['avg_boosted_cores']:.2f}")
    print(f"  steady-state (excl. first {STARTUP_WINDOW_SEC:.0f}s): "
          f"p50={stats['steady_p50_ghz']:.2f}GHz  "
          f"avg_boosted_cores={stats['steady_avg_boosted_cores']:.2f}")
    print()


def print_timeline(rows, boost_threshold=BOOST_THRESHOLD_KHZ, n=15):
    """Show how max frequency and boosted-core count evolve over the run."""
    t0 = rows[0][0]
    print(f"  {'t(s)':>7} {'max(GHz)':>9} {'#cores>1.5GHz':>14}")
    shown = rows[:n]
    for ts, v in shown:
        boosted = sum(1 for x in v if x > boost_threshold)
        print(f"  {ts - t0:7.1f} {max(v) / 1e6:9.2f} {boosted:14d}")
    if len(rows) > n:
        print("  ...")
        for ts, v in rows[-min(5, len(rows) - n):]:
            boosted = sum(1 for x in v if x > boost_threshold)
            print(f"  {ts - t0:7.1f} {max(v) / 1e6:9.2f} {boosted:14d}")
    print()


def print_comparison(labeled_stats):
    """Side-by-side table across two or more runs."""
    print("=== Comparison ===")
    header = f"{'metric':28}" + "".join(f"{label:>22}" for label, _ in labeled_stats)
    print(header)
    rows_to_show = [
        ("whole-run p50 (GHz)", "p50_ghz", "{:.2f}"),
        ("whole-run p90 (GHz)", "p90_ghz", "{:.2f}"),
        ("whole-run max (GHz)", "max_ghz", "{:.2f}"),
        ("avg boosted cores", "avg_boosted_cores", "{:.2f}"),
        ("steady-state p50 (GHz)", "steady_p50_ghz", "{:.2f}"),
        ("steady-state avg boosted", "steady_avg_boosted_cores", "{:.2f}"),
    ]
    for label, key, fmt in rows_to_show:
        line = f"{label:28}"
        for _, stats in labeled_stats:
            line += f"{fmt.format(stats[key]):>22}"
        print(line)
    print()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("paths", nargs="+", help="one or more freq_trace.csv files")
    parser.add_argument("--boost-threshold-ghz", type=float, default=BOOST_THRESHOLD_KHZ / 1e6,
                         help="frequency above which a core counts as 'boosted' (default: 1.5)")
    parser.add_argument("--no-timeline", action="store_true", help="skip the per-sample timeline")
    args = parser.parse_args()

    threshold_khz = int(args.boost_threshold_ghz * 1e6)
    labeled_stats = []

    for path in args.paths:
        p = Path(path)
        label = p.parent.name if p.parent.name not in ("", ".", "logs") else p.stem
        rows = load_trace(path)
        stats = summarize(rows, threshold_khz)
        print_summary(label, path, stats)
        if not args.no_timeline:
            print_timeline(rows, threshold_khz)
        labeled_stats.append((label, stats))

    if len(labeled_stats) > 1:
        print_comparison(labeled_stats)


if __name__ == "__main__":
    try:
        main()
    except (FileNotFoundError, ValueError) as e:
        print(f"error: {e}", file=sys.stderr)
        sys.exit(1)
