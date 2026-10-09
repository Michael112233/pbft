"""Measure the learning agent's decision time on this host, to set oracle_decision_delay_ms.

The oracle stands in for the agent in baseline runs. With the epoch grid on
(timer.epoch_grid, docs/epoch-grid.md) the only gap left between an Adaptive run's
scenario switches and a baseline's is the decision time, agent vs oracle sleep.
This script measures the agent's side so the oracle can be set to match.

It replays exactly what run_decision_worker_quadrf (learningagent/server.py) does
per epoch, minus the gRPC hop: synthetic state for the scenario the node is in,
QuadRF.predict (with RESAMPLE_ON_PREDICT a fresh bootstrap fit of every tried arm,
then a predict per arm), synthetic reward, record. Experience accumulates over the
run, so decision time grows with the generation; the whole scenario schedule from
the config is replayed by default.

One process per node, like the real run (one agent per node on the same host), and
all of them start each generation together (a barrier), because every node's agent
is asked at the same moment at the epoch boundary.

Usage (from the repo root):
    python3 -m learningagent.bench_learner                              # config/run2new.json
    python3 -m learningagent.bench_learner --config config/<run>.json   # the run's own config
    python3 -m learningagent.bench_learner --gens 200 --procs 7         # shorter / explicit

Prints decision time per scenario window and overall, and the value to put in
oracle_decision_delay_ms. The agent runs alone here; in a real run the nodes share the
cores and decisions were ~1.2x slower on the 9 Oct 2026 host, which the suggested value
includes. scripts/epoch_delta.py measures the in-run value from a finished run.
"""

from __future__ import annotations

import argparse
import json
import logging
import multiprocessing as mp
import os
import statistics
import sys
import time

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, REPO_ROOT)

from learningagent.protocols import LearningData, ProtocolName  # noqa: E402
from learningagent.scenario_sync import load_scenario_schedule, scenario_for_sequence  # noqa: E402
from learningagent.server import QuadRF  # noqa: E402
from learningagent.simulate_quadrf import generate_reward, generate_state  # noqa: E402

AGENT_SEED = 5  # server.run_server builds QuadRF(seed=5)


def worker(rank, gens, scenarios, span, barrier, out):
    logging.disable(logging.CRITICAL)  # QuadRF.predict logs every decision
    cmab = QuadRF(seed=AGENT_SEED)
    selected = ProtocolName.FixedRoundRobin
    times = []
    for seq in range(1, gens + 1):
        scenario = scenario_for_sequence(seq, scenarios, span)
        barrier.wait()
        start = time.perf_counter()
        prev = selected
        state = generate_state(scenario, prev)
        selected = ProtocolName(cmab.predict(state, prev))
        reward = generate_reward(selected, scenario)
        cmab.record_state_action_reward(
            LearningData(sequence_id=seq, current_protocol=selected, reward=reward, state=state), prev
        )
        times.append(time.perf_counter() - start)
    out.put((rank, times))


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p / 100 * (len(xs) - 1))))]


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--config", default=os.path.join(REPO_ROOT, "config/run2new.json"))
    parser.add_argument("--procs", type=int, help="concurrent agents (default: node_num)")
    parser.add_argument("--gens", type=int, help="generations to replay (default: the whole scenario schedule)")
    parser.add_argument("--per-gen", action="store_true", help="also print every generation's median")
    args = parser.parse_args()

    with open(args.config) as f:
        cfg = json.load(f)
    scenarios, span = load_scenario_schedule(args.config)
    procs = args.procs or int(cfg.get("node_num", 4))
    gens = args.gens or (len(scenarios) * span if cfg.get("scenario_mode") else 100)

    print(f"config {args.config}: {procs} agents, {gens} generations, "
          f"scenarios {[s.value for s in scenarios]} every {span} generations")
    barrier = mp.Barrier(procs)
    out = mp.Queue()
    workers = [mp.Process(target=worker, args=(r, gens, scenarios, span, barrier, out)) for r in range(procs)]
    wall = time.perf_counter()
    for w in workers:
        w.start()
    results = dict(out.get() for _ in workers)
    for w in workers:
        w.join()
    wall = time.perf_counter() - wall

    # Per generation, the median over the agents: every node decides at once, and the
    # generation switch follows the nodes that decide first, not the slowest one.
    per_gen = [statistics.median(results[r][g] for r in results) * 1000 for g in range(gens)]
    if args.per_gen:
        print("\ngeneration  median ms")
        for g, ms in enumerate(per_gen, start=1):
            print(f"{g:10d} {ms:10.1f}")

    print(f"\n{'window':>12s} {'scenario':>22s} {'median ms':>10s} {'p10':>8s} {'p90':>8s} {'max':>8s}")
    for w in range(0, gens, span):
        xs = per_gen[w:w + span]
        scenario = scenario_for_sequence(w + 1, scenarios, span).value
        print(f"{f'{w + 1}-{w + len(xs)}':>12s} {scenario:>22s} {statistics.median(xs):10.1f} "
              f"{pct(xs, 10):8.1f} {pct(xs, 90):8.1f} {max(xs):8.1f}")

    med = statistics.median(per_gen)
    print(f"\n{'overall':>12s} {'':>22s} {med:10.1f} {pct(per_gen, 10):8.1f} {pct(per_gen, 90):8.1f} {max(per_gen):8.1f}")
    print(f"wall time {wall:.0f} s")
    print(f"\nagent alone: median {round(med)} ms per decision")
    print(f"in a run the nodes share the cores and it is slower: ~1.2x on the 9 Oct 2026 host, so set")
    print(f"  \"oracle_decision_delay_ms\": {round(med * 1.2)}")
    print("or measure it from a real run with scripts/epoch_delta.py (docs/epoch-grid.md section 6)")


if __name__ == "__main__":
    main()
