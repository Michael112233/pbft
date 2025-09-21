#!/bin/bash

# Download dataset first
echo "Downloading dataset..."
python script/download_dataset.py

# Detect operating system
if [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS
    echo "Detected macOS, running macOS version..."
    chmod +x script/local_experiment/macos_version.py
    python script/local_experiment/macos_version.py
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    # Linux
    echo "Detected Linux, running Linux version..."
    chmod +x script/local_experiment/linux_version.py
    python script/local_experiment/linux_version.py
else
    echo "Unsupported operating system: $OSTYPE"
    exit 1
fi
