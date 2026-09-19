#!/usr/bin/env bash
# Usage: ./run_experiments.sh <seconds> <config1.json> [config2.json ...]
set -uo pipefail

if (( $# < 2 )); then
    echo "Usage: $0 <seconds> <config1.json> [config2.json ...]" >&2
    exit 2
fi

DURATION="$1"
shift
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

for cfg in "$@"; do
    if [[ ! -f "$cfg" ]]; then
        echo "Skipping $cfg: file not found." >&2
        continue
    fi

    name="$(basename "$cfg" .json)"
    out="results/$name"
    echo "===== Experiment '$name' ($cfg) for ${DURATION}s ====="

    NO_ATTACH=1 ./alt_run_project.sh "$cfg" || echo "Warning: alt_run_project.sh failed for $name." >&2
    sleep "$DURATION"
    ./stop_experiment.sh || echo "Warning: stop_experiment.sh failed for $name." >&2

    mkdir -p results
    rm -rf "$out"
    mv logs "$out" && mkdir -p logs
    cp "$cfg" "$out/config.json"
    echo "Saved results to $out"
done

echo "All experiments finished."
