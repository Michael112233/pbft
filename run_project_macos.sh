#!/bin/bash

echo "Cleaning up log files..."
rm -f logs/*.log
echo "Log files cleaned up."

echo "Closing all Terminal windows..."
osascript -e 'tell application "Terminal" to close every window'
echo "All Terminal windows closed."

echo "Freeing required ports..."
ports=(20000 28000 28100 28200 28300)
for p in "${ports[@]}"; do
  pids=$(lsof -ti :$p 2>/dev/null || true)
  if [ -n "$pids" ]; then
    echo "Killing processes on port $p: $pids"
    kill -9 $pids 2>/dev/null || true
  fi
done
echo "Ports freed."

echo "Installing Python dependencies..."
pip3 install --break-system-packages requests

echo "Downloading dataset..."
python3 script/download_dataset.py

echo "Building PBFT project..."
go mod tidy
go build -o pbft_main main.go

echo "Starting nodes and client in separate terminals..."

# Get current directory
CURRENT_DIR=$(pwd)

# Start Node 0
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 0\""

# Start Node 1
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 1\""

# Start Node 2
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 2\""

# Start Node 3
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 3\""

# Start Client
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r client -m local\""

echo "All terminals started! Press Ctrl+C in any terminal to stop the experiment."
echo "You can close this terminal now."
