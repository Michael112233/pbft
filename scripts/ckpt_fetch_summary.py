"""Checkpoint state transfer (checkpoint_fetch) per run: fetches, installs, and
ViewChange cert counts, from the node logs.

Usage: python3 scripts/ckpt_fetch_summary.py [--details] <run dir> ...
       e.g. python3 scripts/ckpt_fetch_summary.py results/*_ckpton results/*_ckptrepro_*

Per run it reports, summed over node_<i>.log:
  gens      generation switches (decisions at node 4)
  VCs       ViewChanges sent ("VC sent ... certs=N")
  maxcert   largest cert count in one ViewChange
  >300      ViewChanges above 300 certs: a node stranded below a checkpoint until its
            high watermark (steady-state ceiling is ~290), with the nodes that sent them
  sent      CKPT FETCH requests sent
  inst      fetched states installed: a stranded node repaired (necessary)
  stale     replies that arrived after the node caught up on its own: the fetch was
            not needed (the normal-case false positive)
  rej       replies rejected (no local certificate, or state does not hash to digest)
  ign       requests a peer could not serve
"""

import argparse
import collections
import glob
import os
import re

VC_SENT = re.compile(r"VC sent view=\((\d+),(\d+)\) certs=(\d+)")
DECISION = "received learning-agent decision for generation"
KINDS = ("sent", "served", "installed", "stale", "rejected", "ignored")
INSTALLED = re.compile(r"^\[\w+\] (\S+ \S+) .*CKPT FETCH installed (.*)$")


def summarize(run):
    r = {"gens": 0, "vcs": 0, "maxcert": 0, "over300": collections.Counter(),
         "fetch": collections.Counter(), "installs": []}
    for path in sorted(glob.glob(os.path.join(run, "node_*.log"))):
        m = re.fullmatch(r"node_(\d+)\.log", os.path.basename(path))
        if not m:
            continue
        node = int(m.group(1))
        for line in open(path, errors="ignore"):
            if "CKPT FETCH" in line:
                for k in KINDS:
                    if f"CKPT FETCH {k}" in line:
                        r["fetch"][k] += 1
                        if k == "installed":
                            mi = INSTALLED.match(line)
                            r["installs"].append((node, mi.group(1) if mi else "?", mi.group(2) if mi else line.strip()))
                        break
            elif "VC sent view=" in line:
                mv = VC_SENT.search(line)
                if mv:
                    certs = int(mv.group(3))
                    r["vcs"] += 1
                    r["maxcert"] = max(r["maxcert"], certs)
                    if certs > 300:
                        r["over300"][node] += 1
            elif node == 4 and DECISION in line:
                r["gens"] += 1
    return r


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("runs", nargs="+")
    ap.add_argument("--details", action="store_true", help="list every install")
    args = ap.parse_args()

    print(f"{'run':34s} {'gens':>4s} {'VCs':>6s} {'maxcert':>7s} {'>300 (node:n)':>16s} "
          f"{'sent':>5s} {'inst':>5s} {'stale':>5s} {'rej':>4s} {'ign':>4s}")
    for run in sorted(args.runs):
        r = summarize(run)
        over = ",".join(f"{n}:{c}" for n, c in sorted(r["over300"].items())) or "-"
        f = r["fetch"]
        print(f"{os.path.basename(run.rstrip('/')):34s} {r['gens']:4d} {r['vcs']:6d} {r['maxcert']:7d} {over:>16s} "
              f"{f['sent']:5d} {f['installed']:5d} {f['stale']:5d} {f['rejected']:4d} {f['ignored']:4d}")
        if args.details:
            for node, when, rest in r["installs"]:
                print(f"    node {node} {when} {rest}")


if __name__ == "__main__":
    main()
