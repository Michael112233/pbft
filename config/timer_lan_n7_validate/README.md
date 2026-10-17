# timer_lan_n7_validate: do the measured LAN timers give each scenario its own winner?

## Intention

The LAN Fixed/Perf timeouts were set from measurement (`results/t1000_run`, rule: round up to
50 ms of 1.5 × the p99.9 need): **progress 150 ms, new-view 50 ms**. ProposalDelay is now a
non-blocking spacing with the tenure's first proposal free, so it can stay at **100 ms**, and the
Throttle gate stays at **100 ms**. This batch checks that with those values every scenario keeps a
winner of its own on the 7-host LAN:

| Scenario | Must win | Why |
|---|---|---|
| Healthy | FixedRoundRobin | nothing to fix; Fixed never fires, no forced rotation |
| ProposalDelay | a Perf action (PFRR or PFE) | the slow leader passes the 150 ms timer, only the Perf bar removes it |
| Throttle | PerformanceElection | as ProposalDelay, and Election defeats the client pre-aiming RoundRobin's next leader |
| NetworkDelay | PeriodicRoundRobin | Fixed/Perf cascade under 170 ms (first progress ≥ 345 ms > 150) |
| NetworkDelayFCrash | PeriodicElection | as NetworkDelay, and Election never picks the crashed nodes |

If a scenario loses its winner, the values (or ProposalDelay/Throttle) need another look before the
multi-scenario LAN run.

## Compared against

- `results/t1000_run` (`config/timer_lan_n7/`): the measurement these values come from, timers 1 s,
  old blocking ProposalDelay sleep.
- `results/ckpt_fetch_n7/<scenario>_<action>_ckpton` (single host) and `results/lan_n7`: the same
  25 arms with the old 150/350 ms per-scenario timers. Only for orientation; matching lo is not a goal.

## What changes, what is held fixed

- **Bases:** `config/ckpt_fetch_n7/<scenario>_<action>_ckpton.json`, the latest complete 5 × 5 set
  (`lan_n7` was copied from it).
- **Timers:** `fixed_trigger_timeout_ms` 150 (progress, also the Perf floor),
  `fixed_new_view_timeout_ms` 50, `fixed_timers_shared` false, `epoch_grid` false written out. The
  removed `relaxed_fixed_trigger_timeout_ms` is dropped. The bases had 350 (Healthy, ProposalDelay,
  Throttle) and 150 (network-delay scenarios) for both timers.
- **`proposal_delay_interval_ms` 100:** new key, from the non-blocking ProposalDelay (commit `e0187d1`).
- **Held fixed:** n=7, oracle pinned to one action, 75 s generations, batch 50,
  `max_inflight_seq` 40, `inject_speed` 200, client retry off, `checkpoint_fetch` on (200 ms),
  `proposal_min_interval_ms` 100, Throttle prep 3000 ms and 2 slots, ProposalDelay nodes {1,2},
  crashed nodes {3,5}.

## Configs

`<scenario>_<action>_lanv.json` for every scenario × action, 25 files:

| scenario | actions |
|---|---|
| `healthy` | `frr`, `prr`, `pe`, `pfrr`, `pfe` |
| `pdelay` | same |
| `throttle` | same |
| `ndelay` | same |
| `ndfcrash` | same |

## How to run

On the client machine, from the repo on branch `timer-model` at or after `e0187d1`, on a cluster set
up with `--tune-tcp-lan`:

```bash
git pull
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.json 10m config/timer_lan_n7_validate/*.json
tail -f cluster_run.out      # wait for "All experiments finished."
```

600 s per run plus about 1.5 min each for build, start and log collection: about **4.8 h** in total.
Results land in `results/<config name>/`, e.g. `results/pdelay_frr_lanv`.

## How to analyse

```bash
# winners: per-generation throughput and view-change rate per arm (node logs are in UTC-6)
TZ=America/Denver python3 scripts/scenario_pilot_summary.py results/*_lanv

# timer needs and expiries per arm
python3 scripts/timer_budget_summary.py results/*_lanv

# TCP side
python3 scripts/vc_delivery_summary.py results/*_lanv
```

Pass when:
- **Winners** as in the table above (median throughput per generation).
- **No timer expiries where the timers must survive:** `expiries: progress 0, new-view 0` for
  `healthy_frr`, `pdelay_frr` and `throttle_frr`. In the Perf arms a few progress expiries are
  expected and harmless: the leader's own Perf timer sometimes removes it with fewer than f+1
  replicas agreeing, and the rest wait one progress timeout (seen in `healthy_pfe_t1000`).
- **ProposalDelay as designed:** slow-leader views (leaders 1 and 2) show a first execution in the
  Healthy range (≤ ~81 ms) and gaps of ~107 ms.
- **Cascade kept:** in `ndelay_*` and `ndfcrash_*`, FRR, PFRR and PFE have a high view-change rate and
  throughput near zero.

## Expected outcome / caveats

- **Healthy margins are small.** In the n=7 pilots every Healthy arm was within a few percent; FRR
  wins by not rotating. A 1–2% difference between arms is noise for one 10-min run.
- **ProposalDelay: PFE may beat PFRR.** In the pilots PFE won by ~3% because RoundRobin restarts at
  node 1 (a slow node) every generation. Either Perf action counts as the expected outcome.
- **NetworkDelay: PRR and PE were a tie** in the pilots.
- **ProposalDelay changed mechanism** (`e0187d1`): no blocking sleep, first proposal free. Its numbers
  are not comparable to earlier ProposalDelay runs.
- **The new-view timeout of 50 ms** covers every measured view change (max 30 ms) but not a
  ViewChange fetch retry (20 ms + 40 ms). None happened in ~3,800 view changes; a miss would cost
  one extra view change.

## Provenance

- 2026-10-17, branch `timer-model`, code at `e0187d1` (non-blocking ProposalDelay) on top of
  `72cc397` (two timer keys, `PROGRESS` lines).
- Request: validate the LAN timers (progress 150, new-view 50) with ProposalDelay and Throttle at
  100 ms, across every scenario and action.
