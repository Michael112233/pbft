"""Separate host stalls from view-change pile-up in a perf-trigger run.

Usage: python3 scripts/perf_tick_misses.py --logs <run dir> [--ref-node 4] [--views]

Question it answers: when a fresh (ungated) leader is evicted at the first 1 s perf
tick, was that a one-off stall, or the follow-on of an earlier eviction (big
ViewChanges, long O-set re-runs, bar with no record)?

Per installed view (reference node's clock and log):
  gate      start / mid@<s> / never, from throttle_manager.jsonl (never if absent)
  vc_certs  prepared certs in the ViewChange that led to this view (ref node)
  vc_ms     ref node's VC start -> this view's install
  wopen_ms  install -> perf window open (3 new slots executed)
  tick1     every node's tick-1 throughput; the median of the non-leader nodes is shown
  bar       bar at install and its source (recorded / default)
  dead      ended with no perf window: nothing new executed in the view
  lost      slots short of the baseline rate in the first window-second, from the
            client's 100 ms committed series (report.json)
  slow      slow deliveries ("delivery took too long", ViewChange/NewView excluded) on
            all nodes during the first window-second (count / nodes); informational,
            >5 ms deliveries are common background

A fresh view is one whose leader is ungated for at least its first window-second.
A fresh view evicted at tick 1 is a miss: a SEED if none of the previous
--cascade-views views was a miss or a dead view, otherwise a CASCADE.

Stalls are counted on their own, independent of evictions: the baseline is the median
committed rate over steady 100 ms buckets (ungated leader, >= 0.5 s after its window
opened, no view change on the reference node); a bucket below --dip x baseline is a
dip, and consecutive dips merge into one stall event. Reported per steady minute
observed, so trapped stretches with few steady buckets do not look stall-free.
"""

import argparse
import collections
import datetime as dt
import glob
import json
import os
import re
import statistics

TS = re.compile(r"^\[\w+\] (\S+ \S+)")
INSTALL = re.compile(r"accepted new view message for view \{(\d+) (\d+)\} and from node (\d+)|Became leader for new view \((\d+),(\d+)\) and my id is (\d+)")
VC_CONTENT = re.compile(r"createVCContent carried (\d+) prepared certs; forView \((\d+), (\d+)\)")
BAR = re.compile(r"Max recent throughput for new view \((\d+),(\d+)\) is [\d.]+; target throughput set to ([\d.]+) \(source=(\w+)\)")
WOPEN = re.compile(r"Timed trigger: interval start seq \d+ reached at seq \d+")
TICK = re.compile(r"Perf timer: throughput ([\d.]+) is (above|below) timed target [\d.]+ for view \((\d+),(\d+)\), elapsed time ([\d.]+) seconds")
SLOW = re.compile(r"delivery took too long\. msgType=(\w+) .*elapsed=([\d.]+)ms")


def ts(line):
    m = TS.match(line)
    return dt.datetime.strptime(m.group(1), "%Y-%m-%d %H:%M:%S.%f").timestamp() if m else None


def gate_intervals(run):
    path = os.path.join(run, "throttle_manager.jsonl")
    gates, on = collections.defaultdict(list), {}
    if not os.path.exists(path):
        return gates
    for line in open(path):
        r = json.loads(line)
        if r.get("kind") == "command" and r.get("ok"):
            t = r["t"] / 1e9
            if r["enable"]:
                on.setdefault(r["node"], t)
            elif r["node"] in on:
                gates[r["node"]].append((on.pop(r["node"]), t))
    for node, t in on.items():
        gates[node].append((t, float("inf")))
    return gates


def gate_from(gates, node, t0, t1):
    """Seconds after t0 at which node's gate is live inside [t0, t1), or None."""
    hits = [max(0.0, a - t0) for a, b in gates[node] if b > t0 and a < t1]
    return min(hits) if hits else None


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--logs", required=True)
    ap.add_argument("--ref-node", type=int, default=4)
    ap.add_argument("--dip", type=float, default=0.8, help="a 100 ms bucket below dip x baseline is a stall")
    ap.add_argument("--cascade-views", type=int, default=5)
    ap.add_argument("--views", action="store_true", help="print one line per view")
    args = ap.parse_args()
    run = args.logs.rstrip("/")

    # reference node: installs, VC entries, bars, window opens, ends
    views = collections.OrderedDict()
    vc = {}            # forView -> (start time, certs)
    vc_running = []    # (start, install) intervals on the ref node
    cur, cur_vc_start = None, None
    for line in open(os.path.join(run, f"node_{args.ref_node}.log"), errors="ignore"):
        m = VC_CONTENT.search(line)
        if m:
            t = ts(line)
            vc.setdefault((int(m.group(2)), int(m.group(3))), (t, int(m.group(1))))
            if cur_vc_start is None:
                cur_vc_start = t
            if cur in views and views[cur]["end_t"] is None:
                views[cur]["end_t"] = t
            continue
        m = INSTALL.search(line)
        if m:
            g, c, leader = (m.group(1), m.group(2), m.group(3)) if m.group(1) else (m.group(4), m.group(5), m.group(6))
            v = (int(g), int(c))
            if v in views:
                continue
            t = ts(line)
            if cur_vc_start is not None:
                vc_running.append((cur_vc_start, t))
                cur_vc_start = None
            cur = v
            views[v] = {"install": t, "leader": int(leader), "wopen": None, "bar": None, "src": None,
                        "end_t": None, "end_tick": None, "ticks": {}}
            continue
        if cur is None:
            continue
        m = BAR.search(line)
        if m and (int(m.group(1)), int(m.group(2))) in views:
            views[(int(m.group(1)), int(m.group(2)))].update(bar=float(m.group(3)), src=m.group(4))
            continue
        if views[cur]["wopen"] is None and WOPEN.search(line):
            views[cur]["wopen"] = ts(line)
            continue
        m = TICK.search(line)
        if m and m.group(2) == "below":
            v = (int(m.group(3)), int(m.group(4)))
            if v in views and views[v]["end_tick"] is None:
                views[v]["end_tick"] = round(float(m.group(5)))

    # every node: tick-1 values and slow deliveries
    slow = []
    for path in sorted(glob.glob(os.path.join(run, "node_[0-9]*.log"))):
        if not re.search(r"node_\d+\.log$", path):
            continue
        node = int(re.search(r"node_(\d+)\.log$", path).group(1))
        for line in open(path, errors="ignore"):
            m = TICK.search(line)
            if m and round(float(m.group(5))) == 1:
                v = (int(m.group(3)), int(m.group(4)))
                if v in views:
                    views[v]["ticks"].setdefault(node, float(m.group(1)))
                continue
            m = SLOW.search(line)
            if m and m.group(1) not in ("MsgViewChangeMessage", "MsgNewViewMessage"):
                slow.append((ts(line), node, float(m.group(2))))
    slow.sort()

    def slow_in(a, b):
        return [s for s in slow if a <= s[0] < b]

    # client committed series -> slots/s per 100 ms bucket
    points = json.load(open(os.path.join(run, "report.json")))
    commit_t = [p["timestamp_unix_nano"] / 1e9 for p in points]
    commit_c = [p["committed_total"] for p in points]

    def committed_at(t):
        lo, hi = 0, len(commit_t)
        while lo < hi:
            mid = (lo + hi) // 2
            if commit_t[mid] <= t:
                lo = mid + 1
            else:
                hi = mid
        return commit_c[lo - 1] if lo else 0

    def rate(a, b):
        return (committed_at(b) - committed_at(a)) / 50 / (b - a)

    def med(xs):
        xs = [x for x in xs if x is not None]
        return f"{statistics.median(xs):.0f}" if xs else "-"

    gates = gate_intervals(run)
    keys = list(views)
    t0, t1 = views[keys[0]]["install"], views[keys[-1]]["install"] + 30
    in_vc = lambda t: any(a <= t < b for a, b in vc_running)

    # steady buckets: ungated leader, >= 0.5 s past window open, before the view ends
    steady = []
    for i, v in enumerate(keys):
        d = views[v]
        if d["wopen"] is None:
            continue
        end = d["end_t"] or (views[keys[i + 1]]["install"] if i + 1 < len(keys) else t1)
        a = d["wopen"] + 0.5
        while a + 0.1 <= end:
            if gate_from(gates, d["leader"], a, a + 0.1) is None and not in_vc(a):
                steady.append((a, rate(a, a + 0.1)))
            a += 0.1
    baseline = statistics.median(r for _, r in steady) if steady else 160.0
    events = []
    for a, r in steady:
        if r < args.dip * baseline:
            if events and a - events[-1][1] <= 0.15:
                events[-1][1] = a
                events[-1][2] += (baseline - r) * 0.1
            else:
                events.append([a, a, (baseline - r) * 0.1])
    steady_min = len(steady) * 0.1 / 60

    rows = []
    for i, v in enumerate(keys):
        d = views[v]
        nxt = views[keys[i + 1]]["install"] if i + 1 < len(keys) else d["install"] + 30
        g = gate_from(gates, d["leader"], d["install"], nxt)
        gate = "never" if g is None else ("start" if g < 0.05 else f"mid@{g:.1f}")
        wopen_ms = (d["wopen"] - d["install"]) * 1000 if d["wopen"] else None
        ungated_first_sec = g is None or (d["wopen"] is not None and g >= (d["wopen"] - d["install"]) + 1.0)
        reps = [x for n, x in d["ticks"].items() if n != d["leader"]]
        s = slow_in(d["wopen"], d["wopen"] + 1.0) if d["wopen"] else []
        vcs = vc.get(v)
        rows.append({
            "view": v, "leader": d["leader"], "tenure": nxt - d["install"], "gate": gate,
            "fresh": ungated_first_sec and d["wopen"] is not None,
            "vc_certs": vcs[1] if vcs else None,
            "vc_ms": (d["install"] - vcs[0]) * 1000 if vcs else None,
            "wopen_ms": wopen_ms, "bar": d["bar"], "src": d["src"],
            "tick1": statistics.median(reps) if reps else None,
            "lost": baseline - rate(d["wopen"], d["wopen"] + 1.0) if d["wopen"] else None,
            "end_tick": d["end_tick"],
            "miss": d["end_tick"] == 1,
            "dead": d["wopen"] is None and i + 1 < len(keys),
            "slow_msgs": len(s), "slow_nodes": len({x[1] for x in s}),
        })
    for i, r in enumerate(rows):
        if r["fresh"] and r["miss"]:
            prev = rows[max(0, i - args.cascade_views):i]
            r["kind"] = "CASCADE" if any((p["fresh"] and p["miss"]) or p["dead"] for p in prev) else "SEED"

    if args.views:
        num = lambda x, nd=0: "-" if x is None else str(round(x, nd) if nd else round(x))
        for r in rows:
            print(f"{str(r['view']):9s} L{r['leader']} ten={r['tenure']:5.2f} gate={r['gate']:8s} "
                  f"vc_certs={num(r['vc_certs']):>4s} vc_ms={num(r['vc_ms']):>5s} wopen_ms={num(r['wopen_ms']):>5s} "
                  f"bar={r['bar'] or 0:6.1f} {r['src'] or '-':8s} tick1={num(r['tick1'], 1):>6s} "
                  f"lost={num(r['lost']):>4s} slow={r['slow_msgs']}/{r['slow_nodes']} "
                  f"{'DEAD ' if r['dead'] else ''}{r.get('kind', '')}")
        print()

    fresh = [r for r in rows if r["fresh"]]
    misses = [r for r in fresh if r["miss"]]
    seeds = [r for r in misses if r["kind"] == "SEED"]
    casc = [r for r in misses if r["kind"] == "CASCADE"]
    dur_min = (t1 - t0) / 60
    print(f"run {os.path.basename(run)}: {len(rows)} views over {dur_min:.1f} min (ref node {args.ref_node})")
    print(f"baseline {baseline:.0f} slots/s over {steady_min:.1f} steady min; stall events (100 ms buckets < "
          f"{args.dip:.0%} of baseline, merged): {len(events)} = {len(events) / steady_min if steady_min else 0:.2f} per steady min, "
          f"median {statistics.median(e[2] for e in events) if events else 0:.0f} slots lost each")
    sizes = collections.Counter("<5" if e[2] < 5 else "5-10" if e[2] < 10 else ">=10" for e in events)
    print(f"  by slots lost: <5: {sizes['<5']}   5-10: {sizes['5-10']}   >=10: {sizes['>=10']} "
          f"(a fresh leader's tick-1 margin is baseline - bar, ~{baseline - 0.91 * 164:.0f} slots)")
    print(f"fresh views: {len(fresh)}   tick-1 misses: {len(misses)}  (SEED {len(seeds)}, CASCADE {len(casc)})   dead views: {sum(r['dead'] for r in rows)}")
    lost_p = lambda rs: med(r["lost"] for r in rs)
    print(f"  slots lost in the first window-second, median: seeds {lost_p(seeds)}, cascades {lost_p(casc)}, "
          f"fresh passes {lost_p([r for r in fresh if not r['miss']])}")
    print(f"  bar source on misses: {dict(collections.Counter(r['src'] for r in misses))}")

    # Which perf tick removed each leader. Under throttle with prep P, a fresh leader's
    # gate lands at ~P s, so the expected eviction is tick P+1; a leader gated from the
    # start (already-targeted draw) is expected out at tick 1.
    def tick_dist(rs):
        c = collections.Counter("dead" if r["dead"] else f"t{r['end_tick']}" if r["end_tick"] else "other" for r in rs)
        order = sorted((k for k in c if k.startswith("t")), key=lambda k: int(k[1:])) + [k for k in ("other", "dead") if k in c]
        n = sum(c.values())
        return "  ".join(f"{k}:{c[k]} ({100 * c[k] / n:.0f}%)" for k in order) if n else "-"
    gated_start = [r for r in rows if r["gate"] == "start"]
    print("\neviction tick (ref node; 'other' = no perf eviction logged, e.g. f+1 VC or end of run):")
    print(f"  fresh (ungated first window-second): {tick_dist(fresh)}")
    print(f"  gated from start:                   {tick_dist(gated_start)}")

    print("\nview-change size vs cost (all installed views):")
    print(f"  {'certs':>9s} {'views':>5s} {'vc_ms p50':>9s} {'wopen_ms p50':>12s} {'dead':>4s} {'fresh tick1 p50':>15s} {'fresh misses':>12s}")
    for lo, hi in [(0, 50), (50, 150), (150, 230), (230, 10**6)]:
        sel = [r for r in rows if r["vc_certs"] is not None and lo <= r["vc_certs"] < hi]
        if sel:
            fr = [r for r in sel if r["fresh"]]
            print(f"  {lo:>4d}-{hi if hi < 10**6 else '':<4} {len(sel):5d} {med(r['vc_ms'] for r in sel):>9s} "
                  f"{med(r['wopen_ms'] for r in sel):>12s} {sum(r['dead'] for r in sel):4d} "
                  f"{med(r['tick1'] for r in fr):>15s} {sum(r['miss'] for r in fr):5d}/{len(fr):<6d}")

    print("\nper minute:")
    print(f"  {'min':>4s} {'stalls':>6s} {'>=10':>4s} {'steady s':>8s} {'fresh':>5s} {'seed':>4s} {'casc':>4s} {'dead':>4s} {'bar default':>11s}")
    per = collections.defaultdict(lambda: collections.Counter())
    for r in rows:
        k = int((views[r["view"]]["install"] - t0) // 60)
        per[k]["fresh"] += r["fresh"]
        per[k]["seed"] += r.get("kind") == "SEED"
        per[k]["casc"] += r.get("kind") == "CASCADE"
        per[k]["dead"] += r["dead"]
        per[k]["def"] += r["src"] == "default"
    for e in events:
        per[int((e[0] - t0) // 60)]["stalls"] += 1
        per[int((e[0] - t0) // 60)]["big"] += e[2] >= 10
    for a, _ in steady:
        per[int((a - t0) // 60)]["steady"] += 1
    for k in sorted(per):
        c = per[k]
        print(f"  {k:4d} {c['stalls']:6d} {c['big']:4d} {c['steady'] / 10:8.0f} {c['fresh']:5d} {c['seed']:4d} {c['casc']:4d} {c['dead']:4d} {c['def']:11d}")


if __name__ == "__main__":
    main()
