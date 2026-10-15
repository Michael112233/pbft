# ckpt_fetch_n7: checkpoint state transfer, repro and normal-case check

## Intention

Two questions about `checkpoint_fetch` (node/checkpointfetch.go):

1. **Does it repair the stranding?** In the n=7 multi-scenario run, node 4 (the epoch
   aggregator, which decides first at every generation switch) was stranded below a
   checkpoint in ~10% of NetworkDelay generations under the Periodic actions. It
   executed nothing for the rest of the 10 s tenure and then sent a 500-cert ViewChange.
   With the feature on it should fetch the checkpoint state about 0.5–0.8 s after the
   NewView instead.
2. **Is it quiet in normal operation?** In every n=7 pilot arm it should not fetch at
   all, apart from a real stranding. Every `CKPT FETCH stale` (the node caught up before
   the reply came) is an unnecessary state transfer. Any fetch in Healthy, ProposalDelay,
   Throttle or NetworkDelayFCrash needs explaining. In NetworkDelayFCrash a stranding is
   impossible: with exactly 2f+1 live nodes, a checkpoint only becomes stable once every
   live node has executed it.

**Decision:** if (1) holds and (2) shows no unnecessary fetches, set
`checkpoint_fetch.enabled: true` in `config/run2new.json`. It is `false` there now.

## Compared against

- **Repro:** the `_off` and `_on` arms are the same config apart from
  `checkpoint_fetch.enabled`, run on the same code.
- **Normal case:** the n=7 pilots, `results/all_new_scenario_pilots/<scenario>_<action>`
  (2026-10-08), whose own `config.json` each config here was built from. Their
  throughput is a sanity reference only, because the code has changed since then (see
  caveats).
- **The stranding itself:** `results/new_multiscenario/adaptive_20261010_175105`, node 4.

## What changes, what is held fixed

Base: each pilot's own `results/all_new_scenario_pilots/<arm>/config.json`. Keys are
ordered as in `run2new.json`, and keys the pilots lacked are added with their
`run2new.json` defaults.

| key | value | why |
|---|---|---|
| `checkpoint_fetch` | `{enabled: true, delay_ms: 200}` (`_off`: `enabled: false`) | the variable; 200 ms is the default, reasoned in config/checkpointfetch.go |
| `timer.relaxed_fixed_trigger_timeout_ms` | 350 for Healthy, ProposalDelay, Throttle | **reproduces the pilots.** They ran before the relaxed timer existed, so 350 ms applied in every scenario. Today these scenarios would use the relaxed value (300) instead. NetworkDelay and NetworkDelayFCrash keep 150 ms. |
| `timer.epoch_timer_ms` | 30000, repro arms only | ~60 generation switches in 1800 s, so ~5 strandings are expected per off arm |
| `scenarios`, `throttle` | Throttle scenario, `throttle.enabled: false` (from `config/pilots_n7/throttle_*`) | The throttle pilots ran standalone `throttle.enabled` under Healthy, which scenario mode now rejects |
| `client_num`, `oracle_decision_delay_ms`, `view_change_fetch_delay_ms`, `aware_candidates` | 1, 100, 20, run2new value | absent from the pilot configs; defaults written out |

Held fixed, as in the pilots:
- n=7, oracle pinned to one action, 75 s generations;
- ProposalDelay slow nodes {1,2}; NetworkDelayFCrash crashed nodes {3,5}; 170 ms netem;
- batch 50, `max_inflight_seq` 40, `inject_speed` 200, `carry_state` false, client retry off.

`dummy_account_count` is ignored by the code. The checkpoint state is the 26 accounts
the client uses, so a state reply is under 1 KB.

## Configs

| file | scenario | action | checkpoint_fetch | run |
|---|---|---|---|---|
| `ndelay_prr_ckptrepro_off.json` | NetworkDelay | PeriodicRoundRobin | off | 1800 s, 30 s gens |
| `ndelay_prr_ckptrepro_on.json` | NetworkDelay | PeriodicRoundRobin | on | 1800 s, 30 s gens |
| `ndelay_pe_ckptrepro_off.json` | NetworkDelay | PeriodicElection | off | 1800 s, 30 s gens |
| `ndelay_pe_ckptrepro_on.json` | NetworkDelay | PeriodicElection | on | 1800 s, 30 s gens |
| `<scenario>_<action>_ckpton.json` × 25 | healthy, pdelay, ndelay, ndfcrash, throttle | frr, prr, pe, pfrr, pfe | on | 600 s, 75 s gens |

## How to run

The batch needs the `checkpoint_fetch` code from branch `vc-compact-certs`, which is
not committed yet (see Provenance). Make sure the experiment host has it before
starting. Then run sequentially:

```bash
./run_experiments.sh 1800 config/ckpt_fetch_n7/*_ckptrepro_*.json ; \
./run_experiments.sh 600  config/ckpt_fetch_n7/*_ckpton.json
```

- **Duration:** about 2.2 h for the 4 repro runs and 4.6 h for the 25 normal-case runs,
  including start and stop: **about 7 h** in all.
- **Results:** `results/<config name>/`.
- **Host:** the network-delay scenarios need `sudo -n scripts/netem_scenario.sh` on
  node 4's side, as for the pilots.

## How to analyse

```bash
# fetches, installs, stale replies, ViewChanges above 300 certs, per run
python3 scripts/ckpt_fetch_summary.py --details results/*_ckptrepro_* results/*_ckpton

# throughput per generation, against the pilots (same script, same reference node 4)
python3 scripts/scenario_pilot_summary.py results/*_ckpton
python3 scripts/scenario_pilot_summary.py results/all_new_scenario_pilots/{healthy,pdelay,ndelay,ndfcrash}_{frr,prr,pe,pfrr,pfe}
python3 scripts/scenario_pilot_summary.py results/*_ckptrepro_*
```

What to read:
- **Repro off:** `>300` around 5 per run, all at node 4. `sent` = 0.
- **Repro on:** `>300` = 0. `inst` around the off arm's `>300`, all at node 4. In
  `--details`, each install should come shortly after a generation switch, with
  `last_executed` jumping to the checkpoint and then continuing.
- **Normal arms:** `sent` = `stale` = 0 everywhere. A few `inst` at node 4 in
  `ndelay_prr` and `ndelay_pe` are genuine strandings (~0.7 expected in 7 switches). In
  any arm with `sent` > 0 and `inst` = 0, or with `stale` > 0, look at the
  `CKPT FETCH sent` line: its `view` and `last_executed` show where the stall was.
- **Throughput:** on vs off in the repro; normal arms close to the pilots, within the
  caveats.

## Expected outcome / caveats

- **Expected:** the repro fixes the stranding (no 500-cert ViewChanges), and the
  normal arms show no fetches, since a node that is only behind keeps executing and
  never sees a 200 ms stall.
- **Most likely false positive:** the ProposalDelay leader. Its event loop sleeps 100 ms
  per proposal, so it could miss executions for 200 ms while a certificate is above it.
- **Code changes since the pilots** (VC compaction Phases 1a–3, NewView by digest,
  future-view buffering, the relaxed timer): throughput differences against the pilots
  are not attributable to `checkpoint_fetch` alone. If a normal arm shows fetches *and*
  a throughput change, rerun it with `enabled: false` for a same-code comparison.
- **Throttle arms** now use the Throttle scenario, not the pilots' standalone throttle.
  Compare them with the Throttle generations of the multi-scenario run rather than with
  the throttle pilots.
- **600 s normal arms** have only ~7 generation switches; the stranding statistics come
  from the repro arms.
- **30 s generations in the repro** change nothing in the mechanism: a stranding needs a
  checkpoint boundary inside node 4's ~170 ms head start, independent of epoch length.
  RoundRobin does only visit nodes 1–3 per generation, which doesn't matter here.
- **Not a WAN test:** netem puts 170 ms between every pair. The two-cluster asymmetry
  (C2 always lagging by D) is not modelled.

## Results (2026-10-15)

All 29 runs are in `results/ckpt_fetch_n7/`.

- **Repro:** with the feature off, node 4 was stranded 8 times as a replica in the
  PeriodicElection arm (500-cert ViewChanges) and 6 times in the PeriodicRoundRobin arm,
  out of 59 switches each. One more, in PeriodicElection generation 8, stranded node 4
  as the **elected primary**: no checkpoint stabilized anywhere for its 10 s tenure, and
  that generation's throughput fell to 2,296 against a median of ~3,530 tx/s. With the
  feature on there were no ViewChanges above 289 certs, and 17/17 installs were real
  strandings: every other replica had stabilized the checkpoint before installing the
  view. Each was repaired 0.55–0.74 s after the NewView.
- **Normal arms:** zero fetches in Healthy, NetworkDelay, NetworkDelayFCrash and Throttle.
  ProposalDelay had 8 installs, all by the slow leader (node 1 or 2), and none of them a
  stranding. Its event loop sleeps 100 ms per proposal, so it fell behind its own
  proposals. Accepted as cheap: a certified 26-account state, ~3 ms.
- **Every repro install had `attempts=2`.** The retry fired after 200 ms, shorter than
  the 340 ms netem round trip, so each stranding sent a second request and drew one
  stale reply. Fixed afterwards: the retry now waits 5 × `delay_ms` (1 s).
- **Throughput vs pilots:** +0–3% in most arms, which is not attributable to the
  feature because the code has changed since. Outliers are pdelay_pe (+13.8%, one fetch)
  and throttle_pfe (+38.6%: the pilot was the known trap run, and 5,402 matches the
  30-min rerun's 5,351).

## Provenance

- 2026-10-15, branch `vc-compact-certs` at `2da2c4b`, plus the uncommitted
  `checkpoint_fetch` change: config/checkpointfetch.go, node/checkpointfetch.go, the
  proto, and `checkpoint_fetch` in run2new.json.
- Request: "implement checkpoint fetch with an enable and a delay config; test
  sequentially on more than NetworkDelay + PRR; check that the n=7 baselines in other
  scenarios do not start calling state transfers in normal cases."
