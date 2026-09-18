#!/bin/bash
set -e

SESSION="pbft"
CONFIG_PATH="config/run2new.json"
CURRENT_DIR=$(pwd)

if ! command -v python3 >/dev/null 2>&1; then
    echo "Error: python3 is required to run the learning-agent servers." >&2
    exit 1
fi
if ! python3 -c 'import grpc, google.protobuf' >/dev/null 2>&1; then
    echo "Error: Python gRPC dependencies are missing. Run: python3 -m pip install -r requirements.txt" >&2
    exit 1
fi

NODE_COUNT=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["node_num"])' "$CONFIG_PATH")
if ! [[ "$NODE_COUNT" =~ ^[1-9][0-9]*$ ]]; then
    echo "Error: node_num in $CONFIG_PATH must be a positive integer." >&2
    exit 1
fi

NETEM_ENABLED=$(python3 -c 'import json, sys; print(1 if json.load(open(sys.argv[1])).get("netem", {}).get("enabled", False) else 0)' "$CONFIG_PATH")
NETEM_SOCKET_PATH=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1])).get("netem", {}).get("socket_path", "logs/netem-controller.sock"))' "$CONFIG_PATH")
NETEM_PID_PATH=$(python3 -c 'import json, sys; n=json.load(open(sys.argv[1])).get("netem", {}); s=n.get("socket_path", "logs/netem-controller.sock"); print(n.get("pid_path", s + ".pid"))' "$CONFIG_PATH")
NETEM_CONTROLLER_BINARY="netem_controller"
NETEM_CONTROLLER_LOG="logs/netem_controller.log"

stop_existing_netem_controller() {
    if [[ ! -f "$NETEM_PID_PATH" ]]; then
        return
    fi

    local pid expected_exe actual_exe
    pid=$(tr -d '[:space:]' < "$NETEM_PID_PATH")
    if ! [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: invalid netem controller PID file: $NETEM_PID_PATH" >&2
        exit 1
    fi
    if ! sudo -n kill -0 "$pid" 2>/dev/null; then
        echo "Found stale netem controller PID $pid; it will be cleaned during controller startup."
        return
    fi
    if [[ ! -x "$CURRENT_DIR/$NETEM_CONTROLLER_BINARY" ]]; then
        echo "Error: cannot verify running netem controller PID $pid because $NETEM_CONTROLLER_BINARY is missing." >&2
        exit 1
    fi

    expected_exe=$(readlink -f "$CURRENT_DIR/$NETEM_CONTROLLER_BINARY")
    actual_exe=$(sudo -n readlink -f "/proc/$pid/exe")
    if [[ "$actual_exe" != "$expected_exe" ]]; then
        echo "Error: refusing to stop PID $pid because it is not $expected_exe." >&2
        exit 1
    fi

    echo "Stopping existing netem controller PID $pid..."
    sudo -n kill -TERM "$pid"
    for _ in $(seq 1 200); do
        if ! sudo -n kill -0 "$pid" 2>/dev/null; then
            return
        fi
        sleep 0.05
    done
    echo "Error: netem controller PID $pid did not stop." >&2
    exit 1
}

start_netem_controller() {
    if (( NETEM_ENABLED == 0 )); then
        echo "Event-triggered netem is disabled."
        return
    fi
    if ! command -v tc >/dev/null 2>&1; then
        echo "Error: tc is required. Install the Linux iproute2 package first." >&2
        exit 1
    fi

    : > "$NETEM_CONTROLLER_LOG"
    echo "Starting privileged Go netem controller..."
    sudo -n nohup "$CURRENT_DIR/$NETEM_CONTROLLER_BINARY" -config "$CURRENT_DIR/$CONFIG_PATH" >> "$NETEM_CONTROLLER_LOG" 2>&1 &
    local sudo_pid=$!

    for _ in $(seq 1 200); do
        if [[ -S "$NETEM_SOCKET_PATH" && -f "$NETEM_PID_PATH" ]]; then
            echo "Netem controller is ready; log: $NETEM_CONTROLLER_LOG"
            return
        fi
        if ! kill -0 "$sudo_pid" 2>/dev/null; then
            wait "$sudo_pid" || true
            echo "Error: netem controller exited during startup. See $NETEM_CONTROLLER_LOG." >&2
            exit 1
        fi
        sleep 0.05
    done

    echo "Error: timed out waiting for netem controller readiness. See $NETEM_CONTROLLER_LOG." >&2
    exit 1
}

# if (( NETEM_ENABLED == 1 )) || [[ -f "$NETEM_PID_PATH" ]]; then
#     echo "Authenticating sudo for netem controller management..."
#     sudo -v
#     stop_existing_netem_controller
# fi

pkill -f pbft_main || true
echo "Cleaning up log files..."
mkdir -p logs
rm -f logs/*.log
rm -f logs/*.csv
rm -f logs/*.txt
rm -f logs/*.json
echo "Log files cleaned up."

echo "Cleaning up keys directory..."
rm -f keys/*.pem
echo "Keys directory cleaned."

echo "Building setup..."
go build -o crypto_main setup_crypto/crypto_main.go
chmod +x crypto_main
./crypto_main

echo "Building PBFT project..."
rm -f pbft_main
go mod tidy
go build -o pbft_main main.go
chmod +x pbft_main

if (( NETEM_ENABLED == 1 )); then
    echo "Building Go netem controller..."
    rm -f "$NETEM_CONTROLLER_BINARY"
    go build -o "$NETEM_CONTROLLER_BINARY" ./cmd/netem-controller
    chmod +x "$NETEM_CONTROLLER_BINARY"
fi

echo "Starting $NODE_COUNT nodes in separate tmux windows..."
if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Existing tmux session '$SESSION' found. Killing it..."
    tmux kill-session -t "$SESSION"
fi

# start_netem_controller

echo "Learning-agent launcher log: $CURRENT_DIR/logs/learning-agent-launcher.log"
echo "Learning-agent node logs: $CURRENT_DIR/logs/learning-agent-node-<id>.log"
echo "Follow all learning-agent node logs with: tail -f logs/learning-agent-node-*.log"

tmux new-session -d -s "$SESSION" -n "node1" \
    "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n 1; status=\$?; echo; echo \"node1 exited with status \$status\"; exec bash"
for i in $(seq 2 "$NODE_COUNT"); do
    tmux new-window -t "$SESSION" -n "node$i" \
        "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n $i; status=\$?; echo; echo \"node$i exited with status \$status\"; exec bash"
done

sleep 5
tmux new-window -t "$SESSION" -n "client" \
    "cd \"$CURRENT_DIR\" && ./pbft_main -r client -m loopbackip; status=\$?; echo; echo \"client exited with status \$status\"; exec bash"

echo "All nodes started."
echo "Netem transitions: $NETEM_CONTROLLER_LOG"
echo "Stop the experiment with: ./stop_project_linux.sh"
echo "Attaching to tmux session: $SESSION"

tmux attach -t "$SESSION"
