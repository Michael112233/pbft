#!/bin/bash
set -e

SESSION="pbft"
NODE_COUNT=4
NETEM_INTERFACE="lo"
NETEM_LIMIT=100000
NETEM_DELAY_LOG="logs/netem_schedule.log"

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
        netem limit "$NETEM_LIMIT" delay 50ms

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
        echo "$(date --iso-8601=seconds) delay=0ms"
        sleep 10

        sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
            netem limit "$NETEM_LIMIT" delay 100ms
        echo "$(date --iso-8601=seconds) delay=100ms"
        sleep 10

        sudo -n tc qdisc change dev "$NETEM_INTERFACE" parent 1:3 handle 30: \
            netem limit "$NETEM_LIMIT" delay 0ms
        echo "$(date --iso-8601=seconds) delay=0ms"
    ) >> "$NETEM_DELAY_LOG" 2>&1 &

    NETEM_SCHEDULE_PID=$!
    echo "Started netem schedule with PID $NETEM_SCHEDULE_PID; transitions are logged to $NETEM_DELAY_LOG."
}

pkill -f pbft_main || true
echo "Cleaning up log files..."
rm -f logs/*.log
rm -f logs/*.csv
rm -f logs/*.txt
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
./crypto_main

echo "Building PBFT project..."
rm -f pbft_main
go mod tidy
go build -o pbft_main main.go
chmod +x pbft_main

CURRENT_DIR=$(pwd)

echo "Starting nodes in separate tmux windows..."

# Kill old session if it exists
if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Existing tmux session '$SESSION' found. Killing it..."
    tmux kill-session -t "$SESSION"
fi

# Start node 1 in the first tmux window
tmux new-session -d -s "$SESSION" -n "node1" \
    "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n 1; status=\$?; echo; echo \"node1 exited with status \$status\"; exec bash"

# Start remaining nodes in separate tmux windows
for i in $(seq 2 "$NODE_COUNT"); do
    tmux new-window -t "$SESSION" -n "node$i" \
        "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n $i; status=\$?; echo; echo \"node$i exited with status \$status\"; exec bash"
done

sleep 5

setup_netem
# start_netem_schedule

# Optional: start client in another window
tmux new-window -t "$SESSION" -n "client" \
    "cd \"$CURRENT_DIR\" && ./pbft_main -r client -m loopbackip; status=\$?; echo; echo \"client exited with status \$status\"; exec bash"

echo "All nodes started."
echo "Attaching to tmux session: $SESSION"

tmux attach -t "$SESSION"
