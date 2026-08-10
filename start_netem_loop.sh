#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
SCRIPT_PATH="$SCRIPT_DIR/$(basename -- "${BASH_SOURCE[0]}")"
CONFIG_PATH="$SCRIPT_DIR/config/run2new.json"
LOG_DIR="$SCRIPT_DIR/logs"
NETEM_INTERFACE="lo"
NETEM_LIMIT=100000
NETEM_DELAY_LOG="$LOG_DIR/netem_schedule.log"
NETEM_PID_FILE="$LOG_DIR/netem_loop.pid"

worker_process_matches() {
    local pid=$1
    local cmdline

    [[ -r "/proc/$pid/cmdline" ]] || return 1
    cmdline=$(tr '\0' ' ' < "/proc/$pid/cmdline")
    [[ "$cmdline" == *"$SCRIPT_PATH"* && "$cmdline" == *"--worker"* ]]
}

remove_own_pid_file() {
    local recorded_pid

    [[ -f "$NETEM_PID_FILE" ]] || return 0
    recorded_pid=$(tr -d '[:space:]' < "$NETEM_PID_FILE")
    if [[ "$recorded_pid" == "$$" ]]; then
        rm -f -- "$NETEM_PID_FILE"
    fi
}

worker_cleanup() {
    trap - EXIT TERM INT HUP
    sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        netem limit "$NETEM_LIMIT" delay 0ms >/dev/null 2>&1 || true
    remove_own_pid_file
}

run_worker() {
    trap 'exit 0' TERM INT HUP
    trap worker_cleanup EXIT

    echo "$(date --iso-8601=ns) delay=0ms"
    while true; do
        sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
            netem limit "$NETEM_LIMIT" delay 100ms
        echo "$(date --iso-8601=ns) delay=100ms"
        sleep 0.1

        sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
            netem limit "$NETEM_LIMIT" delay 0ms
        echo "$(date --iso-8601=ns) delay=0ms"
        sleep 3
    done
}

if [[ "${1:-}" == "--worker" ]]; then
    run_worker
    exit 0
fi

if (( $# != 0 )); then
    echo "Usage: $0" >&2
    exit 2
fi

for command_name in tc python3 sudo; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "Error: $command_name is required." >&2
        exit 1
    fi
done

if [[ ! -f "$CONFIG_PATH" ]]; then
    echo "Error: configuration file not found: $CONFIG_PATH" >&2
    exit 1
fi

if ! NODE_COUNT=$(python3 -c \
    'import json, sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["node_num"])' \
    "$CONFIG_PATH"); then
    echo "Error: could not read node_num from $CONFIG_PATH" >&2
    exit 1
fi

if ! [[ "$NODE_COUNT" =~ ^[1-9][0-9]*$ ]]; then
    echo "Error: node_num must be a positive integer." >&2
    exit 1
fi
if (( NODE_COUNT < 1 || NODE_COUNT > 8 )); then
    echo "Error: NODE_COUNT must be between 1 and 8 in loopbackip mode." >&2
    exit 1
fi

mkdir -p -- "$LOG_DIR"

if [[ -f "$NETEM_PID_FILE" ]]; then
    recorded_pid=$(tr -d '[:space:]' < "$NETEM_PID_FILE")
    if [[ "$recorded_pid" =~ ^[1-9][0-9]*$ ]] && kill -0 "$recorded_pid" 2>/dev/null; then
        if worker_process_matches "$recorded_pid"; then
            echo "Error: netem delay loop is already running with PID $recorded_pid." >&2
        else
            echo "Error: $NETEM_PID_FILE points to unrelated live PID $recorded_pid; refusing to overwrite it." >&2
        fi
        exit 1
    fi
    echo "Removing stale netem loop PID file."
    rm -f -- "$NETEM_PID_FILE"
fi

echo "Authenticating sudo for netem configuration..."
sudo -v

setup_started=0
cleanup_failed_start() {
    if (( setup_started == 1 )); then
        sudo -n tc qdisc del dev "$NETEM_INTERFACE" root >/dev/null 2>&1 || true
    fi
}
trap cleanup_failed_start EXIT

echo "Configuring node-to-node netem filters on $NETEM_INTERFACE..."
setup_started=1
sudo tc qdisc del dev "$NETEM_INTERFACE" root 2>/dev/null || true
sudo tc qdisc add dev "$NETEM_INTERFACE" root handle 1: prio bands 3 \
    priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
sudo tc qdisc add dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
    netem limit "$NETEM_LIMIT" delay 0ms

for ((src_id = 1; src_id <= NODE_COUNT; src_id++)); do
    src_ip="127.0.0.$((src_id + 1))"
    for ((dst_id = 1; dst_id <= NODE_COUNT; dst_id++)); do
        dst_ip="127.0.0.$((dst_id + 1))"
        sudo tc filter add dev "$NETEM_INTERFACE" parent 1: protocol ip prio 10 flower \
            src_ip "$src_ip/32" dst_ip "$dst_ip/32" classid 1:3
    done
done

echo "Node-to-node netem filters configured with an initial 0ms delay."
: > "$NETEM_DELAY_LOG"

bash "$SCRIPT_PATH" --worker >> "$NETEM_DELAY_LOG" 2>&1 &
worker_pid=$!
printf '%s\n' "$worker_pid" > "$NETEM_PID_FILE"

sleep 0.05
if ! kill -0 "$worker_pid" 2>/dev/null || ! worker_process_matches "$worker_pid"; then
    rm -f -- "$NETEM_PID_FILE"
    echo "Error: netem delay loop failed to start; inspect $NETEM_DELAY_LOG." >&2
    exit 1
fi

setup_started=0
trap - EXIT
echo "Started netem delay loop with PID $worker_pid."
echo "Transitions are logged to $NETEM_DELAY_LOG."
echo "Run $SCRIPT_DIR/stop_netem_loop.sh to stop it and remove the qdisc."
