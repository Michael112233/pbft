"""Simulate QuadRF (single objective: throughput) on synthetic state and reward taken
from the n=7 scenario x action pilots.

Independent of simulate_quadrf.py (n=4, hand-set numbers, still what server.py serves)
and of simulate_quadrf_multiobj.py (n=4, latency objective, FarNode/FixedAware).

Source of every number: results/all_new_scenario_pilots/<scenario>_<action>, run with
config/pilots_n7/*.json (n=7, f=2, batch 50, client 200 tx per 24 ms, client retry
off, epoch 75 s, oracle pinned to one action, 600 s), summarized per generation by
    python3 scripts/scenario_pilot_summary.py results/all_new_scenario_pilots/*
with node 4 as the reference node. Generation 1 (startup) is dropped, leaving 6-7
generations per arm; GENERATIONS keeps every one of them as (throughput, vc_rate).

  throughput  client tx/s over the generation, the per-epoch reward.
  vc_rate     view changes started per second on node 4 in that generation
              (generation switch included).

Pilot conditions that differ from the planned multi-scenario run:
  - fixed_trigger_timeout_ms 350 in Healthy, ProposalDelay and Throttle; 150 in
    NetworkDelay and NetworkDelayFCrash (the planned run uses 150 everywhere).
  - Throttle pilots ran scenarios ["Healthy"] with throttle.enabled (prep_ms 3000,
    2 slots, 100 ms gate), not the Throttle scenario.
  - ProposalDelay delays nodes {1, 2}; NetworkDelayFCrash crashes {3, 5}.

Throttle PerformanceElection comes from a 30-min run of the same configuration
(config/pilots_n7/throttle_pfe_30min.json, scenario_mode off with the standalone
throttle), 23 generations at 4 917-5 587 tx/s, not from the 10-min pilot. The pilot ran
on a slow stretch of the machine and fell into a tick-1 eviction cascade in generations
2-5 (~3 600 tx/s); in 30 min 92% of fresh leaders were evicted at tick 4 as the
throttle-prep-sweep 4.2 model predicts, and none cascaded.

State (measured while the previous action ran):
  [vc_rate, proposal_interval, inactive_nodes]   (server.LEARNING_DATA_STATE_KEYS)
  proposal_interval = 50000 / throughput ms (batch 50), capped at 1000 ms for arms
  that commit nothing (same cap as the other simulators).
  inactive_nodes = 2 in NetworkDelayFCrash, else 0.

Run from the repo root:
    python -m learningagent.simulate_quadrf_n7 --overlap     # state overlap per prev action
    python -m learningagent.simulate_quadrf_n7 --quiet       # QuadRF over the rotation
"""

from __future__ import annotations

import argparse
from collections import Counter
from itertools import combinations
from time import time

import numpy as np

from learningagent.protocols import LearningData, ProtocolName
from learningagent.scenario_sync import Scenario

FRR = ProtocolName.FixedRoundRobin
PRR = ProtocolName.PeriodicRoundRobin
PE = ProtocolName.PeriodicElection
PFRR = ProtocolName.PerformanceRoundRobin
PFE = ProtocolName.PerformanceElection
ACTIONS = [FRR, PRR, PE, PFRR, PFE]

# The same enum the live agent gets from scenario_sync, so server.py can call
# generate_state / generate_reward with the scenario it derives for a sequence.
HEALTHY = Scenario.Healthy
PDELAY = Scenario.ProposalDelay
NDELAY = Scenario.NetworkDelay
NDFCRASH = Scenario.NetworkDelayFCrash
THROTTLE = Scenario.Throttle
SCENARIOS = [HEALTHY, PDELAY, NDELAY, NDFCRASH, THROTTLE]

# Seed of the generation sampler: server.py's QuadRF seed + 1, as main() uses here, so
# the live agent reproduces a simulated run draw for draw.
SYNTHETIC_RNG_SEED = 6

# (throughput tx/s, vc_rate /s) for every complete generation of each pilot.
GENERATIONS: dict[Scenario, dict[ProtocolName, list[tuple[float, float]]]] = {
    HEALTHY: {
        FRR: [(8137, 0.013), (8137, 0.027), (8139, 0.013), (8132, 0.013), (8140, 0.013), (8138, 0.013), (8064, 0.013)],
        PRR: [(7941, 0.107), (8044, 0.107), (7933, 0.107), (8039, 0.107), (7940, 0.107), (8035, 0.107), (7879, 0.107)],
        PE: [(7942, 0.107), (7936, 0.107), (7872, 0.107), (7930, 0.107), (7954, 0.107), (7949, 0.107), (7794, 0.107)],
        PFRR: [(7975, 0.093), (8012, 0.093), (7987, 0.106), (7980, 0.093), (7953, 0.093), (8004, 0.093), (7934, 0.106)],
        PFE: [(7922, 0.093), (7933, 0.093), (7907, 0.093), (7913, 0.093), (7958, 0.093), (7896, 0.093), (7862, 0.093)],
    },
    PDELAY: {  # nodes 1 and 2 sleep 100 ms before every proposal
        FRR: [(489, 0.013), (487, 0.013), (487, 0.013), (487, 0.013), (487, 0.013), (487, 0.013), (473, 0.013)],
        PRR: [(5546, 0.106), (5547, 0.106), (5555, 0.106), (5543, 0.106), (5543, 0.106), (5549, 0.106), (5527, 0.106)],
        PE: [(7603, 0.106), (4166, 0.106), (6200, 0.106), (6055, 0.106), (7170, 0.106), (5191, 0.106), (6782, 0.106)],
        PFRR: [(5386, 0.358), (6717, 0.226), (7343, 0.146), (7403, 0.146), (7400, 0.146), (7346, 0.146), (7187, 0.146)],
        PFE: [(7921, 0.093), (7444, 0.120), (6567, 0.240), (7796, 0.107), (7941, 0.093), (7420, 0.133), (7684, 0.107)],
    },
    NDELAY: {  # 170 ms on every node pair
        FRR: [(0, 3.022)] * 6,
        PRR: [(3544, 0.106), (3451, 0.093), (3521, 0.106), (3542, 0.106), (3569, 0.106), (3456, 0.093)],
        PE: [(3481, 0.093), (3488, 0.093), (3481, 0.093), (3477, 0.093), (3501, 0.093), (3471, 0.093)],
        PFRR: [(0, 3.022)] * 6,
        PFE: [(0, 1.789), (0, 1.776), (0, 1.803), (0, 1.803), (0, 1.789), (0, 1.736)],
    },
    NDFCRASH: {  # 170 ms on every node pair, nodes 3 and 5 dead
        FRR: [(0, 2.146), (0, 2.146), (0, 2.146), (0, 2.146), (0, 2.160), (0, 2.146)],
        PRR: [(2557, 0.105), (2533, 0.105), (2573, 0.105), (2585, 0.105), (2528, 0.105), (2604, 0.105)],
        PE: [(3372, 0.092), (3450, 0.092), (3450, 0.092), (3451, 0.092), (3463, 0.092), (3455, 0.092)],
        PFRR: [(0, 2.146)] * 6,
        PFE: [(0, 1.751), (0, 1.778), (0, 1.764), (0, 1.764), (0, 1.738), (0, 1.738)],
    },
    THROTTLE: {  # client gates the leader to 1 proposal / 100 ms after a 3 s prep
        FRR: [(847, 0.013), (497, 0.013), (497, 0.013), (497, 0.013), (494, 0.013), (493, 0.013), (491, 0.013)],
        PRR: [(489, 0.107), (491, 0.107), (489, 0.107), (489, 0.107), (491, 0.107), (486, 0.107), (483, 0.107)],
        PE: [(1997, 0.107), (1716, 0.107), (2019, 0.107), (2617, 0.107), (1416, 0.107), (2321, 0.107), (2572, 0.107)],
        PFRR: [(4791, 0.506), (5247, 0.533), (4899, 0.519), (4878, 0.519), (5105, 0.519), (4946, 0.519), (4780, 0.506)],
        # 30-min run (results/all_new_scenario_pilots/throttle_pfe_30min_20261009_173627),
        # 23 generations; the 10-min pilot's PFE was bimodal (see module docstring)
        PFE: [
            (5216, 0.333), (5102, 0.333), (5407, 0.320), (5425, 0.306), (5303, 0.320), (5560, 0.306),
            (5418, 0.293), (5440, 0.320), (5392, 0.306), (5400, 0.320), (5284, 0.306), (5147, 0.333),
            (5414, 0.306), (5246, 0.320), (4917, 0.359), (5178, 0.320), (5404, 0.320), (5343, 0.306),
            (5393, 0.293), (5093, 0.346), (5381, 0.306), (5359, 0.320), (5587, 0.293),
        ],
    },
}

INACTIVE_NODES = {NDFCRASH: 2.0}
BATCH_SIZE = 50
MAX_PROPOSAL_INTERVAL_MS = 1000.0
STATE_KEYS = ("vc_rate", "proposal_interval", "inactive_nodes")

# Extra multiplicative Gaussian noise on top of resampling a measured generation.
THROUGHPUT_NOISE = 0.01
STATE_NOISE = 0.0

ROTATING_SCENARIOS = [
    HEALTHY, PDELAY, NDELAY, NDFCRASH, THROTTLE,
    PDELAY, THROTTLE, HEALTHY, NDFCRASH, NDELAY,
    THROTTLE, HEALTHY, PDELAY, NDELAY, HEALTHY,
]
SCENARIO_SPAN = 100
INIT_PROTOCOL = FRR
# arms whose mean throughput is within this fraction of the best arm count as optimal
OPTIMAL_TOLERANCE = 0.03


def mean_throughput(scenario: Scenario, action: ProtocolName) -> float:
    return float(np.mean([t for t, _ in GENERATIONS[scenario][action]]))


def optimal_actions(scenario: Scenario, tolerance: float) -> set[ProtocolName]:
    means = {a: mean_throughput(scenario, a) for a in ACTIONS}
    best = max(means.values())
    return {a for a, t in means.items() if t >= (1 - tolerance) * best}


def interval_ms(throughput: float) -> float:
    return MAX_PROPOSAL_INTERVAL_MS if throughput <= 0 else min(MAX_PROPOSAL_INTERVAL_MS, 1000.0 * BATCH_SIZE / throughput)


def noisy(value: float, sigma: float, rng: np.random.Generator) -> float:
    return max(0.0, value * (1.0 + sigma * rng.standard_normal())) if sigma > 0 else value


def state_vector(throughput: float, vc_rate: float, scenario: Scenario, drop_inactive: bool) -> np.ndarray:
    s = [vc_rate, interval_ms(throughput)]
    if not drop_inactive:
        s.append(INACTIVE_NODES.get(scenario, 0.0))
    return np.asarray(s, dtype=np.float64)


def generate_state(scenario: Scenario, prev_action: ProtocolName, rng: np.random.Generator, drop_inactive: bool = False) -> np.ndarray:
    throughput, vc_rate = GENERATIONS[scenario][prev_action][rng.integers(len(GENERATIONS[scenario][prev_action]))]
    return state_vector(noisy(throughput, STATE_NOISE, rng), noisy(vc_rate, STATE_NOISE, rng), scenario, drop_inactive)


def generate_reward(action: ProtocolName, scenario: Scenario, rng: np.random.Generator) -> float:
    throughput, _ = GENERATIONS[scenario][action][rng.integers(len(GENERATIONS[scenario][action]))]
    return noisy(throughput, THROUGHPUT_NOISE, rng)


def overlap_report(tolerance: float, drop_inactive: bool, pad: float) -> None:
    """Per previous action: each scenario's measured state range, and every scenario
    pair whose ranges, each widened by `pad` (relative, since 6-7 generations per arm
    understate the spread), intersect on all features. A pair is a CONFLICT when no
    arm is optimal in both (the model cannot be right in both from that state),
    benign when they share an optimal arm."""
    optimal = {s: optimal_actions(s, tolerance) for s in SCENARIOS}
    keys = STATE_KEYS[:2] if drop_inactive else STATE_KEYS
    print("optimal (within {:.0%} of the best mean throughput):".format(tolerance))
    for s in SCENARIOS:
        means = ", ".join(f"{a.value}={mean_throughput(s, a):.0f}" for a in ACTIONS)
        print(f"  {s:19s} {sorted(a.value for a in optimal[s])}   [{means}]")
    for prev in ACTIONS:
        print(f"\nprev = {prev.value}")
        boxes = {}
        for s in SCENARIOS:
            X = np.array([state_vector(t, v, s, drop_inactive) for t, v in GENERATIONS[s][prev]])
            boxes[s] = (X.min(0), X.max(0))
            ranges = "  ".join(f"{k} {lo:8.3f}..{hi:<8.3f}" for k, lo, hi in zip(keys, *boxes[s]))
            print(f"  {s:19s} {ranges}")
        for a, b in combinations(SCENARIOS, 2):
            (lo_a, hi_a), (lo_b, hi_b) = [(lo * (1 - pad), hi * (1 + pad)) for lo, hi in (boxes[a], boxes[b])]
            if np.all((lo_a <= hi_b) & (lo_b <= hi_a)):
                shared = optimal[a] & optimal[b]
                kind = f"benign, both fine with {sorted(x.value for x in shared)}" if shared else "CONFLICT, no arm optimal in both"
                print(f"  ** overlap {a} / {b}: {kind}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--steps", type=int, default=None, help="default: one span per scenario in the order")
    parser.add_argument("--span", type=int, default=SCENARIO_SPAN, help="epochs per scenario")
    parser.add_argument("--seed", type=int, default=5)
    parser.add_argument("--tolerance", type=float, default=OPTIMAL_TOLERANCE)
    parser.add_argument("--drop-inactive", action="store_true", help="state without inactive_nodes")
    parser.add_argument("--overlap", action="store_true", help="print the state overlap report and exit")
    parser.add_argument("--pad", type=float, default=0.05, help="relative widening of each range in --overlap")
    parser.add_argument("--order", default=None,
                        help="comma-separated scenario sequence, e.g. pdelay,ndelay,ndfcrash,throttle,healthy "
                             "(healthy, pdelay, ndelay, ndfcrash, throttle); default ROTATING_SCENARIOS")
    parser.add_argument("--quiet", action="store_true", help="no per-step lines")
    args = parser.parse_args()

    rotation = ROTATING_SCENARIOS
    if args.order:
        short = {"healthy": HEALTHY, "pdelay": PDELAY, "ndelay": NDELAY, "ndfcrash": NDFCRASH, "throttle": THROTTLE}
        rotation = [short[s.strip().lower()] for s in args.order.split(",")]
    steps = args.steps if args.steps is not None else len(rotation) * args.span

    if args.overlap:
        overlap_report(args.tolerance, args.drop_inactive, args.pad)
        return

    # Imported here so --overlap runs without scikit-learn / the server module.
    from learningagent.server import QuadRF

    rng = np.random.default_rng(args.seed + 1)
    model = QuadRF(seed=args.seed)
    optimal = {s: optimal_actions(s, args.tolerance) for s in SCENARIOS}
    print("optimal action(s) per scenario:")
    for s, acts in optimal.items():
        print(f"  {s:19s} {sorted(a.value for a in acts)}")

    prev = INIT_PROTOCOL
    blocks: list[dict] = []
    start = time()
    for step in range(1, steps + 1):
        scenario = rotation[((step - 1) // args.span) % len(rotation)]
        if (step - 1) % args.span == 0:
            blocks.append({"scenario": scenario, "choices": [], "rewards": []})
            if not args.quiet:
                print(f"\nScenario {scenario} from step {step}")
        state = generate_state(scenario, prev, rng, args.drop_inactive)
        action = ProtocolName(model.predict(state, prev))
        reward = generate_reward(action, scenario, rng)
        model.record_state_action_reward(
            LearningData(sequence_id=step, current_protocol=action, reward=reward, state=state),
            prev,
        )
        blocks[-1]["choices"].append(action)
        blocks[-1]["rewards"].append(reward)
        if not args.quiet:
            mark = "*" if action in optimal[scenario] else " "
            print(f"step={step:04d} {scenario:19s} prev={prev.value:22s} -> {action.value:22s}{mark} T={reward:8.1f}")
        prev = action

    print(f"\nSummary ({time() - start:.0f}s, seed={args.seed}, tolerance={args.tolerance}, "
          f"drop_inactive={args.drop_inactive}); optimal% = epochs on an optimal arm, "
          f"regret% = 1 - mean throughput / best arm's mean")
    print(f"{'block':>5s} {'scenario':19s} {'optimal%':>8s} {'2nd half%':>9s} {'first 5-in-a-row':>16s} "
          f"{'regret%':>7s}  choices (2nd half)")
    for i, b in enumerate(blocks):
        choices, opt = b["choices"], optimal[b["scenario"]]
        hits = [c in opt for c in choices]
        half = len(choices) // 2
        run, conv = 0, None
        for j, h in enumerate(hits):
            run = run + 1 if h else 0
            if run == 5:
                conv = j - 4
                break
        best = max(mean_throughput(b["scenario"], a) for a in ACTIONS)
        regret = 100 * (1 - np.mean([mean_throughput(b["scenario"], c) for c in choices]) / best)
        counts = Counter(c.value for c in choices[half:])
        print(f"{i + 1:5d} {b['scenario']:19s} {100 * np.mean(hits):7.0f}% {100 * np.mean(hits[half:]):8.0f}% "
              f"{str(conv) if conv is not None else '-':>16s} {regret:6.1f}%  "
              + " ".join(f"{a}:{n}" for a, n in counts.most_common()))


if __name__ == "__main__":
    main()

# python -m learningagent.simulate_quadrf_n7 --overlap   (run from the repo root)
