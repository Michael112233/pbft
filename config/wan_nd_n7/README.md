# wan_nd_n7: the network-delay baselines on the emulated 5+2 WAN

## Intention

Check that the two network-delay scenarios keep their behaviour on the emulated WAN with the WAN
timers (`docs/wan-timer-values.md`), Fixed/Perf **shared at 200 ms**:

| Scenario | Expected |
|---|---|
| NetworkDelay | FRR, PFRR and PFE cascade (high view-change rate, throughput near 0); PRR and PE make progress; PRR best (PRR and PE were a tie on the LAN) |
| NetworkDelayFCrash | the same cascade; PRR and PE make progress; **PE wins**, because Election never picks the crashed nodes while RoundRobin burns a 10 s timeout on each |

If a Fixed/Perf arm makes progress, the 170 ms delay is not enough on top of the WAN at 200 ms
timers (the model says first progress after install is ≥ 340 ms). If PE does not beat PRR in
NetworkDelayFCrash, Election's advantage does not survive the WAN.

## Compared against

- `results/lan_n7/{ndelay_frr,ndelay_prr,ndfcrash_pe}_lan` and `results/ckpt_fetch_n7/{ndelay,ndfcrash}_*_ckpton`:
  the same scenarios on the LAN (single host / LAN cluster), timers 150 ms. On the LAN: ND PRR ~3,650
  req/s, NDF PE ~3,490 req/s, ND FRR 0 (cascade).
- `results/wan/*_wan_t1000` (`config/timer_wan_n7/`): the WAN timer measurement these timers come from.

## What changes, what is held fixed

- **Bases:** `config/timer_lan_n7_validate/{ndelay,ndfcrash}_<action>_lanv.json`.
- **Timers:** `fixed_trigger_timeout_ms` 200, `fixed_new_view_timeout_ms` 150,
  `fixed_timers_shared` true, so both Fixed/Perf timers are 200 ms (the larger). Periodic stays 10 s.
- **`scenario_network_delay_ms` 170:** written out (the default). On the WAN it is added on top of each
  pair's base: near↔near and far↔far 170 ms, near↔far 194 ms one way.
- **The WAN** comes from the hosts file (`hosts.cloudlab.wan_far67.json`: nodes 6, 7 far, 24 ms one
  way, client near); the configs are cluster-independent.
- **Held fixed:** n = 7, oracle pinned to one action, 75 s generations, batch 50, `inject_speed` 200,
  one client, client retry off, `checkpoint_fetch` on, NDF crashed nodes {3,5}.

## Configs

`<scenario>_<action>_wannd.json`, 10 files:

| scenario | actions |
|---|---|
| `ndelay` | `frr`, `prr`, `pe`, `pfrr`, `pfe` |
| `ndfcrash` | `frr`, `prr`, `pe`, `pfrr`, `pfe` |

## How to run

On the client machine, branch `timer-model` (at or after `ae5b11c`), cluster set up with
`--tune-tcp-lan`:

```bash
git pull
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.wan_far67.json 10m config/wan_nd_n7/*.json
```

600 s per run plus ~1.5 min each: about **2 h**. Results land in `results/<config name>/`.

## How to analyse

```bash
# winners and cascades: per-generation throughput and view-change rate (node logs are UTC-6)
TZ=America/Denver python3 scripts/scenario_pilot_summary.py results/*_wannd

# timer expiries and what each timer waited, by leader site > replica site
python3 scripts/timer_budget_summary.py results/*_wannd

# the WAN and the scenario delay were in place: base at start, base + 170 after the switch
cat results/ndelay_prr_wannd/wan_check_*.txt
grep -h "netem " results/ndelay_prr_wannd/netem_schedule_node1.log | head
```

Pass when:
- **Cascade:** FRR, PFRR and PFE have a high view-change rate and throughput near 0 in both scenarios.
- **Periodic survives:** PRR and PE make steady progress in both.
- **NetworkDelayFCrash:** PE's median throughput per generation is above PRR's.

## Expected outcome / caveats

- **In NetworkDelayFCrash the quorum always crosses the link.** The crashed nodes 3 and 5 are both
  near, which leaves 3 live near nodes; a quorum of 5 then needs both far nodes. Every round crosses at
  194 ms one way, so throughput should come out below the LAN's ~3,490 req/s.
- **NetworkDelay keeps a near quorum** (5 near nodes), so its rounds are near↔near at 170 ms, about as on
  the LAN, except while a far node leads.
- **The new-view timer at 200 ms** (shared) is longer than a 170 ms NewView hop plus processing, so in
  NetworkDelay a near NewView can sometimes install before the timer fires; the progress timer (first
  progress ≥ 340 ms) still cascades the view. The cascade rate can therefore differ a little from the LAN.
- **Election:** the candidate is drawn from the 5 live nodes in NDF (`electionCandidateForView` excludes
  3 and 5), so 2 in 5 are far.
- **NetworkDelay on the WAN has not run before;** the cascade is predicted by the model in
  `docs/wan-timer-values.md`, not yet measured.

## Provenance

- 2026-10-17, branch `timer-model` at `ae5b11c` (client switches leader on f+1 notices, the leader sends
  its own).
- Request: test the baselines in NetworkDelay and NetworkDelayFCrash on the WAN, to see that Fixed and
  Perf cascade, Periodic survives, and Election wins NetworkDelayFCrash, with the Fixed/Perf timers
  shared at 200 ms.
