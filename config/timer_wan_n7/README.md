# timer_wan_n7: what the Fixed/Perf timers have to cover on the emulated 5+2 WAN

## Intention

The WAN counterpart of [`config/timer_lan_n7/`](../timer_lan_n7/): measure how long a view change
and the first progress after it take on the emulated WAN, so the WAN's Fixed/Perf **progress** and
**new-view** timeouts can be set by the same rule as the LAN's (round up to 50 ms of 1.5 × the
p99.9 need; LAN result: 150 / 50 ms, [`docs/lan-timer-values.md`](../../docs/lan-timer-values.md)).

The WAN: nodes 1–5 and the client in one cluster (near), nodes 6 and 7 in the other (far), 24 ms
one way between the clusters, LAN inside each. With n = 7 the quorum is 5, so the near cluster is a
quorum by itself and a far leader's every round crosses the link.

## Compared against

`results/t1000_run` (`config/timer_lan_n7/`): the same 5 arms on the plain LAN. The only intended
differences are the WAN (the hosts file) and the ProposalDelay mechanism (non-blocking spacing,
commit `e0187d1`).

## What changes, what is held fixed

- **Emulated WAN, from the hosts file** (`scripts/cluster/hosts.cloudlab.wan_far67.json`, `wan`
  block): every host delays its own egress to every host in the other cluster by 24 ms
  (`scripts/netem_egress.sh base`, applied by `start.sh` before any process starts and checked
  with pings into `logs/wan_check.txt`). The configs themselves are cluster-independent.
- **Timers:** progress and new-view 1000 ms, so no timer evicts a leader and no need is cut off.
- **`proposal_delay_interval_ms` 100** in place of the old `proposal_delay_sleep_ms`;
  **`scenario_network_delay_ms` 170** written out (no NetworkDelay arm here, so unused).
- **Held fixed** from `timer_lan_n7`: n = 7, oracle pinned to one action, 75 s generations,
  batch 50, `inject_speed` 200, one client, client retry off, `checkpoint_fetch` on,
  ProposalDelay nodes {1,2} (near), Throttle gate 100 ms and prep 3000 ms, `pilot_exec_trace` on.

## Configs

| file | scenario | action | what it measures |
|---|---|---|---|
| `healthy_frr_wan_t1000.json` | Healthy | FixedRoundRobin | generation-switch views, long tenures |
| `healthy_pfrr_wan_t1000.json` | Healthy | PerformanceRoundRobin | RoundRobin view changes, far leader 2 in 7 |
| `healthy_pfe_wan_t1000.json` | Healthy | PerformanceElection | Election view changes, far candidate ~2 in 7 |
| `pdelay_pfrr_wan_t1000.json` | ProposalDelay | PerformanceRoundRobin | slow (near) leaders at interval 100 ms |
| `throttle_pfe_wan_t1000.json` | Throttle | PerformanceElection | throttled leaders at gate 100 ms |

## How to run

On the client machine, from the repo on branch `timer-model` with the emulated-WAN harness, on the
same cluster (set up with `--tune-tcp-lan`; run `setup.sh` once more if this cluster was set up
before the harness change, so the client host loads the netem modules too):

```bash
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.wan_far67.json 10m config/timer_wan_n7/*.json
```

600 s per run plus about 1.5 min each: about **1 h**. Results land in `results/<config name>/`,
each with `hosts.json` (the WAN layout), `wan_check_<host>.txt` and `netem_state_*_<host>.txt`.

## How to analyse

```bash
# first: the WAN was really there on every host
cat results/healthy_frr_wan_t1000/wan_check_*.txt        # near pairs ~0.1 ms, near-far ~48 ms RTT

# the timer needs, split by leader site > replica site (from hosts.json)
python3 scripts/timer_budget_summary.py results/healthy_{frr,pfrr,pfe}_wan_t1000
python3 scripts/timer_budget_summary.py results/{pdelay_pfrr,throttle_pfe}_wan_t1000

# message delivery over the link: near-near ~10 ms, near-far ~35 ms (24 + processing)
python3 scripts/vc_delivery_summary.py results/*_wan_t1000
```

The WAN timeouts are the rule applied to the Healthy runs, over every site combination: one value
has to cover a far leader too.

## Expected outcome / caveats

- **Near leader:** about the LAN needs (progress ~81 ms, new-view ~30 ms). Far replicas trail by
  24 ms but keep executing, so their timers keep resetting.
- **Far leader:** about +2d = +48 ms on both. Its NewView, the client handover and its PrePrepare
  each cross the link, and the near replicas, a quorum on their own, wait for them: progress
  ~130 ms, new-view ~80 ms. Election: the vote round also crosses the link, but the VDF race is
  still outside the timer.
- **So roughly progress 200 ms and new-view 100–150 ms** by the rule. This batch replaces the guess.
- **A far leader's throughput** is also capped by `max_inflight_seq` 40 per 2d round trip (~830
  batches/s at 48 ms, far above the ~163 offered), so it should not slow down; the Perf bar may
  still record different view throughputs near and far.
- **The first big ViewChange or NewView** across the link can take an extra round trip in
  generation 1 (the receive window, `docs/multi-host.md`); generation 1 is skipped in the summaries.

## Provenance

- 2026-10-17, branch `timer-model` after `cba55bd`, with the emulated-WAN harness
  (`netem_egress.sh base/up/down/clear`, the hosts-file `wan` block, `scenario_network_delay_ms`,
  `leader=` in the `PROGRESS` lines).
- Request: measure the progress and new-view timer needs on a 5+2 WAN (24 ms one way, client
  near) the way `timer_lan_n7` did for the LAN.
