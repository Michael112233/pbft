#!/bin/bash

echo "Closing any open terminal emulator windows..."
terminals=(gnome-terminal konsole xterm terminator xfce4-terminal mate-terminal lxterminal alacritty kitty)
for term in "${terminals[@]}"; do
  if pgrep -f "$term" >/dev/null 2>&1; then
    echo "Killing $term processes"
    pkill -f "$term" >/dev/null 2>&1 || true
  fi
done
echo "Terminal emulators closed (if any)."

echo "Cleaning up log files..."
rm -f logs/*.log
echo "Log files cleaned up."

echo "Installing Python dependencies..."
sudo apt-get update
sudo apt-get install -y python3 python3-pip
pip3 install requests

echo "Downloading dataset..."
python3 script/download_dataset.py

echo "Building PBFT project..."
go mod tidy
go build -o pbft_main main.go

echo "Starting nodes and client in background..."

# Start all processes in background
./pbft_main -r node -m local -n 0 &
NODE0_PID=$!

./pbft_main -r node -m local -n 1 &
NODE1_PID=$!

./pbft_main -r node -m local -n 2 &
NODE2_PID=$!

./pbft_main -r node -m local -n 3 &
NODE3_PID=$!

./pbft_main -r client -m local &
CLIENT_PID=$!

echo "All processes started!"
echo "Node 0 PID: $NODE0_PID"
echo "Node 1 PID: $NODE1_PID"
echo "Node 2 PID: $NODE2_PID"
echo "Node 3 PID: $NODE3_PID"
echo "Client PID: $CLIENT_PID"
echo ""
echo "Press Ctrl+C to stop all processes"

# Function to cleanup processes
cleanup() {
    echo "Stopping all processes..."
    kill $NODE0_PID $NODE1_PID $NODE2_PID $NODE3_PID $CLIENT_PID 2>/dev/null
    exit 0
}

# Set trap to cleanup on script exit
trap cleanup SIGINT SIGTERM

# Wait for all background processes
wait