"""What the Fixed/Perf progress and new-view timers have to cover, per run, from the
PROGRESS lines every node logs (node/progressstats.go).

Usage: python3 scripts/timer_budget_summary.py [--factor 1.5] [--round 50] <run dir> ...
       e.g. python3 scripts/timer_budget_summary.py results/timer_lan_n7/*

Per run and action:
  nv        PROGRESS NV nv_timer_to_install_ms, every role: from the last start of the
            node's new-view timer to its install, i.e. what the new-view timer covers.
            RoundRobin starts it at the 2f+1 ViewChanges; Election only when the
            candidate's VDF finishes or the node grants its vote. Views with no timer
            start for that view (-1) are left out.
  quorum    PROGRESS NV quorum_to_install_ms, replicas: 2f+1 ViewChanges to install.
            Equal to nv under RoundRobin; under Election it also holds the VDF race.
and over the replicas only (the leader arms no progress timer):
  first     PROGRESS VIEW first_exec_ms: install to the first executed slot.
  gap       PROGRESS VIEW max_gap_ms: the largest gap between executed slots in a view.
  prog      max(first, gap) per view: what the progress timer must cover.
  expiries  "Leader progress timer expired" / "New view timer expired" lines.

Each column gives p50 / p99 / p99.9 / max in ms. "g1" rows are the first view of a
generation (counter 1), which also carries the generation switch.

On an emulated WAN (the run dir's hosts.json has a wan block, docs/multi-host.md) every row is
also split by site: "leader site>node site", e.g. "far>near" for near replicas under a far
leader. Logs written before the PROGRESS lines carried leader= show "?" for the leader site.

The last lines apply the timer rule over all runs given: timeout = round up to
--round ms of --factor x the p99.9 need. Pass only runs whose timers were long
enough not to evict anyone, or the needs are cut off at the old timeout.
"""

import argparse
import collections
import glob
import json
import math
import os
import re

# leader= is optional: older logs do not have it.
NV = re.compile(r"PROGRESS NV: view \((\d+),(\d+)\) role=(\w+)(?: leader=(\d+))? action=(\w+) "
                r"nv_timer_to_install_ms=(-?[\d.]+) quorum_to_install_ms=(-?[\d.]+)")
VIEW = re.compile(r"PROGRESS VIEW: view \((\d+),(\d+)\) role=(\w+)(?: leader=(\d+))? action=(\w+) "
                  r"first_exec_ms=(-?[\d.]+) max_gap_ms=([\d.]+) slots=(\d+)")
EXPIRY = {"progress": "Leader progress timer expired", "new_view": "New view timer expired"}


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(math.ceil(p * len(xs))) - 1)] if xs else float("nan")


def stats(xs):
    if not xs:
        return f"{'-':>27s}"
    return f"{pct(xs, .5):6.1f} {pct(xs, .99):6.1f} {pct(xs, .999):6.1f} {max(xs):6.1f}"


def node_sites(run):
    """node id -> site name from the run's hosts.json wan block, or None without one."""
    try:
        wan = json.load(open(os.path.join(run, "hosts.json"))).get("wan")
    except (OSError, ValueError):
        return None
    if not wan:
        return None
    return {int(i): site for site, members in wan.get("sites", {}).items() for i in members.get("nodes", [])}


def load(run):
    # keys: (action, first-of-gen, sites), sites = "leader site>node site" on a WAN, else ""
    nv = collections.defaultdict(list)      # new-view timer start to install, every role
    quorum = collections.defaultdict(list)  # replicas' quorum to install
    prog = collections.defaultdict(lambda: {"first": [], "gap": [], "prog": []})
    expiries = collections.Counter()
    sites = node_sites(run)

    def key(view_ctr, action, leader, me):
        where = ""
        if sites is not None:
            where = f"{sites.get(int(leader), '?') if leader else '?'}>{sites.get(me, '?')}"
        return (action, view_ctr == "1", where)

    for path in glob.glob(os.path.join(run, "node_*.log")):
        m = re.search(r"node_(\d+)\.log$", path)
        if not m:
            continue
        me = int(m.group(1))
        for line in open(path, errors="ignore"):
            if "PROGRESS" not in line:
                for kind, text in EXPIRY.items():
                    if text in line:
                        expiries[kind] += 1
                continue
            m = NV.search(line)
            if m:
                k = key(m.group(2), m.group(5), m.group(4), me)
                if float(m.group(6)) >= 0:
                    nv[k].append(float(m.group(6)))
                if m.group(3) == "replica" and float(m.group(7)) >= 0:
                    quorum[k].append(float(m.group(7)))
                continue
            m = VIEW.search(line)
            if m and m.group(3) == "replica" and int(m.group(8)) > 0:
                first, gap = float(m.group(6)), float(m.group(7))
                d = prog[key(m.group(2), m.group(5), m.group(4), me)]
                d["first"].append(first)
                d["gap"].append(gap)
                d["prog"].append(max(first, gap))
    return nv, quorum, prog, expiries


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("runs", nargs="+")
    ap.add_argument("--factor", type=float, default=1.5)
    ap.add_argument("--round", type=int, default=50)
    a = ap.parse_args()

    all_nv, all_prog = [], []
    hdr = "p50    p99  p99.9    max"
    print(f"{'run':26s} {'action':22s} {'view':4s} {'sites':9s} {'n':>5s}  nv: {hdr}   quorum: {hdr}   first: {hdr}   "
          f"gap: {hdr}   prog: {hdr}")
    for run in sorted(a.runs):
        nv, quorum, prog, expiries = load(run)
        name = os.path.basename(run.rstrip("/"))
        for key in sorted(set(nv) | set(quorum) | set(prog)):
            action, g1, where = key
            p = prog.get(key, {"first": [], "gap": [], "prog": []})
            print(f"{name:26s} {action:22s} {'g1' if g1 else 'rest':4s} {where:9s} {len(p['prog']):5d}  "
                  f"nv: {stats(nv.get(key, []))}   quorum: {stats(quorum.get(key, []))}   first: {stats(p['first'])}   "
                  f"gap: {stats(p['gap'])}   "
                  f"prog: {stats(p['prog'])}")
            all_nv += nv.get(key, [])
            all_prog += p["prog"]
        print(f"{name:26s} expiries: progress {expiries['progress']}, new-view {expiries['new_view']}")

    def rule(xs):
        if not xs:
            return float("nan"), "-"
        need = pct(xs, .999)
        return need, a.round * math.ceil(a.factor * need / a.round)
    for label, xs in (("progress (fixed_trigger_timeout_ms)", all_prog), ("new-view (fixed_new_view_timeout_ms)", all_nv)):
        need, value = rule(xs)
        print(f"{label:38s} need p99.9 {need:7.1f} ms over {len(xs)} views -> {value} ms "
              f"(round up to {a.round} of {a.factor} x need)")


if __name__ == "__main__":
    main()
