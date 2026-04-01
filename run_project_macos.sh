#!/bin/bash

echo "Cleaning up log files..."
rm -f logs/*.log
echo "Log files cleaned up."

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
go mod tidy
go build -o pbft_main main.go

echo "Starting nodes and client in separate terminals..."

# Get current directory
CURRENT_DIR=$(pwd)

# Start Node 0
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 1\""

# Start Node 1
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 2\""

# Start Node 2
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 3\""

# Start Node 3
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 4\""

# Start Node 4
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 5\""

# Start Node 5
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 6\""

# Start Node 6
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 7\""

# Start Node 7
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 8\""

# Start Node 8
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 9\""

# Start Node 9
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r node -m local -n 10\""

# Sleep for 5 seconds
sleep 5

# Start Client
osascript -e "tell application \"Terminal\" to do script \"cd '$CURRENT_DIR' && ./pbft_main -r client -m local\""

echo "All terminals started! Press Ctrl+C in any terminal to stop the experiment."
echo "You can close this terminal now."
