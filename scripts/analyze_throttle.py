#!/usr/bin/env python3
"""Analyse a targeted-proposal-throttling run.

Builds per-leader tenure windows from the client's leader timeline, overlays the
controller's ACTIVE gate intervals, and reports the tenure-weighted fraction of
leader tenure spent throttled alongside end-to-end committed TPS. See
docs / the plan "Targeted proposal throttling" for the experiment.

Inputs (all under --logs, default logs/):
  leader_timeline.jsonl   one {gen,counter,leader,observed_at} per accepted leader
  throttle_manager.jsonl  notice / retarget / command records from the controller
  report.json             TPS series ({average_tps, window_tps, committed_total})

Startup transient (why --skip-tenures defaults to 1)
----------------------------------------------------
Node 1 is primary at the genesis view (1,1) without a view change, and a node
only sends LeaderIdUpdate when it installs a NewView (node/view.go). So the
client never gets a leader notice for the genesis tenure: node 1's first tenure
runs completely ungated and does not appear in leader_timeline.jsonl at all.

The first row that *does* appear is the leader of view (1,2) -- node 2 under
round-robin. The controller first learns of it only once it is already sitting,
so it pays a full prep inside that tenure and never pre-prepared it: coverage
there is ~60% at k=1 and ~80% at k>=2, versus the 80% / 100% steady state that
starts with the third leader. Averaging that window in biases the headline
number down by about a point over a few-minute run, which matters when the
hypothesis is a ~14-point gap, so it is dropped by default.

The skip is positional, never by node id: nodes 1 and 2 lead again later in the
rotation (counter 5 -> node 1 at n=4) and those later tenures are steady state.

Usage:
  python3 scripts/analyze_throttle.py [--logs logs] [--json] [--skip-tenures N]
"""

import argparse
import json
import os
import sys


def read_jsonl(path):
    rows = []
    if not os.path.exists(path):
        return rows
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line:
                rows.append(json.loads(line))
    return rows


def merge_intervals(intervals):
    """Merge overlapping [start, end] intervals (ns). A gate re-enabled on the
    same node before the previous disable is acked must not be double-counted."""
    if not intervals:
        return []
    intervals = sorted(intervals)
    merged = [list(intervals[0])]
    for s, e in intervals[1:]:
        if s <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], e)
        else:
            merged.append([s, e])
    return merged


def overlap(a_start, a_end, intervals):
    total = 0
    for s, e in intervals:
        lo, hi = max(a_start, s), min(a_end, e)
        if hi > lo:
            total += hi - lo
    return total


def build_active_intervals(commands, run_end):
    """Per node, pair enable-acks with the next disable-ack (or run_end). Only
    acknowledged (ok) commands count: a slot is ACTIVE from the moment the node
    confirms the gate on until it confirms it off."""
    by_node = {}
    for r in commands:
        if r.get("kind") != "command" or not r.get("ok"):
            continue
        by_node.setdefault(r["node"], []).append((r["t"], r["enable"]))

    active = {}
    for node, events in by_node.items():
        events.sort()
        intervals, open_at = [], None
        for t, enable in events:
            if enable and open_at is None:
                open_at = t
            elif not enable and open_at is not None:
                intervals.append((open_at, t))
                open_at = None
        if open_at is not None:
            intervals.append((open_at, run_end))
        active[node] = merge_intervals(intervals)
    return active


def build_tenures(timeline, run_end):
    """Each timeline entry opens a tenure that closes at the next entry (or run
    end). Returns [(leader, start_ns, end_ns)]."""
    rows = sorted(timeline, key=lambda r: r["observed_at"])
    tenures = []
    for i, r in enumerate(rows):
        start = r["observed_at"]
        end = rows[i + 1]["observed_at"] if i + 1 < len(rows) else run_end
        if end > start:
            tenures.append((r["leader"], start, end))
    return tenures


def load_tps(path):
    if not os.path.exists(path):
        return None
    series = json.load(open(path))
    if not series:
        return None
    last = series[-1]
    return {
        "average_tps": last.get("average_tps"),
        "committed_total": last.get("committed_total"),
        "final_window_tps": last.get("window_tps"),
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--logs", default="logs")
    ap.add_argument("--json", action="store_true", help="emit machine-readable JSON")
    ap.add_argument(
        "--skip-tenures",
        type=int,
        default=1,
        metavar="N",
        help="drop the first N tenure windows as startup transient (default 1; "
        "see the module docstring -- the genesis tenure is absent from the "
        "timeline, so N=1 drops the first reactively-prepped leader). 0 keeps all.",
    )
    args = ap.parse_args()
    if args.skip_tenures < 0:
        print("--skip-tenures must not be negative", file=sys.stderr)
        return 1

    timeline = read_jsonl(os.path.join(args.logs, "leader_timeline.jsonl"))
    throttle = read_jsonl(os.path.join(args.logs, "throttle_manager.jsonl"))
    tps = load_tps(os.path.join(args.logs, "report.json"))

    if not timeline:
        print("no leader_timeline.jsonl rows; nothing to analyse", file=sys.stderr)
        return 1

    # Run end: the latest timestamp seen anywhere.
    run_end = max(r["observed_at"] for r in timeline)
    for r in throttle:
        run_end = max(run_end, r.get("t", 0))

    all_tenures = build_tenures(timeline, run_end)
    active = build_active_intervals(throttle, run_end)

    # Drop the leading transient windows (see the module docstring). Positional,
    # so a later tenure of the same node still counts.
    skipped = [
        {"leader": leader, "len_s": round((end - start) / 1e9, 3)}
        for leader, start, end in all_tenures[: args.skip_tenures]
    ]
    tenures = all_tenures[args.skip_tenures :]
    if not tenures:
        print(
            f"only {len(all_tenures)} tenure window(s) and --skip-tenures="
            f"{args.skip_tenures} drops them all; nothing to analyse",
            file=sys.stderr,
        )
        return 1

    # Per-tenure throttled fraction, and the weighted mean.
    total_len = 0.0
    total_throttled = 0.0
    per_leader = {}
    for leader, start, end in tenures:
        length = end - start
        imp = overlap(start, end, active.get(leader, []))
        total_len += length
        total_throttled += imp
        acc = per_leader.setdefault(leader, [0.0, 0.0, 0])
        acc[0] += length
        acc[1] += imp
        acc[2] += 1

    weighted = (total_throttled / total_len) if total_len else 0.0

    notices = [r for r in throttle if r.get("kind") == "notice"]
    lags = [r["lag_ms"] for r in notices if "lag_ms" in r]
    already = sum(1 for r in notices if r.get("already_targeted"))
    cmd_fail = sum(1 for r in throttle if r.get("kind") == "command" and not r.get("ok"))

    result = {
        "tenures": len(tenures),
        "tenures_total": len(all_tenures),
        "tenures_skipped": skipped,
        "mean_tenure_s": round(total_len / len(tenures) / 1e9, 3) if tenures else 0,
        "throttled_fraction_weighted": round(weighted, 4),
        "notices": len(notices),
        "notice_lag_ms_mean": round(sum(lags) / len(lags), 1) if lags else None,
        "notice_lag_ms_max": max(lags) if lags else None,
        "already_targeted": already,
        "command_failures": cmd_fail,
        "tps": tps,
        "per_leader": {
            str(k): {
                "tenures": v[2],
                "throttled_fraction": round(v[1] / v[0], 4) if v[0] else 0.0,
            }
            for k, v in sorted(per_leader.items())
        },
    }

    if args.json:
        print(json.dumps(result, indent=2))
        return 0

    print(f"tenures analysed         : {result['tenures']} of {result['tenures_total']} (mean {result['mean_tenure_s']}s)")
    if skipped:
        dropped = ", ".join(f"node {s['leader']} ({s['len_s']}s)" for s in skipped)
        print(f"startup transient dropped: {dropped}")
        print("  (genesis tenure of node 1 never reaches the timeline: no NewView, no leader update)")
    print(f"throttled fraction (wtd) : {result['throttled_fraction_weighted']:.1%}")
    if result["notice_lag_ms_mean"] is not None:
        print(f"controller notice lag    : mean {result['notice_lag_ms_mean']}ms, max {result['notice_lag_ms_max']}ms")
    print(f"leader already targeted  : {result['already_targeted']} of {result['notices']} notices")
    print(f"gate command failures    : {result['command_failures']}")
    if tps:
        print(f"committed TPS (avg)      : {tps['average_tps']}  (total {tps['committed_total']})")
    print("per-leader throttled fraction:")
    for leader, v in result["per_leader"].items():
        print(f"  node {leader}: {v['throttled_fraction']:.1%} over {v['tenures']} tenure(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
