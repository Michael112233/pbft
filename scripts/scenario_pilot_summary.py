"""Per-generation state and reward from scenario x action pilots (one action per run).

Usage: python3 scripts/scenario_pilot_summary.py [--ref-node 4] [--out FILE] <run dir> ...
       e.g. python3 scripts/scenario_pilot_summary.py results/all_new_scenario_pilots/*

Run dirs are named <scenario>_<action> (e.g. throttle_pfe). Each run is an oracle run
pinned to one action with epoch_mode on, so every epoch is one generation of that
action. Generation windows are cut at the reference node's "received learning-agent
decision for generation g" lines; generation 1 (startup, genesis view) is skipped.

Per generation it reports, from the reference node (default 4: never dead, never a
proposal-delay node, the epoch aggregator):
  tput          client committed tx/s over the window (report.json)
  vc_rate       view changes started per second (same patterns as
                latency_pilot_summary.py, including the generation switch itself)
  install_rate  views installed per second
  interval_ms   50000 / tput, capped at 1000 (batch 50, as in the synthetic state)
"""

import argparse
import datetime as dt
import json
import os
import re
import statistics

TS = re.compile(r"^\[\w+\] (\S+ \S+)")
VC_START = re.compile(
    r"Leader progress timer expired; entering view change"
    r"|New view timer expired; entering the next view change"
    r"|Entering view change after receiving f\+1"
    r"|Perf timer: .* triggering view change"
    r"|received learning-agent decision for generation"
)
INSTALL = re.compile(r"accepted new view message for view \{\d+ \d+\}|Became leader for new view \(\d+,\d+\)")
DECISION = re.compile(r"received learning-agent decision for generation (\d+)")
BATCH_SIZE = 50
MAX_INTERVAL_MS = 1000.0


def ts(line):
    m = TS.match(line)
    return dt.datetime.strptime(m.group(1), "%Y-%m-%d %H:%M:%S.%f").timestamp() if m else None


def committed_at(t_arr, c_arr, t):
    lo, hi = 0, len(t_arr)
    while lo < hi:
        mid = (lo + hi) // 2
        if t_arr[mid] <= t:
            lo = mid + 1
        else:
            hi = mid
    return c_arr[lo - 1] if lo else 0


def summarize(run, ref_node):
    scenario, action = os.path.basename(run.rstrip("/")).split("_")[:2]
    points = json.load(open(os.path.join(run, "report.json")))
    t_arr = [p["timestamp_unix_nano"] / 1e9 for p in points]
    c_arr = [p["committed_total"] for p in points]

    decisions, vc, inst = {}, [], []
    for line in open(os.path.join(run, f"node_{ref_node}.log"), errors="ignore"):
        if not (VC_START.search(line) or INSTALL.search(line)):
            continue
        t = ts(line)
        if t is None:
            continue
        m = DECISION.search(line)
        if m:
            decisions.setdefault(int(m.group(1)), t)
        if VC_START.search(line):
            vc.append(t)
        if INSTALL.search(line):
            inst.append(t)

    gens = []
    ds = sorted(decisions.items())
    for (g, a), (_, b) in zip(ds, ds[1:]):
        tput = (committed_at(t_arr, c_arr, b) - committed_at(t_arr, c_arr, a)) / (b - a)
        gens.append({
            "gen": g + 1,
            "len_s": b - a,
            "tput": tput,
            "vc_rate": sum(a <= t < b for t in vc) / (b - a),
            "install_rate": sum(a <= t < b for t in inst) / (b - a),
            "interval_ms": MAX_INTERVAL_MS if tput <= 0 else min(MAX_INTERVAL_MS, 1000.0 * BATCH_SIZE / tput),
        })
    return {"scenario": scenario, "action": action, "generations": gens}


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("runs", nargs="+")
    ap.add_argument("--ref-node", type=int, default=4)
    ap.add_argument("--out", default=None, help="write the per-generation rows as JSON")
    args = ap.parse_args()

    rows = [summarize(r, args.ref_node) for r in sorted(args.runs)]
    print(f"{'scenario':9s} {'act':5s} {'n':>2s} {'tput med':>8s} {'tput min..max':>15s} "
          f"{'vc/s med':>8s} {'vc/s min..max':>13s} {'inst/s':>6s} {'int ms med':>10s} {'int min..max':>15s}")
    for r in rows:
        g = r["generations"]
        if not g:
            print(f"{r['scenario']:9s} {r['action']:5s}  no complete generation")
            continue
        col = lambda k: [x[k] for x in g]
        med = lambda k: statistics.median(col(k))
        print(f"{r['scenario']:9s} {r['action']:5s} {len(g):2d} {med('tput'):8.0f} "
              f"{min(col('tput')):7.0f}..{max(col('tput')):<7.0f} {med('vc_rate'):8.3f} "
              f"{min(col('vc_rate')):6.3f}..{max(col('vc_rate')):<6.3f} {med('install_rate'):6.3f} "
              f"{med('interval_ms'):10.1f} {min(col('interval_ms')):7.1f}..{max(col('interval_ms')):<7.1f}")
    if args.out:
        json.dump(rows, open(args.out, "w"), indent=1)


if __name__ == "__main__":
    main()
