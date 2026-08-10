#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
START_SCRIPT="$SCRIPT_DIR/start_netem_loop.sh"
NETEM_INTERFACE="lo"
NETEM_PID_FILE="$SCRIPT_DIR/logs/netem_loop.pid"

worker_process_matches() {
    local pid=$1
    local cmdline

    [[ -r "/proc/$pid/cmdline" ]] || return 1
    cmdline=$(tr '\0' ' ' < "/proc/$pid/cmdline")
    [[ "$cmdline" == *"$START_SCRIPT"* && "$cmdline" == *"--worker"* ]]
}

if (( $# != 0 )); then
    echo "Usage: $0" >&2
    exit 2
fi

for command_name in tc sudo; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "Error: $command_name is required." >&2
        exit 1
    fi
done

echo "Authenticating sudo for netem cleanup..."
sudo -v

status=0
worker_pid=""
if [[ ! -f "$NETEM_PID_FILE" ]]; then
    echo "No netem loop PID file found; continuing with qdisc cleanup."
else
    worker_pid=$(tr -d '[:space:]' < "$NETEM_PID_FILE")
    if ! [[ "$worker_pid" =~ ^[1-9][0-9]*$ ]]; then
        echo "Warning: removing invalid PID file $NETEM_PID_FILE." >&2
        rm -f -- "$NETEM_PID_FILE"
        worker_pid=""
        status=1
    elif ! kill -0 "$worker_pid" 2>/dev/null; then
        echo "Netem loop PID $worker_pid is not running; removing the stale PID file."
        rm -f -- "$NETEM_PID_FILE"
        worker_pid=""
    elif ! worker_process_matches "$worker_pid"; then
        echo "Error: refusing to signal unrelated live PID $worker_pid from $NETEM_PID_FILE." >&2
        worker_pid=""
        status=1
    fi
fi

if [[ -n "$worker_pid" ]]; then
    echo "Stopping netem delay loop PID $worker_pid..."
    kill -TERM "$worker_pid"

    for ((attempt = 0; attempt < 120; attempt++)); do
        if ! kill -0 "$worker_pid" 2>/dev/null; then
            break
        fi
        sleep 0.05
    done

    if kill -0 "$worker_pid" 2>/dev/null; then
        echo "Error: netem delay loop PID $worker_pid did not stop within 6 seconds." >&2
        status=1
    else
        echo "Netem delay loop stopped."
        rm -f -- "$NETEM_PID_FILE"
    fi
fi

echo "Removing root qdisc and node-to-node filters from $NETEM_INTERFACE..."
if sudo tc qdisc del dev "$NETEM_INTERFACE" root 2>/dev/null; then
    echo "Netem qdisc removed."
else
    echo "No removable root qdisc was present on $NETEM_INTERFACE."
fi

exit "$status"
