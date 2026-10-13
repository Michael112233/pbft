#!/usr/bin/env python3
"""Merge the per-client files of a client_num > 1 run into the single client's file names.

Usage: python3 scripts/merge_client_reports.py <logs dir> [--report report.json] [--clients 1 2 ...]

With client_num > 1, client i writes logs/client<i>_<file> (Client.outPath in
client/client.go). This writes the single-client names next to them, so the analysis
scripts read a multi-client run like any other:

  <report>                      TPS series: each client's committed_total linearly
                                interpolated onto one grid (the clients' 100 ms sample
                                interval) and summed; window/average TPS recomputed.
  latency<report>               count and avg_ms exact; p50..p99.9 from the merged
                                latency_hist_us (bucket midpoint, within 0.05 ms below
                                100 ms); counters summed; per-client summaries under
                                "clients". Retried and queue percentiles cannot be merged
                                from the summaries and are null.
  client_request_send_rate.csv  sent/dropped/discarded totals summed on a 1 s grid.
  leader_timeline.jsonl,        client 1's copy. Client 1 runs the throttle manager, and
  throttle_manager.jsonl        every client accepts the same leaders.

The per-client files are left in place. --clients defaults to every client<i>_latency<report>
found in the logs dir.
"""
import argparse
import csv
import datetime
import glob
import json
import math
import os
import re
import shutil
import sys

import numpy as np

SUMMED = ["retries_sent", "uncommitted", "dropped_unsent", "send_queue_dropped", "send_queue_discarded"]
PERCENTILES = [("p50_ms", 0.50), ("p95_ms", 0.95), ("p99_ms", 0.99), ("p99_9_ms", 0.999)]


def client_file(logs, cid, legacy):
    """Mirror of Client.outPath: logs/client<i>_<legacy> without a leading client_."""
    base = legacy[len("client_"):] if legacy.startswith("client_") else legacy
    return os.path.join(logs, f"client{cid}_{base}")


def interp_sum(grid, series):
    """Sum each (t, cumulative) series linearly interpolated onto grid; 0 before a
    series starts, its last value after it ends. Each series starts with its own
    (start, 0) point, so a client's count ramps up from when it started."""
    total = np.zeros(len(grid))
    for t, v in series:
        total += np.interp(grid, t, v, left=0.0, right=float(v[-1]))
    return total


def make_grid(end, step):
    """Offsets step, 2*step, ... up to and including end."""
    grid = np.arange(step, end + 1, step, dtype=np.int64)
    if len(grid) == 0 or grid[-1] < end:
        grid = np.append(grid, np.int64(end))
    return grid


def anchored(t, v, client_start, start):
    """Series as offsets from start, prefixed with the client's own (start, 0)."""
    return (np.concatenate(([client_start - start], t - start)).astype(float), np.concatenate(([0.0], v)))


def merge_tps(logs, ids, report):
    series, starts, ends, intervals = [], [], [], []
    for cid in ids:
        points = json.load(open(client_file(logs, cid, report)))
        if not points:
            continue
        t = np.array([p["timestamp_unix_nano"] for p in points], dtype=np.int64)
        starts.append(int(t[0] - points[0]["elapsed_sec"] * 1e9))
        ends.append(int(t[-1]))
        if len(t) > 1:
            intervals.append(int(np.median(np.diff(t))))
        series.append((t, np.array([p["committed_total"] for p in points], dtype=float)))
    if not series:
        return []
    start, end = min(starts), max(ends)
    step = intervals[0] if intervals else 100_000_000
    # offsets from start keep float64 exact to the nanosecond
    grid = make_grid(end - start, step)
    total = interp_sum(grid.astype(float), [anchored(t, v, cs, start) for (t, v), cs in zip(series, starts)])
    elapsed = grid / 1e9
    window = np.diff(np.concatenate(([0.0], total))) / np.diff(np.concatenate(([0.0], elapsed)))
    return [
        {
            "timestamp_unix_nano": int(start + g),
            "elapsed_sec": float(e),
            "committed_total": int(round(c)),
            "window_tps": float(w),
            "average_tps": float(c / e) if e > 0 else 0.0,
        }
        for g, e, c, w in zip(grid, elapsed, total, window)
    ]


def hist_percentile(hist, count, p):
    """Nearest rank, as percentileLatency in client/transactionmanager.go, reported as
    the midpoint of the bucket holding that rank."""
    rank = max(1, math.ceil(p * count))
    seen = 0
    for lower, (width, n) in sorted(hist.items()):
        seen += n
        if seen >= rank:
            return (lower + width / 2) / 1000.0
    return None


def merge_latency(logs, ids, report):
    reports = [(cid, json.load(open(client_file(logs, cid, "latency" + report)))) for cid in ids]
    first = reports[0][1]
    merged = {
        "latency_measured_from": first.get("latency_measured_from"),
        "percentiles_from": "merged_histogram",
        "clients_merged": ids,
        "retry_enabled": first.get("retry_enabled"),
        "retry_mode": first.get("retry_mode"),
        "latency_samples_ms": first.get("latency_samples_ms", []),
    }
    for key in SUMMED:
        merged[key] = sum(r.get(key, 0) for _, r in reports)

    def weighted(avg_key, count_key):
        n = sum(r.get(count_key, 0) for _, r in reports)
        avg = sum(r.get(avg_key, 0) * r.get(count_key, 0) for _, r in reports) / n if n else 0.0
        return n, avg

    merged["count"], merged["avg_ms"] = weighted("avg_ms", "count")
    merged["retried_count"], merged["retried_avg_ms"] = weighted("retried_avg_ms", "retried_count")
    merged["queue_batches"], merged["queue_avg_ms"] = weighted("queue_avg_ms", "queue_batches")
    for key in ["retried_p50_ms", "retried_p99_ms", "queue_p50_ms", "queue_p95_ms", "queue_p99_ms"]:
        merged[key] = None

    hist = {}
    missing = [cid for cid, r in reports if r.get("count", 0) > 0 and "latency_hist_us" not in r]
    if missing:
        print(f"warning: clients {missing} have no latency_hist_us (built before it was added); "
              "merged percentiles are null", file=sys.stderr)
    else:
        for _, r in reports:
            for lower, width, n in r.get("latency_hist_us") or []:
                if lower in hist:
                    hist[lower][1] += n
                else:
                    hist[lower] = [width, n]
    for key, p in PERCENTILES:
        merged[key] = hist_percentile(hist, merged["count"], p) if hist and merged["count"] else None
    merged["latency_hist_us"] = [[lower, w, n] for lower, (w, n) in sorted(hist.items())]
    merged["clients"] = [
        {"client": cid, **{k: r.get(k) for k in ["count", "avg_ms", "p50_ms", "p95_ms", "p99_ms", "p99_9_ms", "uncommitted"]}}
        for cid, r in reports
    ]
    return merged


SEND_RATE_FIELDS = ["timestamp_unix_nano", "timestamp", "elapsed_sec", "sent_total", "sent_delta",
                    "window_sec", "window_tps", "average_tps", "dropped_total", "discarded_total"]


def merge_send_rate(logs, ids):
    cols = ["sent_total", "dropped_total", "discarded_total"]
    per_client, starts, ends = [], [], []
    for cid in ids:
        path = client_file(logs, cid, "client_request_send_rate.csv")
        if not os.path.exists(path):
            continue
        rows = list(csv.DictReader(open(path)))
        if not rows:
            continue
        t = np.array([int(r["timestamp_unix_nano"]) for r in rows], dtype=np.int64)
        starts.append(int(t[0] - float(rows[0]["elapsed_sec"]) * 1e9))
        ends.append(int(t[-1]))
        per_client.append((t, {c: np.array([float(r[c]) for r in rows]) for c in cols}))
    if not per_client:
        return None
    start, end = min(starts), max(ends)
    grid = make_grid(end - start, 1_000_000_000)
    totals = {c: interp_sum(grid.astype(float), [anchored(t, v[c], cs, start) for (t, v), cs in zip(per_client, starts)])
              for c in cols}
    rows, prev, prev_g = [], 0, 0
    for i, g in enumerate(grid):
        sent = int(round(totals["sent_total"][i]))
        elapsed = g / 1e9
        window = (g - prev_g) / 1e9
        ts = int(start + g)
        rows.append({
            "timestamp_unix_nano": ts,
            "timestamp": datetime.datetime.fromtimestamp(ts / 1e9).astimezone().isoformat(),
            "elapsed_sec": f"{elapsed:.6f}",
            "sent_total": sent,
            "sent_delta": sent - prev,
            "window_sec": f"{window:.6f}",
            "window_tps": f"{(sent - prev) / window:.6f}",
            "average_tps": f"{sent / elapsed:.6f}",
            "dropped_total": int(round(totals["dropped_total"][i])),
            "discarded_total": int(round(totals["discarded_total"][i])),
        })
        prev, prev_g = sent, g
    return rows


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("logs")
    ap.add_argument("--report", default="report.json")
    ap.add_argument("--clients", type=int, nargs="*")
    args = ap.parse_args()

    ids = args.clients
    if not ids:
        pat = re.compile(r"client(\d+)_latency" + re.escape(args.report) + "$")
        ids = sorted(int(m.group(1)) for f in glob.glob(os.path.join(args.logs, "client*_latency" + args.report))
                     if (m := pat.search(os.path.basename(f))))
    ids = sorted(ids)
    if not ids:
        sys.exit(f"no client<i>_latency{args.report} in {args.logs}")

    tps = merge_tps(args.logs, ids, args.report)
    json.dump(tps, open(os.path.join(args.logs, args.report), "w"), indent=2)

    lat = merge_latency(args.logs, ids, args.report)
    json.dump(lat, open(os.path.join(args.logs, "latency" + args.report), "w"), indent=2)

    rows = merge_send_rate(args.logs, ids)
    if rows is not None:
        with open(os.path.join(args.logs, "client_request_send_rate.csv"), "w", newline="") as f:
            w = csv.DictWriter(f, fieldnames=SEND_RATE_FIELDS)
            w.writeheader()
            w.writerows(rows)

    for legacy in ["leader_timeline.jsonl", "throttle_manager.jsonl"]:
        src = client_file(args.logs, ids[0], legacy)
        if os.path.exists(src):
            shutil.copyfile(src, os.path.join(args.logs, legacy))

    last = tps[-1] if tps else {}
    print(f"merged clients {ids}: committed {last.get('committed_total', 0)} "
          f"({last.get('average_tps', 0):.1f} req/s avg), latency count {lat['count']} "
          f"avg {lat['avg_ms']:.2f} ms p50 {lat['p50_ms']} p99 {lat['p99_ms']}")


if __name__ == "__main__":
    main()
