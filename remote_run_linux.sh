#!/bin/bash

set -euo pipefail

usage() {
  echo "Usage: $0 --role <node|client> [--node-id <id>] [--background] [--skip-prepare]"
  echo "Examples:"
  echo "  $0 --role node --node-id 0"
  echo "  $0 --role client"
  echo "  $0 --role client --background"
  echo "  $0 --role node --node-id 1 --skip-prepare"
}

ROLE=""
NODE_ID=""
BACKGROUND="false"
SKIP_PREPARE="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -r|--role)
      ROLE=${2:-}
      shift 2
      ;;
    -n|--node-id)
      NODE_ID=${2:-}
      shift 2
      ;;
    -b|--background)
      BACKGROUND="true"
      shift 1
      ;;
    --skip-prepare)
      SKIP_PREPARE="true"
      shift 1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$ROLE" ]]; then
  echo "Error: --role is required (node|client)" >&2
  usage
  exit 1
fi

if [[ "$ROLE" == "node" && -z "$NODE_ID" ]]; then
  echo "Error: --node-id is required when --role node" >&2
  usage
  exit 1
fi

# Optional environment preparation (mirror run_project_linux.sh essentials)
if [[ "$SKIP_PREPARE" != "true" ]]; then
  echo "Cleaning up log files..."
  rm -f logs/*.log || true

  # Ensure Python and requests exist similar to run_project_linux.sh
  if ! command -v python3 >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
      echo "Installing Python3..."
      sudo apt-get update -y || true
      sudo apt-get install -y python3 python3-pip || true
    else
      echo "python3 not found and apt-get unavailable; please install python3 manually." >&2
    fi
  fi

  if command -v python3 >/dev/null 2>&1; then
    if ! python3 -c 'import requests' >/dev/null 2>&1; then
      echo "Installing Python requests module..."
      if command -v pip3 >/dev/null 2>&1; then
        pip3 install --user requests || true
      else
        echo "pip3 not found; skipping requests installation." >&2
      fi
    fi
  fi

  # Ensure dataset exists (script may no-op if already downloaded)
  if command -v python3 >/dev/null 2>&1; then
    if [[ -f script/download_dataset.py ]]; then
      echo "Ensuring dataset is available..."
      python3 script/download_dataset.py || true
    fi
  fi
fi

# Build or rebuild Linux-correct binary when needed
need_build="false"
if [[ ! -f ./pbft_main ]]; then
  need_build="true"
else
  if ! file ./pbft_main | grep -q 'ELF 64-bit LSB executable'; then
    echo "Existing pbft_main is not a Linux ELF; rebuilding..."
    need_build="true"
  fi
fi

if [[ "$need_build" == "true" ]]; then
  echo "Building PBFT binary for Linux..."
  # Auto-detect arch; default to host arch mapping
  host_arch=$(uname -m)
  case "$host_arch" in
    x86_64) goarch=amd64 ;;
    aarch64|arm64) goarch=arm64 ;;
    armv7l|armv6l) goarch=arm ;;
    *) goarch=amd64 ;;
  esac
  GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go mod tidy
  GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o pbft_main main.go
fi

echo "Starting role=$ROLE ${NODE_ID:+nodeId=$NODE_ID} in remote mode..."

run_cmd=(./pbft_main -r "$ROLE" -m remote)
if [[ "$ROLE" == "node" ]]; then
  run_cmd+=( -n "$NODE_ID" )
elif [[ "$ROLE" == "client" ]]; then
  : # no extra args
else
  echo "Error: invalid role '$ROLE'. Use 'node' or 'client'." >&2
  exit 1
fi

if [[ "$BACKGROUND" == "true" ]]; then
  "${run_cmd[@]}" &
  PID=$!
  echo "Started $ROLE PID: $PID"
  trap 'echo "Stopping $ROLE PID: $PID"; kill $PID 2>/dev/null || true; exit 0' SIGINT SIGTERM
  wait $PID
else
  exec "${run_cmd[@]}"
fi


