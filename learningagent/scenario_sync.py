"""Scenario schedule shared with the Go nodes.

The nodes derive their scenario from the ViewID generation in node/scenario.go:

    scenarios[((gen - 1) / span) % len(scenarios)]

The learning agent sees the same number as ``sequence_id`` (the node sends
``SequenceId: EpochGeneration``), so reading the same config JSON and applying the
same formula keeps both sides on the same scenario without extra messages.
"""

from __future__ import annotations

import json
from pathlib import Path

try:
    from enum import StrEnum  # Python 3.11+
except ImportError:
    from strenum import StrEnum  # Python 3.10


class Scenario(StrEnum):
    Healthy = "healthy"
    ProposalDelay = "proposal_delay"
    NetworkDelay = "network_delay"
    NetworkDelayFCrash = "network_delay_f_crash"


SCENARIOS = [s.value for s in Scenario]

# Scenario names as core/scenario.go spells them in the config JSON.
GO_SCENARIO_NAMES = {
    "Healthy": Scenario.Healthy,
    "ProposalDelay": Scenario.ProposalDelay,
    "NetworkDelay": Scenario.NetworkDelay,
    "NetworkDelayFCrash": Scenario.NetworkDelayFCrash,
}

# Mirrors config.DefaultScenarioGenerations.
DEFAULT_SCENARIO_GENERATIONS = 100


def load_scenario_schedule(config_path: str | Path) -> tuple[list[Scenario], int]:
    """Read the scenario list and span from the Go experiment config.

    Raises on anything unreadable or unrecognised so a mismatch with the nodes
    fails at startup instead of silently training against the wrong scenario.
    """
    config_path = Path(config_path)
    try:
        config = json.loads(config_path.read_text())
    except OSError as error:
        raise RuntimeError(f"cannot read experiment config {config_path}: {error}") from error
    except json.JSONDecodeError as error:
        raise RuntimeError(f"invalid JSON in experiment config {config_path}: {error}") from error

    names = config.get("scenarios") or []
    scenarios = []
    for name in names:
        if name not in GO_SCENARIO_NAMES:
            raise RuntimeError(
                f"unknown scenario {name!r} in {config_path}; "
                f"known scenarios: {', '.join(GO_SCENARIO_NAMES)}"
            )
        scenarios.append(GO_SCENARIO_NAMES[name])

    if not config.get("scenario_mode", False):
        # The nodes are not rotating, so neither do we: hold the first scenario
        # (or Healthy when the list is empty) for the whole run.
        return [scenarios[0] if scenarios else Scenario.Healthy], 1

    if not scenarios:
        raise RuntimeError(f"scenario_mode is enabled but scenarios is empty in {config_path}")

    span = int(config.get("scenario_generations", 0) or DEFAULT_SCENARIO_GENERATIONS)
    if span <= 0:
        raise RuntimeError(f"scenario_generations must be positive in {config_path}, got {span}")

    return scenarios, span


def scenario_for_sequence(sequence_id: int, scenarios: list[Scenario], span: int) -> Scenario:
    """Scenario for a sequence id (== the node's generation), starting at 1."""
    if sequence_id < 1:
        raise ValueError(f"sequence_id must be at least 1, got {sequence_id}")
    return scenarios[((sequence_id - 1) // span) % len(scenarios)]
