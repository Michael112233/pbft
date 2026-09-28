"""Simulate QuadRFMultiObjective (throughput constraint, latency objective) on
synthetic state and reward taken from the 30 scenario x action latency pilots.

Independent of simulate_quadrf.py: it has its own scenarios (adds FarNode), its own
six actions (adds FixedAware) and its own generate_state / generate_reward.

Source of every number: results/<scenario>_<action>_20260927_* / 20260928_*, run
with config/latency_pilots/*.json (n=4, batch 50, client 200 tx per 24 ms, client
retry off, epoch 45 s), summarized by scripts/latency_pilot_summary.py into
results/latency_pilots_summary.json.

  throughput  client tx/s over epoch 2 (one full generation, the per-epoch reward
              unit). NetworkDelay/FCrash cascading arms committed nothing in epoch 2.
  latency     client committed mean over the whole run, in ms. Committed-only: a
              request dropped at a view change is not in it. In overloaded runs
              (ND, FCrash, ProposalDelay with node 1 leading) it is queue backlog and
              grows with run length, so only its ordering is meaningful. Arms with
              zero throughput (cascades) get CASCADE_LATENCY_MS instead.
  vc_rate     view changes started per second on node 3 in epoch 2.

Exceptions (FarNode, node 1 35 ms from every other node):
  FixedRoundRobin  modelled as node 1 leading the whole generation with probability
                   FRR_FAR_LEAD_PROB (default 1): throughput 8156.6 and latency 44.1
                   ms (p50) from epoch 1 of farnode_frr, which node 1 led. Otherwise
                   (node 1 misses the 150 ms new-view timer at the generation switch,
                   as in epoch 2 of the pilot) node 2 leads: 8102.2 tx/s, 10.6 ms.
  FixedAware       latency 11.2 ms: the whole-run mean with epoch 1 (led by node 1,
                   no candidates yet) removed at 44.1 ms. From generation 2 its
                   candidates are [2 3 4], so it never picks node 1.

State (measured while the previous action ran):
  [vc_rate, proposal_interval, inactive_nodes, rtt_spread]
  proposal_interval = 50000 / throughput ms (batch 50), capped at 1000 ms for the
  arms that commit nothing (same cap as simulate_quadrf.py).
  inactive_nodes = 1 in NetworkDelayFCrash, else 0 (not observable in the logs).
  rtt_spread = worst minus best Aware predicted leader latency (AWARE log lines):
  70.0 ms in FarNode (70.6 vs 0.6), ~0 elsewhere. The prober runs under every action,
  so it does not depend on the previous action.

Run from the repo root:
    python -m learningagent.simulate_quadrf_multiobj [--steps 1000] [--delta 0.05] ...
"""

from __future__ import annotations

import argparse
from collections import Counter
from time import time

import numpy as np

from learningagent.protocols import MultiObjectiveData
from learningagent.server import QuadRFMultiObjective

FRR, PRR, PE, PFRR, PFE, FA = (
    "FixedRoundRobin",
    "PeriodicRoundRobin",
    "PeriodicElection",
    "PerformanceRoundRobin",
    "PerformanceElection",
    "FixedAware",
)
ACTIONS = [FRR, PRR, PE, PFRR, PFE, FA]

HEALTHY, FARNODE, PDELAY, NDELAY, NDFCRASH = (
    "Healthy",
    "FarNode",
    "ProposalDelay",
    "NetworkDelay",
    "NetworkDelayFCrash",
)

# (throughput tx/s, latency ms, vc_rate /s) per scenario and action, from the pilots.
PILOT: dict[str, dict[str, tuple[float, float, float]]] = {
    HEALTHY: {
        FRR: (8142.6, 10.6, 0.022),
        PRR: (8092.6, 11.5, 0.111),
        PE: (8051.5, 10.6, 0.111),
        PFRR: (8098.9, 10.6, 0.111),
        PFE: (8064.8, 10.5, 0.111),
        FA: (8133.7, 10.5, 0.022),
    },
    FARNODE: {
        FRR: (8156.6, 44.1, 0.022),  # node 1 leads; see FRR_FAR_* below for the other case
        PRR: (8071.0, 24.0, 0.111),
        PE: (7980.4, 28.1, 0.111),
        PFRR: (8076.8, 24.9, 0.133),
        PFE: (7970.4, 28.0, 0.111),
        FA: (8133.1, 11.2, 0.022),
    },
    PDELAY: {  # node 1 adds 100 ms before every proposal
        FRR: (489.1, 46919.6, 0.022),
        PRR: (5524.2, 253.0, 0.110),
        PE: (2933.3, 619.6, 0.111),
        PFRR: (7627.2, 12.9, 0.155),
        PFE: (7051.6, 16.9, 0.177),
        FA: (487.0, 46295.5, 0.044),  # node 1 led both generations (starved (g,1) leader)
    },
    NDELAY: {  # 170 ms on every node pair
        FRR: (0.0, 646.9, 3.103),
        PRR: (3608.9, 3180.1, 0.110),
        PE: (3479.1, 5139.9, 0.110),
        PFRR: (0.0, 655.1, 3.103),
        PFE: (0.0, 643.1, 1.849),
        FA: (0.0, 641.9, 3.081),
    },
    NDFCRASH: {  # 170 ms on every node pair, node 2 dead
        FRR: (0.0, 845.4, 2.198),
        PRR: (2792.6, 3439.6, 0.109),
        PE: (3408.5, 5185.1, 0.109),
        PFRR: (0.0, 795.3, 2.198),
        PFE: (0.0, 872.7, 1.807),
        FA: (0.0, 849.6, 2.002),
    },
}
# A cascading arm commits nothing, so its latency is unbounded. The pilot latency above
# is only the first few batches committed before the cascade started (450-1250
# requests, ~650-870 ms), which would rank it better than PRR/PE. Replace it with a
# constant above every committed latency in PILOT (max 46919.6 ms, ProposalDelay FRR).
CASCADE_LATENCY_MS = 60000.0
PILOT = {
    s: {a: (t, CASCADE_LATENCY_MS if t <= 0 else l, v) for a, (t, l, v) in arms.items()}
    for s, arms in PILOT.items()
}
assert all(l <= CASCADE_LATENCY_MS for arms in PILOT.values() for _, l, _ in arms.values())

# FarNode FixedRR when node 1 misses the new-view timer and node 2 leads the generation
FRR_FAR_MISS = (8102.2, 10.6, 0.044)
FRR_FAR_LEAD_PROB = 1.0
# defaul 1 used where we assume that node 1 is able to lead the next gen without timing out

INACTIVE_NODES = {NDFCRASH: 1.0}
RTT_SPREAD = {FARNODE: 70.0}
BATCH_SIZE = 50
MAX_PROPOSAL_INTERVAL_MS = 1000.0

# Multiplicative Gaussian noise per epoch (0 = the pilot value every time).
THROUGHPUT_NOISE = 0.02
LATENCY_NOISE = 0.10
STATE_NOISE = 0.0

ROTATING_SCENARIOS = [HEALTHY, FARNODE, PDELAY, NDELAY, NDFCRASH, FARNODE, PDELAY, HEALTHY, NDFCRASH, NDELAY]
SCENARIO_SPAN = 100
INIT_PROTOCOL = FRR
# arms whose mean latency is within this fraction of the best feasible arm count as
# optimal when scoring (Healthy is a near-tie)
OPTIMAL_LATENCY_TOLERANCE = 0.05


def pilot_values(scenario: str, action: str, rng: np.random.Generator, far_lead_prob: float) -> tuple[float, float, float]:
    if scenario == FARNODE and action == FRR and rng.random() >= far_lead_prob:
        return FRR_FAR_MISS
    return PILOT[scenario][action]


def noisy(value: float, sigma: float, rng: np.random.Generator) -> float:
    return max(0.0, value * (1.0 + sigma * rng.standard_normal())) if sigma > 0 else value


def generate_state(scenario: str, prev_action: str, rng: np.random.Generator, far_lead_prob: float) -> np.ndarray:
    throughput, _, vc_rate = pilot_values(scenario, prev_action, rng, far_lead_prob)
    interval = MAX_PROPOSAL_INTERVAL_MS if throughput <= 0 else min(MAX_PROPOSAL_INTERVAL_MS, 1000.0 * BATCH_SIZE / throughput)
    state = np.asarray(
        [
            noisy(vc_rate, STATE_NOISE, rng),
            noisy(interval, STATE_NOISE, rng),
            INACTIVE_NODES.get(scenario, 0.0),
            RTT_SPREAD.get(scenario, 0.0),
        ],
        dtype=np.float64,
    )
    return state


def generate_reward(action: str, scenario: str, rng: np.random.Generator, far_lead_prob: float) -> tuple[float, float]:
    throughput, latency, _ = pilot_values(scenario, action, rng, far_lead_prob)
    return noisy(throughput, THROUGHPUT_NOISE, rng), noisy(latency, LATENCY_NOISE, rng)


def mean_values(scenario: str, action: str, far_lead_prob: float) -> tuple[float, float]:
    t, l, _ = PILOT[scenario][action]
    if scenario == FARNODE and action == FRR:
        t = far_lead_prob * t + (1 - far_lead_prob) * FRR_FAR_MISS[0]
        l = far_lead_prob * l + (1 - far_lead_prob) * FRR_FAR_MISS[1]
    return t, l


def optimal_actions(scenario: str, delta: float, far_lead_prob: float) -> set[str]:
    """The rule applied to the noiseless means: feasible by throughput, then the
    lowest latency within OPTIMAL_LATENCY_TOLERANCE."""
    means = {a: mean_values(scenario, a, far_lead_prob) for a in ACTIONS}
    bar = (1 - delta) * max(t for t, _ in means.values())
    feasible = [a for a, (t, _) in means.items() if t >= bar]
    best = min(means[a][1] for a in feasible)
    return {a for a in feasible if means[a][1] <= best * (1 + OPTIMAL_LATENCY_TOLERANCE)}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--steps", type=int, default=len(ROTATING_SCENARIOS) * SCENARIO_SPAN)
    parser.add_argument("--span", type=int, default=SCENARIO_SPAN, help="epochs per scenario")
    parser.add_argument("--delta", type=float, default=0.05, help="throughput tolerance")
    parser.add_argument("--seed", type=int, default=5)
    parser.add_argument("--trees", type=int, default=100, help="n_estimators per forest")
    parser.add_argument("--frr-far-lead-prob", type=float, default=FRR_FAR_LEAD_PROB)
    parser.add_argument("--quiet", action="store_true", help="no per-step lines")
    args = parser.parse_args()

    rng = np.random.default_rng(args.seed + 1)
    model = QuadRFMultiObjective(ACTIONS, delta=args.delta, seed=args.seed, n_estimators=args.trees)
    optimal = {s: optimal_actions(s, args.delta, args.frr_far_lead_prob) for s in PILOT}
    print("optimal action(s) per scenario under the rule:")
    for s, acts in optimal.items():
        print(f"  {s:20s} {sorted(acts)}")

    prev = INIT_PROTOCOL
    blocks: list[dict] = []
    start = time()
    for step in range(1, args.steps + 1):
        scenario = ROTATING_SCENARIOS[((step - 1) // args.span) % len(ROTATING_SCENARIOS)]
        if not blocks or blocks[-1]["scenario"] != scenario or (step - 1) % args.span == 0:
            blocks.append({"scenario": scenario, "start": step, "choices": [], "rewards": []})
            if not args.quiet:
                print(f"\nScenario {scenario} from step {step}")
        state = generate_state(scenario, prev, rng, args.frr_far_lead_prob)
        action = model.predict(state, prev)
        throughput, latency = generate_reward(action, scenario, rng, args.frr_far_lead_prob)
        model.record_state_action_reward(
            MultiObjectiveData(sequence_id=step, current_protocol=action, throughput=throughput, latency=latency, state=state),
            prev,
        )
        blocks[-1]["choices"].append(action)
        blocks[-1]["rewards"].append((throughput, latency))
        if not args.quiet:
            mark = "*" if action in optimal[scenario] else " "
            print(f"step={step:04d} {scenario:18s} prev={prev:22s} -> {action:22s}{mark} T={throughput:8.1f} L={latency:10.1f}")
        prev = action

    print(f"\nSummary ({time() - start:.0f}s, delta={args.delta}, seed={args.seed}, trees={args.trees}, "
          f"frr_far_lead_prob={args.frr_far_lead_prob}); '*' = optimal under the rule")
    print(f"{'block':>5s} {'scenario':20s} {'optimal%':>8s} {'2nd half%':>9s} {'first 5-in-a-row':>16s} {'mean T':>8s} {'mean L':>10s}  choices (2nd half)")
    for i, b in enumerate(blocks):
        choices, opt = b["choices"], optimal[b["scenario"]]
        hits = [c in opt for c in choices]
        half = len(choices) // 2
        run, conv = 0, None
        for j, h in enumerate(hits):
            run = run + 1 if h else 0
            if run == 5:
                conv = j - 3  # epochs into the block at which the 5-run started
                break
        ts = [t for t, _ in b["rewards"]]
        ls = [l for _, l in b["rewards"]]
        counts = Counter(choices[half:])
        print(f"{i + 1:5d} {b['scenario']:20s} {100 * np.mean(hits):7.0f}% {100 * np.mean(hits[half:]):8.0f}% "
              f"{str(conv) if conv is not None else '-':>16s} {np.mean(ts):8.0f} {np.mean(ls):10.1f}  "
              + " ".join(f"{a}:{n}" for a, n in counts.most_common()))


if __name__ == "__main__":
    main()

# python -m learningagent.simulate_quadrf_multiobj --quiet   (run from the repo root)
