# Running on separate machines (`-m remote`)

One PBFT node per machine, plus a client machine, on a CloudLab LAN now and on a WAN
later. The single-host path (`-m loopbackip`, `alt_run_project.sh`, netem on `lo`) is
unchanged; everything here is a separate code path.

## What is different from one host

| | single host (`loopbackip`) | cluster (`remote`) |
|---|---|---|
| node i address | `127.0.0.(i+1):28000+100i` | `<hosts file ip>:28000+100i` |
| agent i address | `127.0.0.(i+1):29000` | `<node i ip>:29000`, on node i's machine |
| client address | `127.0.0.1` | `<hosts file client ip>`, its own machine |
| launcher | `alt_run_project.sh`, one tmux session | `scripts/cluster/start.sh`, one tmux session `pbft` per machine |
| keys | generated per run | generated once per run on the control machine, pushed to every host |
| startup order | `sleep 5` | agents, then nodes, then client: agents and nodes must answer on their ports, and every node must log `PEERS READY` |
| scenario delay | node 4 runs `netem_scenario.sh` on `lo` for every pair | **every node** runs `netem_egress.sh` on its own NIC for its peers |
| logs | one `logs/` | one `logs/` per host, merged by `stop.sh` into `results/<name>/` |

The ports are the same in both modes, so only the IPs differ, and
`learningagent/address.py` keeps one formula.

### The hosts file

`-m remote` takes its addresses from a hosts file (`--hosts`), not from the experiment
config, so a config runs unchanged on any cluster. The file is copied into every
results directory.

```json
{"run_dir": "pbft-run",
 "nodes":   {"1": {"ip": "10.10.1.1", "ssh": "node1"}, "...": {},
             "7": {"ip": "10.10.1.7", "ssh": "node7"}},
 "clients": {"1": {"ip": "10.10.1.8", "ssh": "client"}}}
```

- `ip` is the address the process binds and sends from. It must be on that machine,
  because a node can't listen otherwise and now fails at start.
- `ssh` is the name the cluster scripts log in with.
- `config.GenerateRemoteNetwork` rejects:
  - a node set that isn't exactly 1..`node_num`
  - fewer clients than `client_num`
  - a duplicate IP
  - a client on a node's IP, which the egress netem would delay along with the node
- `scripts/cluster/hosts.cloudlab.json` matches the profile's fixed IPs for n = 7.

### Network delay on separate hosts

On `lo`, every node-to-node packet crosses one qdisc, and node 4 owns it. On a cluster,
each node instead delays its own egress (`scripts/netem_egress.sh`, device found with
`ip route get`): a root prio qdisc with one band, netem leaf and flower `dst_ip` filter per
destination host; unmatched traffic goes undelayed through band 1.
- A→B is delayed at A and B→A at B, so each directed pair is delayed once, as on `lo`
  (`scenario_network_delay_ms`, default 170 ms one way, 340 ms RTT).
- Client traffic gets no scenario delay, because the client isn't a peer.
- Agent traffic stays on the host's `lo`, so it isn't delayed either.

The only difference is timing: each node applies the delay at its own scenario switch,
within one view change of the others, instead of node 4 doing it for everyone. The
nodes run the script with `sudo -n` (CloudLab accounts have passwordless sudo). The tree
is built once; a scenario switch only changes leaf delays (`tc qdisc change`), which keeps
the packets netem is holding. `stop.sh` removes the qdisc on every host (`clear`), because
killing the tmux session doesn't run the node's own teardown.

### Emulated WAN on the LAN cluster

A hosts file can add a permanent per-pair delay on top of which the scenario delay is
applied. The `wan` block puts every node and client in a site and gives a one-way delay per
site pair:

```json
"wan": {
  "sites": {"near": {"nodes": [1, 2, 3, 4, 5], "clients": [1]}, "far": {"nodes": [6, 7]}},
  "one_way_ms": {"near-far": 24}
}
```

- **Where it lives:** in the hosts file, like the IPs: it is deployment, so the same experiment
  config runs on the LAN or the WAN by picking a hosts file, and each results dir keeps
  `hosts.json`. Go ignores the block. Two ready-made files: `hosts.cloudlab.wan_far67.json`
  (nodes 6, 7 far, the client near) and `hosts.cloudlab.wan_far67_2c.json` (the same plus a
  second client, far, on its own machine: profile `clientCount` 2, config `client_num` 2).
- **What `start.sh` does with it:** after pushing the bundle and before any process starts,
  every active host runs `netem_egress.sh base <ip>=<ms> ...` for every other host (the delay
  between their sites, 0 within a site). Each host then pings every other host into
  `logs/wan_check.txt`; a far pair under 2d − 2 ms of RTT, a near pair over 5 ms, or no reply
  fails the start (exit 4) and the base is removed again. `logs/netem_state_{start,end}.txt`
  record `tc -s qdisc` (with netem drops) and `tc filter` per host.
- **How the scenario delay composes:** `netem_egress.sh up X <peers>` sets each listed peer's
  leaf to its base + X and everything else to its base; `down` puts every leaf back to its base
  (with no base it removes the qdisc, as on a plain LAN); `clear` (stop.sh, and start.sh's
  leftover cleanup) removes the qdisc and the base. So a NetworkDelay of X on the 5+2 WAN gives
  near↔near X and near↔far 24 + X, clients only their base.
- **State:** `logs/netem_egress_state.txt` (destination, band, base, current delay) and
  `logs/netem_base.txt`; every change is logged to `logs/netem_schedule.log` with per-destination
  delays. `scripts/test_netem_egress.sh` checks the commands without root (`NETEM_DRY_RUN=1`).
- **Limit:** prio has 16 bands, so 15 destinations per host; n = 7 with 2 clients needs 8. Past
  that, HTB classes instead of prio bands.
- **Not modelled:** bandwidth limits and jitter. Both can go on the same leaves later (`rate`;
  `delay 24ms 2ms`, which reorders unless a rate is set).

Other ways to emulate a WAN, not used: IFB ingress for the base and egress for the scenario
(two independent trees, but more moving parts and each pair's delay split between two hosts),
netem stacked under netem (not reliably supported), and real CloudLab sites over a stitched
link (real RTT and bandwidth, but d is fixed by the sites and the link is shared,
`docs/cloudlab-stitched-link-bw-latency.md`).

`nodes_dead`, ProposalDelay and Throttle are in-process and don't depend on where the
nodes run.

## Bring-up

1. **Create the experiment** from `cloudlab/profile.py`:
   - 7 `nodeN` machines and one `client` machine on one LAN, with no shaping
   - node i is 10.10.1.i, the client 10.10.1.8
   - all on the same hardware type, default c6320
2. **Give `client` its own ssh key (once per cluster).** `start.sh` and `stop.sh` ssh
   from `client` into every node and into `client` itself, at every run of a batch.
   CloudLab installs only your laptop's public key, so out of the box `client` can reach
   the nodes only through your forwarded agent. That agent goes away when the laptop
   disconnects: the run in progress is then never collected (`stop.sh` fails), and the
   next `start.sh` fails its governor check, which aborts the batch. Reconnecting doesn't
   help, because the batch keeps the old agent socket. So use forwarding once, to install
   a key that lives on `client`:
   1. On the laptop, check that your CloudLab key is loaded in the agent, then log in
      with forwarding:
      ```bash
      ssh-add -l                                  # must list the key
      # if not: ssh-add --apple-use-keychain ~/.ssh/<key>   (macOS; plain ssh-add elsewhere)
      ssh -A <client's CloudLab hostname>
      ```
   2. On `client`, check that the agent was forwarded:
      ```bash
      ssh-add -l    # must list the laptop's key, not "Could not open a connection"
      ```
   3. Create `client`'s own key and allow it on `client` itself:
      ```bash
      [ -f ~/.ssh/id_ed25519 ] || ssh-keygen -t ed25519 -N '' -f ~/.ssh/id_ed25519
      cat ~/.ssh/id_ed25519.pub >> ~/.ssh/authorized_keys
      ```
   4. Install it on every node (logs in with the forwarded key):
      ```bash
      for i in 1 2 3 4 5 6 7; do ssh-copy-id -i ~/.ssh/id_ed25519.pub node$i; done
      ```
   5. Test with forwarding off (`-a`). Expect 8 hostnames, no `Permission denied`:
      ```bash
      for h in node1 node2 node3 node4 node5 node6 node7 client; do
          ssh -a -o BatchMode=yes -o StrictHostKeyChecking=accept-new $h hostname
      done
      ```
   6. `exit`, then log in again without forwarding from now on. VS Code Remote-SSH
      forwards by default whatever `~/.ssh/config` says: turn off **Remote.SSH: Enable
      Agent Forwarding** in its settings. In a new terminal on `client`, `ssh-add -l`
      should now say `Could not open a connection to your authentication agent`, and the
      test in step 5 should still print 8 hostnames.

   Forwarding never copies the laptop's private key to `client`: it only passes signing
   requests back to the laptop's agent while the connection is open, and root on `client`
   can make them too. `client`'s key is a new random key, not derived from the laptop's.
3. **Set up the client machine:** clone the repo, then install Go and the Python
   packages as in `setup.txt`. Go is needed on the client machine only, because nodes
   get a built binary.
4. **Run `scripts/cluster/setup.sh scripts/cluster/hosts.cloudlab.json --tune-tcp-lan`.** It
   checks every host and installs what's missing, then records the CPU baseline
   (record it in `notes/cpu_note.txt`, cpu-baseline skill) and the RTT between nodes.
   It also sets the `performance` governor on every machine (`cpu_check.sh --fix`) and
   fails if a machine can't use it. Run it again after a reboot, because neither the
   governor nor the TCP tuning persists. `start.sh` checks the governor on every machine
   before touching anything and refuses to start (exit 3) if any CPU isn't on
   `performance`. `run_experiments.sh` then aborts the whole batch.

## Running

```bash
H=scripts/cluster/hosts.cloudlab.json
scripts/cluster/start.sh $H config/lan_n7/healthy_frr_lan.json   # start
scripts/cluster/stop.sh  $H                                      # export, stop, collect
scripts/cluster/run_experiments.sh $H 600 config/lan_n7/*.json   # start, sleep, stop per config
```

- **Config path:** it must be inside the repo, because hosts get it at the same relative
  path.
- **What `start.sh` does:**
  - checks that `go` is available on the control machine (also looking in
    `/usr/local/go/bin`, since a VS Code terminal doesn't read `~/.profile`) and the
    governor on every host; if either fails it exits 3 before touching anything, and
    `run_experiments.sh` aborts the batch
  - wipes `~/<run_dir>` on every host
  - builds the binary and keys locally, then pushes them to every host
  - records every host's TCP and NIC counters (`tcp_counters_start.txt`, see below)
  - starts the agents and waits for `:29000`
  - starts the nodes and waits until every node port accepts a connection, then until
    every node has logged `PEERS READY`. Each node dials all its peers at startup
    (`node/peerdial.go`), so the client's first requests find every connection open
  - starts the client
  - with `netem_delay`, delays every node's egress 1 s later
- **What `stop.sh` does:** triggers the same exports as `stop_experiment.sh`, waits for
  them on every host, records the TCP counters again, kills everything, and pulls every
  host's `logs/` into `results/<name>/`. Files that every host writes are suffixed with
  the host's label.
- **Retransmissions and drops:** `python3 scripts/tcp_counters_summary.py results/<name>`
  prints, per host, what happened between the two snapshots:
  - from `tcp_counters_{start,end}_<host>.txt` (`nstat -asz` and `ip -s link` of the
    experiment NIC, plus its link speed): segments sent, `TcpRetransSegs` and its
    percentage, `TcpExtTCPTimeouts` (each a stall of at least the 200 ms RTO floor), fast
    retransmits, lost retransmits, NIC drops and errors. These are host-wide, so the
    harness's own ssh traffic is included (small).
  - from `tcp_sockets_end_<host>.txt` (`ss -tin` on the node ports, taken while the
    processes still run, because the counters disappear with the sockets): retransmits
    and retransmitted bytes over each PBFT connection's life, PBFT traffic only, and
    the largest `rtt` and `cwnd`.

  Runs before 2026-10-16 don't have these files.
- **Long batches:** `run_experiments.sh` detaches itself (`setsid nohup`, like
  `run_long_experiment.sh`), so it returns at once and survives your ssh session, as
  long as `client` has its own key on every host (Bring-up step 2).
  - Progress goes to `cluster_run.out`.
  - `kill <pid>` (printed at launch) stops and saves the current run and skips the rest.
  - If `start.sh` fails before any process started (exit 4: a build error, a push
    failure), the batch skips `stop.sh` for that config and keeps any earlier
    `results/<name>`. `stop.sh` itself doesn't wait for a host whose export was never
    triggered.
  - Only one batch runs at a time.
- **Looking at a live node:** `ssh -t node3 tmux attach -t pbft` (detach with `C-b d`).
  Its logs are in `~/pbft-run/logs/` on that host until `stop.sh` collects them.
- **Not supported yet:** `far_node_netem` (`lo` only), and `client_num` > 1 with clients
  on node machines.

## TCP tuning (`setup.sh --tune-tcp-lan` / `--tune-tcp-wan`)

Use `--tune-tcp-lan` to reproduce single-host (`lo`) results, and `--tune-tcp-wan` for
long fat links. Pass at most one. Neither survives a reboot, and both save the original
values once to `~/sysctl_backup.txt`. `start.sh` records each machine's settings in
`tcp_state_<host>.txt` in the results, so every run says which mode it used.

### `--tune-tcp-lan`: behave like `lo`

TCP's initial window, and the window it falls back to after an idle period
(`tcp_slow_start_after_idle=1`), is 10 **segments**. The segment size comes from the MTU:

| | `lo` (MTU 65536) | LAN (MTU 1500) |
|---|---|---|
| segment (MSS) | 65,483 B | 1,448 B |
| 10 segments | ~640 KiB | ~14 KiB |

ViewChanges and NewViews are up to ~590 / ~510 KiB at n = 7 (batch 50, ~290 certs). On
`lo` they arrived in one flight even under the 170 ms delay, except the first big VC of a
run (below): in `results/ckpt_fetch_n7`, NetworkDelay deliveries of 450–700 KiB VCs took
a median of 186–192 ms, i.e. one 170 ms trip. On a default LAN the same VC would need
about 6 round trips (~1.9 s under the delay). That's a TCP difference, not a protocol
one, and it would change the NetworkDelay results.

`--tune-tcp-lan` sets `initcwnd` and `initrwnd` on the experiment route to
`ceil(10 × 65483 / MSS)`, which is 453 at MTU 1500 (655,944 B against `lo`'s 654,830 B).
It works this out from the device's MTU, so jumbo frames get a matching smaller count.
Linux uses the route's initcwnd for the restart after idle too, so the cwnd floor in
bytes is the same as on `lo`. Only the initcwnd part takes effect; see the first big VC
below. Everything else is set to stock Ubuntu 22.04 / kernel 5.15
values, read on the development host (clnode113) on 2026-10-15. This also undoes an
earlier `--tune-tcp-wan`. If your single-host runs used another machine, check with
`sysctl` that it has the same values (`LO_ADVMSS` and `SINGLE_HOST_SYSCTLS` in
`setup.sh`):
- cubic
- `tcp_slow_start_after_idle=1`
- `tcp_rmem` 4096 131072 6291456, `tcp_wmem` 4096 16384 4194304, `rmem/wmem_max` 212992
- `fq_codel`; a leftover `fq` root qdisc is removed

To check it, compare ViewChange delivery times in a LAN NetworkDelay generation with the
single-host run: they should again be ~170–195 ms, apart from the first big VC.
Measured in `results/lan_n7` (2026-10-16): medians 172–181 ms against the twins'
171–180 ms.

**The first big VC of a run takes more than one flight, on `lo` and on the LAN.** TCP
sends at most min(cwnd, rwnd) unacknowledged bytes:
- **cwnd** (congestion window) is the sender's own limit. initcwnd sets where it starts,
  so it starts at ~640 KiB, as on `lo`.
- **rwnd** (receive window) is what the receiver advertises. The kernel starts it at
  most 64 KiB whatever initrwnd says (`tcp_select_initial_window`, v5.15:
  `rcv_wnd = min(space, U16_MAX)`, and initrwnd can only lower it). It then grows only as
  the receiver sees data arrive.

Before the first view change the peer links carry only small Prepare/Commit traffic, so
rwnd is still below a ~550 KiB VC, and the rest of the VC waits one round trip for the
larger window. In `results/lan_n7/ndelay_prr_lan` the view (1,2) VCs took ~527 ms
(170 + 340 + processing), and 36 of the 42 peer connections were rwnd-limited for one
RTT (336–340 ms) over the whole run. On `lo` the same VC took ~1.21 s (three extra round
trips; why three is not measured). rwnd does not shrink afterwards, so later VCs of the
same size arrive in one flight. Whether it happens depends on the first VC's size: in
`ndfcrash_pe_lan` the first VCs grew gradually (107 → 179 KiB) and none waited. It falls
in generation 1, which the summaries skip.

### `--tune-tcp-wan`: long fat links

For the WAN, where the bandwidth-delay product is large (e.g. 10 Gb/s × 48 ms ≈ 60 MB,
`docs/cloudlab-stitched-link-bw-latency.md`):
- turns off `tcp_slow_start_after_idle`
- sets initcwnd/initrwnd 1500 on the experiment route
- raises the TCP buffers to 128 MB (initial receive buffer 4 MiB). This does not raise
  the first advertised window: the kernel still starts rwnd at 64 KiB at most (see the
  first big VC above). It only raises how far the window can grow.
- switches to BBR with `fq`

To check it during a delay generation, run `ss -tin dst <peer subnet>` and look at
`cwnd`, `mss` and `retrans`.

While a delay scenario is active, netem's prio qdisc replaces `fq` on that device, and
`down` brings `fq` back (`default_qdisc`). BBR still paces in the meantime: without `fq`,
TCP paces by itself (internal pacing, since kernel 4.13). What a delay generation loses
is only `fq`'s per-flow fair queueing, since all peer-bound packets share one netem queue.

## Comparing with single-host results

- **CPU:** each process now has a whole machine. The worker pools use
  `runtime.NumCPU()`, so view changes can be faster than on one shared host.
  `NODE_GOMAXPROCS` and `CLIENT_GOMAXPROCS` are passed through to narrow the gap.
- **Clocks:** each host has its own clock, synced by NTP (about 1 ms on CloudLab).
  The scripts that line up node logs with client timestamps (`scenario_pilot_summary.py`,
  `latency_pilot_summary.py`, `perf_tick_misses.py`, `analyze_exec_trace.py`,
  `merge_client_reports.py`) are fine for second-scale windows, not for sub-millisecond
  ones. `clock_offsets_<host>.txt` records the offsets at start.
- **First validation batch:** `config/lan_n7/`, compared against
  `results/ckpt_fetch_n7`.

## Towards the WAN

The same scripts work for machines at different sites. Point a hosts file at their
addresses. `netem_egress.sh` finds each peer's device, so peers behind different
devices are fine. Known limits:
- The gRPC flow-control window is a fixed 8 MiB
  (`grpcFlowControlWindowSize`, `node/nodeMessageHub.go`), which caps each peer stream
  at about 1.4 Gb/s at 48 ms RTT, whatever the kernel buffers.
- The parallel-flow collapse measured on the stitched link
  (`docs/cloudlab-stitched-link-bw-latency.md`) is what BBR in `--tune-tcp-wan` is for.
- Colocated clients (one per node machine) need filters that tell client traffic apart
  from node traffic first.
