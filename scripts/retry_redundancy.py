"""Check whether client retries displaced useful offered load in a finished run.

Usage: python3 scripts/retry_redundancy.py results/<run> [results/<run> ...]

Reads config.json, client_request_send_rate.csv, latencyreport.json and the
INTAKE lines of node_<id>.log from each run directory. See
notes/client_retry_pilot.md for how to read the output.
"""

import csv
import glob
import json
import os
import re
import sys

CLIENT_SEND_INTERVAL_MS = 24  # client/send.go clientSendInterval


def check(run):
    cfg = json.load(open(os.path.join(run, "config.json")))
    budget = cfg["inject_speed"] * 1000 / CLIENT_SEND_INTERVAL_MS
    rows = list(csv.DictReader(open(os.path.join(run, "client_request_send_rate.csv"))))
    sent = int(rows[-1]["sent_total"])  # fresh + retried request sends
    elapsed = float(rows[-1]["elapsed_sec"])
    retries = json.load(open(os.path.join(run, "latencyreport.json"))).get("retries_sent", 0)
    dropped = 0
    for path in glob.glob(os.path.join(run, "node_[0-9]*.log")):
        for line in open(path, errors="ignore"):
            m = re.search(r"retries dropped at intake=(\d+)", line)
            if m:
                dropped += int(m.group(1))

    util = sent / elapsed / budget
    redundant = dropped / max(sent, 1)
    pacer_bound = util >= 0.95
    print(os.path.basename(run.rstrip("/")))
    print(f"  pacer: {sent / elapsed:.0f}/s of {budget:.0f}/s budget ({util:.0%})"
          f" -> {'pacer-bound' if pacer_bound else 'node backpressure (pacer not binding)'}")
    print(f"  sends: fresh {(sent - retries) / elapsed:.0f}/s, retries {retries / elapsed:.0f}/s,"
          f" dropped at intake {dropped} ({redundant:.2%} of sends)")
    if pacer_bound:
        print(f"  useful offered load: {(sent - dropped) / elapsed:.0f}/s"
              f" (redundant retries displaced {redundant:.2%} of it)")
        if redundant > 0.01:
            print("  WARNING: >1% of the pacer budget went to redundant retries;"
                  " the retry interval is likely below this scenario's normal latency")


for run in sys.argv[1:]:
    check(run)
