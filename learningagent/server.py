from __future__ import annotations

import argparse
import logging
import signal
import threading
from concurrent import futures
from typing import Protocol

import grpc

from learningagent import learning_agent_pb2
from learningagent import learning_agent_pb2_grpc
from learningagent.address import SUPPORTED_MODES, server_address


LOGGER = logging.getLogger(__name__)
GRACEFUL_STOP_SECONDS = 5


class StopEvent(Protocol):
    def is_set(self) -> bool: ...

    def wait(self, timeout: float | None = None) -> bool: ...

    def set(self) -> None: ...


class LearningAgentService(learning_agent_pb2_grpc.LearningAgentServicer):
    def __init__(self, node_id: int):
        self.node_id = node_id

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


def build_server(node_id: int, address: str) -> tuple[grpc.Server, int]:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    learning_agent_pb2_grpc.add_LearningAgentServicer_to_server(
        LearningAgentService(node_id), server
    )
    bound_port = server.add_insecure_port(address)
    if bound_port == 0:
        raise RuntimeError(f"could not bind learning-agent server to {address}")
    return server, bound_port


def run_server(node_id: int, mode: str, stop_event: StopEvent) -> None:
    address = server_address(node_id, mode)
    server, _ = build_server(node_id, address)
    server.start()
    LOGGER.info("node %d learning-agent server listening on %s", node_id, address)
    try:
        while not stop_event.wait(0.5):
            pass
    finally:
        LOGGER.info("stopping node %d learning-agent server", node_id)
        server.stop(GRACEFUL_STOP_SECONDS).wait()


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

