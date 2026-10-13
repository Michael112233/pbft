# n=7 scenario pilots with 7 clients

## Intention

Do the per-generation numbers behind the synthetic bandit data change when the load
comes from 7 clients (one colocated with each node, 1/7 of `inject_speed` each)
instead of one client?

- **If they change:** if throughput or VC rate per (scenario, action) moves enough to
  change which action wins a scenario, the synthetic data in
  `learningagent/simulate_quadrf_n7.py` has to be rebuilt from multi-client pilots
  before the WAN work.
- **If they don't:** the old pilots stay valid and only the latency side needs new
  data. In a short Healthy check, 7 clients cut client latency by ~45%.

This batch covers all five scenarios (Healthy, ProposalDelay, NetworkDelay,
NetworkDelayFCrash, Throttle) for all five baseline actions, plus the 30-min Throttle
PFE run.

## Compared against

`results/all_new_scenario_pilots/<scenario>_<action>`: the n=7 pilots from 2026-10-08,
run with one client. The 30-min config compares with
`throttle_pfe_30min_20261009_173627`. Each config here is that run's own
`config.json`, with only the changes below.

## What changes, what is held fixed

| Key | Value | Why |
|---|---|---|
| `client_num` | 7 | The variable under test. |
| `timer.relaxed_fixed_trigger_timeout_ms` | 350 | **Reproduces the old timing in Healthy and PD.** Since b0f2f83 (2026-10-09), scenario mode uses this key for every scenario except ND and NDF (`config.FixedFloorForScenario`). The old pilots ran at 350 ms through `fixed_trigger_timeout_ms` and had 300 here, unused. Left at 300, Healthy and PD would quietly run at 300 ms. |
| `timer.fixed_trigger_timeout_ms` | 150 (ND, NDF), 350 (others) | Unchanged from the old configs. ND and NDF read it (150 ms, so Fixed and Perf cascade under the 170 ms delay). Throttle reads it because scenario mode is off. Healthy and PD no longer read it. |
| `scenario_mode` (Throttle only) | false | **Needed for the configs to load at all.** The old Throttle pilots ran a static throttle (`throttle.enabled: true`) under `scenario_mode: true, scenarios: ["Healthy"]`, which current code rejects. With scenario mode off, the static throttle runs as before: nodes allow the gate (`throttleGateAllowed`), and the timeout is `fixed_trigger_timeout_ms` = 350. Same as the 2026-10-09 30-min rerun. |
| `timer.epoch_grid` | false | Written out; the old code had no grid, which is the same as false. |
| `oracle_decision_delay_ms` | 100 | Written out; the old oracle had a hardcoded 100 ms delay. |

Held fixed, as in the old pilots:
- **Run shape:** n=7, 75 s epochs, `inject_speed` 200, `max_batch_size` 50; 600 s per
  run, except the 30-min config (1800 s).
- **Oracle and client:** oracle pinned to the run's action; client retry off, so
  latency is committed-only.
- **ND / NDF:** 170 ms netem on all node pairs, applied by node 4.
  - Clients are not delayed: their 127.0.1.x addresses match no filter, the same as
    the old single client at 127.0.0.1.
  - NDF crashes nodes 3 and 5. Clients 3 and 5 keep sending to the leader, so the
    offered load is still the full 200 per 24 ms. A client is not a node, so its
    node crashing doesn't stop it.
- **PD:** nodes 1 and 2 sleep 100 ms in `tryPropose` while leading.
- **Throttle:** 2 slots, `prep_ms` 3000, `proposal_min_interval_ms` 100; strategy
  `roundrobin` for RR actions, `election` for Election actions.
  - Only client 1 runs the throttle manager (a node's gate is one setting).
  - Client 1 sees leader changes through its own 2f quorum, exactly as the single
    client did.
- **Timers and Perf:** Periodic 10 s; Perf bar settings as before.

`aware_candidates` is absent from the old configs and takes its default; no Aware
action runs here.

## Configs

Actions: frr = FixedRoundRobin, prr = PeriodicRoundRobin, pe = PeriodicElection,
pfrr = PerformanceRoundRobin, pfe = PerformanceElection.

| Files | Scenario | Fixed/Perf timeout | Fault |
|---|---|---|---|
| healthy_{frr,prr,pe,pfrr,pfe}_mc7.json | Healthy | 350 ms | none |
| pdelay_{…}_mc7.json | ProposalDelay | 350 ms | nodes 1, 2 sleep 100 ms per proposal |
| ndelay_{…}_mc7.json | NetworkDelay | 150 ms | 170 ms netem on all node pairs |
| ndfcrash_{…}_mc7.json | NetworkDelayFCrash | 150 ms | 170 ms netem + nodes 3, 5 crashed |
| throttle_{…}_mc7.json | Throttle (static, scenario mode off) | 350 ms | 2 throttle slots, prep 3 s |
| throttle_pfe_30min_mc7.json | Throttle (static), PFE | 350 ms | same, 1800 s run |

All 26 load through `config.ReadCfg` (checked 2026-10-13).

## How to run

Use a commit at or after dd9ff4b ("client harness"); `client_num` needs it. Commit this
folder first so the other machine has it.

```bash
# 25 runs x 600 s: about 4.5 h with setup and export
./run_experiments.sh 600 config/pilots_n7_mc7/{healthy,pdelay,ndelay,ndfcrash}_*_mc7.json \
    config/pilots_n7_mc7/throttle_{frr,prr,pe,pfrr,pfe}_mc7.json
# the 30-min Throttle PFE run, about 31 min
./run_experiments.sh 1800 config/pilots_n7_mc7/throttle_pfe_30min_mc7.json
```

- Results land in `results/<config name>/`: per-client files `client<i>_*`, plus the
  merged single-client names (`report.json`, `latencyreport.json`, ...) written by
  `scripts/merge_client_reports.py`.
- With `scripts/run_isolated*.sh`, move the worktree to that commit first
  (`git -C ../pbft-A checkout --detach <commit>`); a reused worktree is not updated.
- The host needs passwordless `sudo -n` for node 4's netem in the ND and NDF runs.

Before the long batch, check that 7 clients sustain ~8,100 req/s on that host,
especially when CPU-pinned. The 7-client test so far ran unpinned on 56 CPUs.

## How to analyse

```bash
python3 scripts/scenario_pilot_summary.py results/all_new_scenario_pilots/{healthy,pdelay,ndelay,ndfcrash,throttle}_{frr,prr,pe,pfrr,pfe} > old.txt
python3 scripts/scenario_pilot_summary.py results/*_mc7 > new.txt
python3 learningagent/simulate_quadrf_n7.py --overlap   # after pointing it at the new summary, if numbers moved
```

Compare per (scenario, action) and per generation: `tput`, `vc_rate`, `install_rate`,
`interval_ms`. Check in particular:
- **Is the best action per scenario the same?** The old pilots gave:
  - Healthy: FRR
  - PD: PFE 7,539 > PFRR 6,969
  - ND: PRR ≈ PE
  - NDF: PE
  - Throttle: PFE (30-min) > PFRR 4,949
- **Does the PD node-1/2 bimodality in throughput look the same?** So also the
  Throttle PFE bimodality.
- **Latency:** `latencyreport.json` (merged) and its per-client `clients` list.

## Expected outcome / caveats

- **Throughput and VC behaviour:** expected close to the old pilots. The offered
  rate is the same, and the node-side fault mechanics don't depend on how many
  clients there are.
- **PD may differ:**
  - Each client now has its own stream to the slow leader, and each fills about 7×
    more slowly.
  - The next leader is fed by 7 send queues instead of one.
- **Latency:** expected lower, as in the Healthy check.
- **Throttle:** the gate timing should match, since there is one manager on client 1,
  as before. Only the request arrival pattern at the gated leader changes.
- **Confounders:**
  - 11 commits landed between the old pilots (9bc29ae) and dd9ff4b, so a difference
    may come from the code rather than the clients.
  - The host or CPU pinning may differ from the old runs.
  - To separate the client effect from the code changes, run the same configs with
    `client_num: 1` on the new commit.

## Provenance

Written 2026-10-13 against dd9ff4b. Request: rerun the n=7 scenario × action pilots
(all five scenarios) with 7 clients, to compare with the one-client pilots. Runs happen
on a separate machine.
