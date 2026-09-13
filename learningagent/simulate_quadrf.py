"""Run a small synthetic experiment against the existing MultiRF model."""

from collections import Counter

try:
    from enum import StrEnum  # Python 3.11+
except ImportError:
    from strenum import StrEnum  # Python 3.10
from time import time

import numpy as np

from learningagent.server import LearningData, MultiRF, ProtocolName, QuadRF

SIMULATION_STEPS = 500
RANDOM_SEED = 5
MIN_SHADOW_COUNT = 6
MAX_SHADOW_COUNT = 10
STATE_SHIFT_AFTER_STEP = 16
SHIFTED_SHADOW_COUNT = 0
INIT_PROTOCOL = ProtocolName.FixedRoundRobin


class Scenario(StrEnum):
    Healthy = "healthy"
    ProposalDelay = "proposal_delay"
    NetworkDelay = "network_delay"
    NetworkDelayFCrash = "network_delay_f_crash"


SCENARIOS = [s.value for s in Scenario]

ROTATING_SCENARIOS = [Scenario.Healthy, Scenario.ProposalDelay, Scenario.NetworkDelay]


def generate_state(
    step: int,
    rng: np.random.Generator,
    scenario: Scenario,
    protocol: ProtocolName,
) -> np.ndarray:
    """Generate a one-feature state containing only shadow_count."""
    vc_rate = 0.0
    proposal_interval = 0.0
    u = 0.0

    if scenario == Scenario.Healthy:
        if protocol == ProtocolName.FixedRoundRobin:
            vc_rate = 0.0
            proposal_interval = 3.76
            u = 0

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        elif (
            protocol == ProtocolName.PeriodicRoundRobin
            or protocol == ProtocolName.PerformanceRoundRobin
        ):
            vc_rate = 0.1  # every 10s rotation
            proposal_interval = 4.2  # estimate
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        elif (
            protocol == ProtocolName.PeriodicElection
            or protocol == ProtocolName.PerformanceElection
        ):
            vc_rate = 0.1  # every 10s rotation, can be more due to split votes
            proposal_interval = 4.2  # estimate, not sure honestly
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")
    elif scenario == Scenario.ProposalDelay:
        if protocol == ProtocolName.FixedRoundRobin:
            vc_rate = 0.0
            proposal_interval = 70  # 2s delay
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        elif protocol == ProtocolName.PeriodicRoundRobin:
            vc_rate = 0.1  # every 10s rotation
            proposal_interval = 5.44  # estimate,
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        elif protocol == ProtocolName.PerformanceRoundRobin:
            vc_rate = 0.12  # every 31s
            proposal_interval = 4.4  # 3.8 +vc tax
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        elif protocol == ProtocolName.PeriodicElection:
            vc_rate = 0.1  # every 10s rotation, can be more due to split votes
            proposal_interval = 5.44  # estimate, not sure honestly, 2s delay
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        elif protocol == ProtocolName.PerformanceElection:
            vc_rate = 0.12  # every 31s
            proposal_interval = 4.4  # 3.8 +vc tax
            u = 0
            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")

    elif scenario == Scenario.NetworkDelay:
        if (
            protocol == ProtocolName.FixedRoundRobin
            or protocol == ProtocolName.PerformanceElection
            or protocol == ProtocolName.PerformanceRoundRobin
        ):
            vc_rate = 10  # EVERY 100MS
            proposal_interval = 1000  # should it be very large or zero
            u = 0

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        if protocol == ProtocolName.PeriodicRoundRobin:
            vc_rate = 0.1
            proposal_interval = 33  # 30 batches persec
            u = 0

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        if protocol == ProtocolName.PeriodicElection:
            vc_rate = 0.1
            proposal_interval = 33  # 30 batches persec
            u = 0

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")
    elif scenario == Scenario.NetworkDelayFCrash:
        if (
            protocol == ProtocolName.FixedRoundRobin
            or protocol == ProtocolName.PerformanceElection
            or protocol == ProtocolName.PerformanceRoundRobin
        ):
            vc_rate = 10  # EVERY 100MS
            proposal_interval = 1000  # should it be very large or zero
            u = 1

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        if protocol == ProtocolName.PeriodicRoundRobin:
            vc_rate = 0.1
            proposal_interval = 45  # 25% no leader 75% of 30
            u = 1

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        if protocol == ProtocolName.PeriodicElection:
            vc_rate = 0.1
            proposal_interval = 33
            u = 1

            return np.asarray([vc_rate, proposal_interval, u], dtype=np.float64)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")


def generate_reward(
    protocol: ProtocolName,
    scenario: Scenario,
    step: int,
    rng: np.random.Generator,
) -> float:
    """Generate synthetic throughput for the selected protocol."""
    if scenario == Scenario.Healthy:
        if protocol == ProtocolName.FixedRoundRobin:
            return float(266)
        elif (
            protocol == ProtocolName.PeriodicRoundRobin
            or protocol == ProtocolName.PerformanceRoundRobin
        ):
            return float(262)
        elif (
            protocol == ProtocolName.PeriodicElection
            or protocol == ProtocolName.PerformanceElection
        ):
            return float(258)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")
    elif scenario == Scenario.ProposalDelay:
        if protocol == ProtocolName.FixedRoundRobin:

            return float(14)
        elif protocol == ProtocolName.PeriodicRoundRobin:

            return float(203)
        elif protocol == ProtocolName.PerformanceRoundRobin:
            return float(258)

        elif protocol == ProtocolName.PeriodicElection:

            return float(200)
        elif protocol == ProtocolName.PerformanceElection:
            return float(254)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")
    elif scenario == Scenario.NetworkDelay:
        if (
            protocol == ProtocolName.FixedRoundRobin
            or protocol == ProtocolName.PerformanceElection
            or protocol == ProtocolName.PerformanceRoundRobin
        ):
            return float(1)

        if protocol == ProtocolName.PeriodicRoundRobin:

            return float(30)
        if protocol == ProtocolName.PeriodicElection:

            return float(27)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")

    elif scenario == Scenario.NetworkDelayFCrash:
        if (
            protocol == ProtocolName.FixedRoundRobin
            or protocol == ProtocolName.PerformanceElection
            or protocol == ProtocolName.PerformanceRoundRobin
        ):
            return float(1)
        if protocol == ProtocolName.PeriodicRoundRobin:
            return float(22)
        if protocol == ProtocolName.PeriodicElection:

            return float(28)
        else:
            raise ValueError(f"unsupported protocol: {protocol}")


# def record_initial_experiences(model: MultiRF) -> int:
#     """Record synthetic starting observations and fit both protocol models."""
#     initial_experiences = (
#         # Teach the model that fixed initially performs well at shadow_count=0.
#         # (ProtocolName.Performance, 1400.0, 0.0),
#         # (ProtocolName.Performance, 1400.0, 0.0),
#         (ProtocolName.Performance, 2460.0, 0.0),
#         (ProtocolName.Performance, 2460.0, 0.0),
#         # Keep the original five alternating observations at shadow_count=10.
#         # (ProtocolName.Periodic, 1200.0, 10.0),
#         (ProtocolName.Performance, 2100.0, 6.0),
#         (ProtocolName.Periodic, 2400.0, 10.0),
#         # (ProtocolName.Performance, 1100.0, 10.0),
#         # (ProtocolName.Periodic, 1200.0, 10.0),
#     )

#     for sequence_id, (protocol, reward, shadow_count) in enumerate(
#         initial_experiences,
#         start=1,
#     ):
#         model.record_state_action_reward(
#             LearningData(
#                 sequence_id=sequence_id,
#                 current_protocol=protocol,
#                 reward=reward,
#                 state=np.asarray([shadow_count], dtype=np.float64),
#             )
#         )

#     # Both models must be fitted before MultiRF.predict() can use them.
#     model.train(ProtocolName.Periodic)
#     model.train(ProtocolName.Performance)
#     return len(initial_experiences)


def main() -> None:
    model = QuadRF(seed=RANDOM_SEED)
    data_rng = np.random.default_rng(RANDOM_SEED)
    selected_protocol = INIT_PROTOCOL
    selections: Counter[ProtocolName] = Counter()
    rewards: dict[ProtocolName, list[float]] = {p: [] for p in ProtocolName}
    scenario = Scenario.Healthy
    for step in range(1, SIMULATION_STEPS + 1):
        # only the arm selected is trained, the other arm is not trained
        timeStart = time()

        last_scenario = scenario
        scenario = ROTATING_SCENARIOS[((step - 1) // 100) % len(ROTATING_SCENARIOS)]
        if scenario != last_scenario:
            print(f"\nScenario changed to {scenario.value} at step {step}")
        prev_protocol = selected_protocol
        state = generate_state(step, data_rng, scenario, prev_protocol)
        selected_protocol = ProtocolName(model.predict(state, prev_protocol))
        reward = generate_reward(selected_protocol, scenario, step, data_rng)
        sequence_id = step

        model.record_state_action_reward(
            LearningData(
                sequence_id=sequence_id,
                current_protocol=selected_protocol,
                reward=reward,
                state=state,
            ),
            prev_protocol,
        )
        # model.train(prev_protocol, selected_protocol)
        timeEnd = time()

        selections[selected_protocol] += 1
        rewards[selected_protocol].append(reward)
        print(
            f"step={step:03d} "
            f"prev={prev_protocol.value} "
            f"selected={selected_protocol.value} "
            f"throughput={reward:.0f}"
            f" time={timeEnd - timeStart:.4f}s"
        )

    print("\nSimulation summary")
    for protocol in ProtocolName:
        protocol_rewards = rewards[protocol]
        mean_reward = (
            float(np.mean(protocol_rewards)) if protocol_rewards else float("nan")
        )
        print(
            f"{protocol.value}: selections={selections[protocol]}, "
            f"mean_generated_throughput={mean_reward:.2f}"
        )


if __name__ == "__main__":
    main()

# python -m learningagent.simulate_multirf

# interesting result when at shift just change state then fixed is selected but initlal data showed state 0 fixed give 1400
# but if reward fixed with state zero still 1100 then periodic selected again but if reward high then fixed selected for all next steps
