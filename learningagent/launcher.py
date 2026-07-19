from __future__ import annotations

import argparse
import logging
import multiprocessing
import signal
import sys
import time

from learningagent.address import SUPPORTED_MODES, server_address
from learningagent.server import run_server


LOGGER = logging.getLogger(__name__)
CHILD_JOIN_SECONDS = 6


def _parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Start one Python learning-agent gRPC server per PBFT node"
    )
    parser.add_argument("--node-count", type=int, required=True)
    parser.add_argument("--mode", choices=SUPPORTED_MODES, required=True)
    args = parser.parse_args()
    if args.node_count < 1:
        parser.error("--node-count must be at least 1")
    return args


def main() -> int:
    args = _parse_args()
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(processName)s %(levelname)s %(message)s",
    )

    context = multiprocessing.get_context("spawn")
    stop_event = context.Event()
    processes: list[multiprocessing.Process] = []

    def request_stop(_signum, _frame) -> None:
        stop_event.set()

    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)

    for node_id in range(1, args.node_count + 1):
        address = server_address(node_id, args.mode)
        process = context.Process(
            target=run_server,
            args=(node_id, args.mode, stop_event),
            name=f"learning-agent-{node_id}",
        )
        process.start()
        processes.append(process)
        LOGGER.info("started node %d server process at %s", node_id, address)

    exit_code = 0
    try:
        while not stop_event.is_set():
            for process in processes:
                if process.exitcode is not None:
                    LOGGER.error(
                        "%s exited unexpectedly with status %s",
                        process.name,
                        process.exitcode,
                    )
                    exit_code = process.exitcode or 1
                    stop_event.set()
                    break
            if not stop_event.is_set():
                time.sleep(0.25)
    except KeyboardInterrupt:
        stop_event.set()
    finally:
        stop_event.set()
        deadline = time.monotonic() + CHILD_JOIN_SECONDS
        for process in processes:
            process.join(max(0, deadline - time.monotonic()))
        for process in processes:
            if process.is_alive():
                LOGGER.warning("terminating unresponsive %s", process.name)
                process.terminate()
        for process in processes:
            process.join()

    return exit_code


if __name__ == "__main__":
    sys.exit(main())

