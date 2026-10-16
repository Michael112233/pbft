from __future__ import annotations

import json
from pathlib import Path

LOCAL_MODE = "local"
LOOPBACK_IP_MODE = "loopbackip"
REMOTE_MODE = "remote"
SUPPORTED_MODES = (LOCAL_MODE, LOOPBACK_IP_MODE, REMOTE_MODE)

NODE_BASE_PORT = 28000
NODE_PORT_STEP = 100
LOCAL_BASE_PORT = 29000
LOOPBACK_PORT = 29000


def load_hosts(path: str | Path) -> dict[int, str]:
    """Node id -> IP from a hosts file, the one the Go side reads for -m remote
    (config.LoadHosts). Only the nodes matter here: an agent binds its node's IP
    and calls back to its node."""
    with open(path, encoding="utf-8") as handle:
        data = json.load(handle)
    return {int(node_id): entry["ip"] for node_id, entry in data["nodes"].items()}


def _host(node_id: int, mode: str, hosts: dict[int, str] | None = None) -> str:
    if node_id < 1:
        raise ValueError("node_id must be at least 1")
    if mode == LOCAL_MODE:
        return "127.0.0.1"
    if mode == LOOPBACK_IP_MODE:
        if node_id > 253:
            raise ValueError("loopbackip mode supports node IDs from 1 through 253")
        return f"127.0.0.{node_id + 1}"
    if mode == REMOTE_MODE:
        if hosts is None:
            raise ValueError("remote mode needs the hosts file (--hosts)")
        if node_id not in hosts:
            raise ValueError(f"node {node_id} is not in the hosts file")
        return hosts[node_id]
    raise ValueError(f"unsupported mode {mode!r}; expected one of {SUPPORTED_MODES}")


def server_address(node_id: int, mode: str, hosts: dict[int, str] | None = None) -> str:
    host = _host(node_id, mode, hosts)
    port = LOCAL_BASE_PORT + node_id if mode == LOCAL_MODE else LOOPBACK_PORT
    return f"{host}:{port}"


def node_address(node_id: int, mode: str, hosts: dict[int, str] | None = None) -> str:
    host = _host(node_id, mode, hosts)
    return f"{host}:{NODE_BASE_PORT + node_id * NODE_PORT_STEP}"
