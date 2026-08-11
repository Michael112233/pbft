"""Run a small synthetic experiment against the existing MultiRF model."""

from collections import Counter
from time import time

import numpy as np

from learningagent.server import LearningData, MultiRF, ProtocolName

SIMULATION_STEPS = 10
RANDOM_SEED = 5
MIN_SHADOW_COUNT = 10
MAX_SHADOW_COUNT = 14
STATE_SHIFT_AFTER_STEP = 50
SHIFTED_SHADOW_COUNT = 0


def generate_state(
    step: int,
    rng: np.random.Generator,
) -> np.ndarray:
    """Generate a one-feature state containing only shadow_count."""
    if step > STATE_SHIFT_AFTER_STEP:
        shadow_count = SHIFTED_SHADOW_COUNT
    else:
        shadow_count = rng.integers(MIN_SHADOW_COUNT, MAX_SHADOW_COUNT + 1)
    return np.asarray([shadow_count], dtype=np.float64)


def generate_reward(
    protocol: ProtocolName,
    step: int,
    rng: np.random.Generator,
) -> float:
    """Generate synthetic throughput for the selected protocol."""
    if protocol == ProtocolName.Periodic:
        return float(rng.integers(1250, 1301))
    if protocol == ProtocolName.Performance:
        if step > STATE_SHIFT_AFTER_STEP:
            return 1400.0
        return float(rng.integers(1100, 1151))
    raise ValueError(f"unsupported protocol: {protocol}")


def record_initial_experiences(model: MultiRF) -> int:
    """Record synthetic starting observations and fit both protocol models."""
    initial_experiences = (
        # Teach the model that fixed initially performs well at shadow_count=0.
        # (ProtocolName.Performance, 1400.0, 0.0),
        # (ProtocolName.Performance, 1400.0, 0.0),
        # (ProtocolName.Performance, 1400.0, 0.0),
        # Keep the original five alternating observations at shadow_count=10.
        # (ProtocolName.Periodic, 1200.0, 10.0),
        (ProtocolName.Performance, 1100.0, 10.0),
        # (ProtocolName.Periodic, 1200.0, 10.0),
        # (ProtocolName.Performance, 1100.0, 10.0),
        # (ProtocolName.Periodic, 1200.0, 10.0),
    )

    for sequence_id, (protocol, reward, shadow_count) in enumerate(
        initial_experiences,
        start=1,
    ):
        model.record_state_action_reward(
            LearningData(
                sequence_id=sequence_id,
                current_protocol=protocol,
                reward=reward,
                state=np.asarray([shadow_count], dtype=np.float64),
            )
        )

    # Both models must be fitted before MultiRF.predict() can use them.
    model.train(ProtocolName.Periodic)
    model.train(ProtocolName.Performance)
    return len(initial_experiences)


def main() -> None:
    model = MultiRF(seed=RANDOM_SEED)
    data_rng = np.random.default_rng(RANDOM_SEED)
    initial_experience_count = record_initial_experiences(model)
    selections: Counter[ProtocolName] = Counter()
    rewards: dict[ProtocolName, list[float]] = {
        ProtocolName.Periodic: [],
        ProtocolName.Performance: [],
    }

    for step in range(1, SIMULATION_STEPS + 1):
        # only the arm selected is trained, the other arm is not trained
        timeStart = time()
        state = generate_state(step, data_rng)
        selected_protocol = ProtocolName(model.predict(state))
        reward = generate_reward(selected_protocol, step, data_rng)
        sequence_id = initial_experience_count + step

        model.record_state_action_reward(
            LearningData(
                sequence_id=sequence_id,
                current_protocol=selected_protocol,
                reward=reward,
                state=state,
            )
        )
        model.train(selected_protocol)
        timeEnd = time()

        selections[selected_protocol] += 1
        rewards[selected_protocol].append(reward)
        print(
            f"step={step:03d} "
            f"shadow_count={int(state[0])} "
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
