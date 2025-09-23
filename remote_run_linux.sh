#!/bin/bash

set -euo pipefail

usage() {
  echo "Usage: $0 --role <node|client> [--node-id <id>]"
  echo "Examples:"
  echo "  $0 --role node --node-id 0"
  echo "  $0 --role client"
}

ROLE=""
NODE_ID=""

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

# Build if binary missing
if [[ ! -x ./pbft_main ]]; then
  echo "Building PBFT binary..."
  go mod tidy
  go build -o pbft_main main.go
fi

echo "Starting role=$ROLE ${NODE_ID:+nodeId=$NODE_ID} in local mode..."

if [[ "$ROLE" == "node" ]]; then
  exec ./pbft_main -r node -m remote -n "$NODE_ID"
elif [[ "$ROLE" == "client" ]]; then
  exec ./pbft_main -r client -m remote
else
  echo "Error: invalid role '$ROLE'. Use 'node' or 'client'." >&2
  exit 1
fi


