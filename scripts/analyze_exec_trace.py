#!/usr/bin/env python3
"""Analyse a pilot_exec_trace run: post-view-change burst, throttle knee, and offline
replay of candidate per-view throughput records for the perf bar.

    python3 scripts/analyze_exec_trace.py results/<run> [--node 3] [--steady R]

Inputs (all in the run directory):
  node_<id>.log           PILOT_EXEC lines (node/pilottrace.go) and perf-timer evictions
  throttle_manager.jsonl  client-side leader notices and gate commands

Times are in seconds. "open" is when the perf window opens: the first executed slot at
or past start_seq (maxSeq + performance.window_delay_slots), as in
observeExecutedSlotForTimedThroughput. Fresh = the client had not targeted this leader
yet when it was installed; held = it already had (gate on from the start).
"""
import argparse
import bisect
import json
import re
import statistics as st
from datetime import datetime, timezone

PILOT = re.compile(
    r"PILOT_EXEC view=\((\d+),(\d+)\) leader=(\w+) install_unix_us=(\d+) max_seq=(-?\d+) "
    r"start_seq=(-?\d+) first_seq=(-?\d+) n=(\d+) end=(\w+) offsets_us=([\d,]*)")
EVICT = re.compile(r"Perf timer: throughput [\d.]+ is below timed target [\d.]+ for view \((\d+),(\d+)\)")
BAR_FACTOR = 0.91
WINDOW_VIEWS = 7  # 3f+1 at n = 7
DEFAULT_MAX = 160.0


def log_ts(line):
    return datetime.strptime(line[7:33].strip(), "%Y-%m-%d %H:%M:%S.%f")


def pct(xs, q):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(q * len(xs)))] if xs else float("nan")


def summary(xs):
    if not xs:
        return "n=0"
    return f"n={len(xs)} p10={pct(xs, .1):.1f} p50={st.median(xs):.1f} p90={pct(xs, .9):.1f}"


def load(run, node):
    views, evict_local = [], {}
    tz_off = None
    for line in open(f"{run}/node_{node}.log"):
        m = PILOT.search(line)
        if m:
            install = int(m[4]) / 1e6
            offs = [int(x) / 1e6 for x in m[10].split(",")] if m[10] else []
            v = dict(view=(int(m[1]), int(m[2])), leader=m[3] == "true", install=install,
                     max_seq=int(m[5]), start_seq=int(m[6]), first_seq=int(m[7]),
                     end=m[9], t=offs)
            views.append(v)
            if tz_off is None:  # node log clock is local wall time; anchor it to unix
                naive = log_ts(line).replace(tzinfo=timezone.utc).timestamp()
                tz_off = round((naive - install) / 1800) * 1800
            continue
        m = EVICT.search(line)
        if m:
            evict_local.setdefault((int(m[1]), int(m[2])), log_ts(line))
    evict = {k: ts.replace(tzinfo=timezone.utc).timestamp() - (tz_off or 0) for k, ts in evict_local.items()}

    notice, gate_on = {}, {}
    pending = {}
    for l in open(f"{run}/throttle_manager.jsonl"):
        e = json.loads(l)
        if e["kind"] == "notice":
            k = (e["gen"], e["counter"])
            notice[k] = not e["already_targeted"]
            if notice[k]:
                pending[e["leader"]] = k
        elif e["kind"] == "command" and e["enable"] and e["node"] in pending:
            gate_on[pending.pop(e["node"])] = e["t"] / 1e9

    views.sort(key=lambda v: v["view"])
    for i, v in enumerate(views):
        k = v["view"]
        v["fresh"] = notice.get(k)  # None: client saw no notice (genesis)
        idx = v["start_seq"] - v["first_seq"]
        v["open"] = v["t"][idx] if v["t"] and 0 <= idx < len(v["t"]) else None
        if k in evict:
            v["vend"] = evict[k] - v["install"]
        elif i + 1 < len(views):
            v["vend"] = views[i + 1]["install"] - v["install"]  # upper bound
        else:
            v["vend"] = v["t"][-1] if v["t"] else 0.0
        v["evicted"] = k in evict
        # gate on, relative to install; held leaders are gated from the start
        if v["fresh"] is False:
            v["gate"] = float("-inf")
        elif k in gate_on:
            v["gate"] = gate_on[k] - v["install"]
        else:
            v["gate"] = None
    return views


def count(t, a, b):
    """Slots executed in [a, b)."""
    return bisect.bisect_left(t, b) - bisect.bisect_left(t, a)


def rate_curve(views, anchor, bin_s, horizon, rel=lambda v: v["open"]):
    """Median execution rate per bin, over views live for that whole bin."""
    rows = []
    nb = int(horizon / bin_s)
    for b in range(nb):
        rs = []
        for v in views:
            a = rel(v)
            if a is None:
                continue
            lo, hi = a + anchor + b * bin_s, a + anchor + (b + 1) * bin_s
            if lo < 0 or hi > min(v["vend"], 2.5):
                continue
            rs.append(count(v["t"], lo, hi) / bin_s)
        if rs:
            rows.append((anchor + b * bin_s, st.median(rs), pct(rs, .1), pct(rs, .9), len(rs)))
    return rows


def print_curve(title, rows):
    print(f"\n{title}")
    print(f"  {'t (s)':>7} {'p50':>6} {'p10':>6} {'p90':>6} {'views':>6}")
    for t, p50, p10, p90, n in rows:
        print(f"  {t:7.2f} {p50:6.0f} {p10:6.0f} {p90:6.0f} {n:6d}")


def burst(views):
    """Views ungated for the whole 2.5 s trace: steady rate R from 1.0 s after open to
    the end of the trace (at least 1 s), burst B = slots in the first second beyond R,
    and when 90% of B is in."""
    rs, bs, settle, pre = [], [], [], []
    for v in views:
        o = v["open"]
        end = min(v["vend"], 2.5)
        if o is None or end - (o + 1.0) < 1.0:
            continue
        if v["gate"] is not None and v["gate"] < end:
            continue
        r = count(v["t"], o + 1.0, end) / (end - o - 1.0)
        b = count(v["t"], o, o + 1.0) - r * 1.0
        rs.append(r)
        bs.append(b)
        pre.append(v["start_seq"] - v["first_seq"])
        if b > 2:
            for k in range(1, 21):
                tt = k * 0.05
                if count(v["t"], o, o + tt) - r * tt >= 0.9 * b:
                    settle.append(tt)
                    break
    return rs, bs, settle, pre


def records(v, H, W, mode):
    """Value the live code would record for this view, or None."""
    o = v["open"]
    if o is None:
        return None
    end = min(v["vend"], 2.5)
    if mode == "fixed":
        a = o + H
        return count(v["t"], a, a + W) / W if a + W <= end else None
    best = None  # peak: best W-span starting at or after open + H, 50 ms steps
    a = o + H
    while a + W <= end:
        r = count(v["t"], a, a + W) / W
        best = r if best is None else max(best, r)
        a += 0.05
    return best


def replay(views, H, W, mode, steady):
    recs = [records(v, H, W, mode) for v in views]
    bars, defaults = [], 0
    for i in range(len(views)):
        prev = [r for r in recs[max(0, i - WINDOW_VIEWS):i] if r is not None]
        if prev:
            bars.append(BAR_FACTOR * max(prev))
        else:
            bars.append(BAR_FACTOR * DEFAULT_MAX)
            defaults += 1
    fresh = [r for r, v in zip(recs, views) if r is not None and v["fresh"]]
    held = [r for r, v in zip(recs, views) if r is not None and v["fresh"] is False]
    have = sum(r is not None for r in recs)
    over = sum(b > steady for b in bars) if steady else None
    return dict(have=have, fresh=fresh, held=held, bars=bars, defaults=defaults, over=over)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("run")
    ap.add_argument("--node", type=int, default=3)
    ap.add_argument("--steady", type=float, default=None,
                    help="steady rate R to judge bars against (default: measured from this run)")
    ap.add_argument("--skip-views", type=int, default=2, help="startup views to drop")
    args = ap.parse_args()

    views = load(args.run, args.node)[args.skip_views:]
    traced = [v for v in views if v["t"]]
    fresh = [v for v in traced if v["fresh"]]
    held = [v for v in traced if v["fresh"] is False]
    print(f"run {args.run}, node {args.node}: {len(views)} views traced, "
          f"{len(fresh)} fresh, {len(held)} held, {sum(v['evicted'] for v in views)} perf-evicted")
    print("slots from install to window open (O-set + delay):",
          summary([v["start_seq"] - v["first_seq"] for v in traced]))
    print("install -> window open (ms):", summary([1e3 * v["open"] for v in traced if v["open"] is not None]))
    print("install -> view end (s):", summary([v["vend"] for v in traced]))
    g = [v["gate"] - v["open"] for v in fresh if v["gate"] is not None and v["open"] is not None]
    print("fresh: client gate-on minus window open (s):", summary(g))

    rs, bs, settle, _ = burst(fresh + [v for v in traced if v["fresh"] is None])
    steady = args.steady or (st.median(rs) if rs else None)
    print("\n== Burst (views ungated for the whole trace)")
    print("steady rate R, 1.0 s after open to trace end (slots/s):", summary(rs))
    print("burst B, first-second slots beyond R:", summary(bs))
    print("time from open until 90% of B is in (s):", summary(settle))

    gated = [v for v in fresh if v["gate"] is not None and v["gate"] < 2.5]
    ungated = [v for v in fresh if v not in gated]
    print_curve("== Fresh leaders gated within the trace: rate vs time since window open (50 ms bins)",
                rate_curve(gated, -0.2, 0.05, 1.7))
    print_curve("== Fresh leaders gated within the trace: rate vs time since client gate-on (50 ms bins)",
                rate_curve(gated, -0.4, 0.05, 0.8, rel=lambda v: v["gate"]))
    print_curve("== Fresh leaders ungated for the trace: rate vs time since window open (50 ms bins)",
                rate_curve(ungated, -0.2, 0.05, 2.4))
    print_curve("== Held leaders: rate vs time since window open (100 ms bins)",
                rate_curve(held, 0.0, 0.1, 1.0))

    if steady is None:
        print("\nno steady rate measured; pass --steady (e.g. from the prep 3000 run)")
        return
    print(f"\n== Record replay (bar = {BAR_FACTOR} x max of last {WINDOW_VIEWS} records, "
          f"default {DEFAULT_MAX}; R = {steady:.1f})")
    print(f"  {'mode':5} {'H':>4} {'W':>4} {'rec%':>5} {'fresh rec p50':>13} {'held p50':>8} "
          f"{'bar p10':>7} {'bar p50':>7} {'bar p90':>7} {'bar>R %':>7} {'dflt %':>6}")
    for mode in ("fixed", "peak"):
        for H in (0.0, 0.1, 0.15, 0.2, 0.3):
            for W in (0.2, 0.3, 0.5, 1.0):
                r = replay(views, H, W, mode, steady)
                n = len(views)
                print(f"  {mode:5} {H:4.2f} {W:4.1f} {100 * r['have'] / n:5.0f} "
                      f"{st.median(r['fresh']) if r['fresh'] else float('nan'):13.1f} "
                      f"{st.median(r['held']) if r['held'] else float('nan'):8.1f} "
                      f"{pct(r['bars'], .1):7.1f} {st.median(r['bars']):7.1f} {pct(r['bars'], .9):7.1f} "
                      f"{100 * r['over'] / n:7.0f} {100 * r['defaults'] / n:6.0f}")


if __name__ == "__main__":
    main()
