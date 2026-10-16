# timer_lan_n7: what the Fixed/Perf timers have to cover on the LAN

## Intention

Measure, on the 7-host LAN cluster, how long a view change and the first progress after
it take, so the Fixed/Perf **progress** and **new-view** timeouts can be set from data
instead of the old per-scenario 150/350 ms hack. The same runs measure how long a slow
leader (ProposalDelay, Throttle) takes between slots, so the ProposalDelay sleep and the
Throttle gate can be set below the progress timeout: a slow leader must pass the timer and
be caught only by the Perf bar.

The values chosen here are used by the LAN validation batch (every scenario × action),
where the goal is Healthy → FRR, ProposalDelay → PFRR, Throttle → PFE, NetworkDelay → PRR,
NetworkDelayFCrash → PE.

## Compared against

Nothing; this is a measurement. The provisional values came from `results/lan_n7`
(timers at 350 ms, so nothing was evicted there either):
- RoundRobin quorum → NewView install: max 23 ms.
- New leader → first client request: p99 32 ms.
- Install → 3rd new slot, Healthy: p99 86 ms.
- Under the 170 ms delay, first progress ≥ 345 ms. Any progress timeout ≤ ~300 ms keeps
  NetworkDelay and NetworkDelayFCrash cascading.

## What changes, what is held fixed

- **Timers:** `fixed_trigger_timeout_ms` (progress) and `fixed_new_view_timeout_ms` both
  1000 ms, `fixed_timers_shared` false. Long enough that no timer evicts a leader, so the
  measured needs are not cut off at the timeout. The Perf bar still rotates leaders about
  every 11 s, which gives many view changes in the Perf arms.
- **`proposal_delay_sleep_ms` 100 and `proposal_min_interval_ms` 100:** today's slow-leader
  values, written out.
- **`pilot_exec_trace` on:** one `PILOT_EXEC` line per view with every slot's execution
  time in the first 2.5 s, in case the shape of the first progress needs a closer look.
- **Held fixed** from the bases: n=7, oracle pinned to one action, 75 s generations, batch
  50, `max_inflight_seq` 40, `inject_speed` 200, client retry off, `checkpoint_fetch` on.
  ProposalDelay nodes {1,2}, Throttle prep 3000 ms, 2 slots.
- **Bases:** `config/lan_n7/{healthy_frr,pdelay_pfrr}_lan.json` and
  `config/ckpt_fetch_n7/{healthy_pfrr,healthy_pfe,throttle_pfe}_ckpton.json` (the configs
  `lan_n7` was copied from). The removed `relaxed_fixed_trigger_timeout_ms` is dropped; the
  code now rejects it.

## Configs

| file | scenario | action | what it measures |
|---|---|---|---|
| `healthy_frr_t1000.json` | Healthy | FixedRoundRobin | gaps over long tenures; generation-switch views |
| `healthy_pfrr_t1000.json` | Healthy | PerformanceRoundRobin | RoundRobin view changes (~50 per run) |
| `healthy_pfe_t1000.json` | Healthy | PerformanceElection | Election view changes (VDF + RequestVote) |
| `pdelay_pfrr_t1000.json` | ProposalDelay | PerformanceRoundRobin | slow leader at sleep 100 ms |
| `throttle_pfe_t1000.json` | Throttle | PerformanceElection | throttled leader at gate 100 ms |

## How to run

On the client machine, from the repo on branch `timer-model` (the code with the two timer
keys and the `PROGRESS` lines), on a cluster set up with `--tune-tcp-lan`:

```bash
scripts/cluster/run_experiments.sh scripts/cluster/hosts.cloudlab.json 10m config/timer_lan_n7/*.json
```

600 s per run plus about 1–2 min for build, start and log collection: about **1 h** in
total. Results land in `results/<config name>/`.

## How to analyse

```bash
# the timeout values: needs from the Healthy arms (RoundRobin and Election)
python3 scripts/timer_budget_summary.py results/healthy_{frr,pfrr,pfe}_t1000

# the slow leaders: first progress and in-view gap at sleep/gate 100 ms
python3 scripts/timer_budget_summary.py results/{pdelay_pfrr,throttle_pfe}_t1000

# TCP side: VC/NV delivery, slow ones split into generation 1 and later
python3 scripts/vc_delivery_summary.py results/*_t1000
```

What to read:
- **Progress timeout:** the rule line of the Healthy run: round up to 50 ms of 1.5 × the
  p99.9 of max(first_exec, max_gap). Keep it ≤ 300 ms (the NetworkDelay bound above).
- **New-view timeout:** the same rule on the `nv` column (`nv_timer_to_install_ms`: from
  the timer's last start to the install). Under Election the timer starts only when the
  candidate's VDF finishes or a node grants its vote, so the VDF race is not in it; the
  `quorum` column, from the 2f+1 ViewChanges, shows the whole Election path.
- **Sleep and gate:** a slow leader adds its sleep or gate S to the healthy first progress
  and gap. Pick S ≤ T − (slow p99.9 of max(first, gap) − 100) − 25 ms margin.
- **Every run:** `expiries: progress 0, new-view 0`. Any expiry means a need longer than
  1 s and the run must be looked at.

## Expected outcome / caveats

- **Expected:** progress need around 60–90 ms (about 150 ms by the rule). New-view need
  around 25 ms for RoundRobin and similar for Election once its VDF is excluded (it was up
  to ~110 ms on lo with the VDF), so 50–100 ms. The ProposalDelay and Throttle arms show
  first progress and gaps of 100 ms plus the healthy values.
- **Election starts no new-view timer at the ViewChange quorum.** If the designated
  candidate never sends RequestVote, no node starts one and the view change waits forever.
  Only the scaffolding prevents it (crashed nodes are never candidates). Nothing here hits
  it, since no node is crashed in these scenarios.
- **The first view of each generation (`g1` rows)** carries the generation switch and the
  decider. It may be slower and is listed separately.
- **Election's VDF time is CPU-bound,** so it is only valid for this cluster's hosts.
- **The Throttle arm's gate depends on the client's attack timing (prep 3000 ms),** so some
  of its leaders run ungated for a while; read its gap column, not its throughput.

## Provenance

- 2026-10-16, branch `timer-model` off `vc-compact-certs` at `3be222b`, plus the Phase 1
  change (two timer keys, `proposal_delay_sleep_ms`, `PROGRESS` lines,
  `scripts/timer_budget_summary.py`).
- Request: derive the LAN progress and new-view timeouts from VC/NV processing and the
  client handover, then set ProposalDelay and Throttle below them; later the same for a
  5+2 two-cluster WAN.
