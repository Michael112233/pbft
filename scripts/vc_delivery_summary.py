"""ViewChange and NewView delivery times per run: the sender's "VC sent" / "NEWVIEW sent"
against each receiver's "HUB: Received ...", from the node logs.

Usage: python3 scripts/vc_delivery_summary.py [--slow-ms 250] [--csv out.csv] <run dir> ...
       e.g. python3 scripts/vc_delivery_summary.py results/lan_n7/*_lan

Node logs from different hosts are compared directly, so this relies on NTP (about
1 ms on the CloudLab clusters, see clock_offsets_*.txt).

Per run and message kind: deliveries n, p50 / p90 / p99 / max in ms, and how many took
longer than --slow-ms, split into generation 1 and later. One flight is the one-way
delay plus processing (~170-190 ms under the 170 ms delay, ~10-25 ms on the LAN). On
the LAN cluster most slow deliveries were the first big ViewChange of a run, waiting
one round trip for the receive window (generation 1, docs/multi-host.md); later ones
are rare (HyStart leaving cwnd at its initial value, a NewView queued behind the
sender's own ViewChange).
"""

import argparse
import csv
import datetime as dt
import glob
import os
import re
import statistics

TS = re.compile(r"^\[\w+\] (\S+ \S+)")
VC_SENT = re.compile(r"VC sent view=\((\d+),(\d+)\) certs=(\d+) size_bytes=(\d+)")
NV_SENT = re.compile(r"NEWVIEW sent view=\((\d+),(\d+)\) refs=\d+ o_set=(\d+) size_bytes=(\d+)")
VC_RECV = re.compile(r"HUB: Received ViewChange message from node (\d+) for view \{(\d+) (\d+)\}")
NV_RECV = re.compile(r"HUB: Received NewView message from node (\d+) for view \{(\d+) (\d+)\}")


def ts(line):
    m = TS.match(line)
    return dt.datetime.strptime(m.group(1), "%Y-%m-%d %H:%M:%S.%f").timestamp() if m else None


def load(run):
    """-> rows: one per (message, receiver), with the delivery time in ms."""
    sent, recv = {}, {}
    for path in glob.glob(os.path.join(run, "node_*.log")):
        m = re.search(r"node_(\d+)\.log$", path)
        if not m:
            continue
        me = int(m.group(1))
        for line in open(path, errors="ignore"):
            if "sent view=" not in line and "HUB: Received" not in line:
                continue
            for kind, rx in (("VC", VC_SENT), ("NV", NV_SENT)):
                mm = rx.search(line)
                if mm:
                    sent.setdefault((kind, me, int(mm.group(1)), int(mm.group(2))),
                                    (ts(line), int(mm.group(4)), int(mm.group(3))))
            for kind, rx in (("VC", VC_RECV), ("NV", NV_RECV)):
                mm = rx.search(line)
                if mm:
                    recv.setdefault((kind, int(mm.group(1)), int(mm.group(2)), int(mm.group(3)), me), ts(line))
    rows = []
    for (kind, frm, g, c, to), t_recv in recv.items():
        s = sent.get((kind, frm, g, c))
        if s is not None:
            rows.append(dict(kind=kind, frm=frm, to=to, gen=g, ctr=c, t_sent=s[0], size=s[1], certs=s[2],
                             ms=(t_recv - s[0]) * 1000))
    return rows


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p * len(xs)))]


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("runs", nargs="+")
    ap.add_argument("--slow-ms", type=float, default=250, help="count deliveries above this (default 250)")
    ap.add_argument("--csv", help="write every delivery as a row")
    a = ap.parse_args()
    out = []
    print(f"{'run':26s} kind      n    p50    p90    p99    max   >{a.slow_ms:.0f}ms gen1 / later")
    for run in sorted(a.runs):
        rows = load(run)
        name = os.path.basename(run.rstrip("/"))
        for kind in ("VC", "NV"):
            rr = [r for r in rows if r["kind"] == kind]
            if not rr:
                continue
            ms = [r["ms"] for r in rr]
            slow1 = sum(r["ms"] > a.slow_ms and r["gen"] == 1 for r in rr)
            slow2 = sum(r["ms"] > a.slow_ms and r["gen"] > 1 for r in rr)
            print(f"{name:26s} {kind:3s} {len(ms):7d} {statistics.median(ms):6.1f} {pct(ms, .9):6.1f} "
                  f"{pct(ms, .99):6.1f} {max(ms):6.1f}   {slow1:5d} / {slow2:<5d}")
        for r in rows:
            r["run"] = name
        out += rows
    if a.csv and out:
        with open(a.csv, "w", newline="") as f:
            w = csv.DictWriter(f, fieldnames=list(out[0].keys()))
            w.writeheader()
            w.writerows(out)


if __name__ == "__main__":
    main()
