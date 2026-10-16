# lan_n7: first runs with one node per machine

## Intention

Does the protocol behave the same when the 7 nodes run on 7 machines of one LAN
(`-m remote`, `scripts/cluster/`) as when they are 7 processes on one host? This is the
gate before any WAN experiment. If an arm differs from its single-host twin beyond the
caveats below, the multi-host harness or the per-host netem is wrong and must be fixed
before any cluster result is used.

## Compared against

`results/ckpt_fetch_n7/<arm>_ckpton` (2026-10-15): the most recent single-host n=7 runs,
on the same code apart from the multi-host change itself. Every single-scenario config
here is a copy of `config/ckpt_fetch_n7/<arm>_ckpton.json` with only the `_note` changed,
so the only intended difference is the deployment. The multi-scenario arm has no
single-host twin; compare each scenario's generations with the matching `<scenario>_prr`
arm.

## What changes, what is held fixed

- **Deployment, not config:** nodes on separate hosts, the client on an 8th host,
  learning agents next to their nodes. The 170 ms delay is applied by every node to its
  own egress (`scripts/netem_egress.sh`), not by node 4 on `lo`. The client is never
  delayed, as on `lo`.
- **Config keys:** none, apart from `timer.epoch_grid: false` written out (the default)
  because the twins predate the key.
- **`multiscen_prr_lan` only:**
  - all four scenarios, `scenario_generations` 2 (150 s each; a cycle takes 10 min)
  - ProposalDelay nodes {1,2} and crashed nodes {3,5}, as in the twins
  - `relaxed_fixed_trigger_timeout_ms` 350, so Healthy and ProposalDelay get the twins'
    350 ms and the network-delay scenarios keep 150 ms

Held fixed, from the twins: n=7, oracle pinned to one action, 75 s generations, batch
50, `max_inflight_seq` 40, `inject_speed` 200, client retry off, `checkpoint_fetch` on
(200 ms).

## Configs

| file | scenario | action | why | twin |
|---|---|---|---|---|
| `healthy_frr_lan.json` | Healthy | FixedRoundRobin | smoke test; run it first, alone | `healthy_frr_ckpton` |
| `pdelay_pfrr_lan.json` | ProposalDelay | PerformanceRoundRobin | in-process fault, Perf bar | `pdelay_pfrr_ckpton` |
| `ndelay_prr_lan.json` | NetworkDelay | PeriodicRoundRobin | per-host netem, expected best | `ndelay_prr_ckpton` |
| `ndelay_frr_lan.json` | NetworkDelay | FixedRoundRobin | negative control: must still cascade | `ndelay_frr_ckpton` |
| `ndfcrash_pe_lan.json` | NetworkDelayFCrash | PeriodicElection | delay plus dead flag | `ndfcrash_pe_ckpton` |
| `multiscen_prr_lan.json` | all 4, cycling | PeriodicRoundRobin | netem down/up and dead toggles at switches | per-scenario `_prr_ckpton` |

## How to run

On the client machine of the CloudLab experiment (`cloudlab/profile.py`), after giving it
its own ssh key on every node (`docs/multi-host.md`, Bring-up step 2, so a batch survives
your logout), from the repo, once per new cluster:

```bash
scripts/cluster/setup.sh scripts/cluster/hosts.cloudlab.json --tune-tcp-lan
```

Smoke run first (600 s), then check it (next section) before running the rest:

```bash
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.json 10m config/lan_n7/healthy_frr_lan.json
```

Then the rest. `run_experiments.sh` detaches itself, so each command returns at once and
keeps running after you log out. Follow it with `tail -f cluster_run.out`. Only one batch
runs at a time, so start the second command after the first prints
`All experiments finished.`:

```bash
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.json 10m config/lan_n7/{pdelay_pfrr,ndelay_prr,ndelay_frr,ndfcrash_pe}_lan.json
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.json 20m config/lan_n7/multiscen_prr_lan.json
```

- **Duration:** 600 s per single-scenario arm and 1200 s for the multi-scenario arm, plus
  about 1–2 min per run for build, push, start and log collection. About **20 min**
  for the smoke run and **1.2 h** for the rest.
- **Results:** `results/<config name>/` on the client machine, flat, like a single-host
  run. Files every host writes get the host's label (`others_node3.log`,
  `netem_schedule_node3.log`, `clock_offsets_node3.txt`). Each results directory also
  has `hosts.json`.

## How to analyse

```bash
# throughput and view-change rate per generation, LAN vs single host
python3 scripts/scenario_pilot_summary.py results/{healthy_frr,pdelay_pfrr,ndelay_prr,ndelay_frr,ndfcrash_pe}_lan
python3 scripts/scenario_pilot_summary.py results/ckpt_fetch_n7/{healthy_frr,pdelay_pfrr,ndelay_prr,ndelay_frr,ndfcrash_pe}_ckpton

# multi-scenario: generations 2 Healthy, 3-4 ProposalDelay, 5-6 NetworkDelay,
# 7-8 NetworkDelayFCrash, 9-10 Healthy, ... (generation 1 is skipped as startup)
python3 scripts/scenario_pilot_summary.py results/multiscen_prr_lan
python3 scripts/scenario_pilot_summary.py results/ckpt_fetch_n7/{healthy,pdelay,ndelay,ndfcrash}_prr_ckpton

# the delay is really on every host, at every switch
grep -h "netem" results/ndelay_prr_lan/node_*.log | grep -c "applied"    # 7 nodes x (down + up)
cat results/multiscen_prr_lan/netem_schedule_node*.log

python3 scripts/ckpt_fetch_summary.py results/*_lan

# TCP retransmissions, RTO timeouts and NIC drops per host during each run
python3 scripts/tcp_counters_summary.py results/*_lan
```

What to read:
- **Smoke:** every `node_<i>.log` shows views installing and the client's `report.json`
  shows throughput close to `healthy_frr_ckpton`. If a node never got its address or
  port, `start.sh` fails at the barrier with `node <i> (...) not reachable`.
- **NetworkDelay:** `ndelay_frr` must cascade (VC rate high, throughput near zero, as in
  the twin) and `ndelay_prr` must make progress. If `ndelay_frr` makes progress, the
  delay is missing on some host: check `netem_schedule_node*.log` and the `netem ...
  failed` lines.
- **Multi-scenario:** every node logs `netem down` and `netem up 170ms applied` at each
  switch into a network-delay scenario, and `netem down` when leaving it. Per-scenario
  throughput should match the `_prr_ckpton` twins.

## Expected outcome / caveats

- **Expected:** Healthy, ProposalDelay and the Periodic network-delay arms within a few
  percent of their twins, and the cascade in `ndelay_frr` preserved.
- **CPU is no longer shared.** Each process has a whole 28-core c6320 instead of sharing
  one, and the worker pools size themselves with `runtime.NumCPU()`. A faster view
  change (more headroom under the 150/350 ms timers) is a real effect of the deployment,
  not a bug. To match the single-host runs more closely, rerun with `NODE_GOMAXPROCS`.
- **TCP has to be matched to `lo`: run setup with `--tune-tcp-lan`.**
  - **On `lo`:** the 10-segment initial and restart window is ~640 KiB (64 KB
    segments). The twins' NetworkDelay VCs and NVs, up to ~590 / ~510 KiB, arrived in
    one flight (median 186–192 ms for 450–700 KiB VCs), **except the first big VC of a
    run** (next point).
  - **On a default 1500-MTU LAN:** that window is 14 KiB, so the same VC would take
    about 6 round trips (~1.9 s).
  - **What `--tune-tcp-lan` does:** sets initcwnd/initrwnd 453 (~640 KiB) on the
    experiment route and leaves everything else at stock values (cubic, slow-start
    restart on). Only the initcwnd part takes effect (next point).
  - **The first big VC of a run takes more than one flight, on `lo` and on the LAN.**
    TCP sends at most min(cwnd, rwnd) unacknowledged bytes:
    - **cwnd** (congestion window) is the sender's own limit. initcwnd sets its start,
      so it starts at ~640 KiB as on `lo`.
    - **rwnd** (receive window) is what the receiver advertises. The kernel starts it
      at most 64 KiB whatever initrwnd says (`tcp_select_initial_window`, v5.15:
      `rcv_wnd = min(space, U16_MAX)`, and initrwnd can only lower it). It then grows
      only as the receiver sees data arrive.
    - Before the first view change the peer links carry only small Prepare/Commit
      traffic, so rwnd is still below a ~550 KiB VC. The rest of the VC waits one round
      trip for the larger window. Measured in `results/lan_n7/ndelay_prr_lan`: the
      view (1,2) VCs took ~527 ms (170 + 340 + processing), and 36 of the 42 peer
      connections were rwnd-limited for one RTT (336–340 ms) over the whole run. On
      `lo` the same VC took ~1.21 s (three extra round trips; why three is not
      measured).
    - rwnd does not shrink afterwards, so later VCs of the same size arrive in one
      flight. Whether it happens depends on the first VC's size. In `ndfcrash_pe_lan`
      the first VCs grew gradually (107 → 179 KiB) and none waited. It falls in
      generation 1, which the summaries skip.
  - **Don't use `--tune-tcp-wan` here.** BBR, `fq` and 128 MB buffers differ from the
    single host.
  - **Check:** `tcp_state_node*.txt` in each results directory shows the settings used.
    VC delivery times in a NetworkDelay generation (`VC sent` on the sender against
    `HUB: Received ViewChange` on the receivers) should be ~170–195 ms, as on `lo`,
    apart from the first big VC above.
- **Delay switches are per node.** On `lo`, node 4 switched the delay for all pairs at
  its own generation switch. Here each node switches its own egress at its own switch,
  within one view change of the others.
- **The client is on its own host:** its latency includes one real LAN round trip
  (~0.1–0.2 ms).
- **Clocks:** the cross-host analysis (client timestamps against node 4's log) relies on
  NTP. `clock_offsets_*.txt` shows each host's offset; ~1 ms doesn't matter for
  75 s windows.
- **Not covered:** Throttle (client-driven, host-independent in the code, but the leader
  notices now carry LAN latency), the learning agent (the oracle is used everywhere), and
  `client_num` > 1 (the hosts file rejects a client on a node's IP).

## Provenance

- 2026-10-15, branch `vc-compact-certs` at `dc30d38`, plus the uncommitted multi-host
  change:
  - `-m remote` with `--hosts` (config/network.go)
  - per-host netem (node/scenario.go, scripts/netem_egress.sh)
  - `scripts/cluster/`, `cloudlab/profile.py`
- Request: "what will be needed to run my pbft system on 7 machines" on a CloudLab LAN,
  with one client on an 8th machine, as the step before the WAN.
