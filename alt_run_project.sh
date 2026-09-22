#!/bin/bash
set -e

SESSION="pbft"
CONFIG_PATH="${1:-config/run2new.json}"
NO_ATTACH="${NO_ATTACH:-0}"

# GOMAXPROCS sweep knobs (see plan: Step0 baseline + A3).
# Empty (default) = unchanged behavior, GOMAXPROCS defaults to the full CPU affinity mask.
# Set e.g. NODE_GOMAXPROCS=8 ./alt_run_project.sh to test a smaller value on node processes.
NODE_GOMAXPROCS="${NODE_GOMAXPROCS:-}"
CLIENT_GOMAXPROCS="${CLIENT_GOMAXPROCS:-}"
NODE_ENV_PREFIX=""
CLIENT_ENV_PREFIX=""
if [ -n "$NODE_GOMAXPROCS" ]; then
    NODE_ENV_PREFIX="GOMAXPROCS=$NODE_GOMAXPROCS "
fi
if [ -n "$CLIENT_GOMAXPROCS" ]; then
    CLIENT_ENV_PREFIX="GOMAXPROCS=$CLIENT_GOMAXPROCS "
fi

if ! command -v python3 >/dev/null 2>&1; then
    echo "Error: python3 is required to run the learning-agent servers." >&2
    exit 1
fi
if ! python3 -c 'import grpc, google.protobuf' >/dev/null 2>&1; then
    echo "Error: Python gRPC dependencies are missing. Run: python3 -m pip install -r requirements.txt" >&2
    exit 1
fi

# Optional bool in the config: "netem_delay": true enables setup_netem + start_netem_schedule.
NETEM_DELAY=$(python3 -c 'import json, sys; print("1" if json.load(open(sys.argv[1])).get("netem_delay", False) is True else "0")' "$CONFIG_PATH")
# "scenario_mode": true lets node 4 own the lo qdisc (scripts/netem_scenario.sh); its
# teardown would wipe the qdisc the netem_delay schedule relies on.
SCENARIO_MODE=$(python3 -c 'import json, sys; print("1" if json.load(open(sys.argv[1])).get("scenario_mode", False) is True else "0")' "$CONFIG_PATH")
if [ "$NETEM_DELAY" = "1" ] && [ "$SCENARIO_MODE" = "1" ]; then
    echo "Error: netem_delay and scenario_mode cannot both be true in $CONFIG_PATH." >&2
    exit 1
fi

NODE_COUNT=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["node_num"])' "$CONFIG_PATH")
if ! [[ "$NODE_COUNT" =~ ^[1-9][0-9]*$ ]]; then
    echo "Error: node_num in $CONFIG_PATH must be a positive integer." >&2
    exit 1
fi

NETEM_INTERFACE="lo"
NETEM_LIMIT=100000
NETEM_DELAY_LOG="logs/netem_schedule.log"
NETEM_SCHEDULE_PID=""

FREQ_TRACE_LOG="logs/freq_trace.csv"
FREQ_TRACE_PID=""

setup_netem() {
    if ! command -v tc >/dev/null 2>&1; then
        echo "Error: tc is required. Install the Linux iproute2 package first." >&2
        exit 1
    fi

    if (( NODE_COUNT < 1 || NODE_COUNT > 8 )); then
        echo "Error: NODE_COUNT must be between 1 and 8 in loopbackip mode." >&2
        exit 1
    fi

    echo "Authenticating sudo for netem configuration..."
    sudo -v

    echo "Configuring node-to-node netem filters on $NETEM_INTERFACE..."
    sudo tc qdisc del dev "$NETEM_INTERFACE" root 2>/dev/null || true
    sudo tc qdisc add dev "$NETEM_INTERFACE" root handle 1: prio bands 3 \
        priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
    sudo tc qdisc add dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        netem limit "$NETEM_LIMIT" delay 0ms

    local src_id dst_id src_ip dst_ip
    for ((src_id = 1; src_id <= NODE_COUNT; src_id++)); do
        src_ip="127.0.0.$((src_id + 1))"
        for ((dst_id = 1; dst_id <= NODE_COUNT; dst_id++)); do
            dst_ip="127.0.0.$((dst_id + 1))"
            sudo tc filter add dev "$NETEM_INTERFACE" parent 1: protocol ip prio 10 flower \
                src_ip "$src_ip/32" dst_ip "$dst_ip/32" classid 1:3
        done
    done

    echo "Node-to-node netem filters configured with an initial 0ms delay."
}

start_netem_schedule() {
    : > "$NETEM_DELAY_LOG"

    (
        set -e
        echo "$(date --iso-8601=ns) delay=0ms"
        sleep 1

        sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
            netem limit "$NETEM_LIMIT" delay 170ms
        echo "$(date --iso-8601=ns) delay=170ms"
        # sleep 10
        # sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        #     netem limit "$NETEM_LIMIT" delay 0ms
        # echo "$(date --iso-8601=ns) delay=0ms"

        # sleep 3

        # sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        #     netem limit "$NETEM_LIMIT" delay 100ms
        # echo "$(date --iso-8601=ns) delay=100ms"
        # sleep 0.1

        # sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        #     netem limit "$NETEM_LIMIT" delay 0ms
        # echo "$(date --iso-8601=ns) delay=0ms"

        # sleep 3

        # sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        #     netem limit "$NETEM_LIMIT" delay 100ms
        # echo "$(date --iso-8601=ns) delay=100ms"
        # sleep 0.1

        # sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
        #     netem limit "$NETEM_LIMIT" delay 0ms
        # echo "$(date --iso-8601=ns) delay=0ms"



 






       


    ) >> "$NETEM_DELAY_LOG" 2>&1 &

    NETEM_SCHEDULE_PID=$!
    echo "Started netem schedule with PID $NETEM_SCHEDULE_PID; transitions are logged to $NETEM_DELAY_LOG."
}

start_repeating_netem_spikes() {
    : > "$NETEM_DELAY_LOG"

    (
        set -e

        while true; do
            sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
                netem limit "$NETEM_LIMIT" delay 150ms
            echo "$(date --iso-8601=ns) delay=150ms"
            sleep 0.15

            sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
                netem limit "$NETEM_LIMIT" delay 0ms
            echo "$(date --iso-8601=ns) delay=0ms"
            sleep 1.5
        done
    ) >> "$NETEM_DELAY_LOG" 2>&1 &

    NETEM_SCHEDULE_PID=$!
    echo "Started repeating netem spikes with PID $NETEM_SCHEDULE_PID; transitions are logged to $NETEM_DELAY_LOG."
}

start_freq_trace() {
    : > "$FREQ_TRACE_LOG"

    (
        while true; do
            ts=$(date +%s.%N)
            freqs=$(cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq 2>/dev/null | tr '\n' ',')
            echo "$ts,$freqs" >> "$FREQ_TRACE_LOG"
            sleep 0.2
        done
    ) &

    FREQ_TRACE_PID=$!
    echo "Started CPU frequency trace with PID $FREQ_TRACE_PID; samples are logged to $FREQ_TRACE_LOG."
    echo "Kill it manually once the experiment finishes: kill $FREQ_TRACE_PID"
}

if [ -n "${RUN_ISOLATED:-}" ]; then
    # scripts/run_isolated.sh gives each run its own tmux server (TMUX_TMPDIR), which owns all
    # of the run's processes. pkill -f would also kill the other runs' pbft_main.
    tmux kill-server 2>/dev/null || true
else
    pkill -f pbft_main || true
fi
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

# echo "Checking tmux..."
# if ! command -v tmux >/dev/null 2>&1; then
#     echo "tmux is not installed."
#     echo "Install it using:"
#     echo "sudo apt update && sudo apt install -y tmux"
#     exit 1
# fi

echo "Building setup..."
go build -o crypto_main setup_crypto/crypto_main.go
chmod +x crypto_main
./crypto_main --config "$CONFIG_PATH"

echo "Building PBFT project..."
rm -f pbft_main
go mod tidy
go build -o pbft_main main.go
chmod +x pbft_main

CURRENT_DIR=$(pwd)

echo "Starting $NODE_COUNT learning-agent servers and nodes in separate tmux windows..."

# Kill old session if it exists
if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Existing tmux session '$SESSION' found. Killing it..."
    tmux kill-session -t "$SESSION"
fi

# Start all Python servers under one launcher in the first tmux window.
tmux new-session -d -s "$SESSION" -n "learning-agents" \
    "cd \"$CURRENT_DIR\" && python3 -m learningagent.launcher --node-count $NODE_COUNT --mode loopbackip --log-dir \"$CURRENT_DIR/logs\" --config \"$CONFIG_PATH\"; status=\$?; echo; echo \"learning-agent launcher exited with status \$status\"; exec bash"

echo "Learning-agent launcher log: $CURRENT_DIR/logs/learning-agent-launcher.log"
echo "Learning-agent node logs: $CURRENT_DIR/logs/learning-agent-node-<id>.log"
echo "Follow all learning-agent node logs with: tail -f logs/learning-agent-node-*.log"



# tmux new-session -d -s "$SESSION" -n "node1" \
#     "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n 1; status=\$?; echo; echo \"node1 exited with status \$status\"; exec bash"
# Start every Go node in its own window.
for i in $(seq 1 "$NODE_COUNT"); do
    tmux new-window -t "$SESSION" -n "node$i" \
        "cd \"$CURRENT_DIR\" && ${NODE_ENV_PREFIX}./pbft_main --config \"$CONFIG_PATH\" -r node -m loopbackip -n $i; status=\$?; echo; echo \"node$i exited with status \$status\"; exec bash"
done

sleep 5
if [ "$NETEM_DELAY" = "1" ]; then
    setup_netem
fi
# Optional: start client in another window

tmux new-window -t "$SESSION" -n "client" \
    "cd \"$CURRENT_DIR\" && ${CLIENT_ENV_PREFIX}./pbft_main --config \"$CONFIG_PATH\" -r client -m loopbackip; status=\$?; echo; echo \"client exited with status \$status\"; exec bash"

# sleep 2
# start_repeating_netem_spikes
if [ "$NETEM_DELAY" = "1" ]; then
    start_netem_schedule
fi
# start_freq_trace

echo "All nodes started."
if [ "$NO_ATTACH" != "1" ]; then
    echo "Attaching to tmux session: $SESSION"
    tmux attach -t "$SESSION"
fi
# Usage:
#   ./alt_run_project.sh [config.json]
#
#   config.json  optional, default: config/run2new.json
#   "netem_delay": true in the config runs setup_netem + start_netem_schedule (default false)
#   NO_ATTACH=1  optional env var, skip the final "tmux attach" (default 0)
#
# Examples:
#   ./alt_run_project.sh                                    # default config, attach to tmux
#   ./alt_run_project.sh config/experiments/a.json          # custom config, attach to tmux
#   NO_ATTACH=1 ./alt_run_project.sh config/experiments/a.json   # custom config, no attach
#
# Multiple experiments in a row (runs this script, waits, stop_experiment.sh, saves logs to results/<name>/):
#   ./run_experiments.sh 300 config/experiments/a.json config/experiments/b.json
