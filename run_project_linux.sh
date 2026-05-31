#!/bin/bash
set -e

SESSION="pbft"
NODE_COUNT=4

echo "Cleaning up log files..."
rm -f logs/*.log
rm -f logs/*.csv
rm -f logs/*.txt
echo "Log files cleaned up."

echo "Cleaning up keys directory..."
rm -f keys/*.pem
echo "Keys directory cleaned."

echo "Checking tmux..."
if ! command -v tmux >/dev/null 2>&1; then
    echo "tmux is not installed."
    echo "Install it using:"
    echo "sudo apt update && sudo apt install -y tmux"
    exit 1
fi

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
    "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m local -n 1; status=\$?; echo; echo \"node1 exited with status \$status\"; exec bash"

# Start remaining nodes in separate tmux windows
for i in $(seq 2 "$NODE_COUNT"); do
    tmux new-window -t "$SESSION" -n "node$i" \
        "cd \"$CURRENT_DIR\" && ./pbft_main -r node -m local -n $i; status=\$?; echo; echo \"node$i exited with status \$status\"; exec bash"
done

# Optional: start client in another window
# tmux new-window -t "$SESSION" -n "client" \
#     "cd \"$CURRENT_DIR\" && ./pbft_main -r client -m local; status=\$?; echo; echo \"client exited with status \$status\"; exec bash"

echo "All nodes started."
echo "Attaching to tmux session: $SESSION"

tmux attach -t "$SESSION"
