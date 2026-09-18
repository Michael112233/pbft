#!/bin/bash
set -e

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

echo "Cleaning up log files..."
rm -f logs/*.log
echo "Log files cleaned up."

rm -f logs/*.csv
rm -f logs/*.txt

echo "Cleaning up keys directory..."
rm -f keys/*.pem
echo "Keys directory cleaned."

# echo "Closing all Terminal windows..."
# osascript -e ''tell application "Terminal" to close every window''
# echo "All Terminal windows closed."

# echo "Freeing required ports..."
# ports=(20000 28000 28100 28200 28300)
# for p in "${ports[@]}"; do
#   pids=$(lsof -ti :$p 2>/dev/null || true)
#   if [ -n "$pids" ]; then
#     echo "Killing processes on port $p: $pids"
#     kill -9 $pids 2>/dev/null || true
#   fi
# done
# echo "Ports freed."

# echo "Installing Python dependencies..."
# pip3 install --break-system-packages requests

# echo "Downloading dataset..."
# python3 script/download_dataset.py


echo "Building setup..."
go build -o crypto_main setup_crypto/crypto_main.go
chmod +x crypto_main
./crypto_main


echo "Building PBFT project..."
rm -f pbft_main
go mod tidy
go build -o pbft_main main.go

echo "Starting $NODE_COUNT learning-agent servers, nodes, and client in separate terminals..."

# Get current directory
CURRENT_DIR=$(pwd)

# Start all Python servers under one launcher in its own Terminal window.
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && python3 -m learningagent.launcher --node-count $NODE_COUNT --mode local\""

# Start every Go node in its own Terminal window.
for i in $(seq 1 "$NODE_COUNT"); do
    osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n $i\""
done

# Sleep for 5 seconds
sleep 5

# Start Client
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r client -m local\""

echo "All terminals started! Press Ctrl+C in any terminal to stop the experiment."
echo "You can close this terminal now."
