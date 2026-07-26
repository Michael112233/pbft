LOCAL_MODE = "local"
LOOPBACK_IP_MODE = "loopbackip"
SUPPORTED_MODES = (LOCAL_MODE, LOOPBACK_IP_MODE)

NODE_BASE_PORT = 28000
NODE_PORT_STEP = 100
LOCAL_BASE_PORT = 29000
LOOPBACK_PORT = 29000


def _host(node_id: int, mode: str) -> str:
    if node_id < 1:
        raise ValueError("node_id must be at least 1")
    if mode == LOCAL_MODE:
        return "127.0.0.1"
    if mode == LOOPBACK_IP_MODE:
        if node_id > 253:
            raise ValueError("loopbackip mode supports node IDs from 1 through 253")
        return f"127.0.0.{node_id + 1}"
    raise ValueError(f"unsupported mode {mode!r}; expected one of {SUPPORTED_MODES}")


def server_address(node_id: int, mode: str) -> str:
    host = _host(node_id, mode)
    port = LOCAL_BASE_PORT + node_id if mode == LOCAL_MODE else LOOPBACK_PORT
    return f"{host}:{port}"


def node_address(node_id: int, mode: str) -> str:
    host = _host(node_id, mode)
    return f"{host}:{NODE_BASE_PORT + node_id * NODE_PORT_STEP}"
