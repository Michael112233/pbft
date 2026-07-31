from __future__ import annotations

import argparse
import logging
import queue
import signal
import threading
from concurrent import futures
from dataclasses import dataclass
import numpy as np
from numpy.typing import NDArray
from typing import Protocol
try:
    from enum import StrEnum          # Python 3.11+
except ImportError:
    from strenum import StrEnum       # Python 3.10
from sklearn.ensemble import RandomForestRegressor
from collections import deque
import grpc

from learningagent import learning_agent_pb2
from learningagent import learning_agent_pb2_grpc
from learningagent.address import SUPPORTED_MODES, node_address, server_address


LOGGER = logging.getLogger(__name__)
GRACEFUL_STOP_SECONDS = 5
NODE_RPC_TIMEOUT_SECONDS = 1.0
MAX_REPLAY_LENGTH = 1000
LEARNING_DATA_REWARD_KEY = "reward"
LEARNING_DATA_STATE_KEYS = ("proposal_interval",)


class StopEvent(Protocol):
    def is_set(self) -> bool: ...

    def wait(self, timeout: float | None = None) -> bool: ...

    def set(self) -> None: ...


class ProtocolName(StrEnum):
    Periodic = "periodic"
    Performance = "performance"

PROTOCOLS = [p.value for p in ProtocolName]

@dataclass(frozen=True, slots=True)
class LearningData:
    sequence_id: int
    current_protocol: ProtocolName
    reward: float
    state: NDArray[np.float64] #1d array


DecisionQueue = queue.Queue[LearningData | None]


def replay_len(experiences):
    return min(len(experiences), MAX_REPLAY_LENGTH)


class MultiRF:
    def __init__(self, seed: int | None = None) -> None:
        self.experiences_X = {}
        self.experiences_y = {}
        self.models = {}
        self.rng = np.random.default_rng(seed)
        for protocol in PROTOCOLS:
            self.experiences_X[protocol] = []
            self.experiences_y[protocol] = []
            self.models[protocol] = RandomForestRegressor(max_depth=5)

    def record_state_action_reward(self, LearningData: LearningData):
        protocol = LearningData.current_protocol
        self.experiences_X[protocol].append(LearningData.state)
        self.experiences_y[protocol].append(LearningData.reward)

    def train(self, protocol: ProtocolName) -> None:
        experiences_X = self.experiences_X[protocol]
        experiences_y = self.experiences_y[protocol]

        if not experiences_y:
            return

        if len(experiences_X) != len(experiences_y):
            raise ValueError(
                f"experience length mismatch for {protocol}: "
                f"X={len(experiences_X)}, y={len(experiences_y)}"
            )

        replay_length = replay_len(experiences_y)

        replay_X = np.asarray(
            experiences_X[-replay_length:],
            dtype=np.float64,
        ) #(N,F)
        replay_y = np.asarray(
            experiences_y[-replay_length:],
            dtype=np.float64,
        ) #(N,)

        bootstrap_indices = np.random.choice(
            replay_length,
            size=replay_length,
            replace=True,
        )

        bootstrap_X = replay_X[bootstrap_indices]
        bootstrap_y = replay_y[bootstrap_indices]

        self.models[protocol].fit(
            bootstrap_X,
            bootstrap_y,
        )
    def predict(self,state: NDArray[np.float64]) -> ProtocolName:
        state = np.asarray(state, dtype=np.float64)

        if state.ndim != 1:
            raise ValueError(
                f"state must be one-dimensional, got shape {state.shape}"
            )

        model_input = state.reshape(1, -1)

        predicted_rewards: dict[ProtocolName, float] = {}

        for protocol in PROTOCOLS:
            prediction = self.models[protocol].predict(model_input)
            predicted_rewards[protocol] = float(prediction[0])

        max_reward = max(predicted_rewards.values())

        best_protocols = [
            protocol
            for protocol, reward in predicted_rewards.items()
            if reward == max_reward
        ]

        selected_index = self.rng.integers(
            0,
            len(best_protocols),
        )
        return best_protocols[selected_index]


class EpsilonGreedyBandit:
    """Context-free epsilon-greedy bandit for protocol selection."""

    def __init__(
        self,
        epsilon: float = 0.1, # smaller then less randomisation
        alpha: float = 0.1, #bigger alpha more reactive to recent
        seed: int | None = None,
    ) -> None:
        epsilon = float(epsilon)
        alpha = float(alpha)

        if not np.isfinite(epsilon) or not 0.0 <= epsilon <= 1.0:
            raise ValueError("epsilon must be finite and between 0 and 1")
        if not np.isfinite(alpha) or not 0.0 < alpha <= 1.0:
            raise ValueError("alpha must be finite and greater than 0 and at most 1")

        self.epsilon = epsilon
        self.alpha = alpha
        self.rng = np.random.default_rng(seed)
        self.values = {protocol: 0.0 for protocol in ProtocolName}
        self.reward_counts = {protocol: 0 for protocol in ProtocolName}
        self.selection_counts = {protocol: 0 for protocol in ProtocolName}

    def train(self, protocol: ProtocolName | str, reward: float) -> None:
        try:
            protocol = ProtocolName(protocol)
        except (TypeError, ValueError) as error:
            raise ValueError(f"unknown protocol: {protocol!r}") from error

        reward = float(reward)
        if not np.isfinite(reward):
            raise ValueError("reward must be finite")

        current_value = self.values[protocol]
        if self.reward_counts[protocol] == 0:
            self.values[protocol] = reward #initialize faster
        else:
            self.values[protocol] += self.alpha * (
                reward - current_value
            )
        # self.values[protocol] = current_value + self.alpha * (
        #     reward - current_value
        # )
        self.reward_counts[protocol] += 1

    def predict(self) -> ProtocolName:
        unselected_protocols = [
            protocol
            for protocol, count in self.selection_counts.items()
            if count == 0
        ]

        if unselected_protocols:
            # selected_protocol = self._random_protocol(unselected_protocols)
            if len(unselected_protocols) == 2:
                selected_protocol = ProtocolName.Performance
            else:
                selected_protocol = ProtocolName.Periodic
            LOGGER.info(
                "initial protocol coverage triggered: selected protocol %s",
                selected_protocol,
            )
        elif self.rng.random() < self.epsilon:
            
            selected_protocol = self._random_protocol(list(ProtocolName))
            LOGGER.info(
                "random exploration triggered: selected protocol %s",
                selected_protocol,
            )
        else:
            max_value = max(self.values.values())
            best_protocols = [
                protocol
                for protocol, value in self.values.items()
                if value == max_value
            ]
            selected_protocol = self._random_protocol(best_protocols)
            LOGGER.info(
                "exploitation triggered: selected protocol %s with value %.4f and both the values are periodic: %.4f and performance: %.4f",
                selected_protocol,
                self.values[selected_protocol],
                self.values[ProtocolName.Periodic],
                self.values[ProtocolName.Performance]
            )

        self.selection_counts[selected_protocol] += 1
        return selected_protocol

    def _random_protocol(
        self,
        protocols: list[ProtocolName],
    ) -> ProtocolName:
        selected_index = int(self.rng.integers(0, len(protocols)))
        return protocols[selected_index]


def run_decision_worker(
    node_id: int,
    node_stub: learning_agent_pb2_grpc.LearningAgentNodeStub,
    request_queue: DecisionQueue,
    bandit: EpsilonGreedyBandit,
    cmab: MultiRF,
) -> None:
    history: deque[LearningData] = deque(maxlen=2)
    sequence_id = 0
    while True:

        task = request_queue.get()


        try:
            if task is None:
                return
            try:
                if task.sequence_id != sequence_id + 1:
                    LOGGER.warning(
                        "node %d decision sequence %d out of order (expected %d)",
                        node_id,
                        task.sequence_id,
                        sequence_id + 1,
                    )
                if len(history) == 2:
                    prev_prev_state_action_reward = history.popleft()
                    completed_experience = LearningData(
                        sequence_id=prev_prev_state_action_reward.sequence_id,
                        current_protocol=prev_prev_state_action_reward.current_protocol,
                        reward=task.reward,
                        state=prev_prev_state_action_reward.state,
                    )
                    if prev_prev_state_action_reward.sequence_id == 1 and prev_prev_state_action_reward.current_protocol != ProtocolName.Performance:
                        logging.warning(
                            "node %d decision sequence %d protocol %s is invalid (expected %s)",
                            node_id,
                            prev_prev_state_action_reward.sequence_id,
                            prev_prev_state_action_reward.current_protocol,
                            ProtocolName.Performance,
                        )
                    if prev_prev_state_action_reward.sequence_id == 2 and prev_prev_state_action_reward.current_protocol != ProtocolName.Periodic:
                        logging.warning(
                            "node %d decision sequence %d protocol %s is invalid (expected %s)",
                            node_id,
                            prev_prev_state_action_reward.sequence_id,
                            prev_prev_state_action_reward.current_protocol,
                            ProtocolName.Periodic,
                        )
                    bandit.train(prev_prev_state_action_reward.current_protocol, task.reward)
                    # cmab.record_state_action_reward(completed_experience)
                    # cmab.train(completed_experience.current_protocol)
                sequence_id = task.sequence_id
                if sequence_id == 1 or sequence_id == 2:
                    next_protocol = bandit.predict()
                    state_action_reward = LearningData(
                        sequence_id=sequence_id,
                        current_protocol=next_protocol,
                        reward=0.0,
                        state=task.state,
                    )
                    history.append(state_action_reward)
                                        
                elif sequence_id > 2:
                    # next_protocol = cmab.predict(task.state)
                    # next_protocol = ProtocolName.Periodic
                    next_protocol = bandit.predict()
                    state_action_reward = LearningData(
                        sequence_id=sequence_id,
                        current_protocol=next_protocol,
                        reward=0.0,
                        state=task.state,
                    )
                    history.append(state_action_reward)
                else:
                    LOGGER.warning(
                        "node %d decision sequence %d is invalid (expected > 0)",
                        node_id,
                        task.sequence_id,
                    )
                    continue

                



                ack = node_stub.SendDecision(
                    learning_agent_pb2.LearningDecision(
                        node_id=node_id,
                        sequence_id=task.sequence_id,
                        next_protocol=next_protocol,
                    ),
                    timeout=NODE_RPC_TIMEOUT_SECONDS,
                )
                if ack.accepted:
                    LOGGER.info(
                        "node %d accepted decision sequence %d protocol %s",
                        node_id,
                        task.sequence_id,
                        next_protocol,
                    )
                else:
                    LOGGER.warning(
                        "node %d rejected decision sequence %d protocol %s: %s",
                        node_id,
                        task.sequence_id,
                        next_protocol,
                        ack.error,
                    )
            except grpc.RpcError as error:
                LOGGER.error(
                    "node %d decision sequence %d RPC failed: %s",
                    node_id,
                    task.sequence_id,
                    error,
                )
                continue
            except Exception:
                LOGGER.exception(
                    "node %d decision sequence %d failed unexpectedly",
                    node_id,
                    task.sequence_id,
                )
        finally:
            request_queue.task_done()


class LearningAgentService(learning_agent_pb2_grpc.LearningAgentServicer):
    def __init__(
        self,
        node_id: int,
        node_stub: learning_agent_pb2_grpc.LearningAgentNodeStub | None = None,
        decision_queue: DecisionQueue | None = None,
    ):
        self.node_id = node_id
        self.node_stub = node_stub # to send messages to node server
        self.decision_queue = decision_queue

    def Exchange(self, request, context):
        if request.node_id != self.node_id:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"server for node {self.node_id} cannot serve node {request.node_id}",
            )
        return learning_agent_pb2.NodeResponse(
            node_id=self.node_id,
            payload=request.payload,
        )
    def SendLearningData(self, request, context):
        if request.node_id != self.node_id:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"server for node {self.node_id} cannot serve node {request.node_id}",
            )

        required_keys = (LEARNING_DATA_REWARD_KEY, *LEARNING_DATA_STATE_KEYS)
        missing_keys = [key for key in required_keys if key not in request.data]
        if missing_keys:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                "learning data is missing required key(s): "
                + ", ".join(missing_keys),
            )

        reward = request.data[LEARNING_DATA_REWARD_KEY]
        state = np.asarray(
            [request.data[key] for key in LEARNING_DATA_STATE_KEYS],
            dtype=np.float64,
        )

        try:
            self.enqueue_decision(
                sequence_id=request.sequence_id,
                current_protocol=request.next_protocol,
                reward=reward,
                state=state,
            )
        except Exception as e:
            context.abort(
                grpc.StatusCode.INTERNAL,
                f"failed to enqueue learning data: {e}",
            )
        return learning_agent_pb2.DecisionAck(
            node_id=self.node_id,
            sequence_id=request.sequence_id,
            accepted=True,
        )

    def send_decision(
        self,
        sequence_id: int,
        current_protocol: str,
        timeout: float = NODE_RPC_TIMEOUT_SECONDS,
    ) -> learning_agent_pb2.DecisionAck:
        if self.node_stub is None:
            raise RuntimeError("learning-agent node callback client is not configured")
        return self.node_stub.SendDecision(
            learning_agent_pb2.LearningDecision(
                node_id=self.node_id,
                sequence_id=sequence_id,
                next_protocol=current_protocol,
            ),
            timeout=timeout,
        )

    def enqueue_decision(self, sequence_id: int, current_protocol: str, reward: float, state: NDArray[np.float64]) -> None:
        if self.decision_queue is None:
            raise RuntimeError("learning-agent decision worker queue is not configured")
        self.decision_queue.put_nowait(
            LearningData(
                sequence_id=sequence_id,
                current_protocol=current_protocol,
                reward=reward,
                state=state,
            )
        )


def build_server(
    node_id: int,
    address: str,
    node_stub: learning_agent_pb2_grpc.LearningAgentNodeStub | None = None,
    decision_queue: DecisionQueue | None = None,
) -> tuple[grpc.Server, int]:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    learning_agent_pb2_grpc.add_LearningAgentServicer_to_server(
        LearningAgentService(node_id, node_stub, decision_queue), server
    )
    bound_port = server.add_insecure_port(address)
    if bound_port == 0:
        raise RuntimeError(f"could not bind learning-agent server to {address}")
    return server, bound_port


def run_server(node_id: int, mode: str, stop_event: StopEvent) -> None:
    address = server_address(node_id, mode)
    callback_address = node_address(node_id, mode)
    node_channel = grpc.insecure_channel(callback_address)
    decision_queue: DecisionQueue = queue.Queue()
    cmab = MultiRF(5)
    bandit = EpsilonGreedyBandit(epsilon=0.1, alpha=0.1, seed=5)
    server = None
    worker = None
    worker_started = False
    try:
        node_stub = learning_agent_pb2_grpc.LearningAgentNodeStub(node_channel)
        server, _ = build_server(node_id, address, node_stub, decision_queue)
        server.start()
        worker = threading.Thread(
            target=run_decision_worker,
            args=(node_id, node_stub, decision_queue, bandit, cmab),
            name=f"learning-agent-node-callback-{node_id}",
            daemon=False,
        )
        worker.start()
        worker_started = True
        LOGGER.info(
            "node %d learning-agent server listening on %s; node callback target %s",
            node_id,
            address,
            callback_address,
        )
        stop_event.wait()
    finally:
        try:
            if server is not None:
                LOGGER.info("stopping node %d learning-agent server", node_id)
                server.stop(GRACEFUL_STOP_SECONDS).wait()
        finally:
            try:
                if worker_started:
                    decision_queue.put(None)
                    decision_queue.join()
                    worker.join()
            finally:
                node_channel.close()


def _parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run one node's learning-agent server")
    parser.add_argument("--node-id", type=int, required=True)
    parser.add_argument("--mode", choices=SUPPORTED_MODES, required=True)
    return parser.parse_args()


def main() -> int:
    args = _parse_args()
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(processName)s %(levelname)s %(message)s",
    )
    stop_event = threading.Event()

    def request_stop(_signum, _frame) -> None:
        stop_event.set()

    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)
    run_server(args.node_id, args.mode, stop_event)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
