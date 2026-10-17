# WAN timers: progress 200 ms, new-view 150 ms on the emulated 5+2 WAN

On the emulated WAN (nodes 1–5 and the client in a near cluster, nodes 6 and 7 in a far cluster,
**d = 24 ms one way** between them, LAN inside each) the Fixed/Perf **leader-progress timeout is
200 ms** and the **new-view timeout is 150 ms**. Same rule as on the LAN
([`lan-timer-values.md`](lan-timer-values.md)): 1.5 × the p99.9 need of the Healthy runs, rounded
up to a multiple of 50 ms.

| Timer | Worst need (Healthy, p99.9 = max) | × 1.5 | Value | LAN value |
|---|---|---|---|---|
| leader progress | 129.6 ms (far replica, far leader) | 194 ms | **200 ms** | 150 ms |
| new-view | 76.6 ms (near replica, far leader) | 115 ms | **150 ms** | 50 ms |

With n = 7 the quorum is 5, so **the near cluster is a quorum on its own**. That one fact explains
every number below: a replica's wait grows only when the quorum it needs sits across the link from
where its timer started.

Data: `results/wan/*_wan_t1000` (configs and run notes in
[`config/timer_wan_n7/`](../config/timer_wan_n7/), hosts file
`scripts/cluster/hosts.cloudlab.wan_far67.json`, harness in [`multi-host.md`](multi-host.md),
"Emulated WAN"). Summaries: `scripts/timer_budget_summary.py`, which splits every row by
**leader site > replica site** from the run's `hosts.json`.

## The runs

- Five 10-min runs, timers at 1 s (nothing evicted, no need cut off): Healthy FRR, PFRR, PFE,
  ProposalDelay PFRR, Throttle PFE. The values come from the three Healthy runs.
- **The WAN was in place:** every host's `wan_check` passed in every run (near pairs ~0.18 ms RTT,
  near↔far 48.25–48.31 ms), netem dropped nothing, and the 24 ms leaves were still set at the end
  of each run (the scenario `down` at start restores the base instead of removing it).
- ViewChanges and NewViews crossed the link in one flight: median ~18–20 ms over all pairs, max
  ~50 ms (24 ms + transfer of up to ~400 KB).

## The model: count the crossings

Times from the moment the ViewChanges go out, with processing and the client's tick left out.
"Need" is what each timer waits: from its start to the install (new-view), and from the install to
the first execution (progress).

| Leader > replica | Timer starts | Installs | First execution | New-view need | Progress need |
|---|---|---|---|---|---|
| near > near | 0 (2f+1 near VCs) | 0 | 0 | 0 | 0 |
| near > far | d (near VCs arrive) | d (NewView arrives) | d (PrePrepare and near votes arrive) | **0** | **0** |
| far > near | 0 | 2d | 4d | **2d** | **2d** |
| far > far | d | d (NewView over the LAN) | 5d | **0** | **4d** |

- **Near leader:** everything the far replica waits for arrives d after the near nodes get it, and
  its timers also start d later, so the shift cancels: a far replica under a near leader sees LAN
  needs.
- **Far leader, near replica (2d):** the near nodes reach their quorum at 0 but the far primary only
  at d (it needs the near ViewChanges); its NewView arrives at 2d. After installing, the client
  learns the leader from the near nodes, its request crosses to the far leader (+d), and the
  PrePrepare crosses back (+d): 2d again.
- **Far leader, far replica (4d):** the far replica installs early (its neighbour's NewView comes
  over the LAN) but its first execution needs the near quorum: the near nodes install (+d), the
  client's request crosses (+d), the PrePrepare crosses (+d), and the near nodes' votes cross back
  (+d).

### Measured

| Leader > replica | Healthy views | New-view p50 / max | First execution p50 / max | Model |
|---|---|---|---|---|
| near > near | 376 | 8–9 / 29 | 29–36 / 85 | 0 / 0 |
| near > far | 188 | 9–13 / 53 | 28–33 / 67 | 0 / 0 |
| far > near | 195 | 59–62 / 77 | 62–70 / 84 | 2d / 2d |
| far > far | 39 | 6 (RoundRobin), 57 (Election) / 71 | 111–116 / 130 | 0 / 4d |

The 2d cases come out at 48 + 10–20 ms and the 4d case at 96 + 15–20 ms at the median. Under a near
leader the far replica's new-view tail (53 ms against the LAN's 30) is a transfer effect, not a d
term: a 300–400 KB NewView sometimes takes a little longer over the link than the ViewChanges that
started the replica's timer.

**Election, far > far:** the far voter's timer starts when it grants its vote, over the LAN, at
once; but the far candidate also needs the near votes, which cross out and back (2d) before the
NewView. So under Election that case needs 2d of new-view, not 0.

## Why the WAN need is not LAN processing + 4d

The LAN's worst progress need (81 ms) is mostly waiting, not processing: the client's tick (0–24 ms)
and the re-run of the NewView's carried-over O-set slots, which new slots must wait behind
(7 → 89 ms as the O-set grows from <50 to 150–249 slots). On the WAN that waiting runs **in
parallel with the crossings**: the near nodes start re-running the O-set the moment they install,
while the client handover and the PrePrepare are still crossing the link. So the O-set barely
matters on the WAN:

| O-set size | LAN: first request → first new slot (p50 / max) | WAN far > far: first execution (p50 / max) |
|---|---|---|
| 0–49 slots | 7 / 15 ms | 107 / 124 ms |
| 50–149 | 19 / 43 ms | 115 / 128 ms |
| 150–249 | 51 / 89 ms | 114 / 137 ms |

The far > far need is therefore **4d (96) + the client's tick wait (0–24) + a few ms of rounds +
whatever O-set re-run outlasts ~2d**: 107–137 ms here, not 81 + 96. Only the largest O-sets (re-run
50–90 ms) poke out past the crossings. The same overlap is why far > near comes out at 2d + 15–35 ms,
not 2d + 81.

Two related numbers: a far leader's handover (becoming leader → its first client request) took
66 ms at the median and 90 ms at most (107 views), against 20 / 39 ms for a near leader.

### The 137 ms view

The single largest far > far wait (137.2 ms) is view (1,6) of the ProposalDelay run: leader node 6,
replica node 7, a normal-speed leader, in generation 1. It is the chain above plus ~25 ms: node 6's
first big NewView to the near nodes (301 KB) took 55–57 ms, against 25–35 ms for every later far
NewView, even 415 KB ones. That is the first-big-message window effect seen on the LAN
([`multi-host.md`](multi-host.md)): a connection's receive window grows only as traffic needs it, and
node 6's links to the near nodes had carried only small messages until then. It happens once per
link, at startup. The timer summaries include startup views on purpose; the timer has to survive
them too.

## The values

- **Progress 200 ms:** 1.5 × 129.6 = 194 → 200. It covers the startup outlier (137 ms) by 63 ms,
  and the worst steady-state combination (96 + 24 tick + ~35 processing ≈ 155 ms) by ~45 ms. Applied
  strictly to all five runs (137 ms) the rule gives 250 ms.
- **New-view 150 ms:** 1.5 × 76.6 = 115 → 150. Including the ProposalDelay run's worst view (97 ms)
  still gives 150.
- **Why the far > far case must be covered:** if only the two far replicas' timers fire, they send 2
  ViewChanges, fewer than the f + 1 = 3 that pull anyone along, and the 5 near nodes go on without
  them. The system doesn't stall, but the far pair stops executing until the next view change (~11 s
  under the Perf actions).
- **Margin on processing only:** the netem delay is constant, so scaling it by 1.5 adds margin for
  nothing variable. Scaling only the processing gives progress 150 ms (only ~20 ms over the far > far
  worst case) and new-view 100 ms (23 ms headroom). Not recommended here: the far > far sample is
  small (39 Healthy views) and a real WAN has jitter.
- **Other d:** the same counting gives progress ≈ LAN-like processing + 4d and new-view ≈ LAN
  new-view need + 2d, with the processing parts overlapping the crossings as above. Re-measure rather
  than extrapolate far from 24 ms.

## ProposalDelay and Throttle still work

Both space a slow leader's proposals at most one per 100 ms and let a tenure's first proposal go at
once (ProposalDelay since `e0187d1`, non-blocking). They bound only the gap between executions:

| | First execution (max) | Gap (max) | Under 200 ms by |
|---|---|---|---|
| ProposalDelay (slow nodes 1, 2, near) | 85 ms (Healthy range) | 103 ms | ≥ 97 ms |
| Throttle | 128 ms (far > far, a normal handover) | 130 ms | ≥ 70 ms |

So a slow leader passes Fixed and only the Perf bar removes it, as on the LAN; the interval or gate
could go up to ~145 ms before the margin gets thin. With the old blocking sleep, the ProposalDelay
leader's first execution was sleep + handover (130 ms at a 100 ms sleep on the LAN); now it is a
normal one.

## NetworkDelay still cascades

NetworkDelay adds X (`scenario_network_delay_ms`) to every node pair on top of the base: near↔near
and far↔far get X, near↔far d + X. Counting only the prepare and commit rounds (the client handover
would add more), each replica's first execution after its install is:

| Leader > replica | First execution − install |
|---|---|
| near > near, near > far, far > near | **2X** (the extra d shifts the install and the votes equally) |
| far > far | **2X + 2d** (installs over the far↔far link; the near quorum's install and votes cross twice) |

At X = 170 that is ≥ 340 ms (388 for far > far), well over 200 ms, so Fixed and Perf cascade for
every leader placement. A NewView needs at least one X-hop, 170 ms > 150 ms, so the new-view timer
expires too. The smallest X that cascades with margin is ~125 ms (2X must exceed 200). This is from
the model; no NetworkDelay run on the WAN has measured it yet.

## Election caveats

- **The VDF race is outside the new-view timer.** Election arms the timer on the candidate when its
  VDF finishes and on every other node when it grants its vote, not at the 2f+1 ViewChanges. Measured
  from the quorum instead, an Election view change with a far candidate takes up to 194 ms (VDF plus
  a vote round across the link); a timer started at the quorum would need ~300 ms here.
- **A far candidate costs 2d of new-view on every replica.** The vote round crosses the link for the
  near voters (RequestVote out, grants back) and for the far voter (the candidate needs the near
  grants). The candidate is drawn uniformly over all 7 nodes (`electionCandidateForView`), so about 2
  views in 7 have a far candidate.
- **Liveness gap.** If the designated candidate never sends RequestVote (crashed, partitioned,
  silent), no node arms a new-view timer and the progress timer was already stopped, so the view change
  waits forever. Only the experiment scaffolding prevents it: crashed nodes are never candidates. On a
  WAN a partitioned far candidate is a realistic way to hit it.

## Caveats

- One 10-min run per arm, one cluster, one d (24 ms), constant delay (no jitter, no bandwidth limit),
  client near. A far client (`hosts.cloudlab.wan_far67_2c.json`) changes the handover: requests from
  it reach a near leader across the link.
- Far > far is the binding case and the smallest sample (39 Healthy views, 114 in all runs).
- The validation batch (all scenarios × actions at 200 / 150 on the WAN) has not run yet.

## Reproduce

```bash
cat results/wan/healthy_frr_wan_t1000/wan_check_*.txt
python3 scripts/timer_budget_summary.py results/wan/healthy_{frr,pfrr,pfe}_wan_t1000
python3 scripts/timer_budget_summary.py results/wan/{pdelay_pfrr,throttle_pfe}_wan_t1000
python3 scripts/vc_delivery_summary.py results/wan/*_wan_t1000
```
