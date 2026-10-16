#!/usr/bin/env python3
"""TCP retransmissions and NIC drops during a cluster run, per host.

    python3 scripts/tcp_counters_summary.py results/<run> [results/<run> ...]

Reads what scripts/cluster/start.sh and stop.sh record on every host:
  tcp_counters_start_<host>.txt / tcp_counters_end_<host>.txt
      host-wide nstat counters and the experiment NIC's `ip -s link` counters, before
      the agents start and before the processes are killed; this prints the difference.
      Host-wide, so the harness's own ssh traffic is in it too (small).
  tcp_sockets_end_<host>.txt
      `ss -tin` of the PBFT node-port sockets just before the stop: per-connection
      totals over the connection's life, PBFT traffic only.

What to read:
  retr%      TcpRetransSegs / TcpOutSegs. 0 on a clean LAN.
  timeouts   TcpExtTCPTimeouts: retransmission timeouts, each a stall of >= 200 ms (RTO
             floor). Any here shows up as a latency spike in the run.
  fast       TcpExtTCPFastRetrans: loss repaired in about one RTT, cheap.
  lost_rtx   TcpExtTCPLostRetransmit: a retransmission that was itself lost.
  nic drop   RX/TX dropped + errors (+ RX missed) on the experiment NIC.
  sockets    sum of `retrans:<now>/<total>` totals and bytes_retrans over the PBFT
             sockets, and the largest rtt (ms) and cwnd seen on one.
"""
import glob
import os
import re
import sys

NSTAT_KEYS = [
    ("TcpOutSegs", "out_segs"),
    ("TcpRetransSegs", "retrans"),
    ("TcpExtTCPTimeouts", "timeouts"),
    ("TcpExtTCPFastRetrans", "fast"),
    ("TcpExtTCPLostRetransmit", "lost_rtx"),
]


def parse_counters(path):
    """-> (link dict, nstat dict, nic dict) from one tcp_counters_*.txt."""
    link, nstat, nic, section = {}, {}, {}, None
    lines = open(path, errors="replace").read().splitlines()
    for i, line in enumerate(lines):
        if line.startswith("== link"):
            link = dict(kv.split("=", 1) for kv in line.split()[2:] if "=" in kv)
        elif line.startswith("== "):
            section = line[3:].strip()
        elif section == "nstat" and line and not line.startswith("#"):
            parts = line.split()
            if len(parts) >= 2 and parts[1].isdigit():
                nstat[parts[0]] = int(parts[1])
        elif section == "ip -s link" and line.strip().startswith(("RX:", "TX:")) and i + 1 < len(lines):
            # "RX:  bytes packets errors dropped missed mcast" then a row of numbers
            names = line.split()
            direction, names = names[0].rstrip(":"), names[1:]
            values = lines[i + 1].split()
            for name, value in zip(names, values):
                if value.isdigit():
                    nic[f"{direction}_{name}"] = int(value)
    return link, nstat, nic


def parse_sockets(path):
    socks = retrans = bytes_retrans = 0
    max_rtt = max_cwnd = 0.0
    for line in open(path, errors="replace"):
        m = re.search(r"\bretrans:\d+/(\d+)", line)
        if m:
            retrans += int(m.group(1))
        m = re.search(r"\bbytes_retrans:(\d+)", line)
        if m:
            bytes_retrans += int(m.group(1))
        m = re.search(r"\brtt:([\d.]+)/", line)
        if m:
            socks += 1
            max_rtt = max(max_rtt, float(m.group(1)))
        m = re.search(r"\bcwnd:(\d+)", line)
        if m:
            max_cwnd = max(max_cwnd, int(m.group(1)))
    return socks, retrans, bytes_retrans, max_rtt, int(max_cwnd)


def summarize(run):
    starts = sorted(glob.glob(os.path.join(run, "tcp_counters_start_*.txt")))
    if not starts:
        print(f"== {run}: no tcp_counters_start_*.txt (run predates the TCP counters)")
        return
    print(f"== {run}")
    print(f"{'host':8} {'dev':10} {'Mb/s':>6} {'secs':>5} {'out_segs':>11} {'retrans':>8} {'retr%':>7} "
          f"{'timeouts':>8} {'fast':>5} {'lost_rtx':>8} {'nic drop':>8} | {'socks':>5} {'s.retr':>6} "
          f"{'s.bytes':>8} {'rtt max':>7} {'cwnd max':>8}")
    for start in starts:
        host = start[len(os.path.join(run, "tcp_counters_start_")):-len(".txt")]
        end = os.path.join(run, f"tcp_counters_end_{host}.txt")
        if not os.path.exists(end):
            print(f"{host:8} no tcp_counters_end_{host}.txt (stop.sh could not reach the host?)")
            continue
        link0, n0, nic0 = parse_counters(start)
        link1, n1, nic1 = parse_counters(end)
        d = {short: n1.get(key, 0) - n0.get(key, 0) for key, short in NSTAT_KEYS}
        drops = sum(nic1.get(k, 0) - nic0.get(k, 0) for k in nic1
                    if k.split("_", 1)[1] in ("dropped", "errors", "missed"))
        try:
            secs = f"{float(link1['taken']) - float(link0['taken']):.0f}"
        except (KeyError, ValueError):
            secs = "?"
        pct = f"{100 * d['retrans'] / d['out_segs']:.4f}" if d["out_segs"] else "-"
        sock_path = os.path.join(run, f"tcp_sockets_end_{host}.txt")
        if os.path.exists(sock_path):
            socks, sretr, sbytes, rtt, cwnd = parse_sockets(sock_path)
            sock_cols = f"{socks:>5} {sretr:>6} {sbytes:>8} {rtt:>7.2f} {cwnd:>8}"
        else:
            sock_cols = f"{'-':>5}"
        print(f"{host:8} {link1.get('dev', '?'):10} {link1.get('speed_mbps', '?'):>6} {secs:>5} "
              f"{d['out_segs']:>11} {d['retrans']:>8} {pct:>7} {d['timeouts']:>8} {d['fast']:>5} "
              f"{d['lost_rtx']:>8} {drops:>8} | {sock_cols}")


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    for run in sys.argv[1:]:
        summarize(run.rstrip("/"))


if __name__ == "__main__":
    main()
