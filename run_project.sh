#!/bin/bash

# PBFT Project Run Script
echo "Compiling PBFT project..."

# Clean logs folder
echo "Cleaning logs folder..."
rm -rf logs/*

# Build project
go mod tidy
go mod init github.com/michael112233/pbft
go build -o pbft_main main.go

# Check if build was successful
if [ $? -eq 0 ]; then
    echo "Build successful!"
    echo "Running PBFT project..."
    ./pbft_main
else
    echo "Build failed! Please check for code errors."
    exit 1
fi
