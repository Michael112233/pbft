#!/bin/bash
set -e

SESSION="pbft"
CONFIG_PATH="config/run2new.json"
CURRENT_DIR=$(pwd)
NETEM_CONTROLLER_BINARY="netem_controller"

NETEM_SOCKET_PATH=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1])).get("netem", {}).get("socket_path", "logs/netem-controller.sock"))' "$CONFIG_PATH")
NETEM_PID_PATH=$(python3 -c 'import json, sys; n=json.load(open(sys.argv[1])).get("netem", {}); s=n.get("socket_path", "logs/netem-controller.sock"); print(n.get("pid_path", s + ".pid"))' "$CONFIG_PATH")

if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Stopping tmux session '$SESSION'..."
    tmux kill-session -t "$SESSION"
fi

if [[ ! -f "$NETEM_PID_PATH" ]]; then
    echo "No running netem controller PID file found."
    exit 0
fi

sudo -v
pid=$(tr -d '[:space:]' < "$NETEM_PID_PATH")
if ! [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
    echo "Error: invalid netem controller PID file: $NETEM_PID_PATH" >&2
    exit 1
fi
if ! sudo -n kill -0 "$pid" 2>/dev/null; then
    echo "Netem controller PID $pid is not running; stale files will be cleaned on the next start."
    exit 0
fi
if [[ ! -x "$CURRENT_DIR/$NETEM_CONTROLLER_BINARY" ]]; then
    echo "Error: cannot verify PID $pid because $NETEM_CONTROLLER_BINARY is missing." >&2
    exit 1
fi

expected_exe=$(readlink -f "$CURRENT_DIR/$NETEM_CONTROLLER_BINARY")
actual_exe=$(sudo -n readlink -f "/proc/$pid/exe")
if [[ "$actual_exe" != "$expected_exe" ]]; then
    echo "Error: refusing to stop PID $pid because it is not $expected_exe." >&2
    exit 1
fi

echo "Stopping netem controller PID $pid..."
sudo -n kill -TERM "$pid"
for _ in $(seq 1 200); do
    if ! sudo -n kill -0 "$pid" 2>/dev/null; then
        echo "Netem controller stopped and qdisc cleanup completed."
        exit 0
    fi
    sleep 0.05
done

echo "Error: netem controller PID $pid did not stop; inspect $NETEM_SOCKET_PATH and logs/netem_controller.log." >&2
exit 1
