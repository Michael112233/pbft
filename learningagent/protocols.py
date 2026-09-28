"""Protocol names and the learning record shared by the server and simulations.

These live outside server.py so the synthetic-data modules can import them
without importing the server, which imports the synthetic-data modules in turn.
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
from numpy.typing import NDArray

try:
    from enum import StrEnum  # Python 3.11+
except ImportError:
    from strenum import StrEnum  # Python 3.10


class ProtocolName(StrEnum):
    # Values are spelled exactly as core.ActiontoString / core.StringtoAction do,
    # so a decision can be sent to the node without translation (an unknown name
    # would silently parse as FixedRoundRobin on the Go side).
    PeriodicRoundRobin = "PeriodicRoundRobin"
    PeriodicElection = "PeriodicElection"
    PerformanceRoundRobin = "PerformanceRoundRobin"
    PerformanceElection = "PerformanceElection"
    FixedRoundRobin = "FixedRoundRobin"


PROTOCOLS = [p.value for p in ProtocolName]


@dataclass(frozen=True, slots=True)
class LearningData:
    sequence_id: int
    current_protocol: ProtocolName
    reward: float
    state: NDArray[np.float64]  # 1d array


@dataclass(frozen=True, slots=True)
class MultiObjectiveData:
    """One epoch's experience for QuadRFMultiObjective: two rewards instead of one."""

    sequence_id: int
    current_protocol: str  # action name as the node spells it, e.g. "FixedAware"
    throughput: float  # tx/s, higher is better
    latency: float  # ms, lower is better
    state: NDArray[np.float64]  # 1d array
