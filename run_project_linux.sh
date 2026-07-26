#!/bin/bash
set -e

SESSION="pbft"
CONFIG_PATH="config/run2new.json"

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

pkill -f pbft_main || true
echo "Cleaning up log files..."
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
./crypto_main

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
# tmux new-session -d -s "$SESSION" -n "learning-agents" \
#     "cd \"$CURRENT_DIR\" && python3 -m learningagent.launcher --node-count $NODE_COUNT --mode loopbackip; status=\$?; echo; echo \"learning-agent launcher exited with status \$status\"; exec bash"

tmux new-session -d -s "$SESSION" -n "node1" \
    "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n 1; status=\$?; echo; echo \"node1 exited with status \$status\"; exec bash"
# Start every Go node in its own window.
for i in $(seq 2 "$NODE_COUNT"); do
    tmux new-window -t "$SESSION" -n "node$i" \
        "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m loopbackip -n $i; status=\$?; echo; echo \"node$i exited with status \$status\"; exec bash"
done

sleep 5
# Optional: start client in another window
tmux new-window -t "$SESSION" -n "client" \
    "cd \"$CURRENT_DIR\" && ./pbft_main -r client -m loopbackip; status=\$?; echo; echo \"client exited with status \$status\"; exec bash"

echo "All nodes started."
echo "Attaching to tmux session: $SESSION"

tmux attach -t "$SESSION"
