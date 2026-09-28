"""Summarize the scenario x action latency pilots (config/latency_pilots/).

Usage: python3 scripts/latency_pilot_summary.py [results/<scenario>_<action>_* ...]
       (no args: every results/{healthy,pdelay,ndelay,ndfcrash,farnode}_* run)

Per run it reports, with node 3 as the reference node (never dead, never far):
  - client throughput in epoch 1, epoch 2 and over the whole run (client TPS series,
    split at node 3's "received learning-agent decision for generation g" lines),
  - view changes started and views installed per second in epoch 2,
  - who led during epoch 2 (share of time per leader, and share spent in view change),
  - client committed latency over the whole run (latencyreport.json),
  - FixedAware candidate lists (AWARE lines).
Epoch 1 starts at the first client commit.
"""

import datetime as dt
import glob
import json
import os
import re
import sys

REF_NODE = 3
TS = re.compile(r"^\[\w+\] (\S+ \S+)")
VC_START = re.compile(
    r"Leader progress timer expired; entering view change"
    r"|New view timer expired; entering the next view change"
    r"|Entering view change after receiving f\+1"
    r"|Perf timer: .* triggering view change"
    r"|received learning-agent decision for generation"
)
INSTALL_REPLICA = re.compile(r"accepted new view message for view \{(\d+) (\d+)\} and from node (\d+)")
INSTALL_LEADER = re.compile(r"Became leader for new view \((\d+),(\d+)\) and my id is (\d+)")
DECISION = re.compile(r"received learning-agent decision for generation (\d+)")
AWARE = re.compile(r"AWARE: source=\S+ gen=(\d+) .* scores=(\[[^\]]*\]) candidates=(\[[^\]]*\])")


def ts(line):
    m = TS.match(line)
    return dt.datetime.strptime(m.group(1), "%Y-%m-%d %H:%M:%S.%f").timestamp() if m else None


def committed_at(points, t):
    """Client committed_total at unix time t (last sample at or before t)."""
    best = 0
    for p in points:
        if p["timestamp_unix_nano"] / 1e9 <= t:
            best = p["committed_total"]
        else:
            break
    return best


def summarize(run):
    name = os.path.basename(run.rstrip("/"))
    scenario, action = name.split("_")[0], name.split("_")[1]
    rep = json.load(open(os.path.join(run, "report.json")))
    points = rep if isinstance(rep, list) else rep.get("points", [])
    lat = json.load(open(os.path.join(run, "latencyreport.json")))

    decisions, vc_starts, installs, aware = {}, [], [], []
    for line in open(os.path.join(run, f"node_{REF_NODE}.log"), errors="ignore"):
        t = ts(line)
        if t is None:
            continue
        m = DECISION.search(line)
        if m and int(m.group(1)) not in decisions:
            decisions[int(m.group(1))] = t
        if VC_START.search(line):
            vc_starts.append(t)
        m = INSTALL_REPLICA.search(line) or INSTALL_LEADER.search(line)
        if m:
            installs.append((t, (int(m.group(1)), int(m.group(2))), int(m.group(3))))
        m = AWARE.search(line)
        if m:
            aware.append((int(m.group(1)), m.group(2), m.group(3)))

    first = next((p for p in points if p["committed_total"] > 0), None)
    t_first = first["timestamp_unix_nano"] / 1e9 if first else None
    t_end = points[-1]["timestamp_unix_nano"] / 1e9 if points else None
    t1, t2 = decisions.get(1), decisions.get(2)

    def tput(a, b):
        if a is None or b is None or b <= a:
            return None
        return (committed_at(points, b) - committed_at(points, a)) / (b - a)

    out = {
        "scenario": scenario, "action": action,
        "tput_e1": tput(t_first, t1), "tput_e2": tput(t1, t2), "tput_run": tput(t_first, t_end),
        "e1_len": (t1 - t_first) if t1 and t_first else None,
        "e2_len": (t2 - t1) if t1 and t2 else None,
        "lat_count": lat.get("count"), "lat_avg": lat.get("avg_ms"), "lat_p50": lat.get("p50_ms"),
        "lat_p99": lat.get("p99_ms"), "lat_p999": lat.get("p99_9_ms"),
        "aware": aware,
    }
    if t1 and t2:
        e2_vc = [t for t in vc_starts if t1 <= t < t2]
        e2_inst = [x for x in installs if t1 <= x[0] < t2]
        out["vc_start_rate_e2"] = len(e2_vc) / (t2 - t1)
        out["install_rate_e2"] = len(e2_inst) / (t2 - t1)
        # leader timeline on the reference node: a view is led from its install until the
        # next view change starts; the rest is time in view change
        events = sorted([(t, "vc", None) for t in vc_starts] + [(t, "inst", ldr) for t, _, ldr in installs])
        leader, share, cur = None, {}, t1
        for t, kind, ldr in events:
            if t < t1:
                leader = ldr if kind == "inst" else None
                continue
            if t >= t2:
                break
            key = f"n{leader}" if leader else "vc"
            share[key] = share.get(key, 0) + (t - cur)
            cur = t
            leader = ldr if kind == "inst" else None
        key = f"n{leader}" if leader else "vc"
        share[key] = share.get(key, 0) + (t2 - cur)
        out["leader_share_e2"] = {k: round(v / (t2 - t1), 3) for k, v in sorted(share.items())}
    return out


def fmt(x, spec):
    return format(x, spec) if isinstance(x, (int, float)) else "-"


runs = sys.argv[1:] or sorted(
    d for s in ("healthy", "pdelay", "ndelay", "ndfcrash", "farnode") for d in glob.glob(f"results/{s}_*")
)
rows = [summarize(r) for r in runs]
print(f"{'scenario':9s} {'act':5s} {'T_e1':>6s} {'T_e2':>6s} {'T_run':>6s} {'vc/s':>5s} {'inst/s':>6s}"
      f" {'L_avg':>8s} {'L_p50':>8s} {'L_p99':>8s} {'L_p99.9':>8s}  leader share in epoch 2")
for r in rows:
    print(f"{r['scenario']:9s} {r['action']:5s} {fmt(r['tput_e1'], '6.0f')} {fmt(r['tput_e2'], '6.0f')}"
          f" {fmt(r['tput_run'], '6.0f')} {fmt(r.get('vc_start_rate_e2'), '5.2f')} {fmt(r.get('install_rate_e2'), '6.2f')}"
          f" {fmt(r['lat_avg'], '8.1f')} {fmt(r['lat_p50'], '8.1f')} {fmt(r['lat_p99'], '8.1f')} {fmt(r['lat_p999'], '8.1f')}"
          f"  {r.get('leader_share_e2', '-')}")
for r in rows:
    if r["aware"]:
        print(f"AWARE {r['scenario']}_{r['action']}: " + "; ".join(f"gen{g} scores={s} cands={c}" for g, s, c in r["aware"][:3]))
json.dump(rows, open("results/latency_pilots_summary.json", "w"), indent=1, default=str)
